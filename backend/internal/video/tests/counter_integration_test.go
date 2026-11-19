package video_test

import video "feed-system-learning/internal/video"

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"feed-system-learning/internal/notification"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func rawCounter(t *testing.T, f *likeFixture, id uint) video.VideoCounter {
	t.Helper()
	var counter video.VideoCounter
	if err := f.db.Where("video_id = ?", id).First(&counter).Error; err != nil {
		t.Fatal(err)
	}
	return counter
}

func counterEvent(id uint, suffix string, delta int64) video.CounterEvent {
	return video.CounterEvent{Version: 1, EventID: fmt.Sprintf("test:%d:%s", id, suffix), VideoID: id, Kind: video.KindLike, Delta: delta}
}

func TestCounterRequestsDoNotWaitForProjectionLock(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	lock := f.db.Begin()
	defer lock.Rollback()
	var counter video.VideoCounter
	if err := lock.Clauses(clause.Locking{Strength: "UPDATE"}).Where("video_id = ?", item.ID).First(&counter).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	liked, err := f.service.Like(ctx, item.ID, item.AuthorID)
	if err != nil || !liked.Changed {
		t.Fatalf("点赞不应等待计数锁：%+v %v", liked, err)
	}
	duplicate, err := f.service.Like(ctx, item.ID, item.AuthorID)
	if err != nil || duplicate.Changed {
		t.Fatalf("重复点赞：%+v %v", duplicate, err)
	}
	favorited, err := video.NewFavoriteRepository(f.db, f.videos).SetFavorited(ctx, item.ID, item.AuthorID, true)
	if err != nil || !favorited.Changed {
		t.Fatalf("收藏不应等待计数锁：%+v %v", favorited, err)
	}
	comment := video.Comment{VideoID: item.ID, AuthorID: item.AuthorID, Content: "正文同步确认"}
	if err := video.NewCommentRepository(f.db, f.videos).Create(ctx, &comment); err != nil {
		t.Fatal(err)
	}
	if got := len(f.counterEvents(t, item.ID)); got != 3 {
		t.Fatalf("计数事件=%d，want=3", got)
	}
	if f.countOutbox(t, item.ID) != 0 {
		t.Fatal("自己的互动不应该产生通知，但应产生计数事件")
	}
	if got := rawCounter(t, f, item.ID); got.LikesCount != 0 || got.FavoritesCount != 0 || got.CommentsCount != 0 {
		t.Fatalf("请求提前更新了计数：%+v", got)
	}
	if err := lock.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	f.applyCounters(t, item.ID)
	if got := rawCounter(t, f, item.ID); got.LikesCount != 1 || got.FavoritesCount != 1 || got.CommentsCount != 1 {
		t.Fatalf("后台没有收敛：%+v", got)
	}
}

func TestCounterDuplicateAndOutOfOrderEvents(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	repo := video.NewCounterRepository(f.db)
	ctx := context.Background()
	removed := counterEvent(item.ID, "removed", -1)
	created := counterEvent(item.ID, "created", 1)
	if err := repo.ApplyBatch(ctx, []video.CounterEvent{removed}); err != nil {
		t.Fatal(err)
	}
	if got := rawCounter(t, f, item.ID).LikesCount; got != -1 {
		t.Fatalf("原始中间值=%d，want=-1", got)
	}
	shown, err := f.videos.GetByID(ctx, item.ID)
	if err != nil || shown.LikesCount != 0 {
		t.Fatalf("展示负数未截断：%+v %v", shown, err)
	}
	if err := repo.ApplyBatch(ctx, []video.CounterEvent{created, created, removed}); err != nil {
		t.Fatal(err)
	}
	if got := rawCounter(t, f, item.ID).LikesCount; got != 0 {
		t.Fatalf("乱序/重投结果=%d，want=0", got)
	}
	final := counterEvent(item.ID, "concurrent", 1)
	runLikeWorkers(t, 8, func(ctx context.Context, _ int) error {
		return repo.ApplyBatch(ctx, []video.CounterEvent{final, final})
	})
	if got := rawCounter(t, f, item.ID).LikesCount; got != 1 {
		t.Fatalf("并发重投计数=%d，want=1", got)
	}
}

func TestCounterFailureRollsBackConsumption(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	event := counterEvent(item.ID, "rollback", 1)
	repo := video.NewCounterRepository(f.db)
	injected := errors.New("注入计数写入失败")
	name := "counter:rollback"
	if err := f.db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "video_counters" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	err := repo.ApplyBatch(context.Background(), []video.CounterEvent{event})
	_ = f.db.Callback().Update().Remove(name)
	if !errors.Is(err, injected) {
		t.Fatalf("没有返回计数故障：%v", err)
	}
	var receipts int64
	if err := f.db.Model(&video.VideoCounterConsumption{}).Where("event_id = ?", event.EventID).Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || rawCounter(t, f, item.ID).LikesCount != 0 {
		t.Fatal("失败事务留下了计数或消费记录")
	}
	if err := repo.ApplyBatch(context.Background(), []video.CounterEvent{event}); err != nil {
		t.Fatal(err)
	}
	if rawCounter(t, f, item.ID).LikesCount != 1 {
		t.Fatal("故障恢复后不能重试")
	}
}

