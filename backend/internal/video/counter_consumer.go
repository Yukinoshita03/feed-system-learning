package video

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"feed-system-learning/internal/mq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type CounterConsumer struct {
	repo   *CounterRepository
	client *mq.Client
}

func NewCounterConsumer(repo *CounterRepository, client *mq.Client) *CounterConsumer {
	return &CounterConsumer{repo: repo, client: client}
}

// ProcessBatch commits a counter batch before acknowledging its deliveries.
// A failure between the database commit and ACK is safe because re-delivery is idempotent.
func (c *CounterConsumer) ProcessBatch(ctx context.Context, batch []amqp.Delivery, events []CounterEvent) error {
	if err := c.repo.ApplyBatch(ctx, events); err != nil {
		return err
	}
	for _, delivery := range batch {
		if err := delivery.Ack(false); err != nil {
			return err
		}
	}
	return nil
}

func (c *CounterConsumer) Run(ctx context.Context) error {
	if err := c.client.SetupCounterTopology(); err != nil {
		return err
	}
	channel, err := c.client.OpenChannel()
	if err != nil {
		return err
	}
	defer channel.Close()
	deliveries, err := c.client.ConsumeCounters(ctx, channel)
	if err != nil {
		return err
	}
	batch := make([]amqp.Delivery, 0, 100)
	events := make([]CounterEvent, 0, 100)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	flush := func() error {
		if err := c.ProcessBatch(ctx, batch, events); err != nil {
			return err
		}
		batch, events = batch[:0], events[:0]
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			// 未提交的批次保持未 ACK，关闭 Channel 后由 Broker 重新投递。
			return nil
		case <-timer.C:
			if len(batch) > 0 {
				if err := flush(); err != nil {
					return err
				}
			}
		case delivery, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("计数 delivery channel 已关闭")
			}
			var event CounterEvent
			err := json.Unmarshal(delivery.Body, &event)
			if delivery.RoutingKey != CounterEventType || err != nil || event.Validate() != nil {
				log.Printf("计数消息不合法，转入死信：routing_key=%s", delivery.RoutingKey)
				if err := delivery.Nack(false, false); err != nil {
					return err
				}
				continue
			}
			if len(batch) == 0 {
				timer.Reset(100 * time.Millisecond)
			}
			batch = append(batch, delivery)
			events = append(events, event)
			if len(batch) == 100 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
}

func (c *CounterConsumer) RunForever(ctx context.Context) {
	backoff := 500 * time.Millisecond
	for ctx.Err() == nil {
		started := time.Now()
		err := c.Run(ctx)
		if ctx.Err() != nil || err == nil {
			return
		}
		if time.Since(started) >= time.Minute {
			backoff = 500 * time.Millisecond
		}
		log.Printf("计数消费者退出，%s 后重启：%v", backoff, err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
