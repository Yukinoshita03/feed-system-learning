package video

import (
	"errors"
	"feed-system-learning/internal/pagination"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type FavoriteHandler struct{ service *FavoriteService }

func NewFavoriteHandler(service *FavoriteService) *FavoriteHandler {
	return &FavoriteHandler{service: service}
}

// POST /favorite/favorite：收藏视频；重复收藏返回当前状态，不重复计数。
func (h *FavoriteHandler) Favorite(c *gin.Context) { h.set(c, true) }

// POST /favorite/unfavorite：取消收藏；不存在的收藏关系按已取消处理。
func (h *FavoriteHandler) Unfavorite(c *gin.Context) { h.set(c, false) }

func (h *FavoriteHandler) set(c *gin.Context, active bool) {
	var req LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供有效的 video_id"})
		return
	}
	actor, ok := currentAccountID(c)
	if !ok {
		return
	}
	result, err := h.service.SetFavorited(c.Request.Context(), req.VideoID, actor, active)
	if err != nil {
		writeFavoriteError(c, err)
		return
	}
	c.JSON(200, result)
}

// POST /favorite/isFavorited：读取当前登录账号的收藏状态。
func (h *FavoriteHandler) IsFavorited(c *gin.Context) {
	var req LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供有效的 video_id"})
		return
	}
	actor, ok := currentAccountID(c)
	if !ok {
		return
	}
	active, err := h.service.IsFavorited(c.Request.Context(), req.VideoID, actor)
	if err != nil {
		writeFavoriteError(c, err)
		return
	}
	c.JSON(200, gin.H{"is_favorited": active})
}

// POST /favorite/listMyFavorites：按收藏记录 ID 倒序分页，只查询自己收藏的视频。
func (h *FavoriteHandler) List(c *gin.Context) {
	var req pagination.Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "收藏分页参数不正确"})
		return
	}
	actor, ok := currentAccountID(c)
	if !ok {
		return
	}
	page, err := h.service.List(c.Request.Context(), actor, req)
	if err != nil {
		writeFavoriteError(c, err)
		return
	}
	c.JSON(200, page)
}

func writeFavoriteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidLike), errors.Is(err, pagination.ErrInvalidPage):
		c.JSON(400, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(404, gin.H{"error": "视频不存在"})
	default:
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "收藏操作失败，请稍后重试"})
	}
}
