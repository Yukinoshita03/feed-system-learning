package router_test

import (
	"context"
	"errors"
	"feed-system-learning/internal/account"
	feeddb "feed-system-learning/internal/db"
	"feed-system-learning/internal/notification"
	"feed-system-learning/internal/social"
	"feed-system-learning/internal/video"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"testing"
	"time"
)

type crudFixture struct {
	db     *gorm.DB
	users  []account.Account
	item   video.Video
	videos *video.VideoRepository
}

func newCRUDFixture(t *testing.T) *crudFixture {
	t.Helper()
	dsn := os.Getenv("FEED_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FEED_TEST_MYSQL_DSN to a dedicated test database")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := feeddb.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}
	f := &crudFixture{db: database, videos: video.NewVideoRepository(database, nil)}
	for i := 0; i < 2; i++ {
		f.users = append(f.users, account.Account{Username: fmt.Sprintf("crud_%d_%d", time.Now().UnixNano(), i), Password: "test-hash"})
	}
	if err := database.Create(&f.users).Error; err != nil {
		t.Fatal(err)
	}
	f.item = video.Video{AuthorID: f.users[0].ID, Title: "CRUD transaction", PlayURL: "/static/test.mp4", CoverURL: "/static/test.png"}
	if err := database.Create(&f.item).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := []uint{f.users[0].ID, f.users[1].ID}
		for _, entity := range []any{&video.Like{}, &video.Favorite{}, &video.Comment{}} {
			if err := database.Where("video_id = ?", f.item.ID).Delete(entity).Error; err != nil {
				t.Error(err)
			}
		}
		if err := database.Where("follower_id IN ? OR vlogger_id IN ?", ids, ids).Delete(&social.Follow{}).Error; err != nil {
			t.Error(err)
		}
		if err := database.Where("receiver_id IN ? OR actor_id IN ?", ids, ids).Delete(&notification.Notification{}).Error; err != nil {
			t.Error(err)
		}
		if err := database.Delete(&f.item).Error; err != nil {
			t.Error(err)
		}
		if err := database.Where("id IN ?", ids).Delete(&account.Account{}).Error; err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *crudFixture) count(t *testing.T, entity any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.db.Model(entity).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *crudFixture) assertVideoCounts(t *testing.T, comments, favorites, likes int64) {
	t.Helper()
	var current video.Video
	if err := f.db.First(&current, f.item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.CommentsCount != comments || current.FavoritesCount != favorites || current.LikesCount != likes {
		t.Fatalf("video counters = comments:%d favorites:%d likes:%d", current.CommentsCount, current.FavoritesCount, current.LikesCount)
	}
}

func TestCRUDCommentAndFavoriteRollbackOnCounterFailure(t *testing.T) {
	for _, kind := range []string{"comment", "favorite"} {
		t.Run(kind, func(t *testing.T) {
			f := newCRUDFixture(t)
			errInjected := errors.New("injected video counter failure")
			name := "crud:fail-counter"
			if err := f.db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table == "videos" {
					tx.AddError(errInjected)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.db.Callback().Update().Remove(name) })
			var err error
			if kind == "comment" {
				_, err = video.NewCommentRepository(f.db, f.videos).Create(context.Background(), &video.Comment{VideoID: f.item.ID, AuthorID: f.users[1].ID, Content: "rollback"})
			} else {
				_, err = video.NewFavoriteRepository(f.db, f.videos).SetFavorited(context.Background(), f.item.ID, f.users[1].ID, true)
			}
			if !errors.Is(err, errInjected) {
				t.Fatalf("got %v, want injected failure", err)
			}
			if f.count(t, &video.Comment{}, "video_id = ?", f.item.ID) != 0 || f.count(t, &video.Favorite{}, "video_id = ?", f.item.ID) != 0 {
				t.Fatal("business row survived rollback")
			}
			f.assertVideoCounts(t, 0, 0, 0)
			if f.count(t, &notification.Notification{}, "receiver_id = ?", f.users[0].ID) != 0 {
				t.Fatal("notification survived rollback")
			}
		})
	}
}

func TestCRUDNotificationFailureRollsBackAllBusinessKinds(t *testing.T) {
	for _, kind := range []string{"like", "favorite", "comment", "follow"} {
		t.Run(kind, func(t *testing.T) {
			f := newCRUDFixture(t)
			errInjected := errors.New("injected notification failure")
			name := "crud:fail-notification"
			if err := f.db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table == "notifications" {
					tx.AddError(errInjected)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.db.Callback().Create().Remove(name) })
			ctx := context.Background()
			var err error
			switch kind {
			case "like":
				_, err = video.NewLikeRepository(f.db, f.videos).SetLiked(ctx, f.item.ID, f.users[1].ID, true)
			case "favorite":
				_, err = video.NewFavoriteRepository(f.db, f.videos).SetFavorited(ctx, f.item.ID, f.users[1].ID, true)
			case "comment":
				_, err = video.NewCommentRepository(f.db, f.videos).Create(ctx, &video.Comment{VideoID: f.item.ID, AuthorID: f.users[1].ID, Content: "rollback notification"})
			case "follow":
				_, err = social.NewRepository(f.db).SetFollowing(ctx, f.users[1].ID, f.users[0].ID, true)
			}
			if !errors.Is(err, errInjected) {
				t.Fatalf("got %v, want injected failure", err)
			}
			for _, entity := range []any{&video.Like{}, &video.Favorite{}, &video.Comment{}} {
				if f.count(t, entity, "video_id = ?", f.item.ID) != 0 {
					t.Fatal("business row survived notification failure")
				}
			}
			if f.count(t, &social.Follow{}, "follower_id = ?", f.users[1].ID) != 0 {
				t.Fatal("follow survived notification failure")
			}
			f.assertVideoCounts(t, 0, 0, 0)
		})
	}
}

func TestCRUDDeleteVideoCleansAllInteractionRows(t *testing.T) {
	f := newCRUDFixture(t)
	ctx := context.Background()
	if _, err := video.NewLikeRepository(f.db, f.videos).SetLiked(ctx, f.item.ID, f.users[1].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := video.NewFavoriteRepository(f.db, f.videos).SetFavorited(ctx, f.item.ID, f.users[1].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := video.NewCommentRepository(f.db, f.videos).Create(ctx, &video.Comment{VideoID: f.item.ID, AuthorID: f.users[1].ID, Content: "delete cleanup"}); err != nil {
		t.Fatal(err)
	}
	if err := f.videos.DeleteVideo(ctx, f.item.ID, f.users[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, entity := range []any{&video.Like{}, &video.Favorite{}, &video.Comment{}} {
		if f.count(t, entity, "video_id = ?", f.item.ID) != 0 {
			t.Fatal("orphan interaction row after video deletion")
		}
	}
}
