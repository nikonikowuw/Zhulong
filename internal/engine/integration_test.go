package engine

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func getTestRTSPBaseURL(t *testing.T) string {
	baseURL := os.Getenv("ZHULONG_RTSP_TEST_SERVER")
	if baseURL == "" {
		t.Skip("ZHULONG_RTSP_TEST_SERVER not set, skipping real RTSP integration tests")
	}
	return baseURL
}

// TestBridgeIntegration_Probe 验证通过真实 RTSP loopback 进行 H.264/H.265/超大参数集探测及异常取消。
func TestBridgeIntegration_Probe(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. 探测 H.264
	infoH264, err := e.Probe(ctx, baseURL+"/live", StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Probe H264 failed: %v", err)
	}
	if infoH264.Codec != CodecH264 {
		t.Fatalf("expected CodecH264, got %v", infoH264.Codec)
	}
	if infoH264.Width <= 0 || infoH264.Height <= 0 {
		t.Fatalf("invalid dimensions: %dx%d", infoH264.Width, infoH264.Height)
	}
	if len(infoH264.ExtraData) == 0 {
		t.Fatal("expected non-empty ExtraData for H264")
	}

	// 2. 探测 H.265
	infoH265, err := e.Probe(ctx, baseURL+"/h265", StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Probe H265 failed: %v", err)
	}
	if infoH265.Codec != CodecH265 {
		t.Fatalf("expected CodecH265, got %v", infoH265.Codec)
	}

	// 3. 探测超大参数集 (> 512 字节)
	infoLarge, err := e.Probe(ctx, baseURL+"/large-extra", StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Probe large-extra failed: %v", err)
	}
	if len(infoLarge.ExtraData) <= 512 {
		t.Fatalf("expected ExtraData > 512 bytes, got %d", len(infoLarge.ExtraData))
	}

	// 4. 探测不支持的编码 (VP8)
	_, err = e.Probe(ctx, baseURL+"/unsupported", StreamOptions{Transport: TransportTCP})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}

	// 5. 探测阻塞并在超时时安全取消（stall-open）
	stallCtx, stallCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stallCancel()
	_, err = e.Probe(stallCtx, baseURL+"/stall-open", StreamOptions{OpenTimeout: 5 * time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded for stall-open, got %v", err)
	}
}

// TestBridgeIntegration_StreamReuseAndGrace 验证同 URL 流复用、配置冲突拒绝及 8 秒宽限期语义。
func TestBridgeIntegration_StreamReuseAndGrace(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	poolURL := baseURL + "/pool"

	// 消费者 1 获取物理流
	s1, err := e.Acquire(ctx, poolURL, 1001, ConsumerPreview, StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Acquire s1 failed: %v", err)
	}

	// 消费者 2 获取相同物理流（应该复用）
	s2, err := e.Acquire(ctx, poolURL, 1002, ConsumerPreview, StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Acquire s2 failed: %v", err)
	}
	if s1.id != s2.id {
		t.Fatalf("expected physical stream reuse, got stream IDs %d and %d", s1.id, s2.id)
	}

	// 重复 consumerID 应当拒绝
	_, err = e.Acquire(ctx, poolURL, 1001, ConsumerPreview, StreamOptions{Transport: TransportTCP})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for duplicate consumerID, got %v", err)
	}

	// 传输参数冲突应当拒绝
	_, err = e.Acquire(ctx, poolURL, 1003, ConsumerPreview, StreamOptions{Transport: TransportUDP})
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("expected ErrConfigConflict for mismatched transport, got %v", err)
	}

	// 释放消费者 2
	if err := s2.Close(); err != nil {
		t.Fatalf("s2.Close failed: %v", err)
	}

	// 释放消费者 1（流进入 8 秒宽限期）
	if err := s1.Close(); err != nil {
		t.Fatalf("s1.Close failed: %v", err)
	}

	// 宽限期内重新 acquire，应当复用原物理连接而无需重新建立
	s3, err := e.Acquire(ctx, poolURL, 1003, ConsumerPreview, StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Acquire s3 during grace failed: %v", err)
	}
	if s3.id != s1.id {
		t.Fatalf("expected grace period reuse of stream ID %d, got %d", s1.id, s3.id)
	}

	_ = s3.Close()
}

