package notification

import (
	"errors"
	"feed-system-learning/internal/middleware"
	"feed-system-learning/internal/pagination"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// POST /notification/list：分页查询自己的通知，可用 unread_only 只看未读。
func (h *Handler) List(c *gin.Context) {
	var req struct {
		pagination.Request
		UnreadOnly bool `json:"unread_only"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "通知查询参数不正确"})
		return
	}
	page, err := h.service.List(c.Request.Context(), c.GetUint(middleware.AccountIDKey), req.Request, req.UnreadOnly)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, page)
}

// POST /notification/unreadCount：统计自己的未读通知数量。
func (h *Handler) UnreadCount(c *gin.Context) {
	count, err := h.service.CountUnread(c.Request.Context(), c.GetUint(middleware.AccountIDKey))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, gin.H{"unread_count": count})
}

// POST /notification/markRead：标记指定通知或全部通知为已读，不操作其他账号。
func (h *Handler) MarkRead(c *gin.Context) {
	var req MarkReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "已读参数不正确"})
		return
	}
	count, err := h.service.MarkRead(c.Request.Context(), c.GetUint(middleware.AccountIDKey), req)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "通知已标记为已读", "updated_count": count})
}

// POST /notification/delete：只删除自己接收的通知。
func (h *Handler) Delete(c *gin.Context) {
	var req struct {
		MessageID uint `json:"message_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供有效的 message_id"})
		return
	}
	if err := h.service.Delete(c.Request.Context(), c.GetUint(middleware.AccountIDKey), req.MessageID); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "通知已删除"})
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidNotification), errors.Is(err, pagination.ErrInvalidPage):
		c.JSON(400, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(404, gin.H{"error": "通知不存在"})
	default:
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "通知操作失败，请稍后重试"})
	}
}
