package feed

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidFeed 供后续 Handler 识别参数错误，返回 HTTP 400。
var ErrInvalidFeed = errors.New("视频流参数不正确")

// FeedService 负责参数校验、作者信息和分页结果的组织。
type FeedService struct {
	repo *FeedRepository
}

func NewFeedService(repo *FeedRepository) *FeedService {
	return &FeedService{repo: repo}
}

// ListLatest 获取最新视频流；数据库查询由 Repo 完成。
func (s *FeedService) ListLatest(ctx context.Context, req ListLatestRequest, viewerID uint) (ListLatestResponse, error) {
	// 1. 默认每页 10 条；限制客户端每页最多取 50 条。
	if req.Limit == 0 {
		req.Limit = 10
	}
	if req.Limit < 1 || req.Limit > 50 {
		return ListLatestResponse{}, fmt.Errorf("%w：limit 必须为 1 到 50", ErrInvalidFeed)
	}
	if req.LatestTime < 0 || (req.LatestTime == 0) != (req.LatestID == 0) {
		return ListLatestResponse{}, fmt.Errorf("%w：latest_time 和 latest_id 必须同时为 0 或同时为正数", ErrInvalidFeed)
	}
	var latestBefore time.Time
	if req.LatestTime > 0 {
		latestBefore = time.UnixMilli(req.LatestTime)
		if latestBefore.Year() > 9999 {
			return ListLatestResponse{}, fmt.Errorf("%w：latest_time 超出数据库支持的时间范围", ErrInvalidFeed)
		}
	}

	// 2. 多查一条来判断是否还有下一页，避免刚好取满一页时误报 has_more。
	videos, err := s.repo.ListLatest(ctx, req.Limit+1, latestBefore, req.LatestID)
	if err != nil {
		return ListLatestResponse{}, fmt.Errorf("查询最新视频失败：%w", err)
	}
	response := ListLatestResponse{
		VideoList: make([]FeedVideoItem, 0, req.Limit),
		HasMore:   len(videos) > req.Limit,
	}
	if response.HasMore {
		videos = videos[:req.Limit]
	}
	if len(videos) == 0 {
		return response, nil
	}

	// 3. 收集本页作者 ID；同一位作者只查一次。
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
	// 当前用户的点赞关系只在响应时批量查询，不写入公共 Feed 或视频缓存。
	videoIDs := make([]uint, 0, len(videos))
	for _, item := range videos {
		videoIDs = append(videoIDs, item.ID)
	}
	liked, err := s.repo.FindLikedVideoIDs(ctx, viewerID, videoIDs)
	if err != nil {
		return ListLatestResponse{}, fmt.Errorf("查询点赞状态失败：%w", err)
	}

	// 4. 把数据库记录转换成首页需要的响应，时间统一使用毫秒时间戳。
	for _, item := range videos {
		author, exists := authorsByID[item.AuthorID]
		if !exists {
			author = FeedAuthor{ID: item.AuthorID, Username: "未知用户"}
		}
		response.VideoList = append(response.VideoList, FeedVideoItem{
			ID:         item.ID,
			Author:     author,
			Title:      item.Title,
			PlayURL:    item.PlayURL,
			CoverURL:   item.CoverURL,
			CreateTime: item.CreateTime.UnixMilli(),
			LikesCount: item.LikesCount,
			IsLiked:    liked[item.ID],
		})
	}

	// 5. 使用实际返回的最后一条视频作为下一页游标，而不是多查出来的那条。
	lastVideo := videos[len(videos)-1]
	response.NextTime = lastVideo.CreateTime.UnixMilli()
	response.NextID = lastVideo.ID
	return response, nil
}
