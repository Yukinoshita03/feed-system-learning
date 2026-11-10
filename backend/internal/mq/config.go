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

	return config, nil
}
