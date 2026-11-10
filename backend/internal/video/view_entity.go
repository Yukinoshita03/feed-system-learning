package video

import "time"

// VideoView 是一条播放明细；冗余计数在 videos.view_count，这里保留事实来源。
// 后续的观看历史和完播率统计都基于这张表。
type VideoView struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	VideoID   uint      `gorm:"not null;index:idx_view_video_created,priority:1" json:"video_id"`
	AccountID uint      `gorm:"not null;index:idx_view_account_created,priority:1" json:"account_id"`
	CreatedAt time.Time `gorm:"index:idx_view_video_created,priority:2;index:idx_view_account_created,priority:2" json:"created_at"`
}

func (VideoView) TableName() string {
	return "video_views"
}
