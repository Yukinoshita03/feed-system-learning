package video

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ViewHandler 接收播放上报请求。
type ViewHandler struct {
	service *ViewService
}

func NewViewHandler(service *ViewService) *ViewHandler {
	return &ViewHandler{service: service}
}

// POST /video/recordView：记录一次播放。
// 同一账号在同一视频上的重复播放，在去重窗口内只计一次；
// counted 告诉客户端这次是否计入，方便前端调试。
func (h *ViewHandler) Record(c *gin.Context) {
	var req VideoIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供大于 0 的视频 id"})
		return
	}
	accountID, ok := currentAccountID(c)
	if !ok {
		return
	}
	counted, err := h.service.Record(c.Request.Context(), req.ID, accountID)
	if err != nil {
		writeVideoError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"counted": counted})
}
