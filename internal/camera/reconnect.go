package camera

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

const (
	// nativeStreamGrace 与 native/src/nodes/capture/rtsp_input.hpp 的 stream_grace_period 对齐。
	//
	// 它是 Go 侧重连节奏的**硬下限**：Native 在最后一个消费者释放后启动 8 秒宽限期，
	// 而宽限期内的 acquire 会被解释成「复用同一路物理流」——失败流不会被重新拉起
	// （native/README.md：reacquire during grace does not restart it）。
	// 更早重试只能拿回同一具尸体，并把退避阶梯白白烧掉。
	nativeStreamGrace = 8 * time.Second

	// defaultRecoveryGrace 是 Go 侧等待物理流被回收的时长：Native 宽限期 + 1 秒余量。
	//
	// ⚠️ 这段时间内**禁止任何 acquire**。命中的流会把 expiry 推回 max，等于替那具
	// 失败流重新续命；在宽限期内反复敲门（重连协程 + 算法包重启同时来）会让它
	// 永远不 retiring —— 现场实测的「一直重连不上」就是这个自锁。
	defaultRecoveryGrace = nativeStreamGrace + time.Second

	// defaultReconnectMinDelay 是首轮重连的等待时间，必须严格大于 defaultRecoveryGrace。
	defaultReconnectMinDelay = defaultRecoveryGrace + time.Second

	// defaultReconnectMaxDelay 是退避上限。再长会让「相机恢复后多久自愈」变成分钟级的赌注，
	// 再短则会在长时间的断电窗口里持续敲打摄像机。
	defaultReconnectMaxDelay = 60 * time.Second

	// reconnectJitterFrac 是每轮延迟的随机抖动比例。
	//
	// 抖动只向上取（1.0x ~ 1+frac），不能向下：向下抖动会把延迟压进 8 秒宽限期，
	// 于是「加了抖动」反而让每台设备的第一轮重试必然失败。
	// 同一交换机下的多台相机同时掉线时，没有抖动等于同步捶打同一台 NVR。
	reconnectJitterFrac = 0.2

	// reconnectAttemptTimeout 是单次重连尝试的预算（RTSP 建连 + broker 就绪）。
	reconnectAttemptTimeout = 20 * time.Second

	// defaultStreamReadyTimeout 是「等到物理流真正跑起来」的预算。
	//
	// Native 的 acquire 是**异步**的：它只登记消费者并启动拉流线程，返回时 RTSP
	// 可能还没连上。直接宣布恢复成功会得到一条 3 秒后才断的流——现场表现是
	// 「recovered」日志连刷、会话在 running/reconnecting 之间闪。
	defaultStreamReadyTimeout = 6 * time.Second

	// streamReadyPollInterval 是等待 RUNNING 时的轮询间隔。
	streamReadyPollInterval = 100 * time.Millisecond

	// recoveryGateSlack 是「等宽限期结束」这条阻塞路径的额外预算：
	// 调用方（算法包重启）最多愿意为一路流等 recoveryGrace + 这段余量。
	recoveryGateSlack = 2 * time.Second
)

// aiRecovery 记录一路相机 AI 分析流的重连状态。
type aiRecovery struct {
	cancel context.CancelFunc
	// notBefore 之前不得 acquire：物理流还在 Native 宽限期内，
	// 此时敲门只会把它的回收时刻往后推（acquire 命中即续命）。
	notBefore time.Time
	gen       uint64
}

// reconnectDelay 返回第 attempt 轮（0 起）的重连等待时间：min*2^n，封顶 max，再叠加向上抖动。
func (h *StreamHub) reconnectDelay(attempt int) time.Duration {
	delay := h.reconnectMinDelay
	for i := 0; i < attempt && delay < h.reconnectMaxDelay; i++ {
		delay *= 2
	}
	if delay > h.reconnectMaxDelay {
		delay = h.reconnectMaxDelay
	}
	jitter := 1 + rand.Float64()*reconnectJitterFrac
	return time.Duration(float64(delay) * jitter)
}

func (h *StreamHub) aiStreamWanted(cameraID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	return h.aiStreamWantedLocked(cameraID)
}

