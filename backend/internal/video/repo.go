package video

import (
	"context"
	"errors"
	"log"

	"feed-system-learning/internal/cache"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// VideoRepository 负责视频数据库读写及详情、作者作品列表的旁路缓存。
type VideoRepository struct {
	db    *gorm.DB
	cache *cache.Client
}

func NewVideoRepository(db *gorm.DB, cacheClient *cache.Client) *VideoRepository {
	return &VideoRepository{db: db, cache: cacheClient}
}

// CreateVideo 新增一条视频；生成的主键会回填到 video.ID。
func (r *VideoRepository) CreateVideo(ctx context.Context, video *Video) error {
	result := r.db.WithContext(ctx).Create(video)
	if result.Error != nil {
		return result.Error
	}
	r.invalidate(ctx, cache.VideoKey(video.ID), cache.AuthorVideosKey(video.AuthorID), cache.LatestFeedKey)
	return nil
}

// GetByID 根据主键查一条记录，不存在时返回 gorm.ErrRecordNotFound。
func (r *VideoRepository) GetByID(ctx context.Context, id uint) (*Video, error) {
	videos, err := r.GetByIDs(ctx, []uint{id})
	if err != nil {
		return nil, err
	}
	if len(videos) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &videos[0], nil
}

// IDList 只缓存共享的视频 ID；详情和用户相关状态在读取时组织。
type IDList struct {
	IDs []uint `json:"ids"`
}

func (list IDList) Valid(maxLength int) bool {
	if list.IDs == nil || len(list.IDs) > maxLength {
		return false
	}
	seen := make(map[uint]bool, len(list.IDs))
	for _, id := range list.IDs {
		if id == 0 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func IDs(videos []Video) IDList {
	list := IDList{IDs: make([]uint, 0, len(videos))}
	for _, item := range videos {
		list.IDs = append(list.IDs, item.ID)
	}
	return list
}

// GetByIDs 按传入顺序返回视频；缓存批量读取，未命中项也只查询一次 MySQL。
// 已删除的视频不出现在结果中，列表调用方发现缺项时重新查询完整列表。
func (r *VideoRepository) GetByIDs(ctx context.Context, ids []uint) ([]Video, error) {
	videos := make([]Video, 0, len(ids))
	if len(ids) == 0 {
		return videos, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = cache.VideoKey(id)
	}
	entries := r.cache.LoadMany(ctx, keys)
	byID := make(map[uint]Video, len(ids))
	missing := make([]uint, 0)
	tickets := make(map[uint]cache.Entry)
	for i, id := range ids {
		var item Video
		if entries[i].Decode(&item) && item.ID == id && item.AuthorID > 0 {
			byID[id] = item
		} else {
			missing = append(missing, id)
			tickets[id] = entries[i]
		}
	}
	if len(missing) > 0 {
		var stored []Video
		if err := r.db.WithContext(ctx).Where("id IN ?", missing).Find(&stored).Error; err != nil {
			return nil, err
		}
		fills := make([]cache.Entry, 0, len(stored))
		values := make([]any, 0, len(stored))
		for _, item := range stored {
			byID[item.ID] = item
			fills = append(fills, tickets[item.ID])
			values = append(values, item)
		}
		if r.cache != nil {
			r.cache.StoreMany(ctx, fills, r.cache.Config().VideoTTL, values)
		}
	}
	for _, id := range ids {
		if item, exists := byID[id]; exists {
			videos = append(videos, item)
		}
	}
	return videos, nil
}

// ListByAuthorID 查询作者最近 200 条作品；没有作品时返回空切片。
func (r *VideoRepository) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	var list IDList
	entry, hit := r.cache.Load(ctx, cache.AuthorVideosKey(authorID), &list)
	if hit && list.Valid(200) {
		videos, err := r.GetByIDs(ctx, list.IDs)
		if err != nil {
			return nil, err
		}
		valid := len(videos) == len(list.IDs)
		for _, item := range videos {
			valid = valid && item.AuthorID == authorID
		}
		if valid {
			return videos, nil
		}
	}
	videos := make([]Video, 0)
	result := r.db.WithContext(ctx).
		Where("author_id = ?", authorID).
		Order("id DESC").
		Limit(200).
		Find(&videos)
	if result.Error != nil {
		return nil, result.Error
	}
	if r.cache != nil {
		r.cache.Store(ctx, entry, r.cache.Config().AuthorTTL, IDs(videos))
	}
	return videos, nil
}

// UpdateVideo 仅更新可编辑字段，并在 SQL 中同时限制视频 ID 和作者 ID。
func (r *VideoRepository) UpdateVideo(ctx context.Context, video *Video) error {
	result := r.db.WithContext(ctx).Model(&Video{}).
		Where("id = ? AND author_id = ?", video.ID, video.AuthorID).
		Updates(map[string]any{
			"title":     video.Title,
			"play_url":  video.PlayURL,
			"cover_url": video.CoverURL,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// MySQL 对相同内容的更新可能返回 0 行，不能直接当成记录不存在。
		var current Video
		if err := r.db.WithContext(ctx).
			Where("id = ? AND author_id = ?", video.ID, video.AuthorID).
			First(&current).Error; err != nil {
			return err
		}
	}
	// 列表仅缓存 ID，修改标题、文件地址时只需让详情失效。
	r.invalidate(ctx, cache.VideoKey(video.ID))
	return nil
}

// DeleteVideo 删除当前作者的视频记录，文件本身不在这一层处理。
func (r *VideoRepository) DeleteVideo(ctx context.Context, id, authorID uint) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND author_id = ?", id, authorID).First(&item).Error; err != nil {
			return err
		}
		if err := tx.Where("video_id = ?", id).Delete(&Like{}).Error; err != nil {
			return err
		}
		if err := tx.Where("video_id = ?", id).Delete(&Comment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("video_id = ?", id).Delete(&Favorite{}).Error; err != nil {
			return err
		}
		// 播放明细同样按视频清理，避免删除后留下孤儿记录。
		if err := tx.Where("video_id = ?", id).Delete(&VideoView{}).Error; err != nil {
			return err
		}
		return tx.Delete(&item).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			r.invalidate(ctx, cache.VideoKey(id))
		}
		return err
	}
	r.invalidate(ctx, cache.VideoKey(id), cache.AuthorVideosKey(authorID), cache.LatestFeedKey)
	return nil
}

func (r *VideoRepository) invalidate(ctx context.Context, keys ...string) {
	// MySQL 已提交，即使客户端取消请求，也尝试在独立的短超时内使缓存失效。
	if err := r.cache.Invalidate(context.WithoutCancel(ctx), keys...); err != nil {
		log.Printf("视频数据已写入 MySQL，但缓存失效失败，TTL 到期后收敛：%v", err)
	}
}
