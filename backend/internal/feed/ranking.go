package feed

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"feed-system-learning/internal/cache"

	"gorm.io/gorm"
)

// 榜单参数。
const (
	// rankingLimit 是每个榜单预先算好并写进 ZSET 的条数。查询接口从中取前 N 名，
	// 比最终展示数量宽一些，留出后续调整展示数量的余地。
	rankingLimit = 100
	// rankingTTL 是 ZSET 的兜底过期时间。重算任务正常时每轮都会覆盖，
	// 任务挂掉时榜单最多存活这么久，不会永远停在旧快照上。
	rankingTTL = 15 * time.Minute
	// bucketLookbackHours 是填桶任务每次回溯的小时数。
	// 处理最近若干个完整小时而不是只处理上一个小时，任务漏跑一次也能自动补上
	// （UPSERT 幂等）。
	bucketLookbackHours = 3
)

// RankingWindow 是榜单的时间窗口。只接受白名单值：每个窗口都要有预先算好的
// 一份 ZSET，放行任意时长等于退化成"查询时实时聚合"，榜单就失去意义了。
type RankingWindow string

const (
	Window24h RankingWindow = "24h"
	Window7d  RankingWindow = "7d"
)

// ParseRankingWindow 把外部传入的字符串转成受支持的窗口。
func ParseRankingWindow(value string) (RankingWindow, error) {
	switch RankingWindow(value) {
	case Window24h:
		return Window24h, nil
	case Window7d:
		return Window7d, nil
	default:
		return "", fmt.Errorf("%w：window 只支持 24h 或 7d", ErrInvalidFeed)
	}
}

