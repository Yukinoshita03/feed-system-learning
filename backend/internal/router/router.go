package router

import (
	"feed-system-learning/internal/account"
	"feed-system-learning/internal/auth"
	"feed-system-learning/internal/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetRouter(db *gorm.DB, tokens *auth.TokenManager) *gin.Engine {
	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
		})
	})
	accountrepo := account.NewAccountRepository(db)
	accountservice := account.NewAccountService(accountrepo, tokens)
	accounthandler := account.NewAccountHandler(accountservice)

	r.POST("/account/register", accounthandler.CreateAccount)
	r.POST("/account/login", accounthandler.Login)
	r.POST("/account/refresh", accounthandler.Refresh)
	// 该接口先执行 JWT 中间件，再执行 Me Handler。
	r.GET("/account/me", middleware.RequireJWT(tokens, accountrepo), accounthandler.Me)
	r.POST("/account/logout", middleware.RequireJWT(tokens, accountrepo), accounthandler.Logout)
	return r
}
