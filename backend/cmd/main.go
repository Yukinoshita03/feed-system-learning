package main

import (
	"context"
	"feed-system-learning/internal/auth"
	"feed-system-learning/internal/db"
	"feed-system-learning/internal/router"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")

	if dsn == "" {
		log.Fatal("请设置MYSQL_DSN")
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6379"
	}
	redisDB := 0
	if value := os.Getenv("REDIS_DB"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			log.Fatal("REDIS_DB 必须是非负整数")
		}
		redisDB = parsed
	}
	accessTokenTTL := 15 * time.Minute
	if value := os.Getenv("ACCESS_TOKEN_TTL"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < time.Second {
			log.Fatal("ACCESS_TOKEN_TTL 必须是至少 1 秒的时长，例如 30m 或 1h")
		}
		accessTokenTTL = parsed
	}
	mydb, err := db.NewDB(dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.AutoMigrate(mydb); err != nil {
		log.Fatal(err)
	}
	redisClient := db.NewRedis(&redis.Options{
		Addr:                  redisAddr,
		Password:              os.Getenv("REDIS_PASSWORD"),
		DB:                    redisDB,
		ContextTimeoutEnabled: true,
	})
	defer redisClient.Close()

	// NewRedis 创建客户端；Ping 实际连接 Redis 并检查服务是否响应。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err = redisClient.Ping(ctx).Err()
	cancel()
	if err != nil {
		_ = redisClient.Close()
		log.Fatal("Redis 连接失败：", err)
	}
	log.Printf("Redis 连接成功：%s，DB=%d", redisAddr, redisDB)

	tokens, temporary, err := auth.NewTokenManager(os.Getenv("JWT_SECRET"), accessTokenTTL)
	if err != nil {
		log.Fatal("初始化 token 失败：", err)
	}
	if temporary {
		log.Print("未设置 JWT_SECRET，使用临时密钥；重启后旧 token 将失效")
	}
	r := router.SetRouter(mydb, tokens, redisClient, accessTokenTTL)
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
