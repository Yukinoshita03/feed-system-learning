package social

import (
	"errors"
	"feed-system-learning/internal/middleware"
	"feed-system-learning/internal/pagination"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

type TargetRequest struct {
	VloggerID uint `json:"vlogger_id" binding:"required"`
}

// POST /social/follow：当前登录账号关注目标；重复关注不会新增关系。
func (h *Handler) Follow(c *gin.Context) { h.set(c, true) }

// POST /social/unfollow：当前登录账号取关目标；重复取关仍返回成功。
func (h *Handler) Unfollow(c *gin.Context) { h.set(c, false) }

func (h *Handler) set(c *gin.Context, active bool) {
	var req TargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供有效的 vlogger_id"})
		return
	}
	result, err := h.service.SetFollowing(c.Request.Context(), c.GetUint(middleware.AccountIDKey), req.VloggerID, active)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, result)
}

// POST /social/isFollowing：查询当前账号与目标账号的关注关系。
func (h *Handler) IsFollowing(c *gin.Context) {
	var req TargetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供有效的 vlogger_id"})
		return
	}
	active, err := h.service.IsFollowing(c.Request.Context(), c.GetUint(middleware.AccountIDKey), req.VloggerID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, gin.H{"is_following": active})
}

// POST /social/getAllFollowers：分页查询目标账号的粉丝，默认查询自己。
func (h *Handler) Followers(c *gin.Context) { h.list(c, true) }

// POST /social/getAllVloggers：分页查询目标账号的关注列表，默认查询自己。
func (h *Handler) Following(c *gin.Context) { h.list(c, false) }

func (h *Handler) list(c *gin.Context, followers bool) {
	var req struct {
		pagination.Request
		VloggerID  uint `json:"vlogger_id"`
		FollowerID uint `json:"follower_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "关注列表参数不正确"})
		return
	}
	id := req.FollowerID
	key := "vloggers"
	if followers {
		id, key = req.VloggerID, "followers"
	}
	if id == 0 {
		id = c.GetUint(middleware.AccountIDKey)
	}
	page, err := h.service.List(c.Request.Context(), id, req.Request, followers)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, gin.H{key: page.Users, "next_id": page.NextID, "has_more": page.HasMore, "total": page.Total})
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidFollow), errors.Is(err, pagination.ErrInvalidPage):
		c.JSON(400, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(404, gin.H{"error": "用户不存在"})
	default:
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "关注操作失败，请稍后重试"})
	}
}
