package social

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"feed-system-learning/internal/account"
	"feed-system-learning/internal/dbtest"
	"feed-system-learning/internal/notification"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newFollowFixture(t *testing.T) (*gorm.DB, *Repository, account.Account, account.Account) {
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
	dbtest.Migrate(t, database, &account.Account{}, &Follow{}, &notification.Notification{}, &notification.OutboxMessage{})

	users := make([]account.Account, 0, 2)
	for i := 0; i < 2; i++ {
		users = append(users, account.Account{
			Username: fmt.Sprintf("follow_%d_%d", time.Now().UnixNano(), i),
			Password: "test-password-hash",
		})
	}
	if err := database.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, user := range users {
			if err := database.Where("follower_id = ? OR vlogger_id = ?", user.ID, user.ID).Delete(&Follow{}).Error; err != nil {
				t.Error(err)
			}
			if err := database.Where("receiver_id = ? OR actor_id = ?", user.ID, user.ID).Delete(&notification.Notification{}).Error; err != nil {
				t.Error(err)
			}
			if err := database.Where("JSON_EXTRACT(payload, '$.receiver_id') = ? OR JSON_EXTRACT(payload, '$.actor_id') = ?", user.ID, user.ID).
				Delete(&notification.OutboxMessage{}).Error; err != nil {
				t.Error(err)
			}
			if err := database.Delete(&user).Error; err != nil {
				t.Error(err)
			}
		}
	})
	return database, NewRepository(database), users[0], users[1]
}

// 关注只写 outbox，通知由 Consumer 异步落库；关注事件没有 video_id。
func TestFollowPublishesOutboxEvent(t *testing.T) {
	db, repo, actor, target := newFollowFixture(t)
	ctx := context.Background()

	result, err := repo.SetFollowing(ctx, actor.ID, target.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsFollowing || result.FollowingCount != 1 || result.FollowerCount != 1 {
		t.Fatalf("result=%+v, want following with 1/1 counters", result)
	}

	var saved Follow
	if err := db.Where("follower_id = ? AND vlogger_id = ?", actor.ID, target.ID).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}

	messages := make([]notification.OutboxMessage, 0, 1)
	if err := db.Model(&notification.OutboxMessage{}).
		Where("JSON_EXTRACT(payload, '$.receiver_id') = ?", target.ID).
		Order("id ASC").Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("outbox rows=%d, want=1", len(messages))
	}
	if messages[0].EventType != "follow.created" {
		t.Fatalf("event_type=%q, want %q", messages[0].EventType, "follow.created")
	}
	if want := fmt.Sprintf("follow:%d", saved.ID); messages[0].EventKey != want {
		t.Fatalf("event_key=%q, want %q", messages[0].EventKey, want)
	}

	var event notification.FollowCreatedEvent
	if err := json.Unmarshal(messages[0].Payload, &event); err != nil {
		t.Fatal(err)
	}
	want := notification.FollowCreatedEvent{FollowID: saved.ID, ActorID: actor.ID, ReceiverID: target.ID}
	if event != want {
		t.Fatalf("payload=%+v, want %+v", event, want)
	}

	// 通知必须由 Consumer 异步落库，关注事务里不能直接写 notifications。
	var notifications int64
	if err := db.Model(&notification.Notification{}).Where("receiver_id = ?", target.ID).
		Count(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if notifications != 0 {
		t.Fatalf("notifications=%d, want=0（说明关注事务里还在同步写通知）", notifications)
	}

	// 重复关注不应该产生第二条事件。
	if _, err := repo.SetFollowing(ctx, actor.ID, target.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&notification.OutboxMessage{}).
		Where("JSON_EXTRACT(payload, '$.receiver_id') = ?", target.ID).
		Count(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if notifications != 1 {
		t.Fatalf("outbox rows after duplicate follow=%d, want=1", notifications)
	}
}

// 关注自己不需要通知。
func TestSelfFollowSkipsOutbox(t *testing.T) {
	db, repo, user, _ := newFollowFixture(t)
	ctx := context.Background()

	if _, err := repo.SetFollowing(ctx, user.ID, user.ID, true); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := db.Model(&notification.OutboxMessage{}).
		Where("JSON_EXTRACT(payload, '$.receiver_id') = ?", user.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("outbox rows=%d, want=0（关注自己不该发通知）", count)
	}
}
