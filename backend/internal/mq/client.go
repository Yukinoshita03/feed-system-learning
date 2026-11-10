package mq

import (
	"context"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	mu     sync.Mutex
	config Config
	conn   *amqp.Connection
}

func NewClient(config Config) (*Client, error) {
	if config.URL == "" {
		return nil, fmt.Errorf("RabbitMQ URL 不能为空")
	}

	client := &Client{config: config}
	if _, err := client.connection(); err != nil {
		return nil, err
	}

	return client, nil
}

// connection 返回当前可用的连接；连接已关闭时重新拨号。
//
// amqp.Connection 断了不会自愈，也不存在"重新打开"——它是一条已经建立的
// 会话。把 Dial 的结果长期保存成字段，就等于把"会断的资源"当成永久对象：
// 一旦断开，之后所有 OpenChannel 都在一个死对象上操作，重试多少次都失败。
// 因此每次取连接都检查一次，必要时重新建立。
func (c *Client) connection() (*amqp.Connection, error) {
	if c == nil {
		return nil, fmt.Errorf("RabbitMQ 客户端未初始化")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil && !c.conn.IsClosed() {
		return c.conn, nil
	}

	conn, err := amqp.Dial(c.config.URL)
	if err != nil {
		return nil, fmt.Errorf("连接 RabbitMQ 失败: %w", err)
	}
	c.conn = conn
	return conn, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// IsClosed 表示当前没有可用连接；之后的 OpenChannel 会尝试重新拨号，
// 所以返回 true 不代表客户端已经废弃。
func (c *Client) IsClosed() bool {
	if c == nil {
		return true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.conn == nil || c.conn.IsClosed()
}

func (c *Client) OpenChannel() (*amqp.Channel, error) {
	conn, err := c.connection()
	if err != nil {
		return nil, err
	}

	channel, err := conn.Channel()
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

	return declareExchange(channel, c.config.Exchange, c.config.ExchangeType)
}

// declareExchange 声明一个 durable exchange；业务 exchange 和死信 exchange 共用。
func declareExchange(channel *amqp.Channel, name, kind string) error {
	err := channel.ExchangeDeclare(
		name,
		kind,
		true,  // durable：RabbitMQ 重启后保留
		false, // autoDelete：没有消费者时不自动删除
		false, // internal：允许生产者直接发布
		false, // noWait：等待 RabbitMQ 确认声明结果
		nil,   // 额外参数
	)
	if err != nil {
		return fmt.Errorf("声明 Exchange %s 失败: %w", name, err)
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
		amqp.Table{
			// 被 Nack(requeue=false) 的消息自动转到这里，不会凭空消失。
			"x-dead-letter-exchange": c.config.DeadLetterExchange,
			// 死信进入 DLX 时默认沿用原 routing key（notification.like.created），
			// 而 DLX 是 direct、绑定的是 dead，两者不匹配会让消息被 exchange
			// 直接丢弃——又是一次静默丢失。这里强制改写，保证能路由到 DLQ。
			"x-dead-letter-routing-key": deadLetterRoutingKey,
		},
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

// DeclareDeadLetterTopology 声明死信 exchange、死信队列，并把两者绑定。
//
// 死信队列自己不带 x-dead-letter-exchange，否则"死信的死信"会绕圈；
// 它也不挂消费者：死信要被人看见并处理，自动消费只会把问题藏起来。
func (c *Client) DeclareDeadLetterTopology(channel *amqp.Channel) error {
	if c == nil || channel == nil {
		return fmt.Errorf("RabbitMQ 客户端或 Channel 未初始化")
	}
	if c.config.DeadLetterExchange == "" || c.config.DeadLetterQueue == "" {
		return fmt.Errorf("死信 Exchange 和死信队列名称不能为空")
	}

	// 用 direct：所有死信共用一个 routing key，规则简单。
	if err := declareExchange(channel, c.config.DeadLetterExchange, "direct"); err != nil {
		return err
	}

	if _, err := channel.QueueDeclare(
		c.config.DeadLetterQueue,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,   // 不挂死信，避免死信再死信
	); err != nil {
		return fmt.Errorf("声明死信队列失败: %w", err)
	}

	if err := channel.QueueBind(
		c.config.DeadLetterQueue,
		deadLetterRoutingKey,
		c.config.DeadLetterExchange,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("绑定死信队列失败: %w", err)
	}

	return nil
}

// deadLetterRoutingKey 是死信进入 DLX 时使用的固定 routing key。
const deadLetterRoutingKey = "dead"

func (c *Client) SetupTopology() error {
	channel, err := c.OpenChannel()
	if err != nil {
		return err
	}
	defer channel.Close()

	// 死信 exchange 必须先于业务队列存在：队列声明时引用一个不存在的
	// exchange，RabbitMQ 会返回 NOT_FOUND。
	if err := c.DeclareDeadLetterTopology(channel); err != nil {
		return err
	}
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
