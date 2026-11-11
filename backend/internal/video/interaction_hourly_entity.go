package video

import "time"

// 互动类型。作为 interaction_hourly.kind 的取值，也是各明细表到桶表的映射键。
const (
	KindLike     = "like"
	KindFavorite = "favorite"
	KindComment  = "comment"
	KindView     = "view"
)

// InteractionHourly 是互动量的小时级预聚合表：
// 一行表示"某视频在某一个小时里产生了多少个某种互动"。
//
// 榜单重算不再扫描明细表（7 天的点赞可能上百万行），而是扫这张窄表：
// 24 小时窗口最多 24×4 行/视频，7 天窗口 168×4 行/视频。
//
// 所有信号共用一张表而不是各建一张，是为了加新信号（分享、投币等）时
// 只需多一条填充 SQL 和一个 kind，不必再维护一套表和聚合逻辑。
//
// Bucket 存整点、本地时间，与各明细表的 created_at 语义一致：写入它的
// DATE_FORMAT 和查询它的时间参数走同一条 driver 时区链路，混用 UTC 会
// 引入固定偏差。
type InteractionHourly struct {
	VideoID uint      `gorm:"primaryKey;autoIncrement:false" json:"video_id"`
	Bucket  time.Time `gorm:"primaryKey;autoIncrement:false" json:"bucket"`
	Kind    string    `gorm:"primaryKey;autoIncrement:false;size:16" json:"kind"`
	Count   int64     `gorm:"not null;default:0" json:"count"`
}

func (InteractionHourly) TableName() string {
	return "interaction_hourly"
}
