package video

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"feed-system-learning/internal/account"

	"github.com/gin-gonic/gin"
)

const maxVideoUploadBytes int64 = 200 << 20 // 200 MiB

// POST /video/uploadVideo：接收 form-data 的 file 字段，保存 MP4 并返回访问地址。
// 上传只保存文件；要生成视频记录，再调用 /video/publish。
func (h *VideoHandler) UploadVideo(c *gin.Context) {
	// 1. 从已有的 JWT 中间件读取当前账号。
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}

	// 2. 限制请求体并解析文件；超出内存预算的 multipart 内容临时写到磁盘。
	// 多出的 1 MiB 用于 multipart 边界和请求头等表单内容。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxVideoUploadBytes+(1<<20))
	err := c.Request.ParseMultipartForm(8 << 20)
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请通过 form-data 上传文件，请求体不能超过大小限制"})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请通过 file 字段上传 MP4 文件"})
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > maxVideoUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "视频文件不能为空且不能超过 200 MiB"})
		return
	}

	// 3. 同时检查扩展名与文件头，不依赖客户端填写的 Content-Type。
	var prefix [512]byte
	n, err := io.ReadFull(file, prefix[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取视频文件失败"})
		return
	}
	if strings.ToLower(filepath.Ext(header.Filename)) != ".mp4" ||
		http.DetectContentType(prefix[:n]) != "video/mp4" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只支持扩展名为 .mp4 且文件头匹配的 MP4 文件"})
		return
	}

	// 4. 使用随机文件名，按账号目录保存，避免同名文件覆盖或使用客户端路径。
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		writeVideoError(c, err)
		return
	}
	filename := hex.EncodeToString(identifier[:]) + ".mp4"
	authorDirectory := strconv.FormatUint(uint64(authorID), 10)
	directory := filepath.Join(account.UploadDirectory(), "videos", authorDirectory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		writeVideoError(c, err)
		return
	}
	filePath := filepath.Join(directory, filename)
	destination, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		writeVideoError(c, err)
		return
	}
	keepFile := false
	defer func() {
		_ = destination.Close()
		if !keepFile {
			_ = os.Remove(filePath)
		}
	}()
	// 把已读取的文件头接回去，再流式复制，避免把整个视频读入内存。
	source := io.MultiReader(bytes.NewReader(prefix[:n]), file)
	written, err := io.Copy(destination, io.LimitReader(source, maxVideoUploadBytes+1))
	if err != nil {
		writeVideoError(c, err)
		return
	}
	if written == 0 || written > maxVideoUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "视频文件为空或超过大小限制"})
		return
	}
	if err := destination.Close(); err != nil {
		writeVideoError(c, err)
		return
	}
	keepFile = true

	// 5. /static 路由已指向相同的上传目录，拼出客户端可以访问的地址。
	urlPath := path.Join("/static", "videos", authorDirectory, filename)
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	if baseURL == "" {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		baseURL = scheme + "://" + c.Request.Host
	}
	playURL := baseURL + urlPath
	c.JSON(http.StatusOK, gin.H{"url": playURL, "play_url": playURL})
}
