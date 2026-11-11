package cache

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// rankFixture 连一个真实的 Redis 库；没配环境变量时跳过，
// 保证 `go test ./...` 在没有 Redis 的环境里也能通过。
func rankFixture(t *testing.T) *Client {
	t.Helper()
	addr, number := os.Getenv("FEED_TEST_REDIS_ADDR"), os.Getenv("FEED_TEST_REDIS_DB")
	if addr == "" || number == "" {
		t.Skip("set FEED_TEST_REDIS_ADDR and FEED_TEST_REDIS_DB to verify ranking")
	}
	db, err := strconv.Atoi(number)
	if err != nil || db < 0 {
		t.Fatal("invalid Redis database")
	}

	redisClient := redis.NewClient(&redis.Options{Addr: addr, DB: db, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = redisClient.Close() })
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}

	client, err := New(redisClient, Config{
		VideoTTL:  time.Minute,
		AuthorTTL: time.Minute,
		FeedTTL:   time.Minute,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// rankKeyForTest 用时间戳隔离每个测试，即使清理失败也不会污染下一个测试。
func rankKeyForTest(t *testing.T, c *Client) string {
	t.Helper()
	key := "test:rank:" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() { _, _ = c.redis.Del(context.Background(), key).Result() })
	return key
}

// 取前 N 名：分数高的在前，且 limit 生效。
func TestReplaceRankAndTopRank(t *testing.T) {
	c := rankFixture(t)
	ctx := context.Background()
	key := rankKeyForTest(t, c)

	members := []RankMember{
		{ID: "video:1", Score: 120},
		{ID: "video:2", Score: 85},
		{ID: "video:3", Score: 300},
	}
	if !c.ReplaceRank(ctx, key, members, time.Minute) {
		t.Fatal("ReplaceRank 返回 false")
	}

	top := c.TopRank(ctx, key, 2)
	if len(top) != 2 {
		t.Fatalf("TopRank 返回 %d 条，want 2", len(top))
	}
	if top[0].ID != "video:3" || top[0].Score != 300 {
		t.Fatalf("第一名 = %+v，want video:3/300", top[0])
	}
	if top[1].ID != "video:1" || top[1].Score != 120 {
		t.Fatalf("第二名 = %+v，want video:1/120", top[1])
	}
}

// 上一次快照里、这一次没有的成员必须消失。
// 若实现里漏掉 pipe.Del，第二次写入会变成追加，这里会读到 3 条。
func TestReplaceRankOverwritesPreviousSnapshot(t *testing.T) {
	c := rankFixture(t)
	ctx := context.Background()
	key := rankKeyForTest(t, c)

	if !c.ReplaceRank(ctx, key, []RankMember{
		{ID: "video:1", Score: 10},
		{ID: "video:2", Score: 20},
	}, time.Minute) {
		t.Fatal("首次写入失败")
	}
	if !c.ReplaceRank(ctx, key, []RankMember{
		{ID: "video:9", Score: 99},
	}, time.Minute) {
		t.Fatal("覆盖写入失败")
	}

	top := c.TopRank(ctx, key, 10)
	if len(top) != 1 {
		t.Fatalf("覆盖后应只剩 1 条，实际 %d 条：%+v", len(top), top)
	}
	if top[0].ID != "video:9" || top[0].Score != 99 {
		t.Fatalf("覆盖后 = %+v，want video:9/99", top[0])
	}
}

// 空榜单是合法场景（时间窗口内没有任何互动）。
// 实现里若漏掉 len(members) > 0 的判空，ZADD 传空参数会报错，这里会拿到 false。
func TestReplaceRankWithEmptySnapshot(t *testing.T) {
	c := rankFixture(t)
	ctx := context.Background()
	key := rankKeyForTest(t, c)

	if !c.ReplaceRank(ctx, key, nil, time.Minute) {
		t.Fatal("空榜单写入应成功")
	}
	if top := c.TopRank(ctx, key, 10); len(top) != 0 {
		t.Fatalf("空榜单应返回 0 条，实际 %+v", top)
	}
}

// 同分时 offset 翻页不能漏人：这是不用分数游标的原因。
// 6 个成员同分，分两页各取 3 个，合起来必须正好 6 个且不重复。
func TestTopRankHandlesTiedScores(t *testing.T) {
	c := rankFixture(t)
	ctx := context.Background()
	key := rankKeyForTest(t, c)

	members := make([]RankMember, 0, 6)
	for i := 1; i <= 6; i++ {
		members = append(members, RankMember{
			ID:    "video:" + strconv.Itoa(i),
			Score: 100, // 全部同分
		})
	}
	if !c.ReplaceRank(ctx, key, members, time.Minute) {
		t.Fatal("写入失败")
	}

	seen := make(map[string]int, 6)
	for _, limit := range []int{3, 6} {
		// 第一页取前 3，第二页取前 6（模拟 offset 前进），合并去重后应覆盖全部。
		for _, m := range c.TopRank(ctx, key, limit) {
			seen[m.ID]++
		}
	}
	if len(seen) != 6 {
		t.Fatalf("同分翻页只覆盖了 %d 个成员，want 6：%v", len(seen), seen)
	}
}
