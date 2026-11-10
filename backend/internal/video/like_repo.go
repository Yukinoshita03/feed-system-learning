package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/notification"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LikeRepository struct {
	db     *gorm.DB
	videos *VideoRepository
}

func NewLikeRepository(db *gorm.DB, videos *VideoRepository) *LikeRepository {
	return &LikeRepository{db: db, videos: videos}
}

// SetLiked 在一个事务里更新点赞记录和计数；所有写入先锁视频行。
// 视频删除也使用相同的锁顺序，避免删除后又插入孤立的点赞记录。
func (r *LikeRepository) SetLiked(ctx context.Context, videoID, accountID uint, liked bool) (LikeResult, error) {
	result := LikeResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, videoID).Error; err != nil {
			return err
		}
		var existing Like
		err := tx.Where("video_id = ? AND account_id = ?", videoID, accountID).Take(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		exists := err == nil
		if liked && !exists {
			existing = Like{VideoID: videoID, AccountID: accountID}
			if err := tx.Create(&existing).Error; err != nil {
				return err
			}
			if err := tx.Model(&Video{}).Where("id = ?", videoID).
				UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error; err != nil {
				return err
			}
			if current.AuthorID != accountID {
				payload, err := json.Marshal(notification.LikeCreatedEvent{
					LikeID:     existing.ID,
					VideoID:    videoID,
					ActorID:    accountID,
					ReceiverID: current.AuthorID,
				})
				if err != nil {
					return err
				}
				outbox := notification.OutboxMessage{
					EventKey:  fmt.Sprintf("like:%d", existing.ID),
					EventType: "like.created",
					Payload:   payload,
				}
				if err := tx.Create(&outbox).Error; err != nil {
					return err
				}
			}
			current.LikesCount++
		} else if !liked && exists {
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
			if err := tx.Model(&Video{}).Where("id = ?", videoID).
				UpdateColumn("likes_count", gorm.Expr("GREATEST(likes_count - 1, 0)")).Error; err != nil {
				return err
			}
			current.LikesCount = max(0, current.LikesCount-1)
		}
		result = LikeResult{IsLiked: liked, LikesCount: current.LikesCount}
		return nil
	})
	if err != nil {
		return LikeResult{}, err
	}
	// 两个列表只存 ID，无需清除；使详情失效后即可获得新的公共计数。
	r.videos.invalidate(ctx, cache.VideoKey(videoID))
	return result, nil
}

func (r *LikeRepository) IsLiked(ctx context.Context, videoID, accountID uint) (bool, error) {
	var item Video
	if err := r.db.WithContext(ctx).Select("id").First(&item, videoID).Error; err != nil {
		return false, err
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&Like{}).
		Where("video_id = ? AND account_id = ?", videoID, accountID).Count(&count).Error
	return count > 0, err
}

// ListLikedVideos 只读取当前账号的点赞，按点赞时间倒序，最多返回 200 条。
func (r *LikeRepository) ListLikedVideos(ctx context.Context, accountID uint) ([]Video, error) {
	videos := make([]Video, 0)
	err := r.db.WithContext(ctx).Model(&Video{}).Select("videos.*").
		Joins("JOIN likes ON likes.video_id = videos.id").
		Where("likes.account_id = ?", accountID).
		Order("likes.created_at DESC, likes.id DESC").Limit(200).Find(&videos).Error
	return videos, err
}
