package feed

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"feed-system-learning/internal/account"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/video"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type contentFixture struct {
	db      *gorm.DB
	redis   *redis.Client
	cache   *cache.Client
	videos  *video.VideoRepository
	feed    *FeedService
	author  account.Account
	created []video.Video
}

func newContentFixture(t *testing.T) *contentFixture {
	t.Helper()
	dsn, addr, number := os.Getenv("FEED_TEST_MYSQL_DSN"), os.Getenv("FEED_TEST_REDIS_ADDR"), os.Getenv("FEED_TEST_REDIS_DB")
	if dsn == "" || addr == "" || number == "" {
		t.Skip("set dedicated FEED_TEST_MYSQL_DSN, FEED_TEST_REDIS_ADDR and FEED_TEST_REDIS_DB")
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
	if err := database.AutoMigrate(&account.Account{}, &video.Video{}, &video.Like{}, &video.Comment{}); err != nil {
		t.Fatal(err)
	}
	redisDB, err := strconv.Atoi(number)
	if err != nil || redisDB < 0 {
		t.Fatal("invalid Redis database")
	}
	client := redis.NewClient(&redis.Options{
		Addr: addr, Password: os.Getenv("FEED_TEST_REDIS_PASSWORD"), DB: redisDB,
		ContextTimeoutEnabled: true, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	dataCache, err := cache.New(client, cache.Config{
		VideoTTL: 3 * time.Minute, AuthorTTL: 2 * time.Minute, FeedTTL: time.Minute, Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &contentFixture{db: database, redis: client, cache: dataCache}
	f.videos = video.NewVideoRepository(database, dataCache)
	f.feed = NewFeedService(NewFeedRepository(database, f.videos, dataCache))
	f.author = account.Account{Username: fmt.Sprintf("content_%d", time.Now().UnixNano()), Password: "test-password-hash"}
	if err := database.Create(&f.author).Error; err != nil {
		t.Fatal(err)
	}
	removeKeys := func() {
		keys := []string{cache.LatestFeedKey, cache.AuthorVideosKey(f.author.ID)}
		for _, item := range f.created {
			keys = append(keys, cache.VideoKey(item.ID))
		}
		for _, key := range append([]string{}, keys...) {
			keys = append(keys, "cache:version:"+key)
		}
		if err := client.Del(context.Background(), keys...).Err(); err != nil {
			t.Errorf("cache cleanup: %v", err)
		}
	}
	removeKeys()
	t.Cleanup(func() {
		removeKeys()
		if err := database.Where("author_id = ?", f.author.ID).Delete(&video.Video{}).Error; err != nil {
			t.Errorf("video cleanup: %v", err)
		}
		if err := database.Delete(&f.author).Error; err != nil {
			t.Errorf("author cleanup: %v", err)
		}
	})
	return f
}

func (f *contentFixture) publish(t *testing.T) video.Video {
	t.Helper()
	item := video.Video{AuthorID: f.author.ID, Title: "cache-original", PlayURL: "/static/a.mp4", CoverURL: "/static/a.png"}
	if err := f.videos.CreateVideo(context.Background(), &item); err != nil {
		t.Fatal(err)
	}
	f.created = append(f.created, item)
	return item
}

func TestContentCacheHitsAvoidVideoQueries(t *testing.T) {
	f := newContentFixture(t)
	item := f.publish(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := f.videos.GetByID(ctx, item.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.videos.ListByAuthorID(ctx, f.author.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.feed.ListLatest(ctx, ListLatestRequest{}, 0); err != nil {
			t.Fatal(err)
		}
	}
	var queries atomic.Int64
	if err := f.db.Callback().Query().Before("gorm:query").Register("content:count_video_queries", func(db *gorm.DB) {
		if db.Statement.Table == "videos" {
			queries.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.videos.GetByID(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.videos.ListByAuthorID(ctx, f.author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.feed.ListLatest(ctx, ListLatestRequest{}, 0); err != nil {
		t.Fatal(err)
	}
	if queries.Load() != 0 {
		t.Fatalf("warm caches queried videos %d times", queries.Load())
	}
	for key, ttl := range map[string]time.Duration{
		cache.VideoKey(item.ID): 3 * time.Minute, cache.AuthorVideosKey(f.author.ID): 2 * time.Minute, cache.LatestFeedKey: time.Minute,
	} {
		got, err := f.redis.PTTL(ctx, key).Result()
		if err != nil || got <= 0 || got > ttl || got < ttl-5*time.Second {
			t.Fatalf("TTL %s=%v, error=%v", key, got, err)
		}
	}
	queries.Store(0)
	if _, err := f.feed.ListLatest(ctx, ListLatestRequest{Limit: 5}, 0); err != nil {
		t.Fatal(err)
	}
	if queries.Load() == 0 {
		t.Fatal("non-default page size unexpectedly reused first-page cache")
	}
}

func TestContentCacheMutationInvalidation(t *testing.T) {
	f := newContentFixture(t)
	ctx := context.Background()
	item := f.publish(t)
	if _, err := f.videos.GetByID(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.videos.ListByAuthorID(ctx, f.author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.feed.ListLatest(ctx, ListLatestRequest{}, 0); err != nil {
		t.Fatal(err)
	}
	item.Title = "cache-updated"
	if err := f.videos.UpdateVideo(ctx, &item); err != nil {
		t.Fatal(err)
	}
	if f.redis.Exists(ctx, cache.VideoKey(item.ID)).Val() != 0 {
		t.Fatal("update left old detail cached")
	}
	page, err := f.feed.ListLatest(ctx, ListLatestRequest{}, 0)
	if err != nil || page.VideoList[0].Title != item.Title {
		t.Fatalf("updated feed=%+v, error=%v", page, err)
	}
	second := f.publish(t)
	if f.redis.Exists(ctx, cache.AuthorVideosKey(f.author.ID), cache.LatestFeedKey).Val() != 0 {
		t.Fatal("publish left list caches populated")
	}
	if _, err := f.videos.ListByAuthorID(ctx, f.author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.feed.ListLatest(ctx, ListLatestRequest{}, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.videos.DeleteVideo(ctx, second.ID, f.author.ID); err != nil {
		t.Fatal(err)
	}
	if f.redis.Exists(ctx, cache.VideoKey(second.ID), cache.AuthorVideosKey(f.author.ID), cache.LatestFeedKey).Val() != 0 {
		t.Fatal("delete left old caches")
	}
	if _, err := f.videos.GetByID(ctx, second.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted video error=%v", err)
	}
	listed, err := f.videos.ListByAuthorID(ctx, f.author.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != item.ID {
		t.Fatalf("author videos=%+v, error=%v", listed, err)
	}
}

func TestContentCacheRejectsOldConcurrentFill(t *testing.T) {
	for _, scenario := range []string{"update_detail", "delete_detail", "publish_author_list", "publish_first_feed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newContentFixture(t)
			item := f.publish(t)
			ctx := context.Background()
			loaded, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var blocked atomic.Bool
			if err := f.db.Callback().Query().After("gorm:query").Register("content:block_old_snapshot", func(db *gorm.DB) {
				if db.Statement.Table == "videos" && blocked.CompareAndSwap(false, true) {
					close(loaded)
					<-release
				}
			}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			key := cache.VideoKey(item.ID)
			if scenario == "publish_author_list" {
				key = cache.AuthorVideosKey(f.author.ID)
			}
			if scenario == "publish_first_feed" {
				key = cache.LatestFeedKey
			}
			go func() {
				var err error
				switch scenario {
				case "publish_author_list":
					_, err = f.videos.ListByAuthorID(ctx, f.author.ID)
				case "publish_first_feed":
					_, err = f.feed.ListLatest(ctx, ListLatestRequest{}, 0)
				default:
					_, err = f.videos.GetByID(ctx, item.ID)
				}
				done <- err
			}()
			select {
			case <-loaded:
			case <-time.After(3 * time.Second):
				t.Fatal("old query did not reach database snapshot")
			}
			switch scenario {
			case "update_detail":
				item.Title = "concurrently-updated"
				if err := f.videos.UpdateVideo(ctx, &item); err != nil {
					t.Fatal(err)
				}
			case "delete_detail":
				if err := f.videos.DeleteVideo(ctx, item.ID, f.author.ID); err != nil {
					t.Fatal(err)
				}
			default:
				f.publish(t)
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
			if f.redis.Exists(ctx, key).Val() != 0 {
				t.Fatal("old snapshot was published after invalidation")
			}
			switch scenario {
			case "update_detail":
				got, err := f.videos.GetByID(ctx, item.ID)
				if err != nil || got.Title != item.Title {
					t.Fatalf("updated result=%+v, error=%v", got, err)
				}
			case "delete_detail":
				if _, err := f.videos.GetByID(ctx, item.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("deleted result=%v", err)
				}
			case "publish_author_list":
				got, err := f.videos.ListByAuthorID(ctx, f.author.ID)
				if err != nil || len(got) != 2 {
					t.Fatalf("author result=%+v, error=%v", got, err)
				}
			case "publish_first_feed":
				got, err := f.feed.ListLatest(ctx, ListLatestRequest{}, 0)
				if err != nil || len(got.VideoList) != 2 {
					t.Fatalf("feed result=%+v, error=%v", got, err)
				}
			}
		})
	}
}

func TestContentCacheCorruptDataAndExpiry(t *testing.T) {
	f := newContentFixture(t)
	item := f.publish(t)
	ctx := context.Background()
	if _, err := f.videos.GetByID(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	key := cache.VideoKey(item.ID)
	if err := f.redis.PExpire(ctx, key, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for f.redis.Exists(ctx, key).Val() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.redis.Exists(ctx, key).Val() != 0 {
		t.Fatal("cache did not expire")
	}
	if _, err := f.videos.GetByID(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.redis.Set(ctx, key, "not-json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := f.videos.GetByID(ctx, item.ID)
	if err != nil || got.ID != item.ID {
		t.Fatalf("corrupt fallback=%+v, error=%v", got, err)
	}
	var cached video.Video
	if _, hit := f.cache.Load(ctx, key, &cached); !hit || cached.ID != item.ID {
		t.Fatal("corrupt value was not repaired")
	}
}

func TestContentCacheVersionExpiryRejectsOldFill(t *testing.T) {
	f := newContentFixture(t)
	item := f.publish(t)
	ctx := context.Background()
	key := cache.VideoKey(item.ID)
	var value video.Video
	old, _ := f.cache.Load(ctx, key, &value)
	// 模拟慢查询期间版本过期；版本不存在也不能接受旧查询的回填。
	if err := f.redis.Del(ctx, "cache:version:"+key).Err(); err != nil {
		t.Fatal(err)
	}
	if f.cache.Store(ctx, old, time.Minute, item) {
		t.Fatal("old fill was accepted after version expiry")
	}
	current, _ := f.cache.Load(ctx, key, &value)
	if f.cache.Store(ctx, old, time.Minute, item) {
		t.Fatal("regenerating the version revived an old fill")
	}
	if !f.cache.Store(ctx, current, time.Minute, item) {
		t.Fatal("current version was rejected")
	}
}

func TestContentCacheRedisFailureUsesMySQL(t *testing.T) {
	f := newContentFixture(t)
	item := f.publish(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	broken := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, DialTimeout: 20 * time.Millisecond, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = broken.Close() })
	dataCache, err := cache.New(broken, f.cache.Config())
	if err != nil {
		t.Fatal(err)
	}
	repo := video.NewVideoRepository(f.db, dataCache)
	ctx := context.Background()
	if got, err := repo.GetByID(ctx, item.ID); err != nil || got.ID != item.ID {
		t.Fatalf("detail fallback=%+v, error=%v", got, err)
	}
	if got, err := repo.ListByAuthorID(ctx, f.author.ID); err != nil || len(got) != 1 {
		t.Fatalf("author fallback=%+v, error=%v", got, err)
	}
	service := NewFeedService(NewFeedRepository(f.db, repo, dataCache))
	if got, err := service.ListLatest(ctx, ListLatestRequest{}, 0); err != nil || len(got.VideoList) != 1 {
		t.Fatalf("feed fallback=%+v, error=%v", got, err)
	}
	item.Title = "saved-during-redis-failure"
	if err := repo.UpdateVideo(ctx, &item); err != nil {
		t.Fatal(err)
	}
	var stored video.Video
	if err := f.db.First(&stored, item.ID).Error; err != nil || stored.Title != item.Title {
		t.Fatalf("database write=%+v, error=%v", stored, err)
	}
}
