package video

import (
	"errors"
	"feed-system-learning/internal/pagination"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"net/http"
)

type CommentHandler struct{ service *CommentService }

func NewCommentHandler(service *CommentService) *CommentHandler {
	return &CommentHandler{service: service}
}

// POST /comment/publish：当前登录账号发布评论，正文和计数一起入库。
func (h *CommentHandler) Publish(c *gin.Context) {
	var req PublishCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供 video_id 和评论正文"})
		return
	}
	actor, ok := currentAccountID(c)
	if !ok {
		return
	}
	item, count, err := h.service.Publish(c.Request.Context(), actor, req)
	if err != nil {
		writeCommentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "评论发布成功", "comment": item, "comments_count": count})
}

// POST /comment/list：游客可以分页阅读；before_id 为上一页的 next_id。
func (h *CommentHandler) List(c *gin.Context) { h.list(c, false) }

// POST /comment/listAll：兼容原前端，返回单页数组；分页调用使用 /comment/list。
func (h *CommentHandler) ListAll(c *gin.Context) { h.list(c, true) }

func (h *CommentHandler) list(c *gin.Context, arrayOnly bool) {
	var req ListCommentsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "评论查询参数不正确"})
		return
	}
	page, err := h.service.List(c.Request.Context(), req)
	if err != nil {
		writeCommentError(c, err)
		return
	}
	if arrayOnly {
		c.JSON(200, page.Comments)
		return
	}
	c.JSON(200, page)
}

// POST /comment/delete：删除自己的评论，其他用户请求返回 403。
func (h *CommentHandler) Delete(c *gin.Context) {
	var req struct {
		CommentID uint `json:"comment_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请提供有效的 comment_id"})
		return
	}
	actor, ok := currentAccountID(c)
	if !ok {
		return
	}
	count, err := h.service.Delete(c.Request.Context(), req.CommentID, actor)
	if err != nil {
		writeCommentError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "评论已删除", "comments_count": count})
}

func writeCommentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidComment), errors.Is(err, pagination.ErrInvalidPage):
		c.JSON(400, gin.H{"error": err.Error()})
	case errors.Is(err, ErrCommentForbidden):
		c.JSON(403, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(404, gin.H{"error": "视频或评论不存在"})
	default:
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "评论操作失败，请稍后重试"})
	}
}
