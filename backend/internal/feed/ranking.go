package feed

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/video"

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
)

// hotWeights 是热度公式里各信号的权重。
//
// view 的权重必须远低于互动：一次"划过去看一眼"代表的信息量远小于一次赞。
// 若播放量权重过高，热度榜会退化成播放量榜，互动率高的内容反而上不去。
var hotWeights = map[string]float64{
	video.KindLike:     1,
	video.KindFavorite: 2,
	video.KindComment:  3,
	video.KindView:     0.1,
}

// bucketSources 描述每类互动从哪张明细表汇总进 bucket，以及回溯多久。
//
// view 的量级比互动大一个数量级，回溯窗口更短：漏跑一次的容错从 2 轮降到 0，
// 换来填桶任务的扫描量可控。
var bucketSources = []struct {
	kind     string
	table    string
	lookback time.Duration
}{
	{video.KindLike, "likes", 3 * time.Hour},
	{video.KindFavorite, "favorites", 3 * time.Hour},
	{video.KindComment, "comments", 3 * time.Hour},
	{video.KindView, "video_views", time.Hour},
}

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
	RankingHot   RankingKind = "hot"
)

// rankKey 生成 Redis ZSET 的 key。
//
// 口径和窗口都必须进 key：一个 ZSET 只有一列 score，混在一起就没法比较
// （7 天的赞数、24 小时的热度分数都不是同一个量纲）。
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
// 走 idx_kind_bucket 索引：WHERE kind='like' AND bucket>=? 只扫点赞的桶，
// 不会把播放记录（量大一个数量级）一起扫进来。
//
// JOIN videos 过滤已删除视频——桶是定时填充的，视频删除到下一轮桶清理之间
// 会残留数据。ORDER BY 带 video_id：同分时顺序必须确定，否则排名会跳动。
func (r *FeedRepository) aggregateLikes(ctx context.Context, window RankingWindow, limit int) ([]rankScore, error) {
	return r.aggregate(ctx, window, limit, `
		SELECT interaction_hourly.video_id AS video_id,
		       SUM(interaction_hourly.count) AS score
		FROM interaction_hourly
		JOIN videos ON videos.id = interaction_hourly.video_id
		WHERE interaction_hourly.kind = ?
		  AND interaction_hourly.bucket >= ?
		GROUP BY interaction_hourly.video_id
		ORDER BY score DESC, interaction_hourly.video_id DESC
		LIMIT ?
	`, video.KindLike, window.startFrom(time.Now()), limit)
}

// aggregateHot 按加权公式聚合热度，取前 limit 名。
//
// 权重通过参数传入而不是写死在 SQL 里：调公式只改 hotWeights。
// CASE 的 ELSE 0 兜住未来可能出现的未知 kind，脏数据不会让整条查询报错。
func (r *FeedRepository) aggregateHot(ctx context.Context, window RankingWindow, limit int) ([]rankScore, error) {
	return r.aggregate(ctx, window, limit, `
		SELECT interaction_hourly.video_id AS video_id,
		       SUM(interaction_hourly.count * CASE interaction_hourly.kind
		             WHEN ? THEN ?
		             WHEN ? THEN ?
		             WHEN ? THEN ?
		             WHEN ? THEN ?
		             ELSE 0
		           END) AS score
		FROM interaction_hourly
		JOIN videos ON videos.id = interaction_hourly.video_id
		WHERE interaction_hourly.bucket >= ?
		GROUP BY interaction_hourly.video_id
		ORDER BY score DESC, interaction_hourly.video_id DESC
		LIMIT ?
	`,
		video.KindLike, hotWeights[video.KindLike],
		video.KindFavorite, hotWeights[video.KindFavorite],
		video.KindComment, hotWeights[video.KindComment],
		video.KindView, hotWeights[video.KindView],
		window.startFrom(time.Now()), limit)
}

// aggregate 执行聚合查询并做统一的入参校验。
func (r *FeedRepository) aggregate(ctx context.Context, window RankingWindow, limit int, query string, args ...any) ([]rankScore, error) {
	if limit <= 0 {
		return nil, errors.New("榜单长度必须大于 0")
	}
	if window.duration() == 0 {
		return nil, fmt.Errorf("不支持的榜单窗口: %s", window)
	}

	scores := make([]rankScore, 0, limit)
	if err := r.db.WithContext(ctx).Raw(query, args...).Scan(&scores).Error; err != nil {
		return nil, err
	}
	return scores, nil
}

// BucketFiller 把各类互动明细增量汇总进 interaction_hourly。
//
// 只处理已经结束的完整小时：未结束的小时统计值会继续变，写进桶里会让
// UPSERT 反复覆盖，且榜单读到的数字和明细对不上。
type BucketFiller struct {
	db *gorm.DB
}

func NewBucketFiller(db *gorm.DB) *BucketFiller {
	return &BucketFiller{db: db}
}

// Fill 按 bucketSources 逐类回溯，把互动汇总写入桶表。
//
// 各类互动的填充不放在同一个事务里：view 的量级大，混在一起会长时间持锁；
// 而且四者互不依赖，某类失败时其余照常推进，失败的那类下一轮幂等补上。
func (f *BucketFiller) Fill(ctx context.Context) error {
	if f == nil || f.db == nil {
		return errors.New("填充任务未初始化")
	}

	end := time.Now().Truncate(time.Hour)
	var errs []error
	for _, source := range bucketSources {
		start := end.Add(-source.lookback)
		// table 和 kind 都来自本文件内的常量表，不是外部输入，可以安全拼接；
		// 表名无法用占位符参数化。
		stmt := fmt.Sprintf(`
			INSERT INTO interaction_hourly (video_id, bucket, kind, count)
			SELECT video_id,
			       DATE_FORMAT(created_at, '%%Y-%%m-%%d %%H:00:00'),
			       ?,
			       COUNT(*)
			FROM %s
			WHERE created_at >= ? AND created_at < ?
			GROUP BY video_id, DATE_FORMAT(created_at, '%%Y-%%m-%%d %%H:00:00')
			ON DUPLICATE KEY UPDATE count = VALUES(count)
		`, source.table)

		if err := f.db.WithContext(ctx).Exec(stmt, source.kind, start, end).Error; err != nil {
			errs = append(errs, fmt.Errorf("填充 %s 桶失败：%w", source.kind, err))
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
		"DELETE FROM interaction_hourly WHERE bucket < ? LIMIT ?", cutoff, limit,
	)
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
	var err error
	switch kind {
	case RankingLikes:
		scores, err = r.repo.aggregateLikes(ctx, window, rankingLimit)
		if err != nil {
			return fmt.Errorf("聚合点赞榜失败：%w", err)
		}
	case RankingHot:
		scores, err = r.repo.aggregateHot(ctx, window, rankingLimit)
		if err != nil {
			return fmt.Errorf("聚合热度榜失败：%w", err)
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
	for _, kind := range []RankingKind{RankingLikes, RankingHot} {
		for _, window := range []RankingWindow{Window24h, Window7d} {
			if err := r.Refresh(ctx, kind, window); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
