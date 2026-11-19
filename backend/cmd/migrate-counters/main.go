package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"feed-system-learning/internal/db"
	"feed-system-learning/internal/video"
)

func main() {
	offline := flag.Bool("offline", false, "确认已经停止旧、新版本互动写入后执行回填")
	flag.Parse()
	if !*offline {
		log.Fatal("请先停止互动写入，再使用 -offline 执行计数迁移")
	}
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("请设置 MYSQL_DSN")
	}
	database, err := db.NewDB(dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		log.Fatal("迁移失败，请核对外键、列类型及存量孤儿记录：", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := video.NewCounterRepository(database).BackfillOffline(ctx); err != nil {
		log.Fatal(err)
	}
	log.Print("计数投影已按事实表回填，可以启动新版本服务")
}