// TestBridgeIntegration_SubscriptionAndBackpressure 验证真实视频包订阅、深拷贝数据完整性及慢消费者背压隔离。
func TestBridgeIntegration_SubscriptionAndBackpressure(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	liveURL := baseURL + "/live"

	// 创建正常消费者
	sFast, err := e.Acquire(ctx, liveURL, 2001, ConsumerPreview, StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Acquire sFast failed: %v", err)
	}
	defer sFast.Close()

	// 创建慢消费者
	sSlow, err := e.Acquire(ctx, liveURL, 2002, ConsumerPreview, StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Acquire sSlow failed: %v", err)
	}
	defer sSlow.Close()

	subFast, err := sFast.Subscribe(ctx, SubscriptionOptions{
		MaxPackets:       64,
		MaxPacketBytes:   1024 * 1024,
		MaxBufferedBytes: 4 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("Subscribe subFast failed: %v", err)
	}
	defer subFast.Close()

	// 慢消费者的队列上限设得非常小（2包），且不调用 Next 读取
	subSlow, err := sSlow.Subscribe(ctx, SubscriptionOptions{
		MaxPackets:       2,
		MaxPacketBytes:   1024 * 1024,
		MaxBufferedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("Subscribe subSlow failed: %v", err)
	}
	defer subSlow.Close()

	// 快消费者持续消费数据包（首包允许 RTSP 握手时间，后续包按帧间隔接收）
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	firstPkt, err := subFast.Next(firstCtx)
	firstCancel()
	if err != nil {
		t.Fatalf("subFast first packet failed: %v", err)
	}
	if firstPkt.Codec != CodecH264 || len(firstPkt.Data) == 0 {
		t.Fatalf("invalid first packet: codec=%v, len=%d", firstPkt.Codec, len(firstPkt.Data))
	}

	readCount := 1
	for i := 1; i < 15; i++ {
		readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
		pkt, err := subFast.Next(readCtx)
		readCancel()
		if err != nil {
			t.Fatalf("subFast.Next[%d] failed: %v", i, err)
		}
		if pkt.Codec != CodecH264 {
			t.Fatalf("unexpected packet codec: %v", pkt.Codec)
		}
		if len(pkt.Data) == 0 {
			t.Fatal("empty packet data received")
		}
		readCount++
	}

	if readCount < 10 {
		t.Fatalf("subFast received too few packets: %d", readCount)
	}

	// 验证慢消费者已因背压触发终态并断开，且没有拖垮快消费者
	select {
	case <-subSlow.Done():
		if !errors.Is(subSlow.Err(), ErrBackpressure) {
			t.Fatalf("expected ErrBackpressure for subSlow, got %v", subSlow.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subSlow did not trigger backpressure within timeout")
	}
}

// TestBridgeIntegration_UDPStream 验证标准 UDP 传输模式下的流消费与订阅。
func TestBridgeIntegration_UDPStream(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := e.Acquire(ctx, baseURL+"/live", 3001, ConsumerPreview, StreamOptions{Transport: TransportUDP})
	if err != nil {
		t.Fatalf("Acquire UDP stream failed: %v", err)
	}
	defer s.Close()

	sub, err := s.Subscribe(ctx, SubscriptionOptions{MaxPackets: 32})
	if err != nil {
		t.Fatalf("Subscribe UDP failed: %v", err)
	}
	defer sub.Close()

	readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readCancel()

	pkt, err := sub.Next(readCtx)
	if err != nil {
		t.Fatalf("Next on UDP stream failed: %v", err)
	}
	if len(pkt.Data) == 0 {
		t.Fatal("empty packet data received on UDP")
	}
}

// TestBridgeIntegration_ProbeDoesNotAffectActiveStream 验证独立 Probe 取消不会误杀正在活跃的流。
func TestBridgeIntegration_ProbeDoesNotAffectActiveStream(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. 启动正常流预览
	s, err := e.Acquire(ctx, baseURL+"/live", 4001, ConsumerPreview, StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer s.Close()

	sub, err := s.Subscribe(ctx, SubscriptionOptions{MaxPackets: 32})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer sub.Close()

	// 读取首包，确保流已就绪
	_, err = sub.Next(ctx)
	if err != nil {
		t.Fatalf("first packet read failed: %v", err)
	}

	// 2. 并发执行一个会被取消的探测
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer probeCancel()

	_, _ = e.Probe(probeCtx, baseURL+"/stall-open", StreamOptions{})

	// 3. 验证正常流依旧稳定接收视频包
	nextCtx, nextCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer nextCancel()
	pkt, err := sub.Next(nextCtx)
	if err != nil {
		t.Fatalf("stream packet read failed after probe cancellation: %v", err)
	}
	if len(pkt.Data) == 0 {
		t.Fatal("received empty packet after probe cancellation")
	}
}
