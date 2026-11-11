package social

import "time"

// Follow 表示 follower_id 关注 vlogger_id；联合唯一索引用于防止重复关系。
type Follow struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	FollowerID uint      `gorm:"not null;uniqueIndex:uq_follow_pair,priority:1" json:"follower_id"`
	VloggerID  uint      `gorm:"not null;index;uniqueIndex:uq_follow_pair,priority:2" json:"vlogger_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// RelationStat 保存关注计数；缺失的旧数据统计行会根据 follows 表初始化。
type RelationStat struct {
	UserID         uint      `gorm:"primaryKey;column:user_id" json:"user_id"`
	FollowingCount int64     `gorm:"not null;default:0" json:"following_count"`
	FollowerCount  int64     `gorm:"not null;default:0" json:"follower_count"`
	Initialized    bool      `gorm:"not null;default:false" json:"-"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type User struct {
	ID        uint   `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	Bio       string `json:"bio"`
	CursorID  uint   `gorm:"column:follow_cursor" json:"-"`
}

type FollowResult struct {
	IsFollowing    bool  `json:"is_following"`
	FollowingCount int64 `json:"following_count"`
	FollowerCount  int64 `json:"follower_count"`
}

type UserPage struct {
	Users   []User
	NextID  uint
	HasMore bool
	Total   int64
}
