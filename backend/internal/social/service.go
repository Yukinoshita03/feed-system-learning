package social

import (
	"context"
	"errors"
	"feed-system-learning/internal/pagination"
)

var ErrInvalidFollow = errors.New("账号 ID 必须大于 0，且不能关注或取关自己")

type Service struct{ repo *Repository }

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

func (s *Service) SetFollowing(ctx context.Context, actorID, targetID uint, active bool) (FollowResult, error) {
	if actorID == 0 || targetID == 0 || actorID == targetID {
		return FollowResult{}, ErrInvalidFollow
	}
	return s.repo.SetFollowing(ctx, actorID, targetID, active)
}

func (s *Service) IsFollowing(ctx context.Context, actorID, targetID uint) (bool, error) {
	if actorID == 0 || targetID == 0 {
		return false, ErrInvalidFollow
	}
	return s.repo.IsFollowing(ctx, actorID, targetID)
}

func (s *Service) List(ctx context.Context, userID uint, req pagination.Request, followers bool) (UserPage, error) {
	if userID == 0 {
		return UserPage{}, ErrInvalidFollow
	}
	limit, err := req.PageSize()
	if err != nil {
		return UserPage{}, err
	}
	items, total, err := s.repo.List(ctx, userID, req.BeforeID, limit+1, followers)
	if err != nil {
		return UserPage{}, err
	}
	page := UserPage{Users: items, Total: total, HasMore: len(items) > limit}
	if page.HasMore {
		page.Users = items[:limit]
	}
	if len(page.Users) > 0 {
		page.NextID = page.Users[len(page.Users)-1].CursorID
	}
	return page, nil
}
