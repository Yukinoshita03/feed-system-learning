package video

import (
	"context"
	"encoding/json"
	"fmt"

	"feed-system-learning/internal/notification"

	"gorm.io/gorm"
)

type LikeRepository struct {
	db     *gorm.DB
	videos *VideoRepository
}

func NewLikeRepository(db *gorm.DB, videos *VideoRepository) *LikeRepository {
	return &LikeRepository{db: db, videos: videos}
}

// SetLiked 同步提交点赞事实和 Outbox；计数消费者异步更新独立的计数表。
func (r *LikeRepository) SetLiked(ctx context.Context, videoID, accountID uint, liked bool) (LikeResult, error) {
	result := LikeResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Video
		if err := tx.Select("id", "author_id").First(&current, videoID).Error; err != nil {
			return err
		}
		result = LikeResult{IsLiked: liked}
		if liked {
			existing := Like{VideoID: videoID, AccountID: accountID}
			if err := tx.Create(&existing).Error; err != nil {
				if duplicateKey(err, "uq_likes_video_account") {
					return nil
				}
				return interactionError(err)
			}
			if err := recordCounterDelta(tx, videoID, KindLike, 1); err != nil {
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
			result.Changed = true
		} else {
			deleted := tx.Where("video_id = ? AND account_id = ?", videoID, accountID).Delete(&Like{})
			if deleted.Error != nil {
				return deleted.Error
			}
			if deleted.RowsAffected > 0 {
				if err := recordCounterDelta(tx, videoID, KindLike, -1); err != nil {
					return err
				}
				result.Changed = true
			}
		}
		return nil
	})
	if err != nil {
		return LikeResult{}, err
	}
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
	if err == nil {
		err = r.videos.FillCounters(ctx, videos)
	}
	return videos, err
}
