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

// 榜单链路需要 MySQL（明细、桶）与 Redis（ZSET）；缺任何一边都跳过，
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
		&account.Account{}, &video.Video{}, &video.Like{}, &video.Favorite{}, &video.Comment{},
		&video.VideoView{}, &video.InteractionHourly{})

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

// 上一个完整小时里的一点偏移，保证落在填桶任务的回溯窗口内。
func lastHourAt(minutes int) time.Time {
	return time.Now().Truncate(time.Hour).Add(-time.Hour).Add(time.Duration(minutes) * time.Minute)
}

// 填桶任务要覆盖所有互动类型，不只是点赞。
func TestBucketFillerCoversAllKinds(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	item := f.publish(t, "四类互动")
	at := lastHourAt(10)

	if err := f.db.Create(&video.Like{VideoID: item.ID, AccountID: 1001, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&video.Favorite{VideoID: item.ID, AccountID: 1002, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&video.Comment{VideoID: item.ID, AuthorID: 1003, Content: "测试评论", CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&video.VideoView{VideoID: item.ID, AccountID: 1004, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}

	if err := f.filler.Fill(ctx); err != nil {
		t.Fatal(err)
	}

	var rows []video.InteractionHourly
	if err := f.db.Where("video_id = ?", item.ID).Order("kind").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(bucketSources) {
		t.Fatalf("桶表有 %d 行，want %d：%+v", len(rows), len(bucketSources), rows)
	}
	got := make(map[string]int64, len(rows))
	for _, row := range rows {
		got[row.Kind] = row.Count
		if row.Bucket.Minute() != 0 || row.Bucket.Second() != 0 {
			t.Fatalf("%s 的桶时间没有对齐到整点：%v", row.Kind, row.Bucket)
		}
	}
	for _, kind := range []string{video.KindLike, video.KindFavorite, video.KindComment, video.KindView} {
		if got[kind] != 1 {
			t.Fatalf("%s 的 count=%d，want 1（全部桶：%v）", kind, got[kind], got)
		}
	}
}

// 填桶任务必须幂等：重复跑不能把 count 累加。
func TestBucketFillerIsIdempotent(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	item := f.publish(t, "幂等测试")
	at := lastHourAt(5)

	if err := f.db.Create(&video.Like{VideoID: item.ID, AccountID: 2001, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := f.filler.Fill(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var row video.InteractionHourly
	if err := f.db.Where("video_id = ? AND kind = ?", item.ID, video.KindLike).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Count != 1 {
		t.Fatalf("跑三次后 count=%d，want 1（重复执行不能累加）", row.Count)
	}
}

// 点赞榜只统计 like，不能被收藏/评论/播放的数量影响。
func TestAggregateLikesIgnoresOtherKinds(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	item := f.publish(t, "只算点赞")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.InteractionHourly{
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 10},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindFavorite, Count: 100},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindComment, Count: 200},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindView, Count: 300},
	}).Error; err != nil {
		t.Fatal(err)
	}

	scores, err := f.refresher.repo.aggregateLikes(ctx, Window24h, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 || scores[0].Score != 10 {
		t.Fatalf("点赞榜 = %+v，want 只有 like 的 10 分", scores)
	}
}

// 热度榜按 hotWeights 加权：10×1 + 5×2 + 2×3 + 100×0.1 = 36。
func TestAggregateHotAppliesWeights(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	item := f.publish(t, "加权测试")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.InteractionHourly{
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 10},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindFavorite, Count: 5},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindComment, Count: 2},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindView, Count: 100},
	}).Error; err != nil {
		t.Fatal(err)
	}

	scores, err := f.refresher.repo.aggregateHot(ctx, Window24h, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 {
		t.Fatalf("结果 = %+v，want 1 条", scores)
	}
	want := 10*1 + 5*2 + 2*3 + 100*0.1
	if scores[0].Score != want {
		t.Fatalf("热度分 = %v，want %v", scores[0].Score, want)
	}
}

// 未知的 kind 不能让整条查询失败，按 0 分处理。
func TestAggregateHotToleratesUnknownKind(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	item := f.publish(t, "未知信号")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.InteractionHourly{
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 4},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: "share", Count: 999},
	}).Error; err != nil {
		t.Fatal(err)
	}

	scores, err := f.refresher.repo.aggregateHot(ctx, Window24h, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 || scores[0].Score != 4 {
		t.Fatalf("结果 = %+v，want 只有 like 的 4 分（未知 kind 记 0）", scores)
	}
}

