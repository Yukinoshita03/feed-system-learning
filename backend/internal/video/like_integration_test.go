package video

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"feed-system-learning/internal/account"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/dbtest"
	"feed-system-learning/internal/notification"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type likeFixture struct {
	db       *gorm.DB
	redis    *redis.Client
	videos   *VideoRepository
	service  *LikeService
	accounts []account.Account
	items    []Video
}

func newLikeFixture(t *testing.T) *likeFixture {
	t.Helper()
	dsn := os.Getenv("FEED_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FEED_TEST_MYSQL_DSN to a dedicated empty test database")
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
	dbtest.Migrate(t, database, &account.Account{}, &Video{}, &Like{}, &Comment{}, &Favorite{}, &notification.Notification{}, &notification.OutboxMessage{})
	f := &likeFixture{db: database}
	var dataCache *cache.Client
	if addr, number := os.Getenv("FEED_TEST_REDIS_ADDR"), os.Getenv("FEED_TEST_REDIS_DB"); addr != "" && number != "" {
		dbNumber, err := strconv.Atoi(number)
		if err != nil || dbNumber < 0 {
			t.Fatal("invalid Redis database")
		}
		f.redis = redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber, Password: os.Getenv("FEED_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true})
		t.Cleanup(func() { _ = f.redis.Close() })
		if err := f.redis.Ping(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		dataCache, err = cache.New(f.redis, cache.Config{VideoTTL: time.Minute, AuthorTTL: time.Minute, FeedTTL: time.Minute, Timeout: 200 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.videos = NewVideoRepository(database, dataCache)
	f.service = NewLikeService(NewLikeRepository(database, f.videos))
	for i := 0; i < 13; i++ {
		f.accounts = append(f.accounts, account.Account{Username: fmt.Sprintf("like_%d_%d", time.Now().UnixNano(), i), Password: "test-password-hash"})
	}
	if err := database.Create(&f.accounts).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, item := range f.items {
			if err := database.Where("video_id = ?", item.ID).Delete(&Like{}).Error; err != nil {
				t.Error(err)
			}
			if err := database.Delete(&item).Error; err != nil {
				t.Error(err)
			}
			if f.redis != nil {
				keys := []string{cache.VideoKey(item.ID), cache.AuthorVideosKey(item.AuthorID), cache.LatestFeedKey}
				for _, key := range append([]string{}, keys...) {
					keys = append(keys, "cache:version:"+key)
				}
				if err := f.redis.Del(context.Background(), keys...).Err(); err != nil {
					t.Error(err)
				}
			}
		}
		for _, user := range f.accounts {
			if err := database.Where("receiver_id = ? OR actor_id = ?", user.ID, user.ID).Delete(&notification.Notification{}).Error; err != nil {
				t.Error(err)
			}
			if err := database.Delete(&user).Error; err != nil {
				t.Error(err)
			}
		}
	})
	return f
}

func (f *likeFixture) publish(t *testing.T) Video {
	t.Helper()
	item := Video{AuthorID: f.accounts[0].ID, Title: "like-test", PlayURL: "/static/test.mp4", CoverURL: "/static/test.png"}
	if err := f.videos.CreateVideo(context.Background(), &item); err != nil {
		t.Fatal(err)
	}
	f.items = append(f.items, item)
	return item
}

func (f *likeFixture) assertCount(t *testing.T, id uint, want int64) {
	t.Helper()
	var item Video
	var count int64
	if err := f.db.First(&item, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&Like{}).Where("video_id = ?", id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if item.LikesCount != want || count != want {
		t.Fatalf("counter=%d, records=%d, want=%d", item.LikesCount, count, want)
	}
}

func runLikeWorkers(t *testing.T, n int, work func(context.Context, int) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	errors := make(chan error, n)
	var workers sync.WaitGroup
	for i := 0; i < n; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			if err := work(ctx, index); err != nil {
				errors <- err
			}
		}(i)
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestLikeConcurrentDuplicateRequests(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	actor := f.accounts[1].ID
	runLikeWorkers(t, 16, func(ctx context.Context, _ int) error { _, err := f.service.Like(ctx, item.ID, actor); return err })
	f.assertCount(t, item.ID, 1)
	if count := f.countOutbox(t, item.ID); count != 1 {
		t.Fatalf("outbox events after duplicate likes=%d, want=1", count)
	}
	if err := f.db.Create(&Like{VideoID: item.ID, AccountID: actor}).Error; err == nil {
		t.Fatal("database accepted a duplicate like")
	}
	runLikeWorkers(t, 16, func(ctx context.Context, _ int) error { _, err := f.service.Unlike(ctx, item.ID, actor); return err })
	f.assertCount(t, item.ID, 0)
	if count := f.countOutbox(t, item.ID); count != 1 {
		t.Fatalf("outbox events after unlike=%d, want=1", count)
	}
}

func TestLikeConcurrentAccountsAndMixedChanges(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	runLikeWorkers(t, 12, func(ctx context.Context, index int) error {
		_, err := f.service.Like(ctx, item.ID, f.accounts[index+1].ID)
		return err
	})
	f.assertCount(t, item.ID, 12)
	runLikeWorkers(t, 12, func(ctx context.Context, index int) error {
		for round := 0; round < 8; round++ {
			var err error
			if round%2 == 0 {
				_, err = f.service.Like(ctx, item.ID, f.accounts[index+1].ID)
			} else {
				_, err = f.service.Unlike(ctx, item.ID, f.accounts[index+1].ID)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	f.assertCount(t, item.ID, 0)
}

func TestLikeCounterFailureRollsBackRecord(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	injected := errors.New("injected counter failure")
	if err := f.db.Callback().Update().Before("gorm:update").Register("like:counter_failure", func(db *gorm.DB) {
		if db.Statement.Table == "videos" {
			db.AddError(injected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Like(context.Background(), item.ID, f.accounts[1].ID)
	if !errors.Is(err, injected) {
		t.Fatalf("error=%v, want counter failure", err)
	}
	f.assertCount(t, item.ID, 0)
	if count := f.countOutbox(t, item.ID); count != 0 {
		t.Fatalf("outbox events after transaction rollback=%d, want=0", count)
	}
}

func (f *likeFixture) countOutbox(t *testing.T, videoID uint) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&notification.OutboxMessage{}).
		Where("JSON_EXTRACT(payload, '$.video_id') = ?", videoID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestLikeListScopeOrderAndDelete(t *testing.T) {
	f := newLikeFixture(t)
	first, second := f.publish(t), f.publish(t)
	ctx := context.Background()
	a, b := f.accounts[1].ID, f.accounts[2].ID
	for _, target := range []uint{first.ID, second.ID, first.ID} {
		if _, err := f.service.Like(ctx, target, a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.Like(ctx, first.ID, b); err != nil {
		t.Fatal(err)
	}
	f.assertCount(t, first.ID, 2)
	listed, err := f.service.ListMyLikedVideos(ctx, a)
	if err != nil || len(listed) != 2 || listed[0].ID != second.ID || listed[1].ID != first.ID {
		t.Fatalf("list=%+v, error=%v", listed, err)
	}
	if err := f.videos.DeleteVideo(ctx, second.ID, f.accounts[0].ID); err != nil {
		t.Fatal(err)
	}
	listed, err = f.service.ListMyLikedVideos(ctx, a)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("after delete=%+v, error=%v", listed, err)
	}
	var remaining int64
	if err := f.db.Model(&Like{}).Where("video_id = ?", second.ID).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("orphan likes=%d, error=%v", remaining, err)
	}
}

func TestLikeConcurrentDeleteLeavesNoOrphans(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	runLikeWorkers(t, 13, func(ctx context.Context, index int) error {
		if index == 12 {
			return f.videos.DeleteVideo(ctx, item.ID, f.accounts[0].ID)
		}
		_, err := f.service.Like(ctx, item.ID, f.accounts[index+1].ID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	})
	var videos, likes int64
	if err := f.db.Model(&Video{}).Where("id = ?", item.ID).Count(&videos).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&Like{}).Where("video_id = ?", item.ID).Count(&likes).Error; err != nil {
		t.Fatal(err)
	}
	if videos != 0 || likes != 0 {
		t.Fatalf("after delete: videos=%d, likes=%d", videos, likes)
	}
}

func TestLikeInvalidatesConcurrentOldCountFill(t *testing.T) {
	f := newLikeFixture(t)
	if f.redis == nil {
		t.Skip("set dedicated Redis configuration for cache invalidation test")
	}
	item := f.publish(t)
	loaded, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var blocked atomic.Bool
	if err := f.db.Callback().Query().After("gorm:query").Register("like:old_count", func(db *gorm.DB) {
		if db.Statement.Table == "videos" && blocked.CompareAndSwap(false, true) {
			close(loaded)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.videos.GetByID(context.Background(), item.ID); done <- err }()
	select {
	case <-loaded:
	case <-time.After(3 * time.Second):
		t.Fatal("old read did not finish SQL")
	}
	if _, err := f.service.Like(context.Background(), item.ID, f.accounts[1].ID); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old reader did not finish")
	}
	if f.redis.Exists(context.Background(), cache.VideoKey(item.ID)).Val() != 0 {
		t.Fatal("old zero count was cached after like")
	}
	got, err := f.videos.GetByID(context.Background(), item.ID)
	if err != nil || got.LikesCount != 1 {
		t.Fatalf("new count=%+v, error=%v", got, err)
	}
}
