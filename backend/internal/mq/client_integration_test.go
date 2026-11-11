package mq

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRabbitMQSmoke(t *testing.T) {
	if os.Getenv("RABBITMQ_SMOKE") != "1" {
		t.Skip("设置 RABBITMQ_SMOKE=1 才运行 RabbitMQ smoke test")
	}

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	// 使用唯一名称，避免污染已有的业务 Exchange 和 Queue。
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	config.Exchange = "feed.smoke." + suffix
	config.Queue = "feed.smoke.queue." + suffix
	config.BindingKey = "notification.smoke." + suffix
	config.ConsumerTag = "feed-smoke-" + suffix

	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetupTopology(); err != nil {
		t.Fatal(err)
	}

	channel, err := client.OpenChannel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	returned := channel.NotifyReturn(make(chan amqp.Return, 1))
	if err := channel.Confirm(false); err != nil {
		t.Fatalf("启用 Publisher Confirm 失败: %v", err)
	}

	defer func() {
		_, _ = channel.QueueDelete(config.Queue, false, false, false)
		_ = channel.ExchangeDelete(config.Exchange, false, false)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deliveries, err := client.Consume(ctx, channel, config.ConsumerTag)
	if err != nil {
		t.Fatal(err)
	}

	expected := []byte(`{"type":"rabbitmq-smoke"}`)
	badRoutingKey := "video.smoke." + suffix
	if err := client.Publish(ctx, channel, badRoutingKey, expected, returned); err == nil {
		t.Fatal("不可路由的消息应让 Publish 返回错误")
	}

	if err := client.Publish(ctx, channel, config.BindingKey, expected, returned); err != nil {
		t.Fatal(err)
	}

	select {
	case delivery, ok := <-deliveries:
		if !ok {
			t.Fatal("RabbitMQ 消费通道已关闭")
		}

		if string(delivery.Body) != string(expected) {
			t.Fatalf("消息内容不一致，期望 %s，实际 %s", expected, delivery.Body)
		}

		if err := delivery.Ack(false); err != nil {
			t.Fatal(err)
		}

	case <-ctx.Done():
		t.Fatal("等待 RabbitMQ 消息超时")
	}
}

// 连接断开后必须能重新拨号，否则消费者和 Publisher 会永久卡在
// "RabbitMQ 连接已经关闭"上——重试多少次都没用，因为重试作用于同一个死对象。
func TestRabbitMQReconnectsAfterConnectionLoss(t *testing.T) {
	if os.Getenv("RABBITMQ_SMOKE") != "1" {
		t.Skip("设置 RABBITMQ_SMOKE=1 才运行 RabbitMQ 测试")
	}
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	first, err := client.OpenChannel()
	if err != nil {
		t.Fatalf("初始 OpenChannel 失败: %v", err)
	}
	_ = first.Close()

	// 模拟网络中断：直接关掉底层连接，等价于 broker 重启。
	client.mu.Lock()
	if err := client.conn.Close(); err != nil {
		client.mu.Unlock()
		t.Fatal(err)
	}
	client.mu.Unlock()

	if !client.IsClosed() {
		t.Fatal("底层连接关闭后 IsClosed 应返回 true")
	}

	// 关键断言：这里应该触发重新拨号，而不是继续在死连接上报错。
	second, err := client.OpenChannel()
	if err != nil {
		t.Fatalf("连接断开后重连失败: %v", err)
	}
	_ = second.Close()

	if client.IsClosed() {
		t.Fatal("重连成功后 IsClosed 应返回 false")
	}
}

// 多个 worker 会在同一时刻发现连接断开并同时重连；锁必须保证它们都能成功，
// 而不是互相踩坏 Client 的 conn 字段。
func TestRabbitMQConcurrentReconnect(t *testing.T) {
	if os.Getenv("RABBITMQ_SMOKE") != "1" {
		t.Skip("设置 RABBITMQ_SMOKE=1 才运行 RabbitMQ 测试")
	}
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	client.mu.Lock()
	if err := client.conn.Close(); err != nil {
		client.mu.Unlock()
		t.Fatal(err)
	}
	client.mu.Unlock()

	const workers = 16
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			<-start
			channel, err := client.OpenChannel()
			if err == nil {
				err = channel.Close()
			}
			errs <- err
		}()
	}
	close(start)

	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("并发重连失败: %v", err)
		}
	}
}

// 验证 x-dead-letter-exchange 真的生效：被 Nack(requeue=false) 的消息
// 应该出现在死信队列里，而不是凭空消失。
func TestRabbitMQRejectedMessageReachesDeadLetterQueue(t *testing.T) {
	if os.Getenv("RABBITMQ_SMOKE") != "1" {
		t.Skip("设置 RABBITMQ_SMOKE=1 才运行 RabbitMQ 测试")
	}
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	config.Exchange = "feed.test.ex." + suffix
	config.Queue = "feed.test.queue." + suffix
	config.BindingKey = "notification.#"
	config.DeadLetterExchange = "feed.test.dlx." + suffix
	config.DeadLetterQueue = "feed.test.dlq." + suffix

	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetupTopology(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	channel, err := client.OpenChannel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = channel.QueueDelete(config.Queue, false, false, false)
		_, _ = channel.QueueDelete(config.DeadLetterQueue, false, false, false)
		_ = channel.ExchangeDelete(config.Exchange, false, false)
		_ = channel.ExchangeDelete(config.DeadLetterExchange, false, false)
		_ = channel.Close()
	}()

	// 发一条消息，然后用 Nack(requeue=false) 拒绝它。
	if err := channel.Confirm(false); err != nil {
		t.Fatal(err)
	}
	if err := channel.PublishWithContext(ctx, config.Exchange, "notification.like.created", false, false,
		amqp.Publishing{ContentType: "application/json", Body: []byte("poison")}); err != nil {
		t.Fatal(err)
	}

	consumeChannel, err := client.OpenChannel()
	if err != nil {
		t.Fatal(err)
	}
	defer consumeChannel.Close()
	deliveries, err := client.Consume(ctx, consumeChannel, "dlq-test-"+suffix)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case delivery := <-deliveries:
		if err := delivery.Nack(false, false); err != nil {
			t.Fatalf("Nack 失败: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("没有收到消息")
	}

	// 死信路由是异步的，轮询等待它落到 DLQ。
	inspect, err := client.OpenChannel()
	if err != nil {
		t.Fatal(err)
	}
	defer inspect.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		queue, err := inspect.QueueInspect(config.DeadLetterQueue)
		if err == nil && queue.Messages == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("消息没有进入死信队列，DLQ 当前深度=%d err=%v", queue.Messages, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
