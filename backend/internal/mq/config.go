package mq

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	URL           string
	Exchange      string
	ExchangeType  string
	Queue         string
	BindingKey    string
	ConsumerTag   string
	PrefetchCount int

	// DeadLetterExchange 接收被拒绝的消息；DeadLetterQueue 保存它们等人处理。
	// 死信不自动消费，否则问题会被"绕过"而不是被发现。
	DeadLetterExchange string
	DeadLetterQueue    string
}

func LoadConfig() (Config, error) {
	//默认参数
	config := Config{
		URL:           "amqp://feeddev:feeddev-local-password@127.0.0.1:5672/",
		Exchange:      "feed.events",
		ExchangeType:  "topic",
		Queue:         "feed.notification",
		BindingKey:    "notification.#",
		ConsumerTag:   "feed-notification-worker",
		PrefetchCount: 10,

		DeadLetterExchange: "feed.dlx",
		DeadLetterQueue:    "feed.notification.dlq",
	}

	if value := strings.TrimSpace(os.Getenv("RABBITMQ_URL")); value != "" {
		config.URL = value
	}
	if value := strings.TrimSpace(os.Getenv("RABBITMQ_EXCHANGE")); value != "" {
		config.Exchange = value
	}
	if value := strings.TrimSpace(os.Getenv("RABBITMQ_QUEUE")); value != "" {
		config.Queue = value
	}
	if value := strings.TrimSpace(os.Getenv("RABBITMQ_BINDING_KEY")); value != "" {
		config.BindingKey = value
	}
	if value := strings.TrimSpace(os.Getenv("RABBITMQ_CONSUMER_TAG")); value != "" {
		config.ConsumerTag = value
	}
	if value := strings.TrimSpace(os.Getenv("RABBITMQ_DLX_EXCHANGE")); value != "" {
		config.DeadLetterExchange = value
	}
	if value := strings.TrimSpace(os.Getenv("RABBITMQ_DLQ")); value != "" {
		config.DeadLetterQueue = value
	}
	if value := strings.TrimSpace(os.Getenv("RABBITMQ_PREFETCH")); value != "" {
		prefetch, err := strconv.Atoi(value)
		if err != nil || prefetch <= 0 {
			return Config{}, fmt.Errorf("RABBITMQ_PREFETCH 必须是正整数")
		}
		config.PrefetchCount = prefetch
	}

	if config.URL == "" {
		return Config{}, fmt.Errorf("RabbitMQ URL 不能为空")
	}
	if config.DeadLetterExchange == "" || config.DeadLetterQueue == "" {
		return Config{}, fmt.Errorf("死信 Exchange 和死信队列名称不能为空")
	}

	return config, nil
}
