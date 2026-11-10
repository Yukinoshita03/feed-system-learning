package social

import (
	"context"
	"encoding/json"
	"errors"
	"feed-system-learning/internal/account"
	"feed-system-learning/internal/notification"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// SetFollowing 按账号 ID 升序逐个锁行，避免 A 关注 B 与 B 关注 A 的死锁。
func (r *Repository) SetFollowing(ctx context.Context, actorID, targetID uint, active bool) (FollowResult, error) {
	var result FollowResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range []uint{min(actorID, targetID), max(actorID, targetID)} {
			var user account.Account
			if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, id).Error; err != nil {
				return err
			}
		}
		var existing Follow
		err := tx.Where("follower_id = ? AND vlogger_id = ?", actorID, targetID).Take(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		exists := err == nil
		if active && !exists {
			existing = Follow{FollowerID: actorID, VloggerID: targetID}
			if err := tx.Create(&existing).Error; err != nil {
				return err
			}
			if targetID != actorID {
				payload, err := json.Marshal(notification.FollowCreatedEvent{
					FollowID:   existing.ID,
					ActorID:    actorID,
					ReceiverID: targetID,
				})
				if err != nil {
					return err
				}
				outbox := notification.OutboxMessage{
					EventKey:  fmt.Sprintf("follow:%d", existing.ID),
					EventType: "follow.created",
					Payload:   payload,
				}
				if err := tx.Create(&outbox).Error; err != nil {
					return err
				}
			}
		} else if !active && exists {
			if err := tx.Delete(&existing).Error; err != nil {
				return err
			}
		}
		result.IsFollowing = active
		if err := tx.Model(&Follow{}).Where("follower_id = ?", actorID).Count(&result.FollowingCount).Error; err != nil {
			return err
		}
		return tx.Model(&Follow{}).Where("vlogger_id = ?", targetID).Count(&result.FollowerCount).Error
	})
	return result, err
}

func (r *Repository) IsFollowing(ctx context.Context, actorID, targetID uint) (bool, error) {
	var user account.Account
	if err := r.db.WithContext(ctx).Select("id").First(&user, targetID).Error; err != nil {
		return false, err
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&Follow{}).Where("follower_id = ? AND vlogger_id = ?", actorID, targetID).Count(&count).Error
	return count > 0, err
}

// List 只返回公开资料；followers=true 查询粉丝，否则查询关注的人。
func (r *Repository) List(ctx context.Context, userID, beforeID uint, limit int, followers bool) ([]User, int64, error) {
	var user account.Account
	if err := r.db.WithContext(ctx).Select("id").First(&user, userID).Error; err != nil {
		return nil, 0, err
	}
	filter, joined := "follows.follower_id", "follows.vlogger_id"
	if followers {
		filter, joined = "follows.vlogger_id", "follows.follower_id"
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&Follow{}).Where(filter+" = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]User, 0)
	query := r.db.WithContext(ctx).Model(&Follow{}).
		Select("accounts.id, accounts.username, accounts.avatar_url, accounts.bio, follows.id AS follow_cursor").
		Joins("JOIN accounts ON accounts.id = "+joined).Where(filter+" = ?", userID)
	if beforeID > 0 {
		query = query.Where("follows.id < ?", beforeID)
	}
	err := query.Order("follows.id DESC").Limit(limit).Scan(&items).Error
	return items, total, err
}
