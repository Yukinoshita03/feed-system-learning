package mq

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	config Config
	conn   *amqp.Connection
}

func NewClient(config Config) (*Client, error) {
	if config.URL == "" {
		return nil, fmt.Errorf("RabbitMQ URL 不能为空")
	}

	conn, err := amqp.Dial(config.URL)
	if err != nil {
		return nil, fmt.Errorf("连接 RabbitMQ 失败: %w", err)
	}

	return &Client{
		config: config,
		conn:   conn,
	}, nil
}
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	return c.conn.Close()
}

func (c *Client) IsClosed() bool {
	return c == nil || c.conn == nil || c.conn.IsClosed()
}
func (c *Client) OpenChannel() (*amqp.Channel, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("RabbitMQ 客户端未初始化")
	}
	if c.conn.IsClosed() {
		return nil, fmt.Errorf("RabbitMQ 连接已经关闭")
	}
	channel, err := c.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("创建 RabbitMQ Channel 失败: %w", err)
	}

	return channel, nil
}
func (c *Client) DeclareExchange(channel *amqp.Channel) error {
	if c == nil || channel == nil {
		return fmt.Errorf("RabbitMQ 客户端或 Channel 未初始化")
	}

	if c.config.Exchange == "" {
		return fmt.Errorf("Exchange 名称不能为空")
	}

	if c.config.ExchangeType == "" {
		return fmt.Errorf("Exchange 类型不能为空")
	}

	err := channel.ExchangeDeclare(
		c.config.Exchange,     // 名称：feed.events
		c.config.ExchangeType, // 类型：topic
		true,                  // durable：RabbitMQ 重启后保留
		false,                 // autoDelete：没有消费者时不自动删除
		false,                 // internal：允许生产者直接发布
		false,                 // noWait：等待 RabbitMQ 确认声明结果
		nil,                   // 额外参数
	)
	if err != nil {
		return fmt.Errorf("声明 Exchange 失败: %w", err)
	}

	return nil
}
func (c *Client) DeclareQueue(channel *amqp.Channel) (amqp.Queue, error) {
	if c == nil || channel == nil {
		return amqp.Queue{}, fmt.Errorf("RabbitMQ 客户端或 Channel 未初始化")
	}

	if c.config.Queue == "" {
		return amqp.Queue{}, fmt.Errorf("Queue 名称不能为空")
	}

	queue, err := channel.QueueDeclare(
		c.config.Queue, // 名称：feed.notification
		true,           // durable：RabbitMQ 重启后保留队列
		false,          // autoDelete：消费者断开后不自动删除
		false,          // exclusive：不归某个连接独占
		false,          // noWait：等待 RabbitMQ 确认
		nil,            // 额外参数
	)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("声明 Queue 失败: %w", err)
	}

	return queue, nil
}
func (c *Client) BindQueue(channel *amqp.Channel) error {
	if c == nil || channel == nil {
		return fmt.Errorf("RabbitMQ 客户端或 Channel 未初始化")
	}

	if c.config.Queue == "" {
		return fmt.Errorf("Queue 名称不能为空")
	}
	if c.config.Exchange == "" {
		return fmt.Errorf("Exchange 名称不能为空")
	}
	if c.config.BindingKey == "" {
		return fmt.Errorf("BindingKey 不能为空")
	}

	err := channel.QueueBind(
		c.config.Queue,      // Queue：feed.notification
		c.config.BindingKey, // BindingKey：notification.#
		c.config.Exchange,   // Exchange：feed.events
		false,               // noWait：等待 RabbitMQ 确认
		nil,                 // 额外参数
	)
	if err != nil {
		return fmt.Errorf("绑定 Queue 失败: %w", err)
	}

	return nil
}

func (c *Client) SetupTopology() error {
	channel, err := c.OpenChannel()
	if err != nil {
		return err
	}
	defer channel.Close()

	if err := c.DeclareExchange(channel); err != nil {
		return err
	}
	if _, err := c.DeclareQueue(channel); err != nil {
		return err
	}
	if err := c.BindQueue(channel); err != nil {
		return err
	}

	return nil
}

func (c *Client) Publish(
	ctx context.Context,
	channel *amqp.Channel,
	routingKey string,
	body []byte,
	returned chan amqp.Return,
) error {
	if c == nil || channel == nil {
		return fmt.Errorf("RabbitMQ 客户端或 Channel 未初始化")
	}

	if c.IsClosed() {
		return fmt.Errorf("RabbitMQ 连接已经关闭")
	}

	if c.config.Exchange == "" {
		return fmt.Errorf("Exchange 名称不能为空")
	}

	if routingKey == "" {
		return fmt.Errorf("routing key 不能为空")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发布 RabbitMQ 消息前 context 已结束: %w", err)
	}

	confirmation, err := channel.PublishWithDeferredConfirm(
		c.config.Exchange,
		routingKey,
		true,  // mandatory：暂时不接收无法路由的消息
		false, // immediate：RabbitMQ 不使用这个参数
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("发布 RabbitMQ 消息失败: %w", err)
	}
	if confirmation == nil {
		return fmt.Errorf("Channel 尚未启用 Publisher Confirm")
	}

	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("等待 RabbitMQ 发布确认失败: %w", err)
	}
	if !acked {
		return fmt.Errorf("RabbitMQ 拒绝了这条消息")
	}
	select {
	case msg, ok := <-returned:
		if !ok {
			return fmt.Errorf("RabbitMQ Return 通知通道已关闭")
		}
		return fmt.Errorf(
			"消息未路由：reply_code=%d reason=%s exchange=%s routing_key=%s",
			msg.ReplyCode,
			msg.ReplyText,
			msg.Exchange,
			msg.RoutingKey,
		)
	default:
	}

	return nil
}
func (c *Client) Consume(
	ctx context.Context,
	channel *amqp.Channel,
	consumerTag string,
) (<-chan amqp.Delivery, error) {
	if c == nil || channel == nil {
		return nil, fmt.Errorf("RabbitMQ 客户端或 Channel 未初始化")
	}

	if c.IsClosed() {
		return nil, fmt.Errorf("RabbitMQ 连接已经关闭")
	}

	if c.config.Queue == "" {
		return nil, fmt.Errorf("Queue 名称不能为空")
	}

	if c.config.PrefetchCount <= 0 {
		return nil, fmt.Errorf("PrefetchCount 必须是正整数")
	}

	if err := channel.Qos(
		c.config.PrefetchCount,
		0,
		false,
	); err != nil {
		return nil, fmt.Errorf("设置 RabbitMQ QoS 失败: %w", err)
	}

	deliveries, err := channel.ConsumeWithContext(
		ctx,
		c.config.Queue, // Queue
		consumerTag,    // ConsumerTag
		false,          // autoAck：关闭自动 ACK
		false,          // exclusive：不独占队列
		false,          // noLocal
		false,          // noWait
		nil,            // 额外参数
	)
	if err != nil {
		return nil, fmt.Errorf("注册 RabbitMQ 消费者失败: %w", err)
	}

	return deliveries, nil
}
