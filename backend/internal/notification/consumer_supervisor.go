package notification

import (
	"context"
	"log"
	"math/rand/v2"
	"time"
)

// supervisorOptions 控制退避节奏。抽成结构体是为了让测试能用毫秒级参数
// 验证"退避会翻倍、抖动会散开"，而不必真的等 30 秒。
type supervisorOptions struct {
	initialBackoff time.Duration
	maxBackoff     time.Duration
	// 稳定运行超过这个时长后再失败，说明是偶发问题，退避重置回初始值。
	stableAfter time.Duration
}

// 指数增长只用于快速从短暂故障中恢复；一旦确认是持久故障，就退化成
// 固定间隔轮询，避免重试间隔涨到失去意义。
var defaultSupervisorOptions = supervisorOptions{
	initialBackoff: 500 * time.Millisecond,
	maxBackoff:     30 * time.Second,
	stableAfter:    time.Minute,
}

// RunForever 反复启动消费者直到 ctx 结束。
//
// Run 返回意味着这个 worker 已经不能继续消费了（连接断开、Channel 关闭、
// 消息处理失败等）。它只打一行日志就永久消失，所以必须由外层重新拉起，
// 否则故障恢复后仍然没有消费者在工作。
func (c *Consumer) RunForever(ctx context.Context, workerName string) {
	c.runForever(ctx, workerName, defaultSupervisorOptions, func(ctx context.Context) error {
		return c.Run(ctx, "")
	})
}

// runForever 是 RunForever 的可测内核：把"被执行单元"和"时间参数"都作为
// 入参，测试可以注入会失败的函数和毫秒级退避，不必依赖真实 RabbitMQ。
func (c *Consumer) runForever(
	ctx context.Context,
	workerName string,
	opt supervisorOptions,
	run func(context.Context) error,
) {
	backoff := opt.initialBackoff
	for {
		started := time.Now()
		err := run(ctx)

		// ctx 结束时 run 正常返回，这是唯一的正常退出路径。
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			return
		}
		// 跑满 stableAfter 才失败，说明是偶发问题，不要让退避一路涨到上限。
		if time.Since(started) >= opt.stableAfter {
			backoff = opt.initialBackoff
		}

		wait := withJitter(backoff)
		log.Printf("通知消费者 %s 异常退出，%s 后重启：%v", workerName, wait, err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, opt.maxBackoff)
	}
}

// withJitter 把等待时间随机散开到 [d/2, d)。
//
// 没有抖动时多个 worker 会在同一毫秒集体重连，故障恢复的瞬间反而把
// RabbitMQ 和数据库打出一波连接尖峰（thundering herd）。
func withJitter(d time.Duration) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(rand.Int64N(int64(half)))
}
