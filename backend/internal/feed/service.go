package feed

import (
	"context"
	"errors"
	"feed-system-learning/internal/video"
	"fmt"
	"time"
)

// ErrInvalidFeed 供后续 Handler 识别参数错误，返回 HTTP 400。
var ErrInvalidFeed = errors.New("视频流参数不正确")

type ListFollowingRequest = ListLatestRequest

// FeedService 负责参数校验、作者信息和分页结果的组织。
type FeedService struct {
	repo *FeedRepository
}

func NewFeedService(repo *FeedRepository) *FeedService {
	return &FeedService{repo: repo}
}

// ListLatest 获取最新视频流；数据库查询由 Repo 完成。
func (s *FeedService) ListLatest(ctx context.Context, req ListLatestRequest, viewerID uint) (ListLatestResponse, error) {
	limit, latestBefore, err := s.parseListCursor(req)
	if err != nil {
		return ListLatestResponse{}, err
	}
	// 多查一条来判断是否还有下一页，避免刚好取满一页时误报 has_more。
	videos, err := s.repo.ListLatest(ctx, limit+1, latestBefore, req.LatestID)
	if err != nil {
		return ListLatestResponse{}, fmt.Errorf("查询最新视频失败：%w", err)
	}
	return s.buildFeedPage(ctx, videos, limit, viewerID)
}

// ListFollowing 只返回当前用户关注的人发布的视频；没有关注任何人时返回空列表。
func (s *FeedService) ListFollowing(ctx context.Context, req ListFollowingRequest, viewerID uint) (ListLatestResponse, error) {
	limit, latestBefore, err := s.parseListCursor(req)
	if err != nil {
		return ListLatestResponse{}, err
	}
	videos, err := s.repo.ListFollowing(ctx, viewerID, limit+1, latestBefore, req.LatestID)
	if err != nil {
		return ListLatestResponse{}, fmt.Errorf("查询关注视频失败：%w", err)
	}
	return s.buildFeedPage(ctx, videos, limit, viewerID)
}

// parseListCursor 校验分页参数并换算游标时间；limit 已归一化为 1 到 50。
func (s *FeedService) parseListCursor(req ListLatestRequest) (int, time.Time, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 50 {
		return 0, time.Time{}, fmt.Errorf("%w：limit 必须为 1 到 50", ErrInvalidFeed)
	}
	if req.LatestTime < 0 || (req.LatestTime == 0) != (req.LatestID == 0) {
		return 0, time.Time{}, fmt.Errorf("%w：latest_time 和 latest_id 必须同时为 0 或同时为正数", ErrInvalidFeed)
	}
	var latestBefore time.Time
	if req.LatestTime > 0 {
		latestBefore = time.UnixMilli(req.LatestTime)
		if latestBefore.Year() > 9999 {
			return 0, time.Time{}, fmt.Errorf("%w：latest_time 超出数据库支持的时间范围", ErrInvalidFeed)
		}
	}
	return limit, latestBefore, nil
}

// buildFeedPage 把本页视频组装成响应：补齐作者、点赞状态和下一页游标。
func (s *FeedService) buildFeedPage(ctx context.Context, videos []video.Video, limit int, viewerID uint) (ListLatestResponse, error) {
	response := ListLatestResponse{
		VideoList: make([]FeedVideoItem, 0, limit),
		HasMore:   len(videos) > limit,
	}
	if response.HasMore {
		videos = videos[:limit]
	}
	if len(videos) == 0 {
		return response, nil
	}

	authorIDs := make([]uint, 0, len(videos))
	seenAuthorIDs := make(map[uint]bool)
	for _, item := range videos {
		if !seenAuthorIDs[item.AuthorID] {
			authorIDs = append(authorIDs, item.AuthorID)
			seenAuthorIDs[item.AuthorID] = true
		}
	}
	authors, err := s.repo.FindAuthorsByIDs(ctx, authorIDs)
	if err != nil {
		return ListLatestResponse{}, fmt.Errorf("查询视频作者失败：%w", err)
	}
	authorsByID := make(map[uint]FeedAuthor, len(authors))
	for _, author := range authors {
		authorsByID[author.ID] = author
	}

	videoIDs := make([]uint, 0, len(videos))
	for _, item := range videos {
		videoIDs = append(videoIDs, item.ID)
	}
	liked, err := s.repo.FindLikedVideoIDs(ctx, viewerID, videoIDs)
	if err != nil {
		return ListLatestResponse{}, fmt.Errorf("查询点赞状态失败：%w", err)
	}

	for _, item := range videos {
		author, exists := authorsByID[item.AuthorID]
		if !exists {
			author = FeedAuthor{ID: item.AuthorID, Username: "未知用户"}
		}
		response.VideoList = append(response.VideoList, FeedVideoItem{
			ID:            item.ID,
			Author:        author,
			Title:         item.Title,
			PlayURL:       item.PlayURL,
			CoverURL:      item.CoverURL,
			CreateTime:    item.CreateTime.UnixMilli(),
			LikesCount:    item.LikesCount,
			IsLiked:       liked[item.ID],
			CommentsCount: item.CommentsCount,
		})
	}

	lastVideo := videos[len(videos)-1]
	response.NextTime = lastVideo.CreateTime.UnixMilli()
	response.NextID = lastVideo.ID
	return response, nil
}
