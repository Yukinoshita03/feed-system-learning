package notification

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// List 只读取当前接收人的通知，动态关联触发者的公开资料。
func (r *Repository) List(ctx context.Context, receiverID, beforeID uint, limit int, unreadOnly bool) ([]View, error) {
	items := make([]View, 0)
	query := r.db.WithContext(ctx).Model(&Notification{}).
		Select("notifications.*, COALESCE(accounts.username, '') AS actor_username, COALESCE(accounts.avatar_url, '') AS actor_avatar_url").
		Joins("LEFT JOIN accounts ON accounts.id = notifications.actor_id").Where("notifications.receiver_id = ?", receiverID)
	if beforeID > 0 {
		query = query.Where("notifications.id < ?", beforeID)
	}
	if unreadOnly {
		query = query.Where("notifications.is_read = ?", false)
	}
	err := query.Order("notifications.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

func (r *Repository) CountUnread(ctx context.Context, receiverID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Notification{}).Where("receiver_id = ? AND is_read = ?", receiverID, false).Count(&count).Error
	return count, err
}

// MarkRead 在 SQL 中限制接收人，传入别人的消息 ID 不会改动别人的通知。
func (r *Repository) MarkRead(ctx context.Context, receiverID uint, ids []uint, all bool) (int64, error) {
	query := r.db.WithContext(ctx).Model(&Notification{}).Where("receiver_id = ? AND is_read = ?", receiverID, false)
	if !all {
		query = query.Where("id IN ?", ids)
	}
	result := query.Updates(map[string]any{"is_read": true, "read_at": time.Now().UTC()})
	return result.RowsAffected, result.Error
}

func (r *Repository) Delete(ctx context.Context, receiverID, id uint) error {
	result := r.db.WithContext(ctx).Where("id = ? AND receiver_id = ?", id, receiverID).Delete(&Notification{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
func (r *Repository) ClaimPendingOutbox(
	ctx context.Context,
	limit int,
	claimToken string,
	lease time.Duration,
) ([]OutboxMessage, error) {
	if limit <= 0 || claimToken == "" || lease <= 0 {
		return nil, fmt.Errorf("limit、claimToken 和 lease 必须有效")
	}

	now := time.Now().UTC()
	claimedUntil := now.Add(lease)
	messages := make([]OutboxMessage, 0, limit)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{
			Strength: "UPDATE",
			Options:  "SKIP LOCKED",
		}).
			Where("published_at IS NULL").
			Where("(claimed_until IS NULL OR claimed_until <= ?)", now).
			Order("id ASC").
			Limit(limit).
			Find(&messages).Error; err != nil {
			return err
		}

		for i := range messages {
			if err := tx.Model(&OutboxMessage{}).
				Where("id = ?", messages[i].ID).
				Updates(map[string]any{
					"claim_token":   claimToken,
					"claimed_until": claimedUntil,
				}).Error; err != nil {
				return err
			}
			messages[i].ClaimToken = claimToken
			messages[i].ClaimedUntil = &claimedUntil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return messages, nil
}
func (r *Repository) MarkOutboxPublished(ctx context.Context, id uint, claimToken string) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id = ? AND published_at IS NULL AND claim_token = ?", id, claimToken).
		Updates(map[string]any{
			"published_at":  now,
			"claim_token":   "",
			"claimed_until": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("outbox id=%d 的认领已失效或已发布", id)
	}
	return nil
}
func (r *Repository) CreateLikeNotification(ctx context.Context, event LikeCreatedEvent) error {
	if event.LikeID == 0 || event.VideoID == 0 ||
		event.ActorID == 0 || event.ReceiverID == 0 {
		return fmt.Errorf("点赞通知事件字段无效")
	}
	if event.ActorID == event.ReceiverID {
		return nil
	}
	item := Notification{
		ReceiverID: event.ReceiverID,
		ActorID:    event.ActorID,
		Kind:       Like,
		VideoID:    event.VideoID,
		Content:    "赞了你的视频",
		EventKey:   fmt.Sprintf("%s:%d", Like, event.LikeID),
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_key"}},
			DoNothing: true,
		}).
		Create(&item).Error
}
func (r *Repository) CreateCommentNotification(ctx context.Context, event CommentCreatedEvent) error {
	if event.CommentID == 0 || event.VideoID == 0 ||
		event.ActorID == 0 || event.ReceiverID == 0 {
		return fmt.Errorf("评论通知事件字段无效")
	}
	if event.ActorID == event.ReceiverID {
		return nil
	}
	item := Notification{
		ReceiverID: event.ReceiverID,
		ActorID:    event.ActorID,
		Kind:       Comment,
		VideoID:    event.VideoID,
		Content:    "评论了你的视频",
		EventKey:   fmt.Sprintf("%s:%d", Comment, event.CommentID),
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_key"}},
			DoNothing: true,
		}).
		Create(&item).Error
}
func (r *Repository) CreateFavoriteNotification(ctx context.Context, event FavoriteCreatedEvent) error {
	if event.FavoriteID == 0 || event.VideoID == 0 ||
		event.ActorID == 0 || event.ReceiverID == 0 {
		return fmt.Errorf("收藏通知事件字段无效")
	}
	if event.ActorID == event.ReceiverID {
		return nil
	}
	item := Notification{
		ReceiverID: event.ReceiverID,
		ActorID:    event.ActorID,
		Kind:       Favorite,
		VideoID:    event.VideoID,
		Content:    "收藏了你的视频",
		EventKey:   fmt.Sprintf("%s:%d", Favorite, event.FavoriteID),
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_key"}},
			DoNothing: true,
		}).
		Create(&item).Error
}
func (r *Repository) CreateFollowNotification(ctx context.Context, event FollowCreatedEvent) error {
	if event.FollowID == 0 || event.ActorID == 0 || event.ReceiverID == 0 {
		return fmt.Errorf("关注通知事件字段无效")
	}
	if event.ActorID == event.ReceiverID {
		return nil
	}
	item := Notification{
		ReceiverID: event.ReceiverID,
		ActorID:    event.ActorID,
		Kind:       Follow,
		Content:    "关注了你",
		EventKey:   fmt.Sprintf("%s:%d", Follow, event.FollowID),
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_key"}},
			DoNothing: true,
		}).
		Create(&item).Error
}
