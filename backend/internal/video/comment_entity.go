package video

import (
	"feed-system-learning/internal/pagination"
	"time"
)

// Comment 保存评论正文；用户名在查询时关联账号表，改名后不保留旧名字。
type Comment struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	VideoID  uint   `gorm:"not null;index:idx_comment_video_id,priority:1" json:"video_id"`
	AuthorID uint   `gorm:"not null;index" json:"author_id"`
	Content  string `gorm:"size:1000;not null" json:"content"`
	// CreatedAt 带索引：填桶任务按时间窗口回溯评论时，没有它只能全表扫描。
	CreatedAt time.Time `gorm:"index:idx_comments_created_at" json:"created_at"`
}

type CommentView struct {
	Comment
	Username string `json:"username"`
}

type PublishCommentRequest struct {
	VideoID uint   `json:"video_id" binding:"required"`
	Content string `json:"content"`
}

type ListCommentsRequest struct {
	pagination.Request
	VideoID uint `json:"video_id" binding:"required"`
}

type CommentPage struct {
	Comments []CommentView `json:"comments"`
	NextID   uint          `json:"next_id"`
	HasMore  bool          `json:"has_more"`
}