// 窗口外的桶要被排除。
func TestAggregateHonoursWindow(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	hot := f.publish(t, "窗口内")
	old := f.publish(t, "窗口外")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.InteractionHourly{
		{VideoID: hot.ID, Bucket: now.Add(-2 * time.Hour), Kind: video.KindLike, Count: 50},
		{VideoID: hot.ID, Bucket: now.Add(-1 * time.Hour), Kind: video.KindLike, Count: 30},
		{VideoID: old.ID, Bucket: now.Add(-30 * time.Hour), Kind: video.KindLike, Count: 999},
	}).Error; err != nil {
		t.Fatal(err)
	}

	scores, err := f.refresher.repo.aggregateLikes(ctx, Window24h, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 || scores[0].VideoID != hot.ID || scores[0].Score != 80 {
		t.Fatalf("结果 = %+v，want 只有窗口内的 hot/80", scores)
	}
}

// 已删除视频不能出现在任何一个榜里。
func TestAggregateExcludesDeletedVideos(t *testing.T) {
	f := newRankingFixture(t)
	ctx := context.Background()
	gone := f.publish(t, "将被删除")
	alive := f.publish(t, "保留")

	now := time.Now().Truncate(time.Hour)
	if err := f.db.Create(&[]video.InteractionHourly{
		{VideoID: gone.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 100},
		{VideoID: alive.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 10},
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 只删视频，留下桶，模拟"删除视频后下一轮清理前"的中间状态。
	if err := f.db.Unscoped().Delete(&video.Video{}, gone.ID).Error; err != nil {
		t.Fatal(err)
	}

	for name, aggregate := range map[string]func(context.Context, RankingWindow, int) ([]rankScore, error){
		"点赞榜": f.refresher.repo.aggregateLikes,
		"热度榜": f.refresher.repo.aggregateHot,
	} {
		scores, err := aggregate(ctx, Window24h, 10)
		if err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		if len(scores) != 1 || scores[0].VideoID != alive.ID {
			t.Fatalf("%s 结果 = %+v，want 只有 alive", name, scores)
		}
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
	if err := f.db.Create(&[]video.InteractionHourly{
		{VideoID: first.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 5},
		{VideoID: second.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 90},
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := f.refresher.Refresh(ctx, RankingLikes, Window24h); err != nil {
		t.Fatal(err)
	}
	top := f.cache.TopRank(ctx, rankKey(RankingLikes, Window24h), 10)
	if len(top) != 2 {
		t.Fatalf("点赞榜 ZSET 有 %d 条，want 2：%+v", len(top), top)
	}
	if top[0].ID != strconv.FormatUint(uint64(second.ID), 10) || top[0].Score != 90 {
		t.Fatalf("点赞榜第一名 = %+v，want %d/90", top[0], second.ID)
	}

	// 热度榜是独立的 key，不该混进点赞数据。
	if err := f.refresher.Refresh(ctx, RankingHot, Window24h); err != nil {
		t.Fatal(err)
	}
	hot := f.cache.TopRank(ctx, rankKey(RankingHot, Window24h), 10)
	if len(hot) != 2 {
		t.Fatalf("热度榜 ZSET 有 %d 条，want 2", len(hot))
	}
	// 只有 like 的桶，热度分应该等于点赞数（权重 1）。
	if hot[0].Score != 90 {
		t.Fatalf("热度榜第一名分数 = %v，want 90", hot[0].Score)
	}

	// 再跑一次：first 的桶删掉后应该从榜上消失（整体覆盖而不是累积）。
	f.db.Where("video_id = ?", first.ID).Delete(&video.InteractionHourly{})
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
	if err := f.db.Create(&[]video.InteractionHourly{
		{VideoID: item.ID, Bucket: now.Add(-20 * 24 * time.Hour), Kind: video.KindLike, Count: 1},
		{VideoID: item.ID, Bucket: now.Add(-time.Hour), Kind: video.KindLike, Count: 2},
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

	var remaining []video.InteractionHourly
	if err := f.db.Where("video_id = ?", item.ID).Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Count != 2 {
		t.Fatalf("清理后剩余 = %+v，want 只保留最近 1 小时的桶", remaining)
	}
}
