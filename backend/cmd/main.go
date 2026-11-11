package main

import (
	"context"
	"fmt"

	"feed-system-learning/internal/auth"
	"feed-system-learning/internal/cache"
	"feed-system-learning/internal/db"
	"feed-system-learning/internal/feed"
	"feed-system-learning/internal/mq"
	"feed-system-learning/internal/notification"
	"feed-system-learning/internal/router"
	"feed-system-learning/internal/video"
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
	cacheConfig, err := cache.LoadConfig()
	if err != nil {
		log.Fatal("业务缓存配置错误：", err)
	}
	mqConfig, err := mq.LoadConfig()
	if err != nil {
		log.Fatal("RabbitMQ 配置错误：", err)
	}
	mqClient, err := mq.NewClient(mqConfig)
	if err != nil {
		log.Fatal("连接 RabbitMQ 失败：", err)
	}
	defer mqClient.Close()
	if err := mqClient.SetupTopology(); err != nil {
		log.Fatal("初始化 RabbitMQ 拓扑失败：", err)
	}
	log.Printf("RabbitMQ 已连接，拓扑就绪：exchange=%s queue=%s", mqConfig.Exchange, mqConfig.Queue)

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
	dataCache, err := cache.New(redisClient, cacheConfig)
	if err != nil {
		log.Fatal("初始化业务缓存失败：", err)
	}

	notificationRepo := notification.NewRepository(mydb)
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	notificationConsumer := notification.NewConsumer(notificationRepo, mqClient)
	for i := 0; i < 10; i++ {
		go notificationConsumer.RunForever(workerCtx, fmt.Sprintf("worker-%d", i))
	}

	// 播放上报不与视频删除抢锁，删除瞬间可能插入一条孤儿明细；
	// 这里用低频扫描兜底收敛，每轮删一批避免长事务。
	orphanViews := video.NewViewRepository(mydb, video.NewVideoRepository(mydb, dataCache))
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				for {
					deleted, err := orphanViews.CleanOrphans(workerCtx, 1000)
					if err != nil {
						log.Printf("清理孤儿播放明细失败：%v", err)
						break
					}
					if deleted == 0 {
						break
					}
					log.Printf("已清理 %d 条孤儿播放明细", deleted)
				}
			}
		}
	}()

	// 榜单：先把点赞明细增量汇总进小时桶，再从桶里聚合出榜单写 Redis。
	// 填桶与重算都要求幂等（UPSERT / 整体覆盖），所以任务漏跑或重跑都能自愈。
	filler := feed.NewBucketFiller(mydb)
	rankingRepo := feed.NewFeedRepository(mydb, video.NewVideoRepository(mydb, dataCache), dataCache)
	rankingRefresher := feed.NewRankingRefresher(rankingRepo, dataCache)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		// 启动时先跑一次，避免刚启动的几分钟里榜单是空的。
		refreshRankings := func() {
			if err := filler.Fill(workerCtx); err != nil {
				log.Printf("填充互动小时桶失败：%v", err)
			}
			if err := rankingRefresher.RefreshAll(workerCtx); err != nil {
				log.Printf("重算榜单失败：%v", err)
			}
		}
		refreshRankings()

		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				refreshRankings()
			}
		}
	}()

	// 预聚合表会随时间无限增长，低频清理过期桶（保留 7 天窗口 + 1 天余量）。
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				for {
					deleted, err := filler.CleanBuckets(workerCtx, 1000)
					if err != nil {
						log.Printf("清理过期点赞桶失败：%v", err)
						break
					}
					if deleted == 0 {
						break
					}
					log.Printf("已清理 %d 条过期点赞桶", deleted)
				}
			}
		}
	}()

	notificationPublisher := notification.NewOutboxPublisher(notificationRepo, mqClient)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if err := notificationPublisher.PublishBatch(workerCtx, 100); err != nil {
					log.Printf("发布通知 outbox 失败：%v", err)
				}
			}
		}
	}()

	tokens, temporary, err := auth.NewTokenManager(os.Getenv("JWT_SECRET"), accessTokenTTL)
	if err != nil {
		log.Fatal("初始化 token 失败：", err)
	}
	if temporary {
		log.Print("未设置 JWT_SECRET，使用临时密钥；重启后旧 token 将失效")
	}
	r := router.SetRouter(mydb, tokens, redisClient, accessTokenTTL, dataCache, mqClient)
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