// duration 返回窗口长度。
func (w RankingWindow) duration() time.Duration {
	switch w {
	case Window24h:
		return 24 * time.Hour
	case Window7d:
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

// startFrom 返回窗口起点，向下对齐到整点，保证和桶的粒度一致。
func (w RankingWindow) startFrom(now time.Time) time.Time {
	return now.Add(-w.duration()).Truncate(time.Hour)
}

// RankingKind 区分不同口径的榜单。
type RankingKind string

const (
	RankingLikes RankingKind = "likes"
)

// rankKey 生成 Redis ZSET 的 key。
//
// 口径和窗口都必须进 key：一个 ZSET 只有一列 score，混在一起就没法比较
// （7 天的赞数和 24 小时的赞数不是同一个量纲）。
func rankKey(kind RankingKind, window RankingWindow) string {
	return fmt.Sprintf("feed:rank:%s:%s", kind, window)
}

// rankScore 是一条聚合结果。
type rankScore struct {
	VideoID uint    `gorm:"column:video_id"`
	Score   float64 `gorm:"column:score"`
}

// aggregateLikes 从小时桶里聚合出窗口内的点赞总数，取前 limit 名。
//
// 与直接扫 likes 明细相比，这里扫的是 like_hourly 窄表：
// 24 小时窗口是 24 行/视频，7 天是 168 行/视频。
//
// JOIN videos 过滤掉已删除的视频——桶是定时填充的，视频删除到下一轮
// 桶清理之间会残留数据，不加这个条件会把不存在的视频排进榜里。
// ORDER BY 带上 video_id：同分时顺序必须确定，否则每轮重算排名会跳动。
func (r *FeedRepository) aggregateLikes(ctx context.Context, window RankingWindow, limit int) ([]rankScore, error) {
	if limit <= 0 {
		return nil, errors.New("榜单长度必须大于 0")
	}
	if window.duration() == 0 {
		return nil, fmt.Errorf("不支持的榜单窗口: %s", window)
	}

	scores := make([]rankScore, 0, limit)
	err := r.db.WithContext(ctx).Raw(`
		SELECT like_hourly.video_id AS video_id, SUM(like_hourly.count) AS score
		FROM like_hourly
		JOIN videos ON videos.id = like_hourly.video_id
		WHERE like_hourly.bucket >= ?
		GROUP BY like_hourly.video_id
		ORDER BY score DESC, like_hourly.video_id DESC
		LIMIT ?
	`, window.startFrom(time.Now()), limit).Scan(&scores).Error
	if err != nil {
		return nil, err
	}
	return scores, nil
}

// BucketFiller 把 likes 明细增量汇总进 like_hourly。
//
// 只处理已经结束的完整小时：未结束的小时统计值会继续变，写进桶里会让
// UPSERT 反复覆盖，且榜单读到的数字和明细对不上。
type BucketFiller struct {
	db *gorm.DB
}

func NewBucketFiller(db *gorm.DB) *BucketFiller {
	return &BucketFiller{db: db}
}

// Fill 回溯最近 bucketLookbackHours 个完整小时，把点赞汇总写入桶表。
// 返回受影响的行数（MySQL 对 INSERT ... ON DUPLICATE KEY UPDATE 的计数
// 包含插入和更新两类）。
func (f *BucketFiller) Fill(ctx context.Context) (int64, error) {
	if f == nil || f.db == nil {
		return 0, errors.New("填充任务未初始化")
	}

	end := time.Now().Truncate(time.Hour)
	start := end.Add(-bucketLookbackHours * time.Hour)

	result := f.db.WithContext(ctx).Exec(`
		INSERT INTO like_hourly (video_id, bucket, count)
		SELECT video_id,
		       DATE_FORMAT(created_at, '%Y-%m-%d %H:00:00') AS bucket,
		       COUNT(*) AS count
		FROM likes
		WHERE created_at >= ? AND created_at < ?
		GROUP BY video_id, DATE_FORMAT(created_at, '%Y-%m-%d %H:00:00')
		ON DUPLICATE KEY UPDATE count = VALUES(count)
	`, start, end)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// RankingRefresher 重新聚合一个榜单并整体覆盖 Redis 里的 ZSET。
type RankingRefresher struct {
	repo  *FeedRepository
	cache *cache.Client
}

func NewRankingRefresher(repo *FeedRepository, cacheClient *cache.Client) *RankingRefresher {
	return &RankingRefresher{repo: repo, cache: cacheClient}
}

// Refresh 重算一个榜单。失败要返回 error，不能静默——榜单停在旧快照上
// 从接口层面看不出任何异常。
func (r *RankingRefresher) Refresh(ctx context.Context, kind RankingKind, window RankingWindow) error {
	if r == nil || r.repo == nil || r.cache == nil {
		return errors.New("榜单任务未初始化")
	}

	var scores []rankScore
	switch kind {
	case RankingLikes:
		var err error
		scores, err = r.repo.aggregateLikes(ctx, window, rankingLimit)
		if err != nil {
			return fmt.Errorf("聚合点赞榜失败：%w", err)
		}
	default:
		return fmt.Errorf("不支持的榜单类型: %s", kind)
	}

	members := make([]cache.RankMember, 0, len(scores))
	for _, s := range scores {
		members = append(members, cache.RankMember{
			ID:    strconv.FormatUint(uint64(s.VideoID), 10),
			Score: s.Score,
		})
	}

	key := rankKey(kind, window)
	if !r.cache.ReplaceRank(ctx, key, members, rankingTTL) {
		return fmt.Errorf("写入榜单 %s 失败", key)
	}
	return nil
}

// RefreshAll 重算所有支持的榜单组合，供定时任务调用。
// 单个榜单失败不影响其它榜单，错误用 errors.Join 汇总返回。
func (r *RankingRefresher) RefreshAll(ctx context.Context) error {
	var errs []error
	for _, kind := range []RankingKind{RankingLikes} {
		for _, window := range []RankingWindow{Window24h, Window7d} {
			if err := r.Refresh(ctx, kind, window); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// CleanBuckets 删除过期桶，避免预聚合表无限增长。
// 保留 8 天：7 天窗口 + 1 天余量。分批删除，避免长事务锁表。
func (f *BucketFiller) CleanBuckets(ctx context.Context, limit int) (int64, error) {
	if f == nil || f.db == nil {
		return 0, errors.New("填充任务未初始化")
	}
	if limit <= 0 {
		return 0, errors.New("清理数量必须大于 0")
	}

	cutoff := time.Now().Truncate(time.Hour).Add(-(7*24 + 24) * time.Hour)
	result := f.db.WithContext(ctx).Exec(
		"DELETE FROM like_hourly WHERE bucket < ? LIMIT ?", cutoff, limit,
	)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
