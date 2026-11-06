package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"feed-system-learning/internal/account"
	"feed-system-learning/internal/video"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 只在显式提供专用测试库时执行；测试记录在事务结束时回滚。
func feedFixture(t *testing.T) (*FeedService, *gorm.DB, []account.Account) {
	t.Helper()
	dsn := os.Getenv("FEED_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FEED_TEST_MYSQL_DSN to a dedicated database for feed integration tests")
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
	if err := database.AutoMigrate(&account.Account{}, &video.Video{}); err != nil {
		t.Fatal(err)
	}
	tx := database.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })
	authors := []account.Account{
		{Username: fmt.Sprintf("feed_a_%d", time.Now().UnixNano()), Password: "private-feed-password"},
		{Username: fmt.Sprintf("feed_b_%d", time.Now().UnixNano()), Password: "private-feed-password"},
	}
	if err := tx.Create(&authors).Error; err != nil {
		t.Fatal(err)
	}
	return NewFeedService(NewFeedRepository(tx)), tx, authors
}

func seedFeedVideo(t *testing.T, tx *gorm.DB, authorID uint, createdAt time.Time) video.Video {
	t.Helper()
	item := video.Video{
		AuthorID: authorID, Title: "Feed integration video",
		PlayURL: "/static/test.mp4", CoverURL: "/static/test.png", CreateTime: createdAt,
	}
	if err := tx.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}

func TestFeedPaginationWithTiedTimestamps(t *testing.T) {
	service, tx, authors := feedFixture(t)
	base := time.Now().Truncate(time.Millisecond).Add(-time.Hour)
	seeded := make([]video.Video, 0, 29)
	for i := 0; i < 29; i++ {
		createdAt := base
		if i < 2 {
			createdAt = base.Add(-time.Millisecond)
		} else if i >= 27 {
			createdAt = base.Add(time.Millisecond)
		}
		seeded = append(seeded, seedFeedVideo(t, tx, authors[i%2].ID, createdAt))
	}

	// 29 条视频中有 25 条处于同一毫秒，预期三页分别为 10、10、9 条。
	// 第一页不传 limit，同时验证默认页大小。
	req := ListLatestRequest{}
	seen := 0
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		page, err := service.ListLatest(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		wantSize := min(10, len(seeded)-seen)
		if len(page.VideoList) != wantSize || page.HasMore != (seen+wantSize < len(seeded)) {
			t.Fatalf("page %d: size=%d, has_more=%v", pageNumber+1, len(page.VideoList), page.HasMore)
		}
		for _, item := range page.VideoList {
			want := seeded[len(seeded)-1-seen]
			if item.ID != want.ID || item.CreateTime != want.CreateTime.UnixMilli() {
				t.Fatalf("position %d: got video %d, want %d", seen, item.ID, want.ID)
			}
			wantAuthor := authors[(len(seeded)-1-seen)%2]
			if item.Author.ID != wantAuthor.ID || item.Author.Username != wantAuthor.Username {
				t.Fatalf("wrong author for video %d: %+v", item.ID, item.Author)
			}
			seen++
		}
		last := page.VideoList[len(page.VideoList)-1]
		if page.NextTime != last.CreateTime || page.NextID != last.ID {
			t.Fatalf("cursor does not reference the last returned video: %+v", page)
		}
		body, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "private-feed-password") || strings.Contains(string(body), "token_hash") {
			t.Fatal("feed exposed private account data")
		}
		req = ListLatestRequest{Limit: 10, LatestTime: page.NextTime, LatestID: page.NextID}
	}
	if seen != len(seeded) {
		t.Fatalf("returned %d videos, want %d", seen, len(seeded))
	}
	empty, err := service.ListLatest(context.Background(), req)
	if err != nil || len(empty.VideoList) != 0 || empty.VideoList == nil || empty.HasMore || empty.NextTime != 0 || empty.NextID != 0 {
		t.Fatalf("end page = %+v, error=%v", empty, err)
	}
}

func TestFeedEmptyAndExactPages(t *testing.T) {
	for _, count := range []int{0, 10, 20} {
		t.Run(fmt.Sprintf("videos_%d", count), func(t *testing.T) {
			service, tx, authors := feedFixture(t)
			for i := 0; i < count; i++ {
				seedFeedVideo(t, tx, authors[0].ID, time.Now().Add(-time.Hour).Truncate(time.Millisecond))
			}
			page, err := service.ListLatest(context.Background(), ListLatestRequest{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if page.VideoList == nil || len(page.VideoList) != min(count, 10) || page.HasMore != (count > 10) {
				t.Fatalf("unexpected first page: %+v", page)
			}
			if count == 20 {
				page, err = service.ListLatest(context.Background(), ListLatestRequest{
					Limit: 10, LatestTime: page.NextTime, LatestID: page.NextID,
				})
				if err != nil || len(page.VideoList) != 10 || page.HasMore {
					t.Fatalf("exact last page = %+v, error=%v", page, err)
				}
			}
		})
	}
}

func TestFeedUnknownAuthorAndMissingTimestamp(t *testing.T) {
	service, tx, authors := feedFixture(t)
	base := time.Now().Truncate(time.Millisecond).Add(-time.Hour)
	known := seedFeedVideo(t, tx, authors[0].ID, base)
	unknown := seedFeedVideo(t, tx, authors[1].ID+1_000_000, base.Add(time.Millisecond))
	legacy := seedFeedVideo(t, tx, authors[0].ID, base.Add(-time.Hour))
	if err := tx.Model(&video.Video{}).Where("id = ?", legacy.ID).UpdateColumn("create_time", nil).Error; err != nil {
		t.Fatal(err)
	}
	seedFeedVideo(t, tx, authors[0].ID, time.UnixMilli(0))
	page, err := service.ListLatest(context.Background(), ListLatestRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.VideoList) != 2 || page.HasMore {
		t.Fatalf("unexpected videos for valid timestamps: %+v", page)
	}
	if page.VideoList[0].ID != unknown.ID || page.VideoList[0].Author.ID != unknown.AuthorID || page.VideoList[0].Author.Username != "未知用户" {
		t.Fatalf("unexpected missing author response: %+v", page.VideoList[0])
	}
	if page.VideoList[1].ID != known.ID || page.VideoList[1].Author.Username != authors[0].Username {
		t.Fatalf("unexpected known author response: %+v", page.VideoList[1])
	}
}

func TestFeedPropagatesCancellation(t *testing.T) {
	service, _, _ := feedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.ListLatest(ctx, ListLatestRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
