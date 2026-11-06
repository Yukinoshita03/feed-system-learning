package notification

import "time"

const (
	Like     = "like"
	Comment  = "comment"
	Favorite = "favorite"
	Follow   = "follow"
)

// Notification 保存互动通知；EventKey 来自业务记录，避免同一事件重复写入。
type Notification struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	ReceiverID uint       `gorm:"not null;index:idx_notification_receiver_read,priority:1" json:"receiver_id"`
	ActorID    uint       `gorm:"not null" json:"actor_id"`
	Kind       string     `gorm:"size:16;not null" json:"kind"`
	VideoID    uint       `gorm:"not null;default:0" json:"video_id"`
	Content    string     `gorm:"size:1000;not null" json:"content"`
	EventKey   string     `gorm:"size:96;not null;uniqueIndex" json:"-"`
	IsRead     bool       `gorm:"not null;default:false;index:idx_notification_receiver_read,priority:2" json:"is_read"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type View struct {
	Notification
	ActorUsername  string `json:"actor_username"`
	ActorAvatarURL string `json:"actor_avatar_url"`
}

type Page struct {
	Messages []View `json:"messages"`
	NextID   uint   `json:"next_id"`
	HasMore  bool   `json:"has_more"`
}

type MarkReadRequest struct {
	MessageIDs []uint `json:"message_ids"`
	All        bool   `json:"all"`
}
