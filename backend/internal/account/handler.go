package account

import "github.com/gin-gonic/gin"
import "errors"
import "strings"
import "feed-system-learning/internal/middleware"
import "gorm.io/gorm"

// 接收注册请求的数据
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
	return &AccountHandler{
		service: service,
	}
}

func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"error": "请求格式不正确",
		})
		return
	}
	if err := h.service.ValidateRegistration(req.Username, req.Password); err != nil {
		c.JSON(400, gin.H{
			"error": err.Error(),
		})
		return
	}
	err := h.service.CreateAccount(
		c.Request.Context(),
		req.Username,
		req.Password,
	)

	if errors.Is(err, ErrUsernameTaken) {
		c.JSON(409, gin.H{
			"error": "用户名已存在",
		})
		return
	}

	if err != nil {
		c.JSON(500, gin.H{
			"error": "注册失败",
		})
		return
	}

	c.JSON(200, gin.H{
		"message": "账号创建成功",
	})
}
func (h *AccountHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"error": "请求格式不正确",
		})
		return
	}
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		c.JSON(400, gin.H{"error": "用户名和密码不能为空"})
		return
	}
	result, err := h.service.Login(c.Request.Context(), req.Username, req.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		c.JSON(401, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "登录失败，请稍后重试"})
		return
	}
	c.JSON(200, result)
}

// Refresh 接收刷新凭证，验证通过后返回新的访问 token。
func (h *AccountHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请求格式不正确"})
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		c.JSON(400, gin.H{"error": "refresh_token 不能为空"})
		return
	}
	result, err := h.service.RefreshAccessToken(c.Request.Context(), req.RefreshToken)
	if errors.Is(err, ErrInvalidRefreshToken) {
		c.JSON(401, gin.H{"error": "刷新 token 无效或已过期"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "刷新失败，请稍后重试"})
		return
	}
	c.JSON(200, result)
}

// Me 返回经过鉴权的当前账号，密码字段不会序列化到 JSON。
func (h *AccountHandler) Me(c *gin.Context) {
	value, exists := c.Get(middleware.AccountIDKey)
	accountID, ok := value.(uint)
	if !exists || !ok {
		c.JSON(500, gin.H{"error": "无法获取当前账号"})
		return
	}
	account, err := h.service.GetByID(c.Request.Context(), accountID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(401, gin.H{"error": "账号不存在"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "查询账号失败"})
		return
	}
	c.JSON(200, account)
}

// Logout 清除当前会话的访问和刷新凭证。
func (h *AccountHandler) Logout(c *gin.Context) {
	value, exists := c.Get(middleware.AccountIDKey)
	accountID, ok := value.(uint)
	hashValue, hashExists := c.Get(middleware.AccessTokenHashKey)
	accessHash, hashOK := hashValue.(string)
	if !exists || !ok || !hashExists || !hashOK {
		c.JSON(500, gin.H{"error": "无法获取当前登录状态"})
		return
	}
	if err := h.service.Logout(c.Request.Context(), accountID, accessHash); err != nil {
		if errors.Is(err, ErrSessionExpired) {
			c.JSON(401, gin.H{"error": "登录状态已失效"})
			return
		}
		c.JSON(500, gin.H{"error": "退出登录失败"})
		return
	}
	c.JSON(200, gin.H{"message": "退出登录成功"})
}
