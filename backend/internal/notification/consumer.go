package notification

import (
	"context"
	"encoding/json"
	"errors"
	"feed-system-learning/internal/mq"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Consumer struct {
	repo   *Repository
	client *mq.Client
}

func NewConsumer(repo *Repository, client *mq.Client) *Consumer {
	return &Consumer{repo: repo, client: client}
}

// ErrPermanent 表示重投也不会成功的错误：消息本身坏了或字段不合法。
// 这类消息必须直接进死信，否则会一直留在队列里反复杀死消费者。
var ErrPermanent = errors.New("消息无法处理")

// fail 决定这次失败的处置方式。
//
// 两类错误的区别是整个死信机制的意义所在：
//   - 永久错误（无法解析、字段为空）：重投一万次也一样，Nack(requeue=false)
//     交给 x-dead-letter-exchange，消息进死信队列等人排查。
//   - 临时错误（数据库抖动、连接断开）：不 ACK 直接返回，连接重建后消息
//     会重新投递，由 supervisor 的退避控制重试频率。
func (c *Consumer) fail(delivery amqp.Delivery, err error) error {
	if errors.Is(err, ErrPermanent) {
		log.Printf("消息无法处理，转入死信队列：routing_key=%s err=%v", delivery.RoutingKey, err)
		// requeue 必须是 false 才会触发死信；写 true 就回到无限循环。
		return delivery.Nack(false, false)
	}
	return err
}

func (c *Consumer) HandleLike(ctx context.Context, delivery amqp.Delivery) error {
	var event LikeCreatedEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		return fmt.Errorf("%w：解析点赞事件失败: %v", ErrPermanent, err)
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
		return fmt.Errorf("%w：解析评论事件失败: %v", ErrPermanent, err)
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
		return fmt.Errorf("%w：解析收藏事件失败: %v", ErrPermanent, err)
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
		return fmt.Errorf("%w：解析关注事件失败: %v", ErrPermanent, err)
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
	// 重连后 broker 可能已经没有队列（重启且未保留数据），而声明是幂等的，
	// 所以每次启动都先确保 exchange/queue/binding 存在。
	if err := c.client.SetupTopology(); err != nil {
		return err
	}

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
					return c.fail(delivery, err)
				}

			case "notification.comment.created":
				if err := c.HandleComment(ctx, delivery); err != nil {
					return c.fail(delivery, err)
				}
			case "notification.favorite.created":
				if err := c.HandleFavorite(ctx, delivery); err != nil {
					return c.fail(delivery, err)
				}
			case "notification.follow.created":
				if err := c.HandleFollow(ctx, delivery); err != nil {
					return c.fail(delivery, err)
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
