package video

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

var ErrInvalidVideo = errors.New("视频参数不正确")
var ErrForbidden = errors.New("只能修改或删除自己发布的视频")

// VideoService 负责参数校验、作者权限等业务规则，再交给 Repo 操作数据库。
type VideoService struct {
	repo *VideoRepository
}

func NewVideoService(repo *VideoRepository) *VideoService {
	return &VideoService{repo: repo}
}

// CreateVideo 用登录用户的 ID 创建记录，客户端不能指定其他作者。
func (s *VideoService) CreateVideo(ctx context.Context, authorID uint, data CreateVideoRequest) (*Video, error) {
	if authorID == 0 {
		return nil, fmt.Errorf("%w：作者 ID 必须大于 0", ErrInvalidVideo)
	}
	video := &Video{
		AuthorID: authorID,
		Title:    data.Title,
		PlayURL:  data.PlayURL,
		CoverURL: data.CoverURL,
	}
	if err := validateVideo(video); err != nil {
		return nil, err
	}
	if err := s.repo.CreateVideo(ctx, video); err != nil {
		return nil, err
	}
	return video, nil
}

func (s *VideoService) GetDetail(ctx context.Context, id uint) (*Video, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w：视频 ID 必须大于 0", ErrInvalidVideo)
	}
	return s.repo.GetByID(ctx, id)
}

func (s *VideoService) ListByAuthorID(ctx context.Context, authorID uint) ([]Video, error) {
	if authorID == 0 {
		return nil, fmt.Errorf("%w：作者 ID 必须大于 0", ErrInvalidVideo)
	}
	return s.repo.ListByAuthorID(ctx, authorID)
}

// UpdateVideo 先查询并检查作者，再修改标题、视频地址和封面地址。
func (s *VideoService) UpdateVideo(ctx context.Context, authorID uint, data UpdateVideoRequest) (*Video, error) {
	video, err := s.GetDetail(ctx, data.ID)
	if err != nil {
		return nil, err
	}
	if video.AuthorID != authorID {
		return nil, ErrForbidden
	}
	video.Title = data.Title
	video.PlayURL = data.PlayURL
	video.CoverURL = data.CoverURL
	if err := validateVideo(video); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateVideo(ctx, video); err != nil {
		return nil, err
	}
	return video, nil
}

// DeleteVideo 先确认记录存在且属于当前用户，再执行删除。
func (s *VideoService) DeleteVideo(ctx context.Context, id, authorID uint) error {
	video, err := s.GetDetail(ctx, id)
	if err != nil {
		return err
	}
	if video.AuthorID != authorID {
		return ErrForbidden
	}
	return s.repo.DeleteVideo(ctx, id, authorID)
}

func validateVideo(video *Video) error {
	video.Title = strings.TrimSpace(video.Title)
	video.PlayURL = strings.TrimSpace(video.PlayURL)
	video.CoverURL = strings.TrimSpace(video.CoverURL)
	if video.Title == "" || utf8.RuneCountInString(video.Title) > 255 {
		return fmt.Errorf("%w：标题必须为 1 到 255 个字符", ErrInvalidVideo)
	}
	if !validMediaURL(video.PlayURL) || !validMediaURL(video.CoverURL) {
		return fmt.Errorf("%w：视频和封面地址须为 HTTP/HTTPS 地址或 /static/ 路径，且不超过 512 字节", ErrInvalidVideo)
	}
	return nil
}

func validMediaURL(value string) bool {
	if value == "" || len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.IsAbs() {
		return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
	}
	return parsed.Host == "" && strings.HasPrefix(parsed.Path, "/static/") && !strings.Contains(parsed.Path, "..")
}
