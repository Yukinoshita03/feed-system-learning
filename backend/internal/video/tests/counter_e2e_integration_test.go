package video_test

import video "feed-system-learning/internal/video"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"feed-system-learning/internal/mq"
	"feed-system-learning/internal/notification"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestCounterOutboxRabbitMQEndToEnd(t *testing.T) {
	if os.Getenv("RABBITMQ_SMOKE") != "1" {
		t.Skip("设置 RABBITMQ_SMOKE=1 才运行真实消息队列测试")
	}
	f := newLikeFixture(t)
	item := f.publish(t)
	config, err := mq.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	config.Exchange = "counter.e2e." + suffix
	config.Queue = config.Exchange + ".notification"
	config.DeadLetterExchange = config.Exchange + ".dlx"
	config.DeadLetterQueue = config.Queue + ".dlq"
	config.CounterQueue = config.Exchange + ".counter"
	config.CounterDeadLetterQueue = config.CounterQueue + ".dlq"
	client, err := mq.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if channel, err := client.OpenChannel(); err == nil {
			for _, name := range []string{config.Queue, config.DeadLetterQueue, config.CounterQueue, config.CounterDeadLetterQueue} {
				_, _ = channel.QueueDelete(name, false, false, false)
			}
			_ = channel.ExchangeDelete(config.Exchange, false, false)
			_ = channel.ExchangeDelete(config.DeadLetterExchange, false, false)
			_ = channel.Close()
		}
		_ = client.Close()
	})
	if err := client.SetupTopology(); err != nil {
		t.Fatal(err)
	}
	if err := client.SetupCounterTopology(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// 消费者未启动，HTTP 对应的业务动作仍应完成，投影保持原值。
	if _, err := f.service.Like(ctx, item.ID, item.AuthorID); err != nil {
		t.Fatal(err)
	}
	publisher := notification.NewOutboxPublisher(notification.NewRepository(f.db), client)
	if err := publisher.PublishBatch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if rawCounter(t, f, item.ID).LikesCount != 0 {
		t.Fatal("未启动计数消费者时计数提前更新")
	}
	consumerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- video.NewCounterConsumer(video.NewCounterRepository(f.db), client).Run(consumerCtx) }()
	defer func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("停止计数消费者失败：%v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("计数消费者未响应退出")
		}
	}()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("计数链路等待超时")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	wait(func() bool { return rawCounter(t, f, item.ID).LikesCount == 1 })
	channel, err := client.OpenChannel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	if err := channel.Confirm(false); err != nil {
		t.Fatal(err)
	}
	returned := channel.NotifyReturn(make(chan amqp.Return, 1))
	// 同一个已提交事件再次从 Broker 投递，计数不能再次增加。
	event := f.counterEvents(t, item.ID)[0]
	body, _ := json.Marshal(event)
	if err := client.Publish(ctx, channel, video.CounterEventType, body, returned); err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(ctx, channel, video.CounterEventType, []byte("坏消息"), returned); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		queue, err := channel.QueueInspect(config.CounterDeadLetterQueue)
		if err != nil {
			t.Fatal(err)
		}
		return queue.Messages == 1
	})
	wait(func() bool {
		// 重投不能增加消费记录；后续取消点赞归零再验证计数没有重复增加。
		var receipts int64
		if err := f.db.Model(&video.VideoCounterConsumption{}).Where("event_id = ?", event.EventID).Count(&receipts).Error; err != nil {
			t.Fatal(err)
		}
		return receipts == 1
	})
	if _, err := f.service.Unlike(ctx, item.ID, item.AuthorID); err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishBatch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return rawCounter(t, f, item.ID).LikesCount == 0 })
}

type counterAck struct {
	fail  bool
	calls int
}

func (a *counterAck) Ack(_ uint64, _ bool) error {
	a.calls++
	if a.fail {
		return errors.New("模拟提交后 ACK 失败")
	}
	return nil
}
func (*counterAck) Nack(uint64, bool, bool) error { return nil }
func (*counterAck) Reject(uint64, bool) error     { return nil }

func TestCounterCommitBeforeAckFailureIsIdempotent(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	event := counterEvent(item.ID, "ack-failure", 1)
	ack := &counterAck{fail: true}
	batch := []amqp.Delivery{{Acknowledger: ack, DeliveryTag: 1}}
	consumer := video.NewCounterConsumer(video.NewCounterRepository(f.db), nil)
	if err := consumer.ProcessBatch(context.Background(), batch, []video.CounterEvent{event}); err == nil {
		t.Fatal("应返回 ACK 错误")
	}
	if rawCounter(t, f, item.ID).LikesCount != 1 {
		t.Fatal("ACK 失败前数据库应已提交")
	}
	ack.fail = false
	if err := consumer.ProcessBatch(context.Background(), batch, []video.CounterEvent{event}); err != nil {
		t.Fatal(err)
	}
	if rawCounter(t, f, item.ID).LikesCount != 1 || ack.calls != 2 {
		t.Fatal("ACK 失败后重投被重复计数")
	}
}
