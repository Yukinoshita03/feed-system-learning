package feed

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"feed-system-learning/internal/account"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/dbtest"
	"feed-system-learning/internal/video"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type rankingFixture struct {
	db        *gorm.DB
	cache     *cache.Client
	refresher *RankingRefresher
	filler    *BucketFiller
	author    uint
}

// 榜单链路需要 MySQL（明细和桶）与 Redis（ZSET）；缺任何一边都跳过，
// 保证没有依赖的环境下 `go test ./...` 依然通过。
func newRankingFixture(t *testing.T) *rankingFixture {
	t.Helper()
	dsn := os.Getenv("FEED_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FEED_TEST_MYSQL_DSN to a dedicated database for ranking tests")
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
	dbtest.Migrate(t, database,
		&account.Account{}, &video.Video{}, &video.Like{},
		&video.VideoView{}, &video.LikeHourly{})

	tx := database.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })

	f := &rankingFixture{db: tx}

	author := account.Account{
		Username: "rank_author_" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Password: "test-password-hash",
	}
	if err := tx.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	f.author = author.ID

	if addr, number := os.Getenv("FEED_TEST_REDIS_ADDR"), os.Getenv("FEED_TEST_REDIS_DB"); addr != "" && number != "" {
		dbNumber, err := strconv.Atoi(number)
		if err != nil || dbNumber < 0 {
			t.Fatal("invalid Redis database")
		}
		redisClient := redis.NewClient(&redis.Options{Addr: addr, DB: dbNumber, ContextTimeoutEnabled: true})
		t.Cleanup(func() { _ = redisClient.Close() })
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		f.cache, err = cache.New(redisClient, cache.Config{
			VideoTTL: time.Minute, AuthorTTL: time.Minute,
			FeedTTL: time.Minute, Timeout: time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	f.filler = NewBucketFiller(tx)
	repo := NewFeedRepository(tx, video.NewVideoRepository(tx, nil), f.cache)
	f.refresher = NewRankingRefresher(repo, f.cache)
	return f
}

// publish 直接建视频记录，不走上层业务校验，测试只关心 ID。
func (f *rankingFixture) publish(t *testing.T, title string) video.Video {
	t.Helper()
	item := video.Video{
		AuthorID: f.author, Title: title,
		PlayURL: "/static/rank.mp4", CoverURL: "/static/rank.png",
	}
	if err := f.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return item
}

// likeAt 在指定时间造一条点赞记录。
func (f *rankingFixture) likeAt(t *testing.T, videoID, accountID uint, at time.Time) {
	t.Helper()
	if err := f.db.Create(&video.Like{VideoID: videoID, AccountID: accountID, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
}

// 填桶任务只处理"已经结束的完整小时"，所以用整点回退来造数据。
func TestBucketFillerAggregatesHourly(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	first := f.publish(t, "桶测试-A")
	second := f.publish(t, "桶测试-B")

	// 上一个完整小时：first 3 个赞，second 1 个赞。
	lastHour := time.Now().Truncate(time.Hour).Add(-time.Hour).Add(10 * time.Minute)
	f.likeAt(t, first.ID, 1001, lastHour)
	f.likeAt(t, first.ID, 1002, lastHour)
	f.likeAt(t, first.ID, 1003, lastHour)
	f.likeAt(t, second.ID, 1004, lastHour)

	if _, err := f.filler.Fill(ctx); err != nil {
		t.Fatal(err)
	}

	var rows []video.LikeHourly
	if err := f.db.Where("video_id IN ?", []uint{first.ID, second.ID}).Order("video_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("桶表有 %d 行，want 2：%+v", len(rows), rows)
	}
	if rows[0].VideoID != first.ID || rows[0].Count != 3 {
		t.Fatalf("first 的桶 = %+v，want count=3", rows[0])
	}
	if rows[1].VideoID != second.ID || rows[1].Count != 1 {
		t.Fatalf("second 的桶 = %+v，want count=1", rows[1])
	}
	if rows[0].Bucket.Minute() != 0 || rows[0].Bucket.Second() != 0 {
		t.Fatalf("桶时间没有对齐到整点：%v", rows[0].Bucket)
	}
}

// 填桶任务必须幂等：重复跑不能把 count 累加。
func TestBucketFillerIsIdempotent(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	item := f.publish(t, "幂等测试")
	lastHour := time.Now().Truncate(time.Hour).Add(-time.Hour).Add(5 * time.Minute)
	f.likeAt(t, item.ID, 2001, lastHour)

	for i := 0; i < 3; i++ {
		if _, err := f.filler.Fill(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var row video.LikeHourly
	if err := f.db.Where("video_id = ?", item.ID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Count != 1 {
		t.Fatalf("跑三次后 count=%d，want 1（重复执行不能累加）", row.Count)
	}
}

// 聚合按窗口求和，并按分数倒序。
func TestAggregateLikesFromBuckets(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	hot := f.publish(t, "热视频")
	warm := f.publish(t, "温视频")
	cold := f.publish(t, "冷视频")

	now := time.Now().Truncate(time.Hour)
	// 都是 24 小时内的桶
	buckets := []video.LikeHourly{
		{VideoID: hot.ID, Bucket: now.Add(-2 * time.Hour), Count: 50},
		{VideoID: hot.ID, Bucket: now.Add(-1 * time.Hour), Count: 30},
		{VideoID: warm.ID, Bucket: now.Add(-3 * time.Hour), Count: 60},
		{VideoID: cold.ID, Bucket: now.Add(-30 * time.Hour), Count: 999}, // 窗口外
	}
	if err := f.db.Create(&buckets).Error; err != nil {
		t.Fatal(err)
	}

	scores, err := f.refresher.repo.aggregateLikes(ctx, Window24h, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 2 {
		t.Fatalf("窗口内应只有 2 个视频，实际 %d：%+v", len(scores), scores)
	}
	if scores[0].VideoID != hot.ID || scores[0].Score != 80 {
		t.Fatalf("第一名 = %+v，want hot/80", scores[0])
	}
	if scores[1].VideoID != warm.ID || scores[1].Score != 60 {
		t.Fatalf("第二名 = %+v，want warm/60", scores[1])
	}
}

// 已删除视频不能出现在榜单里。
func TestAggregateLikesExcludesDeletedVideos(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	gone := f.publish(t, "将被删除")
	alive := f.publish(t, "保留")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.LikeHourly{
		{VideoID: gone.ID, Bucket: now.Add(-time.Hour), Count: 100},
		{VideoID: alive.ID, Bucket: now.Add(-time.Hour), Count: 10},
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 只删视频，留下桶，模拟"删除视频后下一轮清理前"的中间状态。
	if err := f.db.Unscoped().Delete(&video.Video{}, gone.ID).Error; err != nil {
		t.Fatal(err)
	}

	scores, err := f.refresher.repo.aggregateLikes(ctx, Window24h, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 || scores[0].VideoID != alive.ID {
		t.Fatalf("结果 = %+v，want 只有 alive", scores)
	}
}

// 重算任务把聚合结果写进 ZSET，并整体覆盖上一轮。
func TestRefreshWritesRankingToRedis(t *testing.T) {
	f := newRankingFixture(t)
	if f.cache == nil {
		t.Skip("set FEED_TEST_REDIS_ADDR and FEED_TEST_REDIS_DB to verify ranking refresh")
	}
	ctx := context.Background()
	first := f.publish(t, "榜单-A")
	second := f.publish(t, "榜单-B")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.LikeHourly{
		{VideoID: first.ID, Bucket: now.Add(-time.Hour), Count: 5},
		{VideoID: second.ID, Bucket: now.Add(-time.Hour), Count: 90},
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := f.refresher.Refresh(ctx, RankingLikes, Window24h); err != nil {
		t.Fatal(err)
	}

	top := f.cache.TopRank(ctx, rankKey(RankingLikes, Window24h), 10)
	if len(top) != 2 {
		t.Fatalf("ZSET 里有 %d 条，want 2：%+v", len(top), top)
	}
	if top[0].ID != strconv.FormatUint(uint64(second.ID), 10) || top[0].Score != 90 {
		t.Fatalf("第一名 = %+v，want %d/90", top[0], second.ID)
	}

	// 再跑一次：first 的桶清零后应该从榜上消失（整体覆盖而不是累积）。
	f.db.Where("video_id = ?", first.ID).Delete(&video.LikeHourly{})
	if err := f.refresher.Refresh(ctx, RankingLikes, Window24h); err != nil {
		t.Fatal(err)
	}
	top = f.cache.TopRank(ctx, rankKey(RankingLikes, Window24h), 10)
	if len(top) != 1 {
		t.Fatalf("覆盖后应只剩 1 条，实际 %+v", top)
	}
}

// 过期桶要被清理掉，否则预聚合表会无限增长。
func TestCleanBucketsRemovesExpiredRows(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	item := f.publish(t, "清理测试")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.LikeHourly{
		{VideoID: item.ID, Bucket: now.Add(-20 * 24 * time.Hour), Count: 1}, // 20 天前，过期
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Count: 2},           // 1 小时前，保留
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleted, err := f.filler.CleanBuckets(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("没有删除任何过期桶")
	}

	var remaining []video.LikeHourly
	if err := f.db.Where("video_id = ?", item.ID).Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Count != 2 {
		t.Fatalf("清理后剩余 = %+v，want 只保留最近 1 小时的桶", remaining)
	}
}
