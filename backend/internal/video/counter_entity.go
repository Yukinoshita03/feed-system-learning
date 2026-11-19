package video

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"feed-system-learning/internal/notification"

	"gorm.io/gorm"
)

const CounterEventType = "counter.video.delta"

// VideoCounter 是可重建的展示计数；互动关系和评论正文才是事实来源。
// 使用有符号计数，允许乱序消息暂时先减后加；只在展示时截断负数。
type VideoCounter struct {
	VideoID        uint  `gorm:"primaryKey;autoIncrement:false"`
	LikesCount     int64 `gorm:"not null;default:0"`
	FavoritesCount int64 `gorm:"not null;default:0"`
	CommentsCount  int64 `gorm:"not null;default:0"`
	UpdatedAt      time.Time
	Video          *Video `gorm:"foreignKey:VideoID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

// VideoCounterConsumption 与计数在同一事务提交，防止 ACK 丢失后重复加减。
// 不关联视频外键：视频删除后仍需要保留旧事件的去重依据。
type VideoCounterConsumption struct {
	EventID     string    `gorm:"primaryKey;size:64"`
	ProcessedAt time.Time `gorm:"not null"`
}

type CounterEvent struct {
	Version int    `json:"version"`
	EventID string `json:"event_id"`
	VideoID uint   `json:"video_id"`
	Kind    string `json:"kind"`
	Delta   int64  `json:"delta"`
}

var ErrInvalidCounterEvent = errors.New("计数事件不合法")

func (e CounterEvent) Validate() error {
	if e.Version != 1 || len(e.EventID) == 0 || len(e.EventID) > 64 || e.VideoID == 0 ||
		(e.Delta != 1 && e.Delta != -1) {
		return ErrInvalidCounterEvent
	}
	switch e.Kind {
	case KindLike, KindFavorite, KindComment:
		return nil
	default:
		return ErrInvalidCounterEvent
	}
}

// recordCounterDelta 只在事实实际变化时调用，必须传入事实写入所用的事务。
func recordCounterDelta(tx *gorm.DB, videoID uint, kind string, delta int64) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Errorf("生成计数事件 ID 失败：%w", err)
	}
	event := CounterEvent{Version: 1, EventID: "counter:" + hex.EncodeToString(token[:]), VideoID: videoID, Kind: kind, Delta: delta}
	if err := event.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return tx.Create(&notification.OutboxMessage{EventKey: event.EventID, EventType: CounterEventType, Payload: payload}).Error
}
