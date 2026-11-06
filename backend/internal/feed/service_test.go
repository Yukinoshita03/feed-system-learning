package feed

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestFeedRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		req  ListLatestRequest
	}{
		{"negative_limit", ListLatestRequest{Limit: -1}},
		{"limit_over_50", ListLatestRequest{Limit: 51}},
		{"negative_time", ListLatestRequest{LatestTime: -1, LatestID: 1}},
		{"time_without_id", ListLatestRequest{LatestTime: 1}},
		{"id_without_time", ListLatestRequest{LatestID: 1}},
		{"time_outside_mysql_range", ListLatestRequest{LatestTime: math.MaxInt64, LatestID: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// 非法请求应在访问 Repo 之前被拒绝，因此这里不需要数据库。
			service := NewFeedService(nil)
			_, err := service.ListLatest(context.Background(), test.req)
			if !errors.Is(err, ErrInvalidFeed) {
				t.Fatalf("error = %v, want ErrInvalidFeed", err)
			}
		})
	}
}
