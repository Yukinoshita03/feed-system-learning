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

type FavoriteRepository struct {
	db     *gorm.DB
	videos *VideoRepository
}

func NewFavoriteRepository(db *gorm.DB, videos *VideoRepository) *FavoriteRepository {
	return &FavoriteRepository{db: db, videos: videos}
}

// SetFavorited 先锁定视频行，关系和收藏数在同一个事务里修改。
func (r *FavoriteRepository) SetFavorited(ctx context.Context, videoID, accountID uint, active bool) (FavoriteResult, error) {
	var result FavoriteResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, videoID).Error; err != nil {
			return err
		}
		var existing Favorite
		err := tx.Where("video_id = ? AND account_id = ?", videoID, accountID).Take(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		exists := err == nil
		if active && !exists {
			existing = Favorite{VideoID: videoID, AccountID: accountID}
			if err := tx.Create(&existing).Error; err != nil {
				return err
			}
			if err := tx.Model(&Video{}).Where("id = ?", videoID).UpdateColumn("favorites_count", gorm.Expr("favorites_count + 1")).Error; err != nil {
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
			current.FavoritesCount++
		} else if !active && exists {
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
			if err := tx.Model(&Video{}).Where("id = ?", videoID).UpdateColumn("favorites_count", gorm.Expr("GREATEST(favorites_count - 1, 0)")).Error; err != nil {
				return err
			}
			current.FavoritesCount = max(0, current.FavoritesCount-1)
		}
		result = FavoriteResult{IsFavorited: active, FavoritesCount: current.FavoritesCount}
		return nil
	})
	if err == nil {
		r.videos.invalidate(ctx, cache.VideoKey(videoID))
	}
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
