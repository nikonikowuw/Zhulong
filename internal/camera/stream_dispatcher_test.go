package camera

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// newTestDispatcher 构造一个只用于分发策略测试的 dispatcher。
// stream / sub / hub 在这几条路径上均不被访问，留空即可。
func newTestDispatcher(gop []outFrame) *StreamDispatcher {
	d := NewStreamDispatcher(StreamDispatcherConfig{
		CameraID: "cam-1",
		Role:     StreamRoleMain,
	})
	d.gop = gopCache{frames: gop, complete: true}
	for _, frame := range gop {
		d.gop.bytes += len(frame.data)
	}
	return d
}

func newTestClient(d *StreamDispatcher) *StreamClient {
	return NewStreamClient(nil, d, zap.NewNop())
}

func liveFrame(dtsUS int64, keyFrame bool) engine.Packet {
	class := engine.FramePredictive
	if keyFrame {
		class = engine.FrameIDR
	}
	return engine.Packet{
		Codec:      engine.CodecH264,
		PTS:        dtsUS,
		DTS:        dtsUS,
		HasPTS:     true,
		HasDTS:     true,
		PtsUS:      dtsUS,
		DtsUS:      dtsUS,
		KeyFrame:   keyFrame,
		FrameClass: class,
		Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x65},
	}
}

// 回归保护：起播补发完成后，进度基准必须锚在 GOP 的**最后一帧**（实时边缘）。
//
// 锚在第一帧时，紧接着到达的第一枚实时帧看起来落后了整整一个 GOP；相机 GOP 常为
// 2 秒，远超 maxClientLagUS(500ms)，deliverLocked 因此必然判为滞后并清空尚未发出的
// GOP 尾部，客户端只能等相机下一个 IDR —— 外部表现是「起播必定卡 1~2 秒」。
func TestRegisterClientAnchorsProgressAtNewestPrimedFrame(t *testing.T) {
	// 1.2 秒的 GOP：首末帧跨度远超 500ms 的滞后预算
	gop := []outFrame{
		{data: []byte{0x01}, dtsUS: 0, frame: WireFrameIDR},
		{data: []byte{0x02}, dtsUS: 600_000, frame: WireFramePredictive},
		{data: []byte{0x03}, dtsUS: 1_200_000, frame: WireFramePredictive},
	}
	d := newTestDispatcher(gop)
	client := newTestClient(d)

	d.RegisterClient(client)

	if !client.hasProgressDTS {
		t.Fatal("client should have a progress anchor after priming")
	}
	if want := gop[len(gop)-1].dtsUS; client.lastProgressDTSUS != want {
		t.Fatalf("progress anchor = %d, want %d (newest primed frame)", client.lastProgressDTSUS, want)
	}
	if got := len(client.sendChan); got != len(gop) {
		t.Fatalf("primed frames = %d, want %d", got, len(gop))
	}
	if client.needsKey {
		t.Fatal("client must not wait for a keyframe after a complete GOP prime")
	}
}

// 回归保护：补发后到达的第一枚实时帧不得触发重同步，且必须被投递。
func TestFirstLiveFrameAfterPrimeDoesNotResync(t *testing.T) {
	gop := []outFrame{
		{data: []byte{0x01}, dtsUS: 0, frame: WireFrameIDR},
		{data: []byte{0x02}, dtsUS: 600_000, frame: WireFramePredictive},
		{data: []byte{0x03}, dtsUS: 1_200_000, frame: WireFramePredictive},
	}
	d := newTestDispatcher(gop)
	client := newTestClient(d)
	d.RegisterClient(client)

	// 实时边缘之后 40ms 的普通 P 帧
	d.Broadcast(liveFrame(1_240_000, false))

	if got := d.Counters().LagResyncs; got != 0 {
		t.Fatalf("lag resyncs = %d, want 0: cold start must not drop a complete GOP", got)
	}
	if got := len(client.sendChan); got != len(gop)+1 {
		t.Fatalf("queued frames = %d, want %d: live frame missing after prime", got, len(gop)+1)
	}
}

