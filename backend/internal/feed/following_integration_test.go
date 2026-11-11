package feed

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"feed-system-learning/internal/account"
	"feed-system-learning/internal/dbtest"
	"feed-system-learning/internal/notification"
	"feed-system-learning/internal/social"
	"feed-system-learning/internal/video"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// followingFixture 比公共 feedFixture 多迁移 follows 表，并准备多个账号。
// 测试记录在事务结束时回滚，不残留脏数据。
func followingFixture(t *testing.T, userCount int) (*FeedService, *gorm.DB, []account.Account) {
	t.Helper()
	dsn := os.Getenv("FEED_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FEED_TEST_MYSQL_DSN to a dedicated database for feed integration tests")
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
	dbtest.Migrate(t, database,
		&account.Account{}, &video.Video{}, &video.Like{}, &video.Comment{}, &video.Favorite{}, &video.VideoView{},
		&social.Follow{}, &social.RelationStat{}, &notification.Notification{}, &notification.OutboxMessage{})

	tx := database.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })

	users := make([]account.Account, 0, userCount)
	for i := 0; i < userCount; i++ {
		users = append(users, account.Account{
			Username: fmt.Sprintf("feed_follow_%d_%d", time.Now().UnixNano(), i),
			Password: "private-feed-password",
		})
	}
	if err := tx.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	return NewFeedService(NewFeedRepository(tx, video.NewVideoRepository(tx, nil), nil)), tx, users
}

// 关注流只能看到自己关注的人发布的视频，未关注作者的内容必须被过滤掉。
func TestFeedFollowingOnlyReturnsFollowedAuthors(t *testing.T) {
	service, tx, users := followingFixture(t, 3)
	ctx := context.Background()
	followed, stranger, viewer := users[0], users[1], users[2]

	base := time.Now().Truncate(time.Millisecond).Add(-time.Hour)
	followedVideo := seedFeedVideo(t, tx, followed.ID, base)
	strangerVideo := seedFeedVideo(t, tx, stranger.ID, base.Add(time.Second))

	if _, err := social.NewRepository(tx).SetFollowing(ctx, viewer.ID, followed.ID, true); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListFollowing(ctx, ListFollowingRequest{}, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.VideoList) != 1 {
		t.Fatalf("video_list=%d, want=1", len(page.VideoList))
	}
	if page.VideoList[0].ID != followedVideo.ID {
		t.Fatalf("拿到 video %d，want=%d；未关注作者的 %d 不应出现",
			page.VideoList[0].ID, followedVideo.ID, strangerVideo.ID)
	}
	if page.HasMore {
		t.Fatal("只有一条视频时 has_more 应为 false")
	}
}

// 翻页必须不重不漏：三页视频用 limit=2 翻两次，恰好拿到三条且各出现一次。
func TestFeedFollowingPaginationSkipsNone(t *testing.T) {
	service, tx, users := followingFixture(t, 2)
	ctx := context.Background()
	followed, viewer := users[0], users[1]

	base := time.Now().Truncate(time.Millisecond).Add(-time.Hour)
	want := make(map[uint]bool, 3)
	for i := 0; i < 3; i++ {
		item := seedFeedVideo(t, tx, followed.ID, base.Add(time.Duration(i)*time.Millisecond))
		want[item.ID] = true
	}
	if _, err := social.NewRepository(tx).SetFollowing(ctx, viewer.ID, followed.ID, true); err != nil {
		t.Fatal(err)
	}

	req := ListFollowingRequest{Limit: 2}
	seen := make(map[uint]int, 3)
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		page, err := service.ListFollowing(ctx, req, viewer.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.VideoList) == 0 {
			t.Fatalf("第 %d 页为空", pageNumber+1)
		}
		for _, item := range page.VideoList {
			seen[item.ID]++
		}
		req.LatestTime = page.NextTime
		req.LatestID = page.NextID
		if !page.HasMore {
			break
		}
	}

	if len(seen) != len(want) {
		t.Fatalf("翻页共拿到 %d 条，want=%d", len(seen), len(want))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("video %d 出现 %d 次，翻页有重复", id, count)
		}
		if !want[id] {
			t.Fatalf("翻页出现非预期 video %d", id)
		}
	}
}

// 没有关注任何人时返回空列表；JSON 里必须是 [] 而不是 null，分页字段归零。
func TestFeedFollowingEmptyWithoutFollows(t *testing.T) {
	service, tx, users := followingFixture(t, 2)
	ctx := context.Background()
	seedFeedVideo(t, tx, users[0].ID, time.Now().Truncate(time.Millisecond).Add(-time.Hour))

	page, err := service.ListFollowing(ctx, ListFollowingRequest{}, users[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if page.VideoList == nil {
		t.Fatal("video_list 必须是空切片 []，不能是 nil（否则 JSON 输出 null）")
	}
	if len(page.VideoList) != 0 {
		t.Fatalf("没关注任何人却返回 %d 条", len(page.VideoList))
	}
	if page.HasMore || page.NextTime != 0 || page.NextID != 0 {
		t.Fatalf("空结果的分页字段应归零，got has_more=%v next_time=%d next_id=%d",
			page.HasMore, page.NextTime, page.NextID)
	}
}

// 取消关注后视频必须从关注流消失。
func TestFeedFollowingDropsAfterUnfollow(t *testing.T) {
	service, tx, users := followingFixture(t, 2)
	ctx := context.Background()
	followed, viewer := users[0], users[1]
	seedFeedVideo(t, tx, followed.ID, time.Now().Truncate(time.Millisecond).Add(-time.Hour))

	repo := social.NewRepository(tx)
	if _, err := repo.SetFollowing(ctx, viewer.ID, followed.ID, true); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListFollowing(ctx, ListFollowingRequest{}, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.VideoList) != 1 {
		t.Fatalf("关注后 video_list=%d, want=1", len(page.VideoList))
	}

	if _, err := repo.SetFollowing(ctx, viewer.ID, followed.ID, false); err != nil {
		t.Fatal(err)
	}
	page, err = service.ListFollowing(ctx, ListFollowingRequest{}, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.VideoList) != 0 {
		t.Fatalf("取消关注后 video_list=%d, want=0", len(page.VideoList))
	}
}
