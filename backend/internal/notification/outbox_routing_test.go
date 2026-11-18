package notification

import "testing"

func TestOutboxRoutesCountersSeparately(t *testing.T) {
	for _, eventType := range []string{"like.created", "favorite.created", "comment.created", "follow.created"} {
		route, err := outboxRoutingKey(eventType)
		if err != nil || route != "notification."+eventType {
			t.Fatalf("%s => %s, %v", eventType, route, err)
		}
	}
	if route, err := outboxRoutingKey("counter.video.delta"); err != nil || route != "counter.video.delta" {
		t.Fatalf("计数路由=%s err=%v", route, err)
	}
	if _, err := outboxRoutingKey("unknown"); err == nil {
		t.Fatal("未知事件必须显式失败")
	}
}
