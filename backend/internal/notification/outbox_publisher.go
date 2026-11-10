package notification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	if p == nil || p.repo == nil || p.client == nil {
		return fmt.Errorf("outbox publisher 未初始化")
	}
	if limit <= 0 {
		return fmt.Errorf("outbox batch limit 必须是正整数")
	}

	channel, err := p.client.OpenChannel()
	if err != nil {
		return err
	}
	defer channel.Close()
	if err := channel.Confirm(false); err != nil {
		return err
	}
	returned := channel.NotifyReturn(make(chan amqp.Return, 1))

	claimToken, err := newOutboxClaimToken()
	if err != nil {
		return err
	}
	messages, err := p.repo.ClaimPendingOutbox(ctx, limit, claimToken, 2*time.Minute)
	if err != nil {
		return err
	}

	for _, message := range messages {
		routingKey := fmt.Sprintf("notification.%s", message.EventType)
		if err := p.client.Publish(ctx, channel, routingKey, message.Payload, returned); err != nil {
			return fmt.Errorf("发布 outbox id=%d 失败: %w", message.ID, err)
		}
		if err := p.repo.MarkOutboxPublished(ctx, message.ID, message.ClaimToken); err != nil {
			return fmt.Errorf("标记 outbox id=%d 已发布失败: %w", message.ID, err)
		}
	}

	return nil
}

func newOutboxClaimToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("生成 outbox claim token 失败: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}
