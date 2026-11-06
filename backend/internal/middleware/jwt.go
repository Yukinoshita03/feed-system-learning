package middleware

import (
	"context"
	"net/http"
	"strings"

	"feed-system-learning/internal/auth"

	"github.com/gin-gonic/gin"
)

const AccountIDKey = "accountID"
const AccessTokenHashKey = "accessTokenHash"

type AccessTokenStore interface {
	IsAccessTokenActive(ctx context.Context, id uint, hash string) (bool, error)
}

// OptionalJWT 允许游客访问；携带 token 时仍校验签名、有效期和会话撤销状态。
// 有效身份写入 Context，失效凭证返回 401，让前端按已有逻辑刷新或清理登录态。
func OptionalJWT(tokens *auth.TokenManager, store AccessTokenStore) gin.HandlerFunc {
	require := RequireJWT(tokens, store)
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.Next()
			return
		}
		require(c)
	}
}

// RequireJWT 只允许带有效 Bearer token 的请求继续执行。
func RequireJWT(tokens *auth.TokenManager, store AccessTokenStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请提供有效的 Bearer token"})
			return
		}
		claims, err := tokens.ParseToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token 无效或已过期"})
			return
		}
		hash := auth.HashToken(parts[1])
		active, err := store.IsAccessTokenActive(c.Request.Context(), claims.AccountID, hash)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "检查登录状态失败"})
			return
		}
		if !active {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "登录状态已失效"})
			return
		}
		// Gin Context 在同一次请求的中间件与 Handler 之间传递数据。
		c.Set(AccountIDKey, claims.AccountID)
		c.Set(AccessTokenHashKey, hash)
		c.Next()
	}
}
