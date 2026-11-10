package cache

import (
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	for _, name := range []string{"VIDEO_CACHE_TTL", "AUTHOR_VIDEO_CACHE_TTL", "FEED_CACHE_TTL", "CACHE_TIMEOUT"} {
		t.Setenv(name, "")
	}
	config, err := LoadConfig()
	if err != nil || config.VideoTTL != 5*time.Minute || config.AuthorTTL != time.Minute || config.FeedTTL != 30*time.Second || config.Timeout != 200*time.Millisecond {
		t.Fatalf("default config=%+v, error=%v", config, err)
	}
	t.Setenv("VIDEO_CACHE_TTL", "7m")
	t.Setenv("AUTHOR_VIDEO_CACHE_TTL", "2m")
	t.Setenv("FEED_CACHE_TTL", "15s")
	t.Setenv("CACHE_TIMEOUT", "50ms")
	config, err = LoadConfig()
	if err != nil || config.VideoTTL != 7*time.Minute || config.AuthorTTL != 2*time.Minute || config.FeedTTL != 15*time.Second || config.Timeout != 50*time.Millisecond {
		t.Fatalf("injected config=%+v, error=%v", config, err)
	}
}

func TestLoadConfigRejectsInvalidDurations(t *testing.T) {
	for _, name := range []string{"VIDEO_CACHE_TTL", "AUTHOR_VIDEO_CACHE_TTL", "FEED_CACHE_TTL", "CACHE_TIMEOUT"} {
		for _, value := range []string{"0", "-1s", "bad", "1ns", "2000000h"} {
			t.Run(name+"="+value, func(t *testing.T) {
				for _, key := range []string{"VIDEO_CACHE_TTL", "AUTHOR_VIDEO_CACHE_TTL", "FEED_CACHE_TTL", "CACHE_TIMEOUT"} {
					t.Setenv(key, "")
				}
				t.Setenv(name, value)
				if _, err := LoadConfig(); err == nil {
					t.Fatal("invalid duration was accepted")
				}
			})
		}
	}
}
