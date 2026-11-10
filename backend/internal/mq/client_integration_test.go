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

	deliveries, err := client.Consume(ctx, channel)
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
