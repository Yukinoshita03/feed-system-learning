package notification

import (
	"context"
	"encoding/json"
	"feed-system-learning/internal/mq"
	"fmt"
	amqp "github.com/rabbitmq/amqp091-go"
)

type Consumer struct {
	repo   *Repository
	client *mq.Client
}

func NewConsumer(repo *Repository, client *mq.Client) *Consumer {
	return &Consumer{repo: repo, client: client}
}

func (c *Consumer) HandleLike(ctx context.Context, delivery amqp.Delivery) error {
	var event LikeCreatedEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		return fmt.Errorf("解析点赞事件失败: %w", err)
	}

	if err := c.repo.CreateLikeNotification(ctx, event); err != nil {
		return fmt.Errorf("写入点赞通知失败: %w", err)
	}

	if err := delivery.Ack(false); err != nil {
		return fmt.Errorf("确认 RabbitMQ 消息失败: %w", err)
	}
	return nil
}

// 消费
func (c *Consumer) Run(ctx context.Context, consumerTag string) error {
	channel, err := c.client.OpenChannel()
	if err != nil {
		return err
	}
	defer channel.Close()

	deliveries, err := c.client.Consume(ctx, channel, consumerTag)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("RabbitMQ delivery channel 已关闭")
			}
			if err := c.HandleLike(ctx, delivery); err != nil {
				// 不 ACK；退出并关闭 channel 后，RabbitMQ 会重新投递未确认的消息。
				return err
			}
		}
	}
}
