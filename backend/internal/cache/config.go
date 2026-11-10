package cache

import (
	"fmt"
	"os"
	"time"
)

// Config 在启动时读取，再注入使用缓存的 Repo；方法内部不读取环境变量。
type Config struct {
	VideoTTL  time.Duration
	AuthorTTL time.Duration
	FeedTTL   time.Duration
	Timeout   time.Duration
}

func LoadConfig() (Config, error) {
	config := Config{
		VideoTTL: 5 * time.Minute, AuthorTTL: time.Minute,
		FeedTTL: 30 * time.Second, Timeout: 200 * time.Millisecond,
	}
	for _, item := range []struct {
		name   string
		target *time.Duration
	}{
		{"VIDEO_CACHE_TTL", &config.VideoTTL},
		{"AUTHOR_VIDEO_CACHE_TTL", &config.AuthorTTL},
		{"FEED_CACHE_TTL", &config.FeedTTL},
		{"CACHE_TIMEOUT", &config.Timeout},
	} {
		if value := os.Getenv(item.name); value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil || parsed < time.Millisecond || parsed > time.Duration(1<<62-1) {
				return Config{}, fmt.Errorf("%s 必须是至少 1ms 的有效时长，例如 30s 或 5m", item.name)
			}
			*item.target = parsed
		}
	}
	return config, nil
}

func (c Config) valid() bool {
	for _, value := range []time.Duration{c.VideoTTL, c.AuthorTTL, c.FeedTTL, c.Timeout} {
		if value < time.Millisecond || value > time.Duration(1<<62-1) {
			return false
		}
	}
	return true
}
