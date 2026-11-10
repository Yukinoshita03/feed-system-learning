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

func (c *Consumer) HandleComment(ctx context.Context, delivery amqp.Delivery) error {
	var event CommentCreatedEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		return fmt.Errorf("解析评论事件失败: %w", err)
	}

	if err := c.repo.CreateCommentNotification(ctx, event); err != nil {
		return fmt.Errorf("写入评论通知失败: %w", err)
	}

	if err := delivery.Ack(false); err != nil {
		return fmt.Errorf("确认 RabbitMQ 消息失败: %w", err)
	}
	return nil
}

func (c *Consumer) HandleFavorite(ctx context.Context, delivery amqp.Delivery) error {
	var event FavoriteCreatedEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		return fmt.Errorf("解析收藏事件失败: %w", err)
	}

	if err := c.repo.CreateFavoriteNotification(ctx, event); err != nil {
		return fmt.Errorf("写入收藏通知失败: %w", err)
	}

	if err := delivery.Ack(false); err != nil {
		return fmt.Errorf("确认 RabbitMQ 消息失败: %w", err)
	}
	return nil
}
func (c *Consumer) HandleFollow(ctx context.Context, delivery amqp.Delivery) error {
	var event FollowCreatedEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		return fmt.Errorf("解析关注事件失败: %w", err)
	}

	if err := c.repo.CreateFollowNotification(ctx, event); err != nil {
		return fmt.Errorf("写入关注通知失败: %w", err)
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
			switch delivery.RoutingKey {
			case "notification.like.created":
				if err := c.HandleLike(ctx, delivery); err != nil {
					return err
				}

			case "notification.comment.created":
				if err := c.HandleComment(ctx, delivery); err != nil {
					return err
				}
			case "notification.favorite.created":
				if err := c.HandleFavorite(ctx, delivery); err != nil {
					return err
				}
			case "notification.follow.created":
				if err := c.HandleFollow(ctx, delivery); err != nil {
					return err
				}
			default:
				// 未知事件不应该无限重试
				if err := delivery.Nack(false, false); err != nil {
					return err
				}
			}
		}
	}
}
