package video

import "time"

// Like 表示一个账号给一个视频点赞；联合唯一索引保证同一组合最多一条记录。
type Like struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	VideoID   uint   `gorm:"not null;uniqueIndex:uq_likes_video_account" json:"video_id"`
	AccountID uint   `gorm:"not null;uniqueIndex:uq_likes_video_account;index" json:"account_id"`
	Video     *Video `gorm:"foreignKey:VideoID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
	// CreatedAt 带索引：填桶任务按时间窗口回溯点赞时，没有它只能全表扫描。
	CreatedAt time.Time `gorm:"index:idx_likes_created_at" json:"created_at"`
}

// 点赞人从 JWT 获取，客户端只需提供视频 ID。
type LikeRequest struct {
	VideoID uint `json:"video_id" binding:"required"`
}

// LikeResult 确认点赞关系；展示计数由前端乐观更新、后台异步追赶。
type LikeResult struct {
	IsLiked bool `json:"is_liked"`
	Changed bool `json:"changed"`
}
