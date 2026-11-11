package video

import "time"

// Favorite 的联合唯一索引保证同一用户不能重复收藏同一视频。
type Favorite struct {
	ID        uint `gorm:"primaryKey" json:"id"`
	VideoID   uint `gorm:"not null;uniqueIndex:uq_favorite_video_account,priority:1" json:"video_id"`
	AccountID uint `gorm:"not null;index;uniqueIndex:uq_favorite_video_account,priority:2" json:"account_id"`
	// CreatedAt 带索引：填桶任务按时间窗口回溯收藏时，没有它只能全表扫描。
	CreatedAt time.Time `gorm:"index:idx_favorites_created_at" json:"created_at"`
}

type FavoriteResult struct {
	IsFavorited    bool  `json:"is_favorited"`
	FavoritesCount int64 `json:"favorites_count"`
}

type favoriteVideo struct {
	Video
	CursorID uint `gorm:"column:favorite_cursor"`
}

type FavoritePage struct {
	VideoList []Video `json:"video_list"`
	NextID    uint    `json:"next_id"`
	HasMore   bool    `json:"has_more"`
}
