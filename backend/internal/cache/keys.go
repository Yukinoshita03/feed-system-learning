package cache

import "fmt"

const LatestFeedKey = "feed:latest:first:10"

func VideoKey(id uint) string        { return fmt.Sprintf("video:detail:%d", id) }
func AuthorVideosKey(id uint) string { return fmt.Sprintf("video:author:%d", id) }

// 版本 key 与数据 key 配对，TTL 到期后可以一起回收，不永久积累版本记录。
func versionKey(key string) string { return "cache:version:" + key }
func ViewDedupKey(videoID, accountID uint) string {
	return fmt.Sprintf("view:dedup:%d:%d", videoID, accountID)
}
