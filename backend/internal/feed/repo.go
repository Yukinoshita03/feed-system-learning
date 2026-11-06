package feed

import (
	"context"
	"errors"
	"feed-system-learning/internal/account"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/video"
	"time"

	"gorm.io/gorm"
)

// FeedRepository 负责视频流的数据库查询。
type FeedRepository struct {
	db     *gorm.DB
	videos *video.VideoRepository
	cache  *cache.Client
}

// NewFeedRepository 注入数据库连接，供后面的查询方法使用。
func NewFeedRepository(db *gorm.DB, videos *video.VideoRepository, cacheClient *cache.Client) *FeedRepository {
	return &FeedRepository{db: db, videos: videos, cache: cacheClient}
}

// ListLatest 按发布时间倒序查询视频；相同时间按 ID 倒序排列。
// 第一页的 latestBefore、idBefore 都为零；后续用时间和 ID 一起定位下一页。
func (r *FeedRepository) ListLatest(ctx context.Context, limit int, latestBefore time.Time, idBefore uint) ([]video.Video, error) {
	// GORM 的负数 Limit 会取消数量限制，因此这里要求查询数量为正数。
	if limit <= 0 {
		return nil, errors.New("查询数量必须大于 0")
	}
	if latestBefore.IsZero() != (idBefore == 0) {
		return nil, errors.New("查询游标必须同时提供时间和 ID")
	}
	// Service 默认返回 10 条，多查一条判断 has_more，所以这里缓存 11 个 ID。
	// 其他页大小及后续游标直接走 MySQL，控制缓存 key 的数量。
	firstPage := limit == 11 && latestBefore.IsZero()
	var entry cache.Entry
	if firstPage && r.cache != nil {
		var list video.IDList
		var hit bool
		entry, hit = r.cache.Load(ctx, cache.LatestFeedKey, &list)
		if hit && list.Valid(11) {
			videos, err := r.videos.GetByIDs(ctx, list.IDs)
			if err != nil {
				return nil, err
			}
			if len(videos) == len(list.IDs) {
				return videos, nil
			}
		}
	}

	// 初始化为空切片，没有视频时返回 []，方便后面组织 JSON 响应。
	videos := make([]video.Video, 0)
	query := r.db.WithContext(ctx).
		Model(&video.Video{}).
		// 旧记录缺少发布时间时无法生成有效时间游标，补时间后再进入视频流。
		Where("create_time > ?", time.UnixMilli(0)).
		Order("create_time DESC, id DESC")

	if !latestBefore.IsZero() {
		// 相同毫秒内继续按更小的 ID 查询，避免这一批视频在翻页时被跳过。
		query = query.Where(
			"(create_time < ? OR (create_time = ? AND id < ?))",
			latestBefore, latestBefore, idBefore,
		)
	}

	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	if firstPage && r.cache != nil {
		r.cache.Store(ctx, entry, r.cache.Config().FeedTTL, video.IDs(videos))
	}
	return videos, nil
}

// FindAuthorsByIDs 一次查询本页作者的公开资料，避免逐条视频查询作者。
func (r *FeedRepository) FindAuthorsByIDs(ctx context.Context, ids []uint) ([]FeedAuthor, error) {
	authors := make([]FeedAuthor, 0)
	if len(ids) == 0 {
		return authors, nil
	}
	err := r.db.WithContext(ctx).
		Model(&account.Account{}).
		Select("id", "username").
		Where("id IN ?", ids).
		Find(&authors).Error
	if err != nil {
		return nil, err
	}
	return authors, nil
}

func (r *FeedRepository) FindLikedVideoIDs(ctx context.Context, accountID uint, ids []uint) (map[uint]bool, error) {
	liked := make(map[uint]bool)
	if accountID == 0 || len(ids) == 0 {
		return liked, nil
	}
	var videoIDs []uint
	err := r.db.WithContext(ctx).Model(&video.Like{}).
		Where("account_id = ? AND video_id IN ?", accountID, ids).Pluck("video_id", &videoIDs).Error
	if err != nil {
		return nil, err
	}
	for _, id := range videoIDs {
		liked[id] = true
	}
	return liked, nil
}
