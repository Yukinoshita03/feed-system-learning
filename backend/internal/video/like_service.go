package video

import (
	"context"
	"errors"
)

var ErrInvalidLike = errors.New("视频 ID 和账号 ID 必须大于 0")

type LikeService struct {
	repo *LikeRepository
}

func NewLikeService(repo *LikeRepository) *LikeService { return &LikeService{repo: repo} }

func (s *LikeService) Like(ctx context.Context, videoID, accountID uint) (LikeResult, error) {
	return s.setLiked(ctx, videoID, accountID, true)
}

func (s *LikeService) Unlike(ctx context.Context, videoID, accountID uint) (LikeResult, error) {
	return s.setLiked(ctx, videoID, accountID, false)
}

func (s *LikeService) setLiked(ctx context.Context, videoID, accountID uint, liked bool) (LikeResult, error) {
	if videoID == 0 || accountID == 0 {
		return LikeResult{}, ErrInvalidLike
	}
	return s.repo.SetLiked(ctx, videoID, accountID, liked)
}

func (s *LikeService) IsLiked(ctx context.Context, videoID, accountID uint) (bool, error) {
	if videoID == 0 || accountID == 0 {
		return false, ErrInvalidLike
	}
	return s.repo.IsLiked(ctx, videoID, accountID)
}

func (s *LikeService) ListMyLikedVideos(ctx context.Context, accountID uint) ([]Video, error) {
	if accountID == 0 {
		return nil, ErrInvalidLike
	}
	return s.repo.ListLikedVideos(ctx, accountID)
}
