package video

import (
	"context"
	"feed-system-learning/internal/pagination"
)

type FavoriteService struct{ repo *FavoriteRepository }

func NewFavoriteService(repo *FavoriteRepository) *FavoriteService {
	return &FavoriteService{repo: repo}
}

func (s *FavoriteService) SetFavorited(ctx context.Context, videoID, accountID uint, active bool) (FavoriteResult, error) {
	if videoID == 0 || accountID == 0 {
		return FavoriteResult{}, ErrInvalidLike
	}
	return s.repo.SetFavorited(ctx, videoID, accountID, active)
}

func (s *FavoriteService) IsFavorited(ctx context.Context, videoID, accountID uint) (bool, error) {
	if videoID == 0 || accountID == 0 {
		return false, ErrInvalidLike
	}
	return s.repo.IsFavorited(ctx, videoID, accountID)
}

func (s *FavoriteService) List(ctx context.Context, accountID uint, req pagination.Request) (FavoritePage, error) {
	limit, err := req.PageSize()
	if err != nil {
		return FavoritePage{}, err
	}
	items, err := s.repo.List(ctx, accountID, req.BeforeID, limit+1)
	if err != nil {
		return FavoritePage{}, err
	}
	page := FavoritePage{VideoList: make([]Video, 0), HasMore: len(items) > limit}
	if page.HasMore {
		items = items[:limit]
	}
	for _, item := range items {
		page.VideoList = append(page.VideoList, item.Video)
		page.NextID = item.CursorID
	}
	return page, nil
}
