package video

import "time"

// LikeHourly 是点赞数的小时级预聚合表：一行表示"某视频在某一个小时里得了多少个赞"。
//
// 榜单重算不再扫描 likes 原始明细（7 天可能上百万行），而是扫这张窄表：
// 24 小时窗口只需 24 行/视频，7 天窗口 168 行/视频。
//
// Bucket 存的是「整点时间」，语义与 likes.created_at 一致（本地时间），
// 因为写入它的 DATE_FORMAT 和查询它的时间参数都走同一条 driver 时区链路，
// 混用 UTC 会引入 8 小时偏差。
type LikeHourly struct {
	VideoID uint      `gorm:"primaryKey;autoIncrement:false" json:"video_id"`
	Bucket  time.Time `gorm:"primaryKey;autoIncrement:false;index:idx_like_hourly_bucket,priority:1" json:"bucket"`
	Count   int64     `gorm:"not null;default:0" json:"count"`
}

func (LikeHourly) TableName() string {
	return "like_hourly"
}
