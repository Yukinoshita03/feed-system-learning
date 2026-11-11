package feed

// ListLatestRequest 是最新视频流的请求；第一页的时间和 ID 都为 0。
// 后续请求同时带上上一页返回的 next_time、next_id。
type ListLatestRequest struct {
	Limit      int   `json:"limit"`
	LatestTime int64 `json:"latest_time"`
	LatestID   uint  `json:"latest_id"`
}

// FeedAuthor 只包含作者的公开信息，不包含密码和登录凭证。
type FeedAuthor struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}

// FeedVideoItem 是首页展示的一条视频，区别于数据库里的 Video 记录。
type FeedVideoItem struct {
	ID            uint       `json:"id"`
	Author        FeedAuthor `json:"author"`
	Title         string     `json:"title"`
	PlayURL       string     `json:"play_url"`
	CoverURL      string     `json:"cover_url"`
	CreateTime    int64      `json:"create_time"`
	LikesCount    int64      `json:"likes_count"`
	IsLiked       bool       `json:"is_liked"`
	CommentsCount int64      `json:"comments_count"`
}

// ListRankedRequest 是榜单请求。window 只能是受支持的窗口（24h / 7d），
// 因为每个窗口对应一份预先算好的榜单，不支持的窗口没有数据可读。
type ListRankedRequest struct {
	Window string `json:"window"`
	Limit  int    `json:"limit"`
}

// ListRankedResponse 是榜单结果。榜单不支持翻页：语义就是"最热的一小撮"，
// 而且 ZSET 用分数游标在同分时会漏数据。
type ListRankedResponse struct {
	VideoList []FeedVideoItem `json:"video_list"`
	Window    string          `json:"window"`
}

// ListLatestResponse 包含本页视频以及下一页的时间、ID 游标。
type ListLatestResponse struct {
	VideoList []FeedVideoItem `json:"video_list"`
	NextTime  int64           `json:"next_time"`
	NextID    uint            `json:"next_id"`
	HasMore   bool            `json:"has_more"`
}