// GOP 装不下队列时整段放弃并等下一个 IDR，绝不发半截参考链。
func TestRegisterClientDropsWholeGOPWhenQueueTooSmall(t *testing.T) {
	gop := make([]outFrame, defaultClientBufferSize+1)
	for index := range gop {
		frame := WireFramePredictive
		if index == 0 {
			frame = WireFrameIDR
		}
		gop[index] = outFrame{data: []byte{0x01}, dtsUS: int64(index) * 40_000, frame: frame}
	}
	d := newTestDispatcher(gop)
	client := newTestClient(d)

	d.RegisterClient(client)

	if got := len(client.sendChan); got != 0 {
		t.Fatalf("queued frames = %d, want 0 (priming must be abandoned)", got)
	}
	if !client.needsKey {
		t.Fatal("client must wait for the next IDR when the GOP cannot fit")
	}
	if client.hasProgressDTS {
		t.Fatal("abandoned priming must not leave a progress anchor behind")
	}
}

// lateAudioStream 模拟「acquire 已返回、但流还没 ready」：前 N 次 audio_info 报错。
type lateAudioStream struct {
	mockMediaStream
	attempts atomic.Int32
	failures int32
}

func (s *lateAudioStream) AudioInfo(ctx context.Context) (engine.AudioInfo, error) {
	if int(s.attempts.Add(1)) <= int(s.failures) {
		return engine.AudioInfo{}, engine.ErrNotRunning
	}
	return s.mockMediaStream.AudioInfo(ctx)
}

// 能力帧必须等流就绪，而不是把「还没 ready」当成「没有音频」。
//
// 这条用例来自板端 16 路压测的实测：全部 16 路都被记成 audio info unavailable，
// 其中 4 路的音频通道其实工作正常。能力帧只发一次、前端不重试，所以误判是永久的。
func TestAudioCapabilityWaitsForStreamReady(t *testing.T) {
	stream := &lateAudioStream{failures: 3}
	stream.audioInfo = engine.AudioInfo{
		HasAudio:   true,
		Codec:      engine.CodecPCMU,
		SampleRate: 8000,
		Channels:   1,
	}
	d := NewStreamDispatcher(StreamDispatcherConfig{
		CameraID: "cam-1",
		Role:     StreamRoleMain,
		Stream:   stream,
	})

	started := time.Now()
	capability := d.AudioCapability(context.Background())
	elapsed := time.Since(started)

	if !capability.Present {
		t.Fatalf("capability = %+v, want present: the stream only needed a moment to become ready", capability)
	}
	if capability.Codec != "pcmu" || capability.SampleRate != 8000 {
		t.Fatalf("capability = %+v, want pcmu/8000", capability)
	}
	if elapsed >= capabilityReadyTimeout {
		t.Fatalf("elapsed = %v, want < %v (must return as soon as info is available)", elapsed, capabilityReadyTimeout)
	}
	if attempts := stream.attempts.Load(); attempts < 4 {
		t.Fatalf("audio_info attempts = %d, want >= 4: the wait must actually retry", attempts)
	}
}

// 真的查不到时仍要给出 present=false + reason，且不超过预算。
func TestAudioCapabilityReportsUnavailableAfterBudget(t *testing.T) {
	stream := &lateAudioStream{failures: 1 << 30}
	d := NewStreamDispatcher(StreamDispatcherConfig{
		CameraID: "cam-1",
		Role:     StreamRoleMain,
		Stream:   stream,
	})

	budget := 80 * time.Millisecond
	started := time.Now()
	capability := d.audioCapabilityWithin(context.Background(), budget)
	elapsed := time.Since(started)

	if capability.Present {
		t.Fatal("capability must be absent when audio info never becomes available")
	}
	if capability.Reason == "" {
		t.Fatal("absent capability must carry a reason for the frontend tooltip")
	}
	if elapsed < budget/2 || elapsed > budget+time.Second {
		t.Fatalf("elapsed = %v, want roughly the %v budget", elapsed, budget)
	}
}
