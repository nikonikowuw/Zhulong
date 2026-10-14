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

// validateConsumer 校验消费者 ID 与类型。
func validateConsumer(consumerID uint64, kind ConsumerKind) error {
	if consumerID == 0 {
		return fmt.Errorf("%w: consumerID must be non-zero", ErrInvalidArgument)
	}
	if kind != ConsumerPreview && kind != ConsumerRecording && kind != ConsumerAI {
		return fmt.Errorf("%w: invalid consumer kind %d", ErrInvalidArgument, kind)
	}
	return nil
}

const (
	defaultTimeoutMs  = 5000
	maxAllowedTimeout = 24 * time.Hour
)

// resolveTimeoutMs 计算毫秒超时值。
//
// clampToContext 为 true 时，若 ctx 带有更早的 Deadline，则把超时收敛到该 Deadline
// （至少 1ms）。这适用于**同步、不可异步取消**的调用（如 Acquire 的阻塞式 CGO 调用），
// 避免底层阻塞超出调用方给定的 ctx 预算。
//
// ⚠️ **异步可取消的调用（如 Probe，已 select ctx.Done()）必须传 false**：
// 若对其做收敛，毫秒截断会让 native 自身的超时**先于** ctx deadline 到期
// （例：剩余 199.7ms 被截断为 199ms），此时 ctx.Err() 仍为 nil，调用返回的是
// 引擎的 TIMEOUT 而非 context.DeadlineExceeded —— 取消语义退化为调度时序竞态。
// 不收敛时 ctx 取消是唯一权威，native 超时只作为兜底。
func resolveTimeoutMs(d time.Duration, ctx context.Context, clampToContext bool) (uint32, error) {
	if d < 0 {
		return 0, fmt.Errorf("%w: timeout cannot be negative", ErrInvalidArgument)
	}
	if d > maxAllowedTimeout {
		return 0, fmt.Errorf("%w: timeout exceeds maximum allowed 24h", ErrInvalidArgument)
	}

	timeoutMs := uint32(defaultTimeoutMs)
	if d > 0 {
		timeoutMs = uint32(d.Milliseconds())
		if timeoutMs == 0 {
			timeoutMs = 1
		}
	}

	if ctx == nil {
		return timeoutMs, nil
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return timeoutMs, nil
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}
	if !clampToContext {
		return timeoutMs, nil
	}

	remMs := uint32(remaining.Milliseconds())
	if remMs == 0 {
		remMs = 1 // 保证至少为 1ms，不能为 0（0 会回退到底层默认 5000ms）
	}
	if remMs < timeoutMs {
		timeoutMs = remMs
	}
	return timeoutMs, nil
}
