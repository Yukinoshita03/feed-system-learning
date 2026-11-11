package notification

import (
	"context"
	"errors"
	"fmt"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

// stubAcknowledger 记录 Nack 的 requeue 参数，用来断言一条消息是被"送进死信"
// 还是"退回队列重试"。
type stubAcknowledger struct {
	nacked  bool
	requeue bool
	acked   bool
	err     error
}

func (s *stubAcknowledger) Ack(uint64, bool) error {
	s.acked = true
	return s.err
}

func (s *stubAcknowledger) Nack(_ uint64, _ bool, requeue bool) error {
	s.nacked = true
	s.requeue = requeue
	return s.err
}

func (s *stubAcknowledger) Reject(uint64, bool) error { return s.err }

// 永久错误必须 Nack(requeue=false)：只有 false 才会触发队列上配置的
// x-dead-letter-exchange，true 会让消息回到队列，又变成无限循环。
func TestFailSendsPermanentErrorToDeadLetter(t *testing.T) {
	acker := &stubAcknowledger{}
	delivery := amqp.Delivery{
		Acknowledger: acker,
		RoutingKey:   "notification.like.created",
	}
	err := fmt.Errorf("%w：解析点赞事件失败", ErrPermanent)

	consumer := &Consumer{}
	if got := consumer.fail(delivery, err); got != nil {
		t.Fatalf("永久错误不应再向上返回，实际 %v", got)
	}
	if !acker.nacked {
		t.Fatal("永久错误必须 Nack")
	}
	if acker.requeue {
		t.Fatal("requeue 必须是 false，否则消息回到队列继续无限重试")
	}
}

// 临时错误不能 Nack：要留给连接重建后重新投递，否则数据库抖动一次就丢消息。
func TestFailKeepsTransientErrorForRedelivery(t *testing.T) {
	acker := &stubAcknowledger{}
	delivery := amqp.Delivery{
		Acknowledger: acker,
		RoutingKey:   "notification.like.created",
	}
	transient := errors.New("写入点赞通知失败: database is down")

	consumer := &Consumer{}
	got := consumer.fail(delivery, transient)
	if !errors.Is(got, transient) {
		t.Fatalf("临时错误应原样返回，实际 %v", got)
	}
	if acker.nacked || acker.acked {
		t.Fatal("临时错误既不应 ACK 也不应 Nack，交给连接重建后重新投递")
	}
}

// 字段校验失败属于永久错误：重投一万次字段还是空的。
func TestFieldValidationErrorsArePermanent(t *testing.T) {
	repo := NewRepository(nil)
	ctx := context.Background()

	cases := map[string]error{
		"点赞": repo.CreateLikeNotification(ctx, LikeCreatedEvent{}),
		"评论": repo.CreateCommentNotification(ctx, CommentCreatedEvent{}),
		"收藏": repo.CreateFavoriteNotification(ctx, FavoriteCreatedEvent{}),
		"关注": repo.CreateFollowNotification(ctx, FollowCreatedEvent{}),
	}
	for name, err := range cases {
		if err == nil {
			t.Fatalf("%s：字段全为空时应报错", name)
		}
		if !errors.Is(err, ErrPermanent) {
			t.Fatalf("%s：字段校验失败应标记为永久错误，实际 %v", name, err)
		}
	}
}

// 解析失败必须被标记为永久错误，否则 fail() 会把损坏的消息一直留在队列里。
func TestParseFailureIsPermanent(t *testing.T) {
	consumer := &Consumer{}
	acker := &stubAcknowledger{}
	delivery := amqp.Delivery{
		Acknowledger: acker,
		RoutingKey:   "notification.like.created",
		Body:         []byte("this is not json"),
	}

	err := consumer.HandleLike(context.Background(), delivery)
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("解析失败应返回 ErrPermanent，实际 %v", err)
	}

	// 交给 fail 之后应进死信，而不是退回队列。
	if got := consumer.fail(delivery, err); got != nil {
		t.Fatalf("fail 应吞掉永久错误，实际 %v", got)
	}
	if !acker.nacked || acker.requeue {
		t.Fatalf("解析失败的消息应 Nack(requeue=false)，实际 nacked=%v requeue=%v",
			acker.nacked, acker.requeue)
	}
}
