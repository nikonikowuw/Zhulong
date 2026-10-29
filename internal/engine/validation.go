package engine

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// validateRTSPURI 校验 RTSP 地址合法性。
// 规则：
//  1. 必须非空且以 rtsp:// 开头。
//  2. 严禁包含 NUL 字节（\x00）、未转义空格/换行符、URL 片段（#）。
//  3. 必须包含有效的主机名。
func validateRTSPURI(rawURI string) error {
	if rawURI == "" {
		return fmt.Errorf("%w: URI cannot be empty", ErrInvalidArgument)
	}
	if strings.ContainsRune(rawURI, 0) {
		return fmt.Errorf("%w: URI contains embedded NUL byte", ErrInvalidArgument)
	}
	if strings.ContainsAny(rawURI, " \t\r\n") {
		return fmt.Errorf("%w: URI contains unescaped whitespace", ErrInvalidArgument)
	}
	if strings.ContainsRune(rawURI, '#') {
		return fmt.Errorf("%w: URI contains fragment '#'", ErrInvalidArgument)
	}

	parsed, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("%w: invalid URI format", ErrInvalidArgument)
	}
	if !strings.EqualFold(parsed.Scheme, "rtsp") {
		return fmt.Errorf("%w: unsupported scheme, must be rtsp", ErrInvalidArgument)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: URI missing host", ErrInvalidArgument)
	}
	return nil
}

// validateConsumer 校验消费者身份。
func validateConsumer(consumer Consumer) error {
	if consumer.ID == 0 {
		return fmt.Errorf("%w: consumer ID must be non-zero", ErrInvalidArgument)
	}
	if consumer.Kind != ConsumerPreview && consumer.Kind != ConsumerRecording && consumer.Kind != ConsumerAI {
		return fmt.Errorf("%w: invalid consumer kind %d", ErrInvalidArgument, consumer.Kind)
	}
	return nil
}

// subscriptionLimits 是收敛后的订阅队列预算。
type subscriptionLimits struct {
	maxPackets       int
	maxPacketBytes   int
	maxBufferedBytes int
}

// validate 把调用方给出的上限收敛成确定值：0 取默认值，超过硬上限即拒绝。
func (l subscriptionLimits) validate() (subscriptionLimits, error) {
	limits := subscriptionLimits{
		maxPackets:       DefaultMaxPackets,
		maxPacketBytes:   DefaultMaxPacketBytes,
		maxBufferedBytes: DefaultMaxBufferedBytes,
	}

	if l.maxPackets < 0 || l.maxPackets > HardLimitMaxPackets {
		return subscriptionLimits{}, fmt.Errorf("%w: MaxPackets must be within [1, %d]", ErrInvalidArgument, HardLimitMaxPackets)
	}
	if l.maxPacketBytes < 0 || l.maxPacketBytes > HardLimitMaxPacketBytes {
		return subscriptionLimits{}, fmt.Errorf("%w: MaxPacketBytes must be within [1, %d]", ErrInvalidArgument, HardLimitMaxPacketBytes)
	}
	if l.maxBufferedBytes < 0 || l.maxBufferedBytes > HardLimitMaxBufferedBytes {
		return subscriptionLimits{}, fmt.Errorf("%w: MaxBufferedBytes must be within [1, %d]", ErrInvalidArgument, HardLimitMaxBufferedBytes)
	}

	if l.maxPackets > 0 {
		limits.maxPackets = l.maxPackets
	}
	if l.maxPacketBytes > 0 {
		limits.maxPacketBytes = l.maxPacketBytes
	}
	if l.maxBufferedBytes > 0 {
		limits.maxBufferedBytes = l.maxBufferedBytes
	}
	// 注意：MaxBufferedBytes < MaxPacketBytes 是合法配置 —— 它等价于
	// 「单个包超过总字节预算就立即触发背压」，用于按总字节强制限流。
	return limits, nil
}

const (
	defaultTimeoutMs = 5000
	// maxAllowedTimeout 同时是优雅停机的最坏阻塞上界：Close 必须等在途的阻塞式
	// CGO 调用（如 Acquire 的 RTSP open）返回之后才能销毁句柄，所以这个上限
	// 不能大到让停机超时。实网 RTSP 建连通常 < 5s，2 分钟已是极宽裕的门槛。
	maxAllowedTimeout = 2 * time.Minute
)

// normalizeTimeoutMs 把调用方给出的时长收敛成合法的毫秒值：0 取默认值，负值与超上限拒绝。
//
// 截断后归零时提升为 1ms：毫秒值 0 在 ABI 上表示「用底层默认超时」，
// 把「几乎立即超时」静默放大成 5 秒会改变取消语义。
func normalizeTimeoutMs(d time.Duration) (uint32, error) {
	if d < 0 {
		return 0, fmt.Errorf("%w: timeout cannot be negative", ErrInvalidArgument)
	}
	if d > maxAllowedTimeout {
		return 0, fmt.Errorf("%w: timeout exceeds maximum allowed %s", ErrInvalidArgument, maxAllowedTimeout)
	}
	if d == 0 {
		return defaultTimeoutMs, nil
	}

	timeoutMs := uint32(d.Milliseconds())
	if timeoutMs == 0 {
		timeoutMs = 1
	}
	return timeoutMs, nil
}

// clampToDeadline 把超时收敛进 ctx 的 deadline（至少 1ms）。
//
// ⚠️ 只适用于**同步、不可异步取消**的调用（如 Acquire 的阻塞式 CGO open）：
// 这类调用没有别的取消通道，超时必须花掉 ctx 的预算，否则底层阻塞会超出调用方
// 给定的截止时间。
//
// **异步可取消的调用（如 Probe，已 select ctx.Done()）绝不能用它**：
// 毫秒截断会让 native 自身的超时**先于** ctx deadline 到期（例：剩余 199.7ms 被截断为
// 199ms），此时 ctx.Err() 仍为 nil，调用返回的是引擎的 TIMEOUT 而非
// context.DeadlineExceeded —— 取消语义退化为调度时序竞态。那种场景下 ctx 取消是唯一
// 权威，native 超时只作兜底。
func clampToDeadline(timeoutMs uint32, ctx context.Context) (uint32, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return timeoutMs, nil
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}

	remainingMs := uint32(remaining.Milliseconds())
	if remainingMs == 0 {
		remainingMs = 1 // 不能为 0（0 会回退到底层默认 5000ms）
	}
	if remainingMs < timeoutMs {
		timeoutMs = remainingMs
	}
	return timeoutMs, nil
}
