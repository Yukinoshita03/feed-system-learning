package video

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type LikeHandler struct{ service *LikeService }

func NewLikeHandler(service *LikeService) *LikeHandler { return &LikeHandler{service: service} }

// POST /like/like：当前登录账号点赞；重复请求返回当前状态。
func (h *LikeHandler) Like(c *gin.Context) { h.setLiked(c, true) }

// POST /like/unlike：当前登录账号取消点赞；重复取消不会继续扣减。
func (h *LikeHandler) Unlike(c *gin.Context) { h.setLiked(c, false) }

func (h *LikeHandler) setLiked(c *gin.Context, liked bool) {
	var req LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供大于 0 的 video_id"})
		return
	}
	accountID, ok := currentAccountID(c)
	if !ok {
		return
	}
	var result LikeResult
	var err error
	if liked {
		result, err = h.service.Like(c.Request.Context(), req.VideoID, accountID)
	} else {
		result, err = h.service.Unlike(c.Request.Context(), req.VideoID, accountID)
	}
	if err != nil {
		writeLikeError(c, err)
		return
	}
	message := "点赞成功"
	if !liked {
		message = "取消点赞成功"
	}
	c.JSON(http.StatusOK, gin.H{"message": message, "is_liked": result.IsLiked, "likes_count": result.LikesCount})
}

// POST /like/isLiked：查询当前登录账号对该视频的点赞状态。
func (h *LikeHandler) IsLiked(c *gin.Context) {
	var req LikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供大于 0 的 video_id"})
		return
	}
	accountID, ok := currentAccountID(c)
	if !ok {
		return
	}
	liked, err := h.service.IsLiked(c.Request.Context(), req.VideoID, accountID)
	if err != nil {
		writeLikeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"is_liked": liked})
}

// POST /like/listMyLikedVideos：查询自己赞过的视频，空结果为 []。
func (h *LikeHandler) ListMyLikedVideos(c *gin.Context) {
	accountID, ok := currentAccountID(c)
	if !ok {
		return
	}
	videos, err := h.service.ListMyLikedVideos(c.Request.Context(), accountID)
	if err != nil {
		writeLikeError(c, err)
		return
	}
	c.JSON(http.StatusOK, videos)
}

func writeLikeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidLike):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "视频不存在"})
	default:
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "点赞操作失败，请稍后重试"})
	}
}