// aiStreamWantedLocked 判断是否还有算法包想要这一路 AI 流；要求持有 h.mu。
func (h *StreamHub) aiStreamWantedLocked(cameraID string) bool {
	return len(h.aiSubscribers[cameraID]) > 0
}

// scheduleAIRecovery 在 AI 分析流失败后拉起后台重连。
//
// 只有仍存在 AI 订阅者时才重连：任务被停用或算法包被摘掉后继续拉流，
// 等于给一台没人看的相机持续付带宽与解码预算。
// 重复调用幂等——同一相机同时只有一个重连协程，但**会刷新宽限期起点**：
// 每多一次释放，Native 的回收时刻就往后挪一次。
func (h *StreamHub) scheduleAIRecovery(cameraID string) {
	notBefore := time.Now().Add(h.recoveryGrace)

	h.mu.Lock()
	if h.closed || !h.aiStreamWantedLocked(cameraID) {
		h.mu.Unlock()
		return
	}
	if rec, running := h.recoveries[cameraID]; running {
		rec.notBefore = notBefore
		h.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(h.recoveryCtx)
	h.recoverySeq++
	gen := h.recoverySeq
	h.recoveries[cameraID] = &aiRecovery{cancel: cancel, gen: gen, notBefore: notBefore}
	h.mu.Unlock()

	h.logger.Info("ai stream recovery scheduled",
		zap.String("camera_id", cameraID),
		zap.Duration("recovery_grace", h.recoveryGrace),
		zap.Duration("min_delay", h.reconnectMinDelay),
		zap.Duration("max_delay", h.reconnectMaxDelay),
	)

	go h.runAIRecovery(ctx, cameraID, gen)
}

// runAIRecovery 按指数退避反复尝试重建 AI 分析流，直到成功、失去订阅者或 Hub 停机。
func (h *StreamHub) runAIRecovery(ctx context.Context, cameraID string, gen uint64) {
	defer h.finishAIRecovery(cameraID, gen)

	for attempt := 0; ; attempt++ {
		delay := h.recoveryWait(cameraID, attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if !h.aiStreamWanted(cameraID) {
			h.logger.Info("ai stream recovery abandoned: no subscriber left",
				zap.String("camera_id", cameraID))
			return
		}

		h.logger.Info("ai stream reconnect attempt",
			zap.String("camera_id", cameraID),
			zap.Int("attempt", attempt+1),
			zap.Duration("waited", delay),
		)

		attemptCtx, cancel := context.WithTimeout(ctx, reconnectAttemptTimeout)
		_, err := h.acquireAIStreamBrokerPath(attemptCtx, cameraID)
		cancel()

		if err == nil {
			h.logger.Info("ai stream recovered",
				zap.String("camera_id", cameraID),
				zap.Int("attempts", attempt+1),
			)
			return
		}

		// 相机被停用或删除后重连没有意义：继续重试只会刷日志。
		if errors.Is(err, ErrCameraDisabled) || errors.Is(err, ErrCameraNotFound) {
			h.logger.Warn("ai stream recovery stopped: camera is no longer usable",
				zap.String("camera_id", cameraID), zap.Error(err))
			return
		}

		// 引擎交回的仍是同一具失败流：这次 acquire 又把宽限期续上了，
		// 下一轮必须重新等满一个回收周期，否则就是自锁。
		if errors.Is(err, ErrStreamRecovering) {
			h.extendRecoveryGrace(cameraID, gen)
		}

		h.logger.Warn("ai stream reconnect attempt failed",
			zap.String("camera_id", cameraID),
			zap.Int("attempt", attempt+1),
			zap.Duration("next_retry_in", h.recoveryWait(cameraID, attempt+1)),
			zap.Error(err),
		)
	}
}

// recoveryWait 是本轮重试前的等待时间：指数退避与实际宽限期两者取大。
func (h *StreamHub) recoveryWait(cameraID string, attempt int) time.Duration {
	delay := h.reconnectDelay(attempt)

	h.mu.Lock()
	if rec, ok := h.recoveries[cameraID]; ok {
		if until := time.Until(rec.notBefore); until > delay {
			delay = until
		}
	}
	h.mu.Unlock()
	return delay
}

// extendRecoveryGrace 在「又摸到一具尸体」后把宽限期起点推到现在。
func (h *StreamHub) extendRecoveryGrace(cameraID string, gen uint64) {
	notBefore := time.Now().Add(h.recoveryGrace)
	h.mu.Lock()
	defer h.mu.Unlock()
	if rec, ok := h.recoveries[cameraID]; ok && rec.gen == gen {
		rec.notBefore = notBefore
	}
}

// finishAIRecovery 清理重连登记；只有代次仍匹配时才删除，避免覆盖后一次失败拉起的新协程。
func (h *StreamHub) finishAIRecovery(cameraID string, gen uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rec, ok := h.recoveries[cameraID]; ok && rec.gen == gen {
		rec.cancel()
		delete(h.recoveries, cameraID)
	}
}

// waitForRecoveryGate 在物理流回收窗口内阻塞，直到可以安全 acquire。
//
// 调用方是「现在就要一条 broker 路径」的外部路径（算法包重启、任务同步）。
// 直接返回错误会让它拿不到路径、带着一个已死的 socket 去启动 runner，
// 于是插件退出 → 重启 → 再退出，三跳之后进 CRASH_LOOP——现场实测的
// 「视频流恢复后算法却停了」就是这么来的。所以这里宁可等。
func (h *StreamHub) waitForRecoveryGate(ctx context.Context, cameraID string) error {
	// 预算有界：正常情况下 notBefore 就在一个回收周期之内；若反复失败把它一路往后推，
	// 说明相机真的还没回来，此时让调用方拿到明确错误，比把它无限挂住更有用。
	budget := time.Now().Add(h.recoveryGrace + recoveryGateSlack)

	for {
		h.mu.Lock()
		rec, active := h.recoveries[cameraID]
		var wait time.Duration
		if active {
			wait = time.Until(rec.notBefore)
		}
		h.mu.Unlock()

		if !active || wait <= 0 {
			return nil
		}
		if time.Now().Add(wait).After(budget) {
			return ErrStreamRecovering
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// awaitStreamReady 等到物理流真正进入 RUNNING，或明确失败。
//
// 「拿到引用」不等于「流能用」：Native 的 acquire 只保证受理。没跑起来就宣布恢复，
// 会把一个几秒后必然断掉的 broker 路径交给算法包，runner 随即退出再重启，
// 用户看到的是状态反复横跳。
func (h *StreamHub) awaitStreamReady(ctx context.Context, stream MediaStream) error {
	deadline := time.NewTimer(h.streamReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(streamReadyPollInterval)
	defer ticker.Stop()

	for {
		status, err := stream.Status(ctx)
		if err != nil {
			return err
		}
		switch status.State {
		case engine.StreamRunning:
			return nil
		case engine.StreamFailed:
			if status.Error != nil {
				return fmt.Errorf("%w: %v", ErrStreamRecovering, status.Error)
			}
			return ErrStreamRecovering
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w: stream did not become ready within %s", ErrStreamRecovering, h.streamReadyTimeout)
		case <-ticker.C:
		}
	}
}

// streamIsFailed 判断引擎刚交回的引用是否指向一路已经失败的物理流。
//
// 查询失败不影响主流程：真正的故障会由随后的订阅 / 收包错误暴露，
// 而在这里把一次 transient 的 CGO 失败当成「流坏了」反而会误杀健康流。
func streamIsFailed(ctx context.Context, stream MediaStream) (engine.StreamStatus, bool) {
	if stream == nil {
		return engine.StreamStatus{}, false
	}
	status, err := stream.Status(ctx)
	if err != nil {
		return engine.StreamStatus{}, false
	}
	return status, status.State == engine.StreamFailed
}

// aiSubscriptionIsCurrent 判断一条 AI 结果订阅是否仍属于该相机当前的 AI 流。
//
// 重连或任务同步会重建 AI 流，而旧订阅的失败回调可能迟到：没有这层代次校验，
// 一条来自上一代流的历史错误就能把刚恢复的新流拆掉，形成「重连成功立刻又失败」的循环。
func (h *StreamHub) aiSubscriptionIsCurrent(cameraID string, sub *engine.AIResultSubscription) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	ref, ok := h.aiStreams[cameraID]
	return ok && ref.aiSub == sub
}
