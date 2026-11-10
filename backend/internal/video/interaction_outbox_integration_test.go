package video

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"feed-system-learning/internal/notification"
)

// 评论和收藏都必须只写 notification_outbox，把通知落库交给 Consumer 异步完成。
// 这里断言三件事：outbox 有且只有一行、事件字段正确、notifications 表没有被同步写入。
func TestCommentPublishesOutboxEvent(t *testing.T) {
	f := newLikeFixture(t)
	t.Cleanup(cleanupInteractions(t, f))
	item := f.publish(t)
	comments := NewCommentRepository(f.db, f.videos)
	actor := f.accounts[1].ID
	ctx := context.Background()

	count, err := comments.Create(ctx, &Comment{VideoID: item.ID, AuthorID: actor, Content: "第一条评论"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("comments_count=%d, want=1", count)
	}

	var saved Comment
	if err := f.db.Where("video_id = ? AND author_id = ?", item.ID, actor).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}

	messages := f.outboxForVideo(t, item.ID)
	if len(messages) != 1 {
		t.Fatalf("outbox rows=%d, want=1", len(messages))
	}
	if messages[0].EventType != "comment.created" {
		t.Fatalf("event_type=%q, want %q", messages[0].EventType, "comment.created")
	}
	if want := fmt.Sprintf("comment:%d", saved.ID); messages[0].EventKey != want {
		t.Fatalf("event_key=%q, want %q", messages[0].EventKey, want)
	}

	var event notification.CommentCreatedEvent
	if err := json.Unmarshal(messages[0].Payload, &event); err != nil {
		t.Fatal(err)
	}
	want := notification.CommentCreatedEvent{
		CommentID:  saved.ID,
		VideoID:    item.ID,
		ActorID:    actor,
		ReceiverID: item.AuthorID,
	}
	if event != want {
		t.Fatalf("payload=%+v, want %+v", event, want)
	}

	if got := f.countNotificationsFor(t, item.ID); got != 0 {
		t.Fatalf("notifications=%d, want=0（说明评论事务里还在同步写通知）", got)
	}
}

func TestFavoritePublishesOutboxEvent(t *testing.T) {
	f := newLikeFixture(t)
	t.Cleanup(cleanupInteractions(t, f))
	item := f.publish(t)
	favorites := NewFavoriteRepository(f.db, f.videos)
	actor := f.accounts[1].ID
	ctx := context.Background()

	if _, err := favorites.SetFavorited(ctx, item.ID, actor, true); err != nil {
		t.Fatal(err)
	}

	var saved Favorite
	if err := f.db.Where("video_id = ? AND account_id = ?", item.ID, actor).Take(&saved).Error; err != nil {
		t.Fatal(err)
	}

	messages := f.outboxForVideo(t, item.ID)
	if len(messages) != 1 {
		t.Fatalf("outbox rows=%d, want=1", len(messages))
	}
	if messages[0].EventType != "favorite.created" {
		t.Fatalf("event_type=%q, want %q", messages[0].EventType, "favorite.created")
	}
	if want := fmt.Sprintf("favorite:%d", saved.ID); messages[0].EventKey != want {
		t.Fatalf("event_key=%q, want %q", messages[0].EventKey, want)
	}

	var event notification.FavoriteCreatedEvent
	if err := json.Unmarshal(messages[0].Payload, &event); err != nil {
		t.Fatal(err)
	}
	want := notification.FavoriteCreatedEvent{
		FavoriteID: saved.ID,
		VideoID:    item.ID,
		ActorID:    actor,
		ReceiverID: item.AuthorID,
	}
	if event != want {
		t.Fatalf("payload=%+v, want %+v", event, want)
	}

	if got := f.countNotificationsFor(t, item.ID); got != 0 {
		t.Fatalf("notifications=%d, want=0（说明收藏事务里还在同步写通知）", got)
	}

	// 取消后再收藏会生成新的收藏记录，因此必须产生第二条事件；
	// 如果幂等键误用了 video_id，这里会被唯一索引挡掉。
	if _, err := favorites.SetFavorited(ctx, item.ID, actor, false); err != nil {
		t.Fatal(err)
	}
	if _, err := favorites.SetFavorited(ctx, item.ID, actor, true); err != nil {
		t.Fatal(err)
	}
	if got := len(f.outboxForVideo(t, item.ID)); got != 2 {
		t.Fatalf("outbox rows after re-favorite=%d, want=2", got)
	}
}

// 自己评论、收藏自己的视频不需要通知，outbox 里不应该出现任何事件。
func TestSelfInteractionSkipsOutbox(t *testing.T) {
	f := newLikeFixture(t)
	t.Cleanup(cleanupInteractions(t, f))
	item := f.publish(t)
	comments := NewCommentRepository(f.db, f.videos)
	favorites := NewFavoriteRepository(f.db, f.videos)
	author := item.AuthorID
	ctx := context.Background()

	if _, err := comments.Create(ctx, &Comment{VideoID: item.ID, AuthorID: author, Content: "自己评论自己"}); err != nil {
		t.Fatal(err)
	}
	if _, err := favorites.SetFavorited(ctx, item.ID, author, true); err != nil {
		t.Fatal(err)
	}

	if messages := f.outboxForVideo(t, item.ID); len(messages) != 0 {
		t.Fatalf("outbox rows=%d, want=0（自己操作自己的视频不该发通知）", len(messages))
	}
	if got := f.countNotificationsFor(t, item.ID); got != 0 {
		t.Fatalf("notifications=%d, want=0", got)
	}
}

func (f *likeFixture) outboxForVideo(t *testing.T, videoID uint) []notification.OutboxMessage {
	t.Helper()
	messages := make([]notification.OutboxMessage, 0, 1)
	if err := f.db.Model(&notification.OutboxMessage{}).
		Where("JSON_EXTRACT(payload, '$.video_id') = ?", videoID).
		Order("id ASC").
		Find(&messages).Error; err != nil {
		t.Fatal(err)
	}
	return messages
}

func (f *likeFixture) countNotificationsFor(t *testing.T, videoID uint) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&notification.Notification{}).
		Where("video_id = ?", videoID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// cleanupInteractions 必须在 likeFixture 自己的清理之前执行（t.Cleanup 是后进先出），
// 否则视频行先被删掉，互动记录就变成孤儿了。
func cleanupInteractions(t *testing.T, f *likeFixture) func() {
	return func() {
		videoIDs := make([]uint, 0, len(f.items))
		for _, item := range f.items {
			videoIDs = append(videoIDs, item.ID)
		}
		if len(videoIDs) == 0 {
			return
		}
		if err := f.db.Where("video_id IN ?", videoIDs).Delete(&Comment{}).Error; err != nil {
			t.Error(err)
		}
		if err := f.db.Where("video_id IN ?", videoIDs).Delete(&Favorite{}).Error; err != nil {
			t.Error(err)
		}
		if err := f.db.Where("JSON_EXTRACT(payload, '$.video_id') IN ?", videoIDs).
			Delete(&notification.OutboxMessage{}).Error; err != nil {
			t.Error(err)
		}
	}
}
