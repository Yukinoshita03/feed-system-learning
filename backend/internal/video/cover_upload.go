package video

import (
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

const maxCoverUploadBytes int64 = 10 << 20 // 10 MiB

// POST /video/uploadCover：接收 form-data 的 file 字段，保存封面并返回地址。
// 封面文件保存在 covers/<账号ID>/，返回的 cover_url 可交给发布接口。
func (h *VideoHandler) UploadCover(c *gin.Context) {
	// 1. 读取已有 JWT 中间件保存的当前账号 ID。
	authorID, ok := currentAccountID(c)
	if !ok {
		return
	}

	// 2. 限制请求体并提取 file；multipart 临时文件在请求结束前删除。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCoverUploadBytes+(1<<20))
	err := c.Request.ParseMultipartForm(1 << 20)
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请通过 form-data 上传封面，请求体不能超过大小限制"})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请通过 file 字段上传封面图片"})
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > maxCoverUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "封面文件不能为空且不能超过 10 MiB"})
		return
	}

	// 3. 图片最多读取 10 MiB + 1 字节，检查实际大小、扩展名与文件头。
	data, err := io.ReadAll(io.LimitReader(file, maxCoverUploadBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取封面文件失败"})
		return
	}
	if len(data) == 0 || int64(len(data)) > maxCoverUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "封面文件不能为空且不能超过 10 MiB"})
		return
	}
	extension := strings.ToLower(filepath.Ext(header.Filename))
	contentType := http.DetectContentType(data[:min(len(data), 512)])
	if !validCoverType(extension, contentType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只支持内容与扩展名匹配的 JPG、PNG 或 WEBP 封面"})
		return
	}

	// 4. 按账号建立封面目录，使用随机文件名，避免客户端路径和同名覆盖。
	var identifier [16]byte
	if _, err := rand.Read(identifier[:]); err != nil {
		writeVideoError(c, err)
		return
	}
	filename := hex.EncodeToString(identifier[:]) + extension
	authorDirectory := strconv.FormatUint(uint64(authorID), 10)
	directory := filepath.Join(account.UploadDirectory(), "covers", authorDirectory)
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
	if _, err := destination.Write(data); err != nil {
		writeVideoError(c, err)
		return
	}
	if err := destination.Close(); err != nil {
		writeVideoError(c, err)
		return
	}
	keepFile = true

	// 5. 使用与 /static 一致的路径，返回前端可访问的封面地址。
	urlPath := path.Join("/static", "covers", authorDirectory, filename)
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	if baseURL == "" {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		baseURL = scheme + "://" + c.Request.Host
	}
	coverURL := baseURL + urlPath
	c.JSON(http.StatusOK, gin.H{"url": coverURL, "cover_url": coverURL})
}

func validCoverType(extension, contentType string) bool {
	switch extension {
	case ".jpg", ".jpeg":
		return contentType == "image/jpeg"
	case ".png":
		return contentType == "image/png"
	case ".webp":
		return contentType == "image/webp"
	default:
		return false
	}
}
