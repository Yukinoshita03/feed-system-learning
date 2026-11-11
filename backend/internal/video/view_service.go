package video

import (
	"context"
	"fmt"
)

// ViewService 校验播放上报的参数。
type ViewService struct {
	repo *ViewRepository
}

func NewViewService(repo *ViewRepository) *ViewService {
	return &ViewService{repo: repo}
}

// Record 记录一次播放；counted 表示这次是否计入播放量。
func (s *ViewService) Record(ctx context.Context, videoID, accountID uint) (bool, error) {
	if videoID == 0 {
		return false, fmt.Errorf("%w：视频 ID 必须大于 0", ErrInvalidVideo)
	}
	if accountID == 0 {
		return false, fmt.Errorf("%w：账号 ID 必须大于 0", ErrInvalidVideo)
	}
	return s.repo.Record(ctx, videoID, accountID)
}
