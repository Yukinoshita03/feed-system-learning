package cache

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type RankMember struct {
	ID    string
	Score float64
}

func (c *Client) ReplaceRank(ctx context.Context, key string, members []RankMember, ttl time.Duration) bool {
	if c == nil || c.redis == nil || key == "" || ttl < time.Millisecond {
		return false
	}

	pipe := c.redis.Pipeline()
	pipe.Del(ctx, key)
	if len(members) > 0 {
		values := make([]redis.Z, 0, len(members))
		for _, m := range members {
			values = append(values, redis.Z{Score: m.Score, Member: m.ID})
		}
		pipe.ZAdd(ctx, key, values...)
	}
	pipe.Expire(ctx, key, ttl)

	cacheCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	if _, err := pipe.Exec(cacheCtx); err != nil {
		log.Printf("重写榜单 %s 失败：%v", key, err)
		return false
	}
	return true
}
func (c *Client) TopRank(ctx context.Context, key string, limit int) []RankMember {
	if c == nil || c.redis == nil || key == "" || limit <= 0 {
		return nil
	}

	cacheCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	values, err := c.redis.ZRevRangeWithScores(cacheCtx, key, 0, int64(limit-1)).Result()
	if err != nil {
		log.Printf("读取榜单 %s 失败：%v", key, err)
		return nil
	}

	members := make([]RankMember, 0, len(values))
	for _, v := range values {
		id, ok := v.Member.(string)
		if !ok {
			continue
		}
		members = append(members, RankMember{ID: id, Score: v.Score})
	}
	return members
}
