package notification

import "time"

type OutboxMessage struct {
	ID           uint       `gorm:"primaryKey"`
	EventKey     string     `gorm:"size:96;not null;uniqueIndex"`
	EventType    string     `gorm:"size:32;not null;index"`
	Payload      []byte     `gorm:"type:json;not null"`
	ClaimToken   string     `gorm:"size:64;not null;default:''"`
	ClaimedUntil *time.Time `gorm:"index"`
	CreatedAt    time.Time
	PublishedAt  *time.Time `gorm:"index"`
}

func (OutboxMessage) TableName() string {
	return "notification_outbox"
}
