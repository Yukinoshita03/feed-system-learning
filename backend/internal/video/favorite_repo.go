package video

import (
	"context"
	"encoding/json"
	"fmt"

	"feed-system-learning/internal/notification"

	"gorm.io/gorm"
)

type FavoriteRepository struct {
	db     *gorm.DB
	videos *VideoRepository
}

func NewFavoriteRepository(db *gorm.DB, videos *VideoRepository) *FavoriteRepository {
	return &FavoriteRepository{db: db, videos: videos}
}

// SetFavorited 只同步写收藏事实和 Outbox，不锁视频行或更新展示计数。
func (r *FavoriteRepository) SetFavorited(ctx context.Context, videoID, accountID uint, active bool) (FavoriteResult, error) {
	var result FavoriteResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Video
		if err := tx.Select("id", "author_id").First(&current, videoID).Error; err != nil {
			return err
		}
		result = FavoriteResult{IsFavorited: active}
		if active {
			existing := Favorite{VideoID: videoID, AccountID: accountID}
			if err := tx.Create(&existing).Error; err != nil {
				if duplicateKey(err, "uq_favorite_video_account") {
					return nil
				}
				return interactionError(err)
			}
			if err := recordCounterDelta(tx, videoID, KindFavorite, 1); err != nil {
				return err
			}
			// 只写 outbox，通知由 Consumer 异步落库；自己收藏自己的视频不通知。
			if current.AuthorID != accountID {
				payload, err := json.Marshal(notification.FavoriteCreatedEvent{
					FavoriteID: existing.ID,
					VideoID:    videoID,
					ActorID:    accountID,
					ReceiverID: current.AuthorID,
				})
				if err != nil {
					return err
				}
				outbox := notification.OutboxMessage{
					EventKey:  fmt.Sprintf("favorite:%d", existing.ID),
					EventType: "favorite.created",
					Payload:   payload,
				}
				if err := tx.Create(&outbox).Error; err != nil {
					return err
				}
			}
			result.Changed = true
		} else {
			deleted := tx.Where("video_id = ? AND account_id = ?", videoID, accountID).Delete(&Favorite{})
			if deleted.Error != nil {
				return deleted.Error
			}
			if deleted.RowsAffected > 0 {
				if err := recordCounterDelta(tx, videoID, KindFavorite, -1); err != nil {
					return err
				}
				result.Changed = true
			}
		}
		return nil
	})
	return result, err
}

func (r *FavoriteRepository) IsFavorited(ctx context.Context, videoID, accountID uint) (bool, error) {
	var current Video
	if err := r.db.WithContext(ctx).Select("id").First(&current, videoID).Error; err != nil {
		return false, err
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&Favorite{}).Where("video_id = ? AND account_id = ?", videoID, accountID).Count(&count).Error
	return count > 0, err
}

func (r *FavoriteRepository) List(ctx context.Context, accountID, beforeID uint, limit int) ([]favoriteVideo, error) {
	items := make([]favoriteVideo, 0)
	query := r.db.WithContext(ctx).Model(&Video{}).Select("videos.*, favorites.id AS favorite_cursor").
		Joins("JOIN favorites ON favorites.video_id = videos.id").Where("favorites.account_id = ?", accountID)
	if beforeID > 0 {
		query = query.Where("favorites.id < ?", beforeID)
	}
	err := query.Order("favorites.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}
