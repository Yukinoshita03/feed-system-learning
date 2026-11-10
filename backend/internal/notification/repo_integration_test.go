package notification

import (
	"context"
	"os"
	"testing"

	"feed-system-learning/internal/dbtest"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newNotificationFixture(t *testing.T) (*gorm.DB, *Repository) {
	t.Helper()
	dsn := os.Getenv("FEED_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FEED_TEST_MYSQL_DSN to a dedicated empty test database")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	dbtest.Migrate(t, database, &Notification{}, &OutboxMessage{})
	return database, NewRepository(database)
}

func countReceiver(t *testing.T, db *gorm.DB, receiverID uint) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&Notification{}).Where("receiver_id = ?", receiverID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// 同一条消息可能被重复投递（未 ACK 后连接重建），落库必须靠 event_key 保持幂等。
func TestCreateNotificationIsIdempotent(t *testing.T) {
	db, repo := newNotificationFixture(t)
	ctx := context.Background()
	const receiver, actor = uint(990001), uint(990002)
	t.Cleanup(func() {
		if err := db.Where("receiver_id = ?", receiver).Delete(&Notification{}).Error; err != nil {
			t.Error(err)
		}
	})

	comment := CommentCreatedEvent{CommentID: 7, VideoID: 11, ActorID: actor, ReceiverID: receiver}
	for i := 0; i < 3; i++ {
		if err := repo.CreateCommentNotification(ctx, comment); err != nil {
			t.Fatal(err)
		}
	}
	if got := countReceiver(t, db, receiver); got != 1 {
		t.Fatalf("notifications=%d after 3 deliveries, want=1", got)
	}

	favorite := FavoriteCreatedEvent{FavoriteID: 9, VideoID: 11, ActorID: actor, ReceiverID: receiver}
	for i := 0; i < 3; i++ {
		if err := repo.CreateFavoriteNotification(ctx, favorite); err != nil {
			t.Fatal(err)
		}
	}
	if got := countReceiver(t, db, receiver); got != 2 {
		t.Fatalf("notifications=%d after comment+favorite, want=2", got)
	}
}

// 评论 ID 和收藏 ID 来自两张不同的表，数字相同是完全正常的。
// event_key 必须带事件类型前缀，否则后写入的那条会被唯一索引静默丢弃。
func TestCommentAndFavoriteKeysDoNotCollide(t *testing.T) {
	db, repo := newNotificationFixture(t)
	ctx := context.Background()
	const receiver, actor = uint(990011), uint(990012)
	t.Cleanup(func() {
		if err := db.Where("receiver_id = ?", receiver).Delete(&Notification{}).Error; err != nil {
			t.Error(err)
		}
	})

	sameID := uint(42)
	if err := repo.CreateCommentNotification(ctx, CommentCreatedEvent{
		CommentID: sameID, VideoID: 1, ActorID: actor, ReceiverID: receiver,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateFavoriteNotification(ctx, FavoriteCreatedEvent{
		FavoriteID: sameID, VideoID: 1, ActorID: actor, ReceiverID: receiver,
	}); err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 0, 2)
	if err := db.Model(&Notification{}).Where("receiver_id = ?", receiver).
		Order("id ASC").Pluck("event_key", &keys).Error; err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("event_keys=%v, want 2 条（数字 ID 相同不代表是同一个事件）", keys)
	}
	if keys[0] != "comment:42" || keys[1] != "favorite:42" {
		t.Fatalf("event_keys=%v, want [comment:42 favorite:42]", keys)
	}
}

// 自己操作自己时不产生通知；消费端也要挡住这种事件，不能只依赖生产端过滤。
func TestCreateNotificationSkipsSelfAction(t *testing.T) {
	db, repo := newNotificationFixture(t)
	ctx := context.Background()
	const receiver = uint(990021)
	t.Cleanup(func() {
		if err := db.Where("receiver_id = ?", receiver).Delete(&Notification{}).Error; err != nil {
			t.Error(err)
		}
	})

	if err := repo.CreateCommentNotification(ctx, CommentCreatedEvent{
		CommentID: 1, VideoID: 1, ActorID: receiver, ReceiverID: receiver,
	}); err != nil {
		t.Fatal(err)
	}
	if got := countReceiver(t, db, receiver); got != 0 {
		t.Fatalf("notifications=%d, want=0（自己评论自己不该产生通知）", got)
	}
}

// 事件字段缺失说明生产者写错了或消息损坏，必须在落库前拒绝，避免写入脏数据。
func TestCreateNotificationRejectsInvalidEvent(t *testing.T) {
	_, repo := newNotificationFixture(t)
	ctx := context.Background()

	if err := repo.CreateCommentNotification(ctx, CommentCreatedEvent{CommentID: 1}); err == nil {
		t.Fatal("缺少 video_id/actor_id/receiver_id 的评论事件没有被拒绝")
	}
	if err := repo.CreateFavoriteNotification(ctx, FavoriteCreatedEvent{FavoriteID: 1}); err == nil {
		t.Fatal("缺少 video_id/actor_id/receiver_id 的收藏事件没有被拒绝")
	}
	if err := repo.CreateFollowNotification(ctx, FollowCreatedEvent{FollowID: 1}); err == nil {
		t.Fatal("缺少 actor_id/receiver_id 的关注事件没有被拒绝")
	}
}
