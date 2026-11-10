package video

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"feed-system-learning/internal/mq"
	"feed-system-learning/internal/notification"
)

// 端到端验证整条链路：
//
//	评论事务 → notification_outbox → Publisher → RabbitMQ → Consumer → notifications
//
// 它补上了 DB 级测试覆盖不到的部分：事件类型拼出的 routing key 必须和 Consumer 的
// 分发分支完全一致。如果写成 "favorite.create" 而 Consumer 等的是
// "notification.favorite.created"，消息会被 Nack 丢弃，这里就会超时失败。
//
// 需要真实的 MySQL 和 RabbitMQ，默认跳过。
func TestCommentOutboxEndToEnd(t *testing.T) {
	if os.Getenv("RABBITMQ_SMOKE") != "1" {
		t.Skip("设置 RABBITMQ_SMOKE=1 才运行 RabbitMQ 端到端测试")
	}
	f := newLikeFixture(t)
	t.Cleanup(cleanupInteractions(t, f))
	item := f.publish(t)
	actor := f.accounts[1].ID

	// 使用唯一名称，避免污染正在运行的开发环境里的 Exchange 和 Queue。
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	config, err := mq.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Exchange = "feed.e2e." + suffix
	config.Queue = "feed.e2e.queue." + suffix
	config.BindingKey = "notification.#"

	client, err := mq.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if channel, err := client.OpenChannel(); err == nil {
			_, _ = channel.QueueDelete(config.Queue, false, false, false)
			_ = channel.ExchangeDelete(config.Exchange, false, false)
			_ = channel.Close()
		}
		_ = client.Close()
	})
	if err := client.SetupTopology(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := notification.NewRepository(f.db)

	// ① 生产端：真实的评论事务，只应该写出 outbox。
	comments := NewCommentRepository(f.db, f.videos)
	if _, err := comments.Create(ctx, &Comment{VideoID: item.ID, AuthorID: actor, Content: "端到端验证"}); err != nil {
		t.Fatal(err)
	}
	var saved Comment
	if err := f.db.Where("video_id = ? AND author_id = ?", item.ID, actor).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	eventKey := fmt.Sprintf("comment:%d", saved.ID)

	// ② Publisher：反复发布，直到这条事件被标记为已投递。
	publisher := notification.NewOutboxPublisher(repo, client)
	for attempt := 0; attempt < 20; attempt++ {
		if err := publisher.PublishBatch(ctx, 100); err != nil {
			t.Fatal(err)
		}
		var published int64
		if err := f.db.Model(&notification.OutboxMessage{}).
			Where("event_key = ? AND published_at IS NOT NULL", eventKey).
			Count(&published).Error; err != nil {
			t.Fatal(err)
		}
		if published == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// ③ Consumer：后台消费，等待通知落库。
	consumer := notification.NewConsumer(repo, client)
	consumerCtx, stopConsumer := context.WithCancel(ctx)
	defer stopConsumer()
	go func() { _ = consumer.Run(consumerCtx, "feed-e2e-"+suffix) }()

	deadline := time.Now().Add(15 * time.Second)
	for {
		var count int64
		if err := f.db.Model(&notification.Notification{}).
			Where("receiver_id = ? AND event_key = ?", item.AuthorID, eventKey).
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待通知落库超时：event_key=%s 没有出现在 notifications 表里，"+
				"通常是 routing key 和 Consumer 的分发分支不一致", eventKey)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// ④ 落库内容正确。
	var savedEvent notification.Notification
	if err := f.db.Where("receiver_id = ? AND event_key = ?", item.AuthorID, eventKey).
		Take(&savedEvent).Error; err != nil {
		t.Fatal(err)
	}
	if savedEvent.Kind != notification.Comment {
		t.Fatalf("kind=%q, want %q", savedEvent.Kind, notification.Comment)
	}
	if savedEvent.Content != "评论了你的视频" {
		t.Fatalf("content=%q, want %q", savedEvent.Content, "评论了你的视频")
	}
	if savedEvent.ActorID != actor || savedEvent.VideoID != item.ID {
		t.Fatalf("notification=%+v, want actor=%d video=%d", savedEvent, actor, item.ID)
	}
}
