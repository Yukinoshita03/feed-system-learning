package notification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"feed-system-learning/internal/mq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type OutboxPublisher struct {
	repo   *Repository
	client *mq.Client
}

func NewOutboxPublisher(repo *Repository, client *mq.Client) *OutboxPublisher {
	return &OutboxPublisher{repo: repo, client: client}
}

func (p *OutboxPublisher) PublishBatch(ctx context.Context, limit int) error {
	_, err := p.publishBatch(ctx, limit)
	return err
}

func (p *OutboxPublisher) publishBatch(ctx context.Context, limit int) (int, error) {
	if p == nil || p.repo == nil || p.client == nil {
		return 0, fmt.Errorf("outbox publisher 未初始化")
	}
	if limit <= 0 {
		return 0, fmt.Errorf("outbox batch limit 必须是正整数")
	}

	channel, err := p.client.OpenChannel()
	if err != nil {
		return 0, err
	}
	defer channel.Close()
	if err := channel.Confirm(false); err != nil {
		return 0, err
	}
	returned := channel.NotifyReturn(make(chan amqp.Return, 1))

	claimToken, err := newOutboxClaimToken()
	if err != nil {
		return 0, err
	}
	messages, err := p.repo.ClaimPendingOutbox(ctx, limit, claimToken, 2*time.Minute)
	if err != nil {
		return 0, err
	}

	for _, message := range messages {
		routingKey, err := outboxRoutingKey(message.EventType)
		if err != nil {
			return 0, err
		}
		if err := p.client.Publish(ctx, channel, routingKey, message.Payload, returned); err != nil {
			return 0, fmt.Errorf("发布 outbox id=%d 失败: %w", message.ID, err)
		}
		if err := p.repo.MarkOutboxPublished(ctx, message.ID, message.ClaimToken); err != nil {
			return 0, fmt.Errorf("标记 outbox id=%d 已发布失败: %w", message.ID, err)
		}
	}

	return len(messages), nil
}

func outboxRoutingKey(eventType string) (string, error) {
	switch eventType {
	case "like.created", "favorite.created", "comment.created", "follow.created":
		return "notification." + eventType, nil
	case "counter.video.delta":
		return eventType, nil
	default:
		return "", fmt.Errorf("未知 Outbox 事件类型：%s", eventType)
	}
}

// RunForever 满批后立即继续发送；只有未满批、空闲或故障时才等待。
func (p *OutboxPublisher) RunForever(ctx context.Context) {
	for ctx.Err() == nil {
		count, err := p.publishBatch(ctx, 100)
		if err == nil && count == 100 {
			continue
		}
		wait := 200 * time.Millisecond
		if err != nil {
			log.Printf("发布 Outbox 失败：%v", err)
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func newOutboxClaimToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("生成 outbox claim token 失败: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}
