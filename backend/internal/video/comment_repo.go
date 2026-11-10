package video

import (
	"context"
	"encoding/json"
	"errors"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/notification"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CommentRepository struct {
	db     *gorm.DB
	videos *VideoRepository
}

func NewCommentRepository(db *gorm.DB, videos *VideoRepository) *CommentRepository {
	return &CommentRepository{db: db, videos: videos}
}

// Create 先锁视频，再插入评论和增加计数；视频删除也遵守相同锁顺序。
func (r *CommentRepository) Create(ctx context.Context, item *Comment) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, item.VideoID).Error; err != nil {
			return err
		}
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		if err := tx.Model(&Video{}).Where("id = ?", item.VideoID).
			UpdateColumn("comments_count", gorm.Expr("comments_count + 1")).Error; err != nil {
			return err
		}
		if current.AuthorID != item.AuthorID {
			payload, err := json.Marshal(notification.CommentCreatedEvent{
				CommentID:  item.ID,
				VideoID:    item.VideoID,
				ActorID:    item.AuthorID,
				ReceiverID: current.AuthorID,
			})
			if err != nil {
				return err
			}
			outbox := notification.OutboxMessage{
				EventKey:  fmt.Sprintf("comment:%d", item.ID),
				EventType: "comment.created",
				Payload:   payload,
			}
			if err := tx.Create(&outbox).Error; err != nil {
				return err
			}
		}
		count = current.CommentsCount + 1
		return nil
	})
	if err == nil {
		r.videos.invalidate(ctx, cache.VideoKey(item.VideoID))
	}
	return count, err
}

// List 只选择评论和公开用户名，不把账号密码或凭证带到响应里。
func (r *CommentRepository) List(ctx context.Context, videoID, beforeID uint, limit int) ([]CommentView, error) {
	var current Video
	if err := r.db.WithContext(ctx).Select("id").First(&current, videoID).Error; err != nil {
		return nil, err
	}
	items := make([]CommentView, 0)
	query := r.db.WithContext(ctx).Model(&Comment{}).
		Select("comments.*, accounts.username").
		Joins("JOIN accounts ON accounts.id = comments.author_id").Where("comments.video_id = ?", videoID)
	if beforeID > 0 {
		query = query.Where("comments.id < ?", beforeID)
	}
	err := query.Order("comments.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

// Delete 只允许作者删除；正文删除和计数扣减在同一事务完成。
func (r *CommentRepository) Delete(ctx context.Context, commentID, authorID uint) (int64, error) {
	var videoID uint
	var count int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item Comment
		if err := tx.First(&item, commentID).Error; err != nil {
			return err
		}
		if item.AuthorID != authorID {
			return ErrCommentForbidden
		}
		videoID = item.VideoID
		var current Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, videoID).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND author_id = ?", commentID, authorID).Delete(&Comment{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Model(&Video{}).Where("id = ?", videoID).
			UpdateColumn("comments_count", gorm.Expr("GREATEST(comments_count - 1, 0)")).Error; err != nil {
			return err
		}
		count = max(0, current.CommentsCount-1)
		return nil
	})
	if err == nil {
		r.videos.invalidate(ctx, cache.VideoKey(videoID))
	}
	return count, err
}

var ErrCommentForbidden = errors.New("只能删除自己的评论")
