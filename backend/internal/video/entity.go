package video

import "time"

// Video 对应 videos 表的一条记录；保存视频资料和文件地址。
type Video struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	AuthorID      uint   `gorm:"not null;index" json:"author_id"`
	Title         string `gorm:"size:255;not null" json:"title"`
	PlayURL       string `gorm:"size:512;not null" json:"play_url"`
	CoverURL      string `gorm:"size:512;not null" json:"cover_url"`
	LikesCount    int64  `gorm:"not null;default:0" json:"likes_count"`
	CommentsCount int64  `gorm:"not null;default:0" json:"comments_count"`
	// CreateTime 在新建记录时由 GORM 自动填写，供最新视频流排序使用。
	CreateTime time.Time `gorm:"autoCreateTime;index" json:"create_time"`
}

// CreateVideoRequest 是创建时接收的 JSON，作者身份从登录态获取。
type CreateVideoRequest struct {
	Title    string `json:"title"`
	PlayURL  string `json:"play_url"`
	CoverURL string `json:"cover_url"`
}

// UpdateVideoRequest 修改时提交完整的标题、视频地址和封面地址。
type UpdateVideoRequest struct {
	ID       uint   `json:"id" binding:"required"`
	Title    string `json:"title"`
	PlayURL  string `json:"play_url"`
	CoverURL string `json:"cover_url"`
}

type VideoIDRequest struct {
	ID uint `json:"id" binding:"required"`
}

type ListByAuthorIDRequest struct {
	AuthorID uint `json:"author_id" binding:"required"`
}
