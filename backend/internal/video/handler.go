package video

import (
	"errors"
	"net/http"

	"feed-system-learning/internal/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// VideoHandler 负责接收 HTTP 请求、调用 Service、返回 JSON 响应。
type VideoHandler struct {
	service *VideoService
}

func NewVideoHandler(service *VideoService) *VideoHandler {
	return &VideoHandler{service: service}
}

// POST /video/publish：接收标题、视频地址和封面地址，新增视频记录。
// 作者 ID 从 JWT 中间件保存的登录信息读取，不信任请求里的 author_id。
func (h *VideoHandler) CreateVideo(c *gin.Context) {
	var req CreateVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}
	video, err := h.service.CreateVideo(c.Request.Context(), authorID, req)
	if err != nil {
		writeVideoError(c, err)
		return
	}
	c.JSON(http.StatusOK, video)
}

// POST /video/getDetail：按 id 查询单条视频，公开访问，不存在时返回 404。
func (h *VideoHandler) GetDetail(c *gin.Context) {
	var req VideoIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供大于 0 的视频 id"})
		return
	}
	video, err := h.service.GetDetail(c.Request.Context(), req.ID)
	if err != nil {
		writeVideoError(c, err)
		return
	}
	c.JSON(http.StatusOK, video)
}

// POST /video/listByAuthorID：查询某作者最近 200 条视频，按 ID 倒序返回。
func (h *VideoHandler) ListByAuthorID(c *gin.Context) {
	var req ListByAuthorIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供大于 0 的 author_id"})
		return
	}
	videos, err := h.service.ListByAuthorID(c.Request.Context(), req.AuthorID)
	if err != nil {
		writeVideoError(c, err)
		return
	}
	c.JSON(http.StatusOK, videos)
}

// POST /video/update：提交 id 和完整资料，仅允许作者修改自己的视频。
func (h *VideoHandler) UpdateVideo(c *gin.Context) {
	var req UpdateVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确，且视频 id 必须大于 0"})
		return
	}
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}
	video, err := h.service.UpdateVideo(c.Request.Context(), authorID, req)
	if err != nil {
		writeVideoError(c, err)
		return
	}
	c.JSON(http.StatusOK, video)
}

// POST /video/delete：根据 id 删除自己的视频记录，其他账号返回 403。
func (h *VideoHandler) DeleteVideo(c *gin.Context) {
	var req VideoIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供大于 0 的视频 id"})
		return
	}
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteVideo(c.Request.Context(), req.ID, authorID); err != nil {
		writeVideoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "视频记录已删除"})
}

func currentAccountID(c *gin.Context) (uint, bool) {
	value, exists := c.Get(middleware.AccountIDKey)
	id, valid := value.(uint)
	if !exists || !valid || id == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "登录状态无效"})
		return 0, false
	}
	return id, true
}

func writeVideoError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidVideo):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "视频不存在"})
	default:
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "视频操作失败，请稍后重试"})
	}
}
