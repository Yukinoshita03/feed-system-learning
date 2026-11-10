package router

import (
	"feed-system-learning/internal/account"
	"feed-system-learning/internal/auth"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/feed"
	"feed-system-learning/internal/middleware"
	"feed-system-learning/internal/mq"
	"feed-system-learning/internal/notification"
	"feed-system-learning/internal/social"
	"feed-system-learning/internal/video"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func SetRouter(db *gorm.DB, tokens *auth.TokenManager, redisClient *redis.Client, accessTokenTTL time.Duration, dataCache *cache.Client, mqClient *mq.Client) *gin.Engine {
	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
		})
	})
	// 头像、视频和封面都从同一个上传目录通过 /static 提供访问。
	r.Static("/static", account.UploadDirectory())

	accountrepo := account.NewAccountRepository(db, redisClient, accessTokenTTL)
	accountservice := account.NewAccountService(accountrepo, tokens)
	accounthandler := account.NewAccountHandler(accountservice)

	r.POST("/account/register", accounthandler.CreateAccount)
	r.POST("/account/login", accounthandler.Login)
	r.POST("/account/refresh", accounthandler.Refresh)
	r.POST("/account/changePassword", accounthandler.ChangePassword)
	r.POST("/account/findByID", accounthandler.FindByID)
	r.POST("/account/findByUsername", accounthandler.FindByUsername)
	// 该接口先执行 JWT 中间件，再执行 Me Handler。
	r.GET("/account/me", middleware.RequireJWT(tokens, accountrepo), accounthandler.Me)
	r.POST("/account/logout", middleware.RequireJWT(tokens, accountrepo), accounthandler.Logout)
	r.POST("/account/rename", middleware.RequireJWT(tokens, accountrepo), accounthandler.Rename)
	r.POST("/account/uploadAvatar", middleware.RequireJWT(tokens, accountrepo), accounthandler.UploadAvatar)
	r.POST("/account/updateProfile", middleware.RequireJWT(tokens, accountrepo), accounthandler.UpdateProfile)

	// Repo 接收数据库，Service 接收 Repo，Handler 接收 Service。
	videorepo := video.NewVideoRepository(db, dataCache)
	videoservice := video.NewVideoService(videorepo)
	videohandler := video.NewVideoHandler(videoservice)
	videoRoutes := r.Group("/video")
	videoRoutes.POST("/getDetail", videohandler.GetDetail)
	videoRoutes.POST("/listByAuthorID", videohandler.ListByAuthorID)
	// 新增、修改、删除都先执行登录校验，再进入视频 Handler。
	protectedVideoRoutes := videoRoutes.Group("", middleware.RequireJWT(tokens, accountrepo))
	protectedVideoRoutes.POST("/uploadVideo", videohandler.UploadVideo)
	protectedVideoRoutes.POST("/uploadCover", videohandler.UploadCover)
	protectedVideoRoutes.POST("/publish", videohandler.CreateVideo)
	protectedVideoRoutes.POST("/update", videohandler.UpdateVideo)
	protectedVideoRoutes.POST("/delete", videohandler.DeleteVideo)

	commentrepo := video.NewCommentRepository(db, videorepo)
	commenthandler := video.NewCommentHandler(video.NewCommentService(commentrepo))
	comments := r.Group("/comment")
	comments.POST("/list", commenthandler.List)
	comments.POST("/listAll", commenthandler.ListAll)
	comments.POST("/publish", middleware.RequireJWT(tokens, accountrepo), commenthandler.Publish)
	comments.POST("/delete", middleware.RequireJWT(tokens, accountrepo), commenthandler.Delete)

	favoriterepo := video.NewFavoriteRepository(db, videorepo)
	favoritehandler := video.NewFavoriteHandler(video.NewFavoriteService(favoriterepo))
	favorites := r.Group("/favorite", middleware.RequireJWT(tokens, accountrepo))
	favorites.POST("/favorite", favoritehandler.Favorite)
	favorites.POST("/unfavorite", favoritehandler.Unfavorite)
	favorites.POST("/isFavorited", favoritehandler.IsFavorited)
	favorites.POST("/listMyFavorites", favoritehandler.List)

	likerepo := video.NewLikeRepository(db, videorepo)
	likeservice := video.NewLikeService(likerepo)
	likehandler := video.NewLikeHandler(likeservice)
	likeRoutes := r.Group("/like", middleware.RequireJWT(tokens, accountrepo))
	likeRoutes.POST("/like", likehandler.Like)
	likeRoutes.POST("/unlike", likehandler.Unlike)
	likeRoutes.POST("/isLiked", likehandler.IsLiked)
	likeRoutes.POST("/listMyLikedVideos", likehandler.ListMyLikedVideos)

	socialhandler := social.NewHandler(social.NewService(social.NewRepository(db)))
	socialRoutes := r.Group("/social", middleware.RequireJWT(tokens, accountrepo))
	socialRoutes.POST("/follow", socialhandler.Follow)
	socialRoutes.POST("/unfollow", socialhandler.Unfollow)
	socialRoutes.POST("/isFollowing", socialhandler.IsFollowing)
	socialRoutes.POST("/getAllFollowers", socialhandler.Followers)
	socialRoutes.POST("/getAllVloggers", socialhandler.Following)

	notificationhandler := notification.NewHandler(notification.NewService(notification.NewRepository(db), mqClient))
	notificationRoutes := r.Group("/notification", middleware.RequireJWT(tokens, accountrepo))
	notificationRoutes.POST("/list", notificationhandler.List)
	notificationRoutes.POST("/unreadCount", notificationhandler.UnreadCount)
	notificationRoutes.POST("/markRead", notificationhandler.MarkRead)
	notificationRoutes.POST("/delete", notificationhandler.Delete)

	// 最新视频流同样按 Repo → Service → Handler 注入，允许游客访问。
	feedrepo := feed.NewFeedRepository(db, videorepo, dataCache)
	feedservice := feed.NewFeedService(feedrepo)
	feedhandler := feed.NewFeedHandler(feedservice)
	feedRoutes := r.Group("/feed", middleware.OptionalJWT(tokens, accountrepo))
	feedRoutes.POST("/listLatest", feedhandler.ListLatest)
	return r
}
