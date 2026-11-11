package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
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
	dbtest.Migrate(t, database, &account.Account{}, &Follow{}, &RelationStat{}, &notification.Notification{}, &notification.OutboxMessage{})

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
			if err := database.Where("user_id = ?", user.ID).Delete(&RelationStat{}).Error; err != nil {
				t.Error(err)
			}
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
	var actorStat, targetStat RelationStat
	if err := db.First(&actorStat, actor.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&targetStat, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !actorStat.Initialized || actorStat.FollowingCount != 1 || targetStat.FollowerCount != 1 {
		t.Fatalf("relation stats actor=%+v target=%+v", actorStat, targetStat)
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

func TestConcurrentDuplicateFollowCreatesOneRelationshipAndOutbox(t *testing.T) {
	db, repo, actor, target := newFollowFixture(t)
	const workers = 32
	start := make(chan struct{})
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		go func() {
			<-start
			_, err := repo.SetFollowing(context.Background(), actor.ID, target.ID, true)
			errs <- err
		}()
	}
	close(start)

	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent follow failed: %v", err)
		}
	}

	var follow Follow
	if err := db.Where("follower_id = ? AND vlogger_id = ?", actor.ID, target.ID).
		Take(&follow).Error; err != nil {
		t.Fatal(err)
	}
	var followCount, outboxCount int64
	if err := db.Model(&Follow{}).
		Where("follower_id = ? AND vlogger_id = ?", actor.ID, target.ID).
		Count(&followCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&notification.OutboxMessage{}).
		Where("event_key = ?", fmt.Sprintf("follow:%d", follow.ID)).
		Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if followCount != 1 {
		t.Fatalf("follow rows=%d, want=1", followCount)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox rows=%d, want=1", outboxCount)
	}
	var stat RelationStat
	if err := db.First(&stat, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stat.FollowerCount != 1 {
		t.Fatalf("follower_count=%d, want=1", stat.FollowerCount)
	}
}

func TestConcurrentUniqueFollowsUpdateRelationStats(t *testing.T) {
	db, repo, _, target := newFollowFixture(t)
	const workers = 64
	actors := make([]account.Account, workers)
	stamp := time.Now().UnixNano()
	for i := range actors {
		actors[i] = account.Account{Username: fmt.Sprintf("parallel_follow_%d_%d", stamp, i), Password: "test-password-hash"}
	}
	if err := db.Create(&actors).Error; err != nil {
		t.Fatal(err)
	}
	actorIDs := make([]uint, len(actors))
	for i := range actors {
		actorIDs[i] = actors[i].ID
	}
	statIDs := append(append([]uint(nil), actorIDs...), target.ID)
	t.Cleanup(func() {
		if err := db.Where("follower_id IN ? OR vlogger_id = ?", actorIDs, target.ID).Delete(&Follow{}).Error; err != nil {
			t.Error(err)
		}
		if err := db.Where("user_id IN ?", statIDs).Delete(&RelationStat{}).Error; err != nil {
			t.Error(err)
		}
		if err := db.Where("JSON_EXTRACT(payload, '$.actor_id') IN ? OR JSON_EXTRACT(payload, '$.receiver_id') = ?", actorIDs, target.ID).
			Delete(&notification.OutboxMessage{}).Error; err != nil {
			t.Error(err)
		}
		if err := db.Where("id IN ?", actorIDs).Delete(&account.Account{}).Error; err != nil {
			t.Error(err)
		}
	})

	start := make(chan struct{})
	errs := make(chan error, workers)
	for i := range actors {
		go func(actorID uint) {
			<-start
			_, err := repo.SetFollowing(context.Background(), actorID, target.ID, true)
			errs <- err
		}(actors[i].ID)
	}
	close(start)
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent follow failed: %v", err)
		}
	}

	var followCount, outboxCount int64
	if err := db.Model(&Follow{}).Where("vlogger_id = ? AND follower_id IN ?", target.ID, actorIDs).Count(&followCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&notification.OutboxMessage{}).
		Where("JSON_EXTRACT(payload, '$.receiver_id') = ?", target.ID).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if followCount != workers || outboxCount != workers {
		t.Fatalf("follow/outbox rows=%d/%d, want=%d/%d", followCount, outboxCount, workers, workers)
	}

	var targetStat RelationStat
	if err := db.First(&targetStat, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !targetStat.Initialized || targetStat.FollowingCount != 0 || targetStat.FollowerCount != workers {
		t.Fatalf("target relation stat=%+v, want initialized follower_count=%d", targetStat, workers)
	}
	var actorStatCount int64
	if err := db.Model(&RelationStat{}).Where("user_id IN ?", actorIDs).Count(&actorStatCount).Error; err != nil {
		t.Fatal(err)
	}
	if actorStatCount != workers {
		t.Fatalf("actor relation stat rows=%d, want=%d", actorStatCount, workers)
	}
	var incorrectActorStats int64
	if err := db.Model(&RelationStat{}).
		Where("user_id IN ? AND (initialized = ? OR following_count <> 1 OR follower_count <> 0)", actorIDs, false).
		Count(&incorrectActorStats).Error; err != nil {
		t.Fatal(err)
	}
	if incorrectActorStats != 0 {
		t.Fatalf("actor stats with incorrect values=%d, want=0", incorrectActorStats)
	}
}

