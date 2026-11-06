package notification

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// Record 由业务 Repo 在现有事务内调用；通知失败时业务变更也会回滚。
// 重复的点赞、收藏、关注不会走到这里；取消后再次操作会产生新事件。
func Record(tx *gorm.DB, receiverID, actorID uint, kind string, sourceID, videoID uint, content string) error {
	if receiverID == actorID {
		return nil
	}
	item := Notification{
		ReceiverID: receiverID, ActorID: actorID, Kind: kind, VideoID: videoID,
		Content: content, EventKey: fmt.Sprintf("%s:%d", kind, sourceID),
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item).Error
}

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
