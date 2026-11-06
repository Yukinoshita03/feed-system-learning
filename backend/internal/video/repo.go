package video

import (
	"context"

	"gorm.io/gorm"
)

// VideoRepository 保存数据库对象，只负责 videos 表的读写。
type VideoRepository struct {
	db *gorm.DB
}

func NewVideoRepository(db *gorm.DB) *VideoRepository {
	return &VideoRepository{db: db}
}

// CreateVideo 新增一条视频；生成的主键会回填到 video.ID。
func (r *VideoRepository) CreateVideo(ctx context.Context, video *Video) error {
	result := r.db.WithContext(ctx).Create(video)
	return result.Error
}

// GetByID 根据主键查一条记录，不存在时返回 gorm.ErrRecordNotFound。
func (r *VideoRepository) GetByID(ctx context.Context, id uint) (*Video, error) {
	var video Video
	result := r.db.WithContext(ctx).First(&video, id)
	if result.Error != nil {
		return nil, result.Error
	}
	return &video, nil
}

// ListByAuthorID 查询作者最近 200 条作品；没有作品时返回空切片。
func (r *VideoRepository) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	videos := make([]Video, 0)
	result := r.db.WithContext(ctx).
		Where("author_id = ?", authorID).
		Order("id DESC").
		Limit(200).
		Find(&videos)
	return videos, result.Error
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
		return r.db.WithContext(ctx).
			Where("id = ? AND author_id = ?", video.ID, video.AuthorID).
			First(&current).Error
	}
	return nil
}

// DeleteVideo 删除当前作者的视频记录，文件本身不在这一层处理。
func (r *VideoRepository) DeleteVideo(ctx context.Context, id, authorID uint) error {
	result := r.db.WithContext(ctx).
		Where("id = ? AND author_id = ?", id, authorID).
		Delete(&Video{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
