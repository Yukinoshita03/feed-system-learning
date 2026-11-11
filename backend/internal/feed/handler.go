package feed

import (
	"errors"
	"net/http"

	"feed-system-learning/internal/middleware"

	"github.com/gin-gonic/gin"
)

// FeedHandler 接收 HTTP 请求，调用 Service，并返回 JSON 响应。
type FeedHandler struct {
	service *FeedService
}

func NewFeedHandler(service *FeedService) *FeedHandler {
	return &FeedHandler{service: service}
}

// POST /feed/listLatest：公开读取最新视频流，游客也可以浏览。
// 第一页的 latest_time、latest_id 都为 0；下一页同时传入 next_time、next_id。
func (h *FeedHandler) ListLatest(c *gin.Context) {
	var req ListLatestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	viewerID, _ := c.Get(middleware.AccountIDKey)
	accountID, _ := viewerID.(uint)
	result, err := h.service.ListLatest(c.Request.Context(), req, accountID)
	if err != nil {
		writeFeedError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// POST /feed/listFollowing：读取当前登录用户的关注流。
// 第一页游标为 0；下一页传上一页返回的 next_time、next_id。
func (h *FeedHandler) ListFollowing(c *gin.Context) {
	var req ListFollowingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	viewerID, _ := c.Get(middleware.AccountIDKey)
	accountID, _ := viewerID.(uint)
	result, err := h.service.ListFollowing(c.Request.Context(), req, accountID)
	if err != nil {
		writeFeedError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// POST /feed/listByLikes：读取点赞排行榜。
// window 只接受 24h / 7d；榜单是预先算好的，不支持的窗口没有数据可读。
func (h *FeedHandler) ListByLikes(c *gin.Context) {
	var req ListRankedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	viewerID, _ := c.Get(middleware.AccountIDKey)
	accountID, _ := viewerID.(uint)
	result, err := h.service.ListByLikes(c.Request.Context(), req, accountID)
	if err != nil {
		writeFeedError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// POST /feed/listByHot：读取热度排行榜。参数与 listByLikes 相同。
func (h *FeedHandler) ListByHot(c *gin.Context) {
	var req ListRankedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	viewerID, _ := c.Get(middleware.AccountIDKey)
	accountID, _ := viewerID.(uint)
	result, err := h.service.ListByHot(c.Request.Context(), req, accountID)
	if err != nil {
		writeFeedError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeFeedError(c *gin.Context, err error) {
	if errors.Is(err, ErrInvalidFeed) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 内部错误交给 Gin 记录，客户端仅接收通用提示。
	_ = c.Error(err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "获取视频流失败，请稍后重试"})
}
