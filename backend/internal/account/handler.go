package account

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"feed-system-learning/internal/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxAvatarUploadBytes = 10 << 20

type CreateAccountRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type AccountHandler struct {
	service *AccountService
}

func NewAccountHandler(service *AccountService) *AccountHandler {
	return &AccountHandler{service: service}
}

func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if err := h.service.ValidateRegistration(req.Username, req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.CreateAccount(c.Request.Context(), req.Username, req.Password); err != nil {
		if errors.Is(err, ErrUsernameTaken) {
			c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "账号创建成功"})
}

func (h *AccountHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名和密码不能为空"})
		return
	}
	result, err := h.service.Login(c.Request.Context(), req.Username, req.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败，请稍后重试"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// Refresh validates the refresh token and returns a new access token.
func (h *AccountHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token 不能为空"})
		return
	}
	result, err := h.service.RefreshAccessToken(c.Request.Context(), req.RefreshToken)
	if errors.Is(err, ErrInvalidRefreshToken) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "刷新 token 无效或已过期"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "刷新失败，请稍后重试"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// Me returns the authenticated account's public profile fields.
func (h *AccountHandler) Me(c *gin.Context) {
	accountID, _, ok := requestIdentity(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法获取当前账号"})
		return
	}
	account, err := h.service.GetByID(c.Request.Context(), accountID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "账号不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询账号失败"})
		return
	}
	c.JSON(http.StatusOK, account)
}

// Logout clears the access and refresh credentials for the current session.
func (h *AccountHandler) Logout(c *gin.Context) {
	accountID, accessHash, ok := requestIdentity(c)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法获取当前登录状态"})
		return
	}
	if err := h.service.Logout(c.Request.Context(), accountID, accessHash); err != nil {
		if errors.Is(err, ErrSessionExpired) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态已失效"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "退出登录失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "退出登录成功"})
}

// ChangePassword uses the existing frontend contract: username, old password,
// and new password are sent in JSON; a successful change logs out all sessions.
func (h *AccountHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if err := h.service.ChangePassword(c.Request.Context(), req.Username, req.OldPassword, req.NewPassword); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			// 401 triggers the frontend's access-token refresh flow; this endpoint
			// verifies a password, so report credential mismatch as a bad request.
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户名或旧密码错误"})
			return
		}
		if errors.Is(err, ErrPasswordInvalid) || errors.Is(err, ErrPasswordFieldsRequired) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "修改密码失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "密码修改成功，请重新登录"})
}

// Rename changes the username and returns the replacement access token.
func (h *AccountHandler) Rename(c *gin.Context) {
	var req RenameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	accountID, oldAccessHash, ok := requestIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态无效"})
		return
	}
	token, err := h.service.Rename(c.Request.Context(), accountID, oldAccessHash, req.NewUsername)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameTaken):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, ErrNewUsernameRequired), errors.Is(err, ErrUsernameTooLong):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, ErrSessionExpired):
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "修改用户名失败"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}

// FindByID returns only fields safe to expose on a public profile.
func (h *AccountHandler) FindByID(c *gin.Context) {
	var req FindByIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if req.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 必须大于 0"})
		return
	}
	account, err := h.service.GetByID(c.Request.Context(), req.ID)
	h.writePublicAccount(c, account, err)
}

// FindByUsername returns only fields safe to expose on a public profile.
func (h *AccountHandler) FindByUsername(c *gin.Context) {
	var req FindByUsernameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username 不能为空"})
		return
	}
	account, err := h.service.GetByUsername(c.Request.Context(), username)
	h.writePublicAccount(c, account, err)
}

func (h *AccountHandler) writePublicAccount(c *gin.Context, account *Account, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询账号失败"})
		return
	}
	c.JSON(http.StatusOK, account)
}

// UploadAvatar accepts a bounded image upload, stores it below .run/uploads,
// and updates the authenticated account's avatar URL.
func (h *AccountHandler) UploadAvatar(c *gin.Context) {
	accountID, _, ok := requestIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态无效"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAvatarUploadBytes+(1<<20))
	file, header, err := c.Request.FormFile("file")
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传头像文件"})
		return
	}
	defer file.Close()
	if header.Size <= 0 || header.Size > maxAvatarUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "头像文件大小必须在 1 到 10 MiB 之间"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAvatarUploadBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取头像文件失败"})
		return
	}
	if len(data) == 0 || int64(len(data)) > maxAvatarUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "头像文件大小必须在 1 到 10 MiB 之间"})
		return
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	contentType := http.DetectContentType(data[:min(len(data), 512)])
	if !validAvatarType(ext, contentType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只支持内容与扩展名匹配的 JPG、PNG 或 WEBP 图片"})
		return
	}

	accountDirName := strconv.FormatUint(uint64(accountID), 10)
	directory := filepath.Join(UploadDirectory(), "avatars", accountDirName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建头像目录失败"})
		return
	}
	name, err := randomHex(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成头像文件名失败"})
		return
	}
	filename := name + ext
	filePath := filepath.Join(directory, filename)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存头像失败"})
		return
	}
	keepFile := false
	defer func() {
		if !keepFile {
			_ = os.Remove(filePath)
		}
	}()

	urlPath := path.Join("/static", "avatars", accountDirName, filename)
	avatarURL := buildAvatarURL(c, urlPath)
	if err := h.service.UpdateAvatar(c.Request.Context(), accountID, avatarURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存头像资料失败"})
		return
	}
	keepFile = true
	c.JSON(http.StatusOK, gin.H{"avatar_url": avatarURL})
}

func validAvatarType(extension, contentType string) bool {
	switch contentType {
	case "image/jpeg":
		return extension == ".jpg" || extension == ".jpeg"
	case "image/png":
		return extension == ".png"
	case "image/webp":
		return extension == ".webp"
	default:
		return false
	}
}

func buildAvatarURL(c *gin.Context, urlPath string) string {
	if publicBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"); publicBaseURL != "" {
		return publicBaseURL + urlPath
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, c.Request.Host, urlPath)
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// UpdateProfile changes the supplied profile fields for the authenticated user.
func (h *AccountHandler) UpdateProfile(c *gin.Context) {
	accountID, _, ok := requestIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "登录状态无效"})
		return
	}
	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if err := h.service.UpdateProfile(c.Request.Context(), accountID, req); err != nil {
		switch {
		case errors.Is(err, ErrProfileEmpty), errors.Is(err, ErrAvatarURLInvalid), errors.Is(err, ErrBioTooLong):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新个人资料失败"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "个人资料更新成功"})
}

func requestIdentity(c *gin.Context) (uint, string, bool) {
	accountValue, accountExists := c.Get(middleware.AccountIDKey)
	accountID, accountOK := accountValue.(uint)
	hashValue, hashExists := c.Get(middleware.AccessTokenHashKey)
	accessHash, hashOK := hashValue.(string)
	return accountID, accessHash, accountExists && accountOK && hashExists && hashOK
}
