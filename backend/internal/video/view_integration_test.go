package video

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"feed-system-learning/internal/account"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/dbtest"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type viewFixture struct {
	db     *gorm.DB
	redis  *redis.Client
	views  *ViewRepository
	videos *VideoRepository
	author uint
}

// 去重依赖 Redis，所以 Redis 是可选的：没有配置时跳过去重相关断言，
// 只验证不依赖缓存的计数、删除和清理逻辑。
func newViewFixture(t *testing.T) *viewFixture {
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
	dbtest.Migrate(t, database, &account.Account{}, &Video{}, &Like{}, &Comment{}, &Favorite{}, &VideoView{})

	f := &viewFixture{db: database}
	var dataCache *cache.Client
	if addr, number := os.Getenv("FEED_TEST_REDIS_ADDR"), os.Getenv("FEED_TEST_REDIS_DB"); addr != "" && number != "" {
		dbNumber, err := strconv.Atoi(number)
		if err != nil || dbNumber < 0 {
			t.Fatal("invalid Redis database")
		}
		f.redis = redis.NewClient(&redis.Options{
			Addr: addr, DB: dbNumber,
			Password:              os.Getenv("FEED_TEST_REDIS_PASSWORD"),
			ContextTimeoutEnabled: true,
		})
		t.Cleanup(func() { _ = f.redis.Close() })
		if err := f.redis.Ping(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		dataCache, err = cache.New(f.redis, cache.Config{
			VideoTTL: time.Minute, AuthorTTL: time.Minute,
			FeedTTL: time.Minute, Timeout: 200 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.videos = NewVideoRepository(database, dataCache)
	f.views = NewViewRepository(database, f.videos)

	author := account.Account{
		Username: fmt.Sprintf("view_author_%d", time.Now().UnixNano()),
		Password: "test-password-hash",
	}
	if err := database.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	f.author = author.ID
	t.Cleanup(func() {
		if err := database.Where("author_id = ?", author.ID).Delete(&Video{}).Error; err != nil {
			t.Error(err)
		}
		if err := database.Delete(&author).Error; err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *viewFixture) publish(t *testing.T) Video {
	t.Helper()
	item := Video{AuthorID: f.author, Title: "view-test", PlayURL: "/static/v.mp4", CoverURL: "/static/v.png"}
	if err := f.videos.CreateVideo(context.Background(), &item); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.db.Where("video_id = ?", item.ID).Delete(&VideoView{}).Error; err != nil {
			t.Error(err)
		}
		if err := f.db.Unscoped().Where("id = ?", item.ID).Delete(&Video{}).Error; err != nil {
			t.Error(err)
		}
	})
	return item
}

// viewRows 只数播放明细，不要求视频行还存在。
func (f *viewFixture) viewRows(t *testing.T, videoID uint) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&VideoView{}).Where("video_id = ?", videoID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func (f *viewFixture) counts(t *testing.T, videoID uint) (views int64, counter int64) {
	t.Helper()
	if err := f.db.Model(&VideoView{}).Where("video_id = ?", videoID).Count(&views).Error; err != nil {
		t.Fatal(err)
	}
	var item Video
	if err := f.db.Select("view_count").First(&item, videoID).Error; err != nil {
		t.Fatal(err)
	}
	return views, item.ViewCount
}

// 同一账号在去重窗口内重复播放只计一次，明细和冗余计数都只加一次。
func TestViewRecordsOncePerAccount(t *testing.T) {
	f := newViewFixture(t)
	if f.redis == nil {
		t.Skip("set FEED_TEST_REDIS_ADDR and FEED_TEST_REDIS_DB to verify deduplication")
	}
	ctx := context.Background()
	item := f.publish(t)
	const viewer = uint(700001)

	for i := 0; i < 3; i++ {
		counted, err := f.views.Record(ctx, item.ID, viewer)
		if err != nil {
			t.Fatal(err)
		}
		want := i == 0
		if counted != want {
			t.Fatalf("第 %d 次 counted=%v, want=%v", i+1, counted, want)
		}
	}

	views, counter := f.counts(t, item.ID)
	if views != 1 || counter != 1 {
		t.Fatalf("明细=%d 计数=%d, want 1/1", views, counter)
	}
}

// 不同账号各自占用一份去重额度，播放量按账号累加。
func TestViewCountsDifferentAccounts(t *testing.T) {
	f := newViewFixture(t)
	if f.redis == nil {
		t.Skip("set FEED_TEST_REDIS_ADDR and FEED_TEST_REDIS_DB to verify deduplication")
	}
	ctx := context.Background()
	item := f.publish(t)

	for _, viewer := range []uint{700011, 700012, 700013} {
		counted, err := f.views.Record(ctx, item.ID, viewer)
		if err != nil {
			t.Fatal(err)
		}
		if !counted {
			t.Fatalf("账号 %d 首次播放应为 counted=true", viewer)
		}
	}

	views, counter := f.counts(t, item.ID)
	if views != 3 || counter != 3 {
		t.Fatalf("明细=%d 计数=%d, want 3/3", views, counter)
	}
}

// 去重窗口过期后，同一账号再次播放会重新计数。
func TestViewCountsAgainAfterWindowExpires(t *testing.T) {
	f := newViewFixture(t)
	if f.redis == nil {
		t.Skip("set FEED_TEST_REDIS_ADDR and FEED_TEST_REDIS_DB to verify deduplication")
	}
	ctx := context.Background()
	item := f.publish(t)
	const viewer = uint(700021)

	if counted, err := f.views.Record(ctx, item.ID, viewer); err != nil || !counted {
		t.Fatalf("首次播放 counted=%v err=%v", counted, err)
	}
	// 直接删除标记，等价于 TTL 到期。
	if err := f.redis.Del(ctx, cache.ViewDedupKey(item.ID, viewer)).Err(); err != nil {
		t.Fatal(err)
	}
	if counted, err := f.views.Record(ctx, item.ID, viewer); err != nil || !counted {
		t.Fatalf("窗口过期后 counted=%v err=%v", counted, err)
	}

	views, counter := f.counts(t, item.ID)
	if views != 2 || counter != 2 {
		t.Fatalf("明细=%d 计数=%d, want 2/2", views, counter)
	}
}

// 视频不存在时不能留下指向空视频的播放明细。
func TestViewRejectsMissingVideo(t *testing.T) {
	f := newViewFixture(t)
	_, err := f.views.Record(context.Background(), 99999999, 700031)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("err=%v, want gorm.ErrRecordNotFound", err)
	}
}

// 删除视频时必须连带清理播放明细，否则会留下孤儿记录。
func TestViewDeleteVideoRemovesViews(t *testing.T) {
	f := newViewFixture(t)
	ctx := context.Background()
	item := f.publish(t)
	if _, err := f.views.Record(ctx, item.ID, 700041); err != nil {
		t.Fatal(err)
	}

	if err := f.videos.DeleteVideo(ctx, item.ID, f.author); err != nil {
		t.Fatal(err)
	}

	var remaining int64
	if err := f.db.Model(&VideoView{}).Where("video_id = ?", item.ID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("删除视频后仍有 %d 条播放明细", remaining)
	}
}

// CleanOrphans 负责收敛"播放上报与删视频交错"留下的孤儿明细。
func TestViewCleanOrphans(t *testing.T) {
	f := newViewFixture(t)
	ctx := context.Background()
	item := f.publish(t)
	if _, err := f.views.Record(ctx, item.ID, 700051); err != nil {
		t.Fatal(err)
	}

	// 直接删表里的视频行，模拟"删除时播放并发交错"留下的孤儿。
	// 这里只数明细表，不查 videos：视频行已经不存在了。
	if err := f.db.Unscoped().Delete(&Video{}, item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if views := f.viewRows(t, item.ID); views != 1 {
		t.Fatalf("构造的孤儿明细数=%d, want 1", views)
	}

	deleted, err := f.views.CleanOrphans(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("CleanOrphans 没有删除任何孤儿明细")
	}
	if views := f.viewRows(t, item.ID); views != 0 {
		t.Fatalf("清理后仍有 %d 条孤儿明细", views)
	}
}
