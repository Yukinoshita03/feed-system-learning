package notification

import (
	"context"
	"errors"
	"feed-system-learning/internal/mq"
	"feed-system-learning/internal/pagination"
)

var ErrInvalidNotification = errors.New("请提供 1 到 100 个有效消息 ID，或使用 all=true 标记全部已读")

type Service struct {
	repo     *Repository
	mqClient *mq.Client
}

func NewService(repo *Repository, mqClient *mq.Client) *Service {
	return &Service{repo: repo, mqClient: mqClient}
}

func (s *Service) List(ctx context.Context, receiverID uint, req pagination.Request, unreadOnly bool) (Page, error) {
	limit, err := req.PageSize()
	if err != nil {
		return Page{}, err
	}
	items, err := s.repo.List(ctx, receiverID, req.BeforeID, limit+1, unreadOnly)
	if err != nil {
		return Page{}, err
	}
	page := Page{Messages: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Messages = items[:limit]
	}
	if len(page.Messages) > 0 {
		page.NextID = page.Messages[len(page.Messages)-1].ID
	}
	return page, nil
}

func (s *Service) CountUnread(ctx context.Context, receiverID uint) (int64, error) {
	return s.repo.CountUnread(ctx, receiverID)
}

func (s *Service) MarkRead(ctx context.Context, receiverID uint, req MarkReadRequest) (int64, error) {
	if req.All {
		if len(req.MessageIDs) > 0 {
			return 0, ErrInvalidNotification
		}
	} else if len(req.MessageIDs) == 0 || len(req.MessageIDs) > 100 {
		return 0, ErrInvalidNotification
	}
	for _, id := range req.MessageIDs {
		if id == 0 {
			return 0, ErrInvalidNotification
		}
	}
	return s.repo.MarkRead(ctx, receiverID, req.MessageIDs, req.All)
}

func (s *Service) Delete(ctx context.Context, receiverID, id uint) error {
	if id == 0 {
		return ErrInvalidNotification
	}
	return s.repo.Delete(ctx, receiverID, id)
}
