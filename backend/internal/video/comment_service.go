package video

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrInvalidComment = errors.New("评论必须为 1 到 1000 个字符，视频和评论 ID 必须大于 0")

type CommentService struct{ repo *CommentRepository }

func NewCommentService(repo *CommentRepository) *CommentService { return &CommentService{repo: repo} }

func (s *CommentService) Publish(ctx context.Context, authorID uint, req PublishCommentRequest) (*Comment, int64, error) {
	content := strings.TrimSpace(req.Content)
	if authorID == 0 || req.VideoID == 0 || content == "" || utf8.RuneCountInString(content) > 1000 {
		return nil, 0, ErrInvalidComment
	}
	item := &Comment{VideoID: req.VideoID, AuthorID: authorID, Content: content}
	count, err := s.repo.Create(ctx, item)
	return item, count, err
}

func (s *CommentService) List(ctx context.Context, req ListCommentsRequest) (CommentPage, error) {
	if req.VideoID == 0 {
		return CommentPage{}, ErrInvalidComment
	}
	limit, err := req.PageSize()
	if err != nil {
		return CommentPage{}, err
	}
	items, err := s.repo.List(ctx, req.VideoID, req.BeforeID, limit+1)
	if err != nil {
		return CommentPage{}, err
	}
	page := CommentPage{Comments: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Comments = items[:limit]
	}
	if len(page.Comments) > 0 {
		page.NextID = page.Comments[len(page.Comments)-1].ID
	}
	return page, nil
}

func (s *CommentService) Delete(ctx context.Context, commentID, authorID uint) (int64, error) {
	if commentID == 0 || authorID == 0 {
		return 0, ErrInvalidComment
	}
	return s.repo.Delete(ctx, commentID, authorID)
}