func TestCounterLateEventDoesNotRecreateDeletedVideo(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	if err := f.videos.DeleteVideo(context.Background(), item.ID, item.AuthorID); err != nil {
		t.Fatal(err)
	}
	event := counterEvent(item.ID, "late", 1)
	if err := video.NewCounterRepository(f.db).ApplyBatch(context.Background(), []video.CounterEvent{event}); err != nil {
		t.Fatal(err)
	}
	var rows int64
	if err := f.db.Model(&video.VideoCounter{}).Where("video_id = ?", item.ID).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("迟到消息复活了已删除视频的计数")
	}
	for _, child := range []any{&video.Like{VideoID: item.ID, AccountID: item.AuthorID}, &video.Favorite{VideoID: item.ID, AccountID: item.AuthorID}, &video.Comment{VideoID: item.ID, AuthorID: item.AuthorID, Content: "孤儿"}} {
		if err := f.db.Create(child).Error; err == nil {
			t.Fatalf("数据库接受了孤儿互动记录：%#v", child)
		}
	}
}

func TestCounterMissingProjectionIsNotAcknowledged(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	if err := f.db.Where("video_id = ?", item.ID).Delete(&video.VideoCounter{}).Error; err != nil {
		t.Fatal(err)
	}
	event := counterEvent(item.ID, "missing", 1)
	if err := video.NewCounterRepository(f.db).ApplyBatch(context.Background(), []video.CounterEvent{event}); err == nil {
		t.Fatal("现存视频缺失投影时不应该吞掉消息")
	}
	var receipts int64
	if err := f.db.Model(&video.VideoCounterConsumption{}).Where("event_id = ?", event.EventID).Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if receipts != 0 {
		t.Fatal("投影故障被标记为已消费")
	}
}

func TestCounterOfflineBackfillAndReadiness(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	ctx := context.Background()
	repo := video.NewCounterRepository(f.db)
	if err := f.db.Create(&video.Like{VideoID: item.ID, AccountID: item.AuthorID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&video.Favorite{VideoID: item.ID, AccountID: item.AuthorID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&video.Comment{VideoID: item.ID, AuthorID: item.AuthorID, Content: "迁移事实"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("video_id = ?", item.ID).Delete(&video.VideoCounter{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckReady(ctx); err == nil {
		t.Fatal("未迁移的库不应该启动新写路径")
	}
	for i := 0; i < 2; i++ {
		if err := repo.BackfillOffline(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rawCounter(t, f, item.ID); got.LikesCount != 1 || got.FavoritesCount != 1 || got.CommentsCount != 1 {
		t.Fatalf("回填不是来自事实表：%+v", got)
	}
	if _, err := f.service.Unlike(ctx, item.ID, item.AuthorID); err != nil {
		t.Fatal(err)
	}
	if err := repo.BackfillOffline(ctx); err == nil {
		t.Fatal("未排空计数事件时不能重算后再重复应用增量")
	}
	f.applyCounters(t, item.ID)
	if err := repo.BackfillOffline(ctx); err != nil {
		t.Fatal(err)
	}
	if rawCounter(t, f, item.ID).LikesCount != 0 {
		t.Fatal("回填与已处理的增量不一致")
	}
}

func TestCounterOutboxFailureRollsBackSelfInteraction(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	injected := errors.New("计数 Outbox 不可写")
	if err := f.db.Callback().Create().Before("gorm:create").Register("counter:outbox-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "notification_outbox" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Like(context.Background(), item.ID, item.AuthorID)
	if !errors.Is(err, injected) {
		t.Fatalf("自己的点赞也必须可靠记录计数事件：%v", err)
	}
	var rows int64
	if err := f.db.Model(&video.Like{}).Where("video_id = ?", item.ID).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("计数事件失败后点赞关系没有回滚")
	}
}

func TestCounterRejectsInvalidEventsBeforeWriting(t *testing.T) {
	f := newLikeFixture(t)
	item := f.publish(t)
	for _, event := range []video.CounterEvent{
		{Version: 2, EventID: "bad-version", VideoID: item.ID, Kind: video.KindLike, Delta: 1},
		{Version: 1, EventID: "bad-delta", VideoID: item.ID, Kind: video.KindLike, Delta: 100},
		{Version: 1, EventID: "bad-kind", VideoID: item.ID, Kind: "likes_count = 100", Delta: 1},
	} {
		if err := video.NewCounterRepository(f.db).ApplyBatch(context.Background(), []video.CounterEvent{event}); !errors.Is(err, video.ErrInvalidCounterEvent) {
			t.Fatalf("非法事件=%+v err=%v", event, err)
		}
	}
	if rawCounter(t, f, item.ID).LikesCount != 0 {
		t.Fatal("非法消息修改了计数")
	}
	var count int64
	if err := f.db.Model(&notification.OutboxMessage{}).Where("event_type = ? AND JSON_EXTRACT(payload, '$.video_id') = ?", video.CounterEventType, item.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("非法消费产生了新 Outbox：%d %v", count, err)
	}
}
