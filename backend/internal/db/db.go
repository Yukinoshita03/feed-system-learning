package db

import (
	"feed-system-learning/internal/account"
	"feed-system-learning/internal/social"
	"feed-system-learning/internal/video"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/redis/go-redis/v9"
)

func NewDB(dsn string) (*gorm.DB, error) {
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return database, nil
}
func NewRedis(opt *redis.Options) *redis.Client {
	client := redis.NewClient(opt)

	return client
}
func AutoMigrate(database *gorm.DB) error {
	return database.AutoMigrate(&account.Account{}, &video.Video{}, &video.Like{}, &video.Comment{}, &video.Favorite{}, &social.Follow{})
}
