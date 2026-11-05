package main

import (
	"feed-system-learning/internal/auth"
	"feed-system-learning/internal/db"
	"feed-system-learning/internal/router"
	"log"
	"os"
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("请设置MYSQL_DSN")
	}
	mydb, err := db.NewDB(dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.AutoMigrate(mydb); err != nil {
		log.Fatal(err)
	}
	tokens, temporary, err := auth.NewTokenManager(os.Getenv("JWT_SECRET"))
	if err != nil {
		log.Fatal("初始化 token 失败：", err)
	}
	if temporary {
		log.Print("未设置 JWT_SECRET，使用临时密钥；重启后旧 token 将失效")
	}
	r := router.SetRouter(mydb, tokens)
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