func TestRelationStatLazilyBackfillsExistingFollows(t *testing.T) {
	db, repo, actor, target := newFollowFixture(t)
	if err := db.Create(&Follow{FollowerID: actor.ID, VloggerID: target.ID}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := repo.SetFollowing(context.Background(), actor.ID, target.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsFollowing || result.FollowingCount != 1 || result.FollowerCount != 1 {
		t.Fatalf("backfilled result=%+v, want 1/1", result)
	}
	var stat RelationStat
	if err := db.First(&stat, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stat.Initialized || stat.FollowerCount != 1 {
		t.Fatalf("backfilled stat=%+v", stat)
	}
	var outboxCount int64
	if err := db.Model(&notification.OutboxMessage{}).
		Where("JSON_EXTRACT(payload, '$.actor_id') = ? AND JSON_EXTRACT(payload, '$.receiver_id') = ?", actor.ID, target.ID).
		Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("duplicate follow emitted %d outbox rows, want=0", outboxCount)
	}
}

func TestOutboxFailureRollsBackFollowAndRelationStats(t *testing.T) {
	db, repo, actor, target := newFollowFixture(t)
	injected := errors.New("injected outbox failure")
	callbackName := fmt.Sprintf("social:fail-follow-outbox-%d", time.Now().UnixNano())
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "notification_outbox" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callbackName) })

	if _, err := repo.SetFollowing(context.Background(), actor.ID, target.ID, true); !errors.Is(err, injected) {
		t.Fatalf("got %v, want injected failure", err)
	}
	var followCount, statCount int64
	if err := db.Model(&Follow{}).Where("follower_id = ? AND vlogger_id = ?", actor.ID, target.ID).Count(&followCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&RelationStat{}).Where("user_id IN ?", []uint{actor.ID, target.ID}).Count(&statCount).Error; err != nil {
		t.Fatal(err)
	}
	if followCount != 0 || statCount != 0 {
		t.Fatalf("follow/stat rows after rollback=%d/%d, want=0/0", followCount, statCount)
	}
}

func TestConcurrentFollowAndUnfollowOnSamePair(t *testing.T) {
	db, repo, actor, target := newFollowFixture(t)
	ctx := context.Background()

	for round := 1; round <= 12; round++ {
		if _, err := repo.SetFollowing(ctx, actor.ID, target.ID, false); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		for _, active := range []bool{true, false} {
			go func(active bool) {
				<-start
				_, err := repo.SetFollowing(ctx, actor.ID, target.ID, active)
				errs <- err
			}(active)
		}
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("round %d concurrent change failed: %v", round, err)
			}
		}

		var followCount, outboxCount int64
		if err := db.Model(&Follow{}).
			Where("follower_id = ? AND vlogger_id = ?", actor.ID, target.ID).
			Count(&followCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&notification.OutboxMessage{}).
			Where("JSON_EXTRACT(payload, '$.actor_id') = ? AND JSON_EXTRACT(payload, '$.receiver_id') = ?", actor.ID, target.ID).
			Count(&outboxCount).Error; err != nil {
			t.Fatal(err)
		}
		if followCount > 1 {
			t.Fatalf("round %d follow rows=%d, want at most 1", round, followCount)
		}
		if outboxCount != int64(round) {
			t.Fatalf("round %d outbox rows=%d, want=%d", round, outboxCount, round)
		}
	}
}

func TestFollowDoesNotLockAccountRows(t *testing.T) {
	db, repo, actor, target := newFollowFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	callbackName := fmt.Sprintf("social:pause-follow-create-%d", time.Now().UnixNano())
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "follows" {
			pauseOnce.Do(func() {
				close(entered)
				<-release
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		releaseOnce.Do(func() { close(release) })
		_ = db.Callback().Create().Remove(callbackName)
	}()

	followDone := make(chan error, 1)
	go func() {
		_, err := repo.SetFollowing(context.Background(), actor.ID, target.ID, true)
		followDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("follow did not reach the paused insert")
	}

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- db.Model(&account.Account{}).
			Where("id = ?", target.ID).Update("bio", "concurrent account update").Error
	}()

	var updateErr error
	select {
	case updateErr = <-updateDone:
	case <-time.After(2 * time.Second):
		releaseOnce.Do(func() { close(release) })
		<-followDone
		t.Fatal("account update waited on the in-flight follow transaction")
	}
	releaseOnce.Do(func() { close(release) })
	if updateErr != nil {
		t.Fatalf("concurrent account update failed: %v", updateErr)
	}
	if err := <-followDone; err != nil {
		t.Fatalf("follow failed: %v", err)
	}
}
