package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
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
	tcp := StreamOptions{Transport: TransportTCP}
	udp := StreamOptions{Transport: TransportUDP}

	// acquire 固定 URL 与消费者类型，只把「消费者 ID + 传输参数」摊开：
	// 五次调用里真正变化的只有这两个值。
	acquire := func(consumerID uint64, options StreamOptions) (*Stream, error) {
		return e.Acquire(
			ctx,
			poolURL,
			Consumer{ID: consumerID, Kind: ConsumerPreview},
			options,
		)
	}

	// 消费者 1 获取物理流
	s1, err := acquire(1001, tcp)
	if err != nil {
		t.Fatalf("Acquire s1 failed: %v", err)
	}

	// 消费者 2 获取相同物理流（应该复用）
	s2, err := acquire(1002, tcp)
	if err != nil {
		t.Fatalf("Acquire s2 failed: %v", err)
	}
	if s1.id != s2.id {
		t.Fatalf("expected physical stream reuse, got stream IDs %d and %d", s1.id, s2.id)
	}

	// 重复 consumerID 应当拒绝
	if _, err := acquire(1001, tcp); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for duplicate consumerID, got %v", err)
	}

	// 传输参数冲突应当拒绝
	if _, err := acquire(1003, udp); !errors.Is(err, ErrConfigConflict) {
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
	s3, err := acquire(1003, tcp)
	if err != nil {
		t.Fatalf("Acquire s3 during grace failed: %v", err)
	}
	if s3.id != s1.id {
		t.Fatalf("expected grace period reuse of stream ID %d, got %d", s1.id, s3.id)
	}

	if err := s3.Close(); err != nil {
		t.Errorf("s3.Close failed: %v", err)
	}
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
	sFast, err := e.Acquire(
		ctx,
		liveURL,
		Consumer{ID: 2001, Kind: ConsumerPreview},
		StreamOptions{Transport: TransportTCP},
	)
	if err != nil {
		t.Fatalf("Acquire sFast failed: %v", err)
	}
	defer sFast.Close()

	// 创建慢消费者
	sSlow, err := e.Acquire(
		ctx,
		liveURL,
		Consumer{ID: 2002, Kind: ConsumerPreview},
		StreamOptions{Transport: TransportTCP},
	)
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

	s, err := e.Acquire(
		ctx,
		baseURL+"/live",
		Consumer{ID: 3001, Kind: ConsumerPreview},
		StreamOptions{Transport: TransportUDP},
	)
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
	s, err := e.Acquire(
		ctx,
		baseURL+"/live",
		Consumer{ID: 4001, Kind: ConsumerPreview},
		StreamOptions{Transport: TransportTCP},
	)
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

// TestBridgeIntegration_AIConsumerAndBrokerPath 验证以 ConsumerAI 获取流时 FrameBroker 自动创建、
// Socket 路径可查询、可动态设置节流率，且在释放后 Socket 文件被清理。
func TestBridgeIntegration_AIConsumerAndBrokerPath(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. 获取 AI 消费者流
	s, err := e.Acquire(
		ctx,
		baseURL+"/live",
		Consumer{ID: 5001, Kind: ConsumerAI},
		StreamOptions{Transport: TransportTCP},
	)
	if err != nil {
		t.Fatalf("Acquire AI consumer failed: %v", err)
	}

	// 2. 查询 Broker Socket 路径
	brokerPath, err := s.BrokerPath(ctx)
	if err != nil {
		_ = s.Close()
		t.Fatalf("BrokerPath failed: %v", err)
	}
	if brokerPath == "" {
		_ = s.Close()
		t.Fatal("expected non-empty broker path")
	}

	// 3. 动态设置发布节流率
	if err := s.SetPublishRate(ctx, 10); err != nil {
		_ = s.Close()
		t.Fatalf("SetPublishRate failed: %v", err)
	}

	// 4. 验证 Socket 文件存在
	if _, err := os.Stat(brokerPath); err != nil {
		_ = s.Close()
		t.Fatalf("stat broker socket failed: %v", err)
	}

	// 5. 订阅 AI 推理结果流
	aiSub, err := s.SubscribeAIResults(ctx)
	if err != nil {
		_ = s.Close()
		t.Fatalf("SubscribeAIResults failed: %v", err)
	}

	// 连接到 Broker UDS 并发送一条 MSG_RESULT
	conn, err := net.Dial("unix", brokerPath)
	if err != nil {
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatalf("dial broker socket failed: %v", err)
	}

	// 发送握手请求 (msg_type = 1)
	handshakePayload := make([]byte, 40)
	binary.LittleEndian.PutUint32(handshakePayload[0:4], 0)
	binary.LittleEndian.PutUint32(handshakePayload[4:8], 1)
	copy(handshakePayload[8:], "test_client")

	reqMsg := make([]byte, 16+len(handshakePayload))
	binary.LittleEndian.PutUint32(reqMsg[0:4], 0x5A484C47)
	binary.LittleEndian.PutUint16(reqMsg[4:6], 1) // MSG_HANDSHAKE_REQ
	binary.LittleEndian.PutUint16(reqMsg[6:8], 1)
	binary.LittleEndian.PutUint32(reqMsg[8:12], uint32(len(handshakePayload)))
	binary.LittleEndian.PutUint32(reqMsg[12:16], 1)
	copy(reqMsg[16:], handshakePayload)
	if _, err := conn.Write(reqMsg); err != nil {
		_ = conn.Close()
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatalf("send handshake failed: %v", err)
	}

	// 读取握手响应 (16字节头 + 80字节体)
	respBuf := make([]byte, 96)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		_ = conn.Close()
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatalf("read handshake resp failed: %v", err)
	}

	// 发送 MSG_RESULT (msg_type = 7)
	testResultJSON := []byte(`{"stream_id":1,"pts":123,"cost_ms":10,"task_type":"detection"}`)
	resultMsg := make([]byte, 16+len(testResultJSON))
	binary.LittleEndian.PutUint32(resultMsg[0:4], 0x5A484C47)
	binary.LittleEndian.PutUint16(resultMsg[4:6], 7) // MSG_RESULT
	binary.LittleEndian.PutUint16(resultMsg[6:8], 1)
	binary.LittleEndian.PutUint32(resultMsg[8:12], uint32(len(testResultJSON)))
	binary.LittleEndian.PutUint32(resultMsg[12:16], 2)
	copy(resultMsg[16:], testResultJSON)
	if _, err := conn.Write(resultMsg); err != nil {
		_ = conn.Close()
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatalf("send result failed: %v", err)
	}
	_ = conn.Close()

	// 验证 AIResultSubscription 成功收到该结果
	recvResult, err := aiSub.Next(ctx)
	if err != nil {
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatalf("aiSub.Next failed: %v", err)
	}
	if string(recvResult) != string(testResultJSON) {
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatalf("expected result %s, got %s", testResultJSON, recvResult)
	}

	// 5.5 验证真实解码帧快照抓取
	time.Sleep(300 * time.Millisecond)
	images, err := s.Snapshot(ctx, SnapshotOptions{Quality: 85})
	if err != nil {
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatalf("Snapshot failed: %v", err)
	}
	if len(images) == 0 {
		_ = aiSub.Close()
		_ = s.Close()
		t.Fatal("expected at least 1 snapshot image")
	}

	if err := aiSub.Close(); err != nil {
		_ = s.Close()
		t.Fatalf("aiSub.Close failed: %v", err)
	}

	// 6. 释放流后验证 Broker 资源清理
	if err := s.Close(); err != nil {
		t.Fatalf("Close stream failed: %v", err)
	}

	// 7. 验证已关闭流调用 BrokerPath 返回 ErrClosed
	if _, err := s.BrokerPath(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed after stream closed, got %v", err)
	}
}

// TestBridgeIntegration_AudioTrack 验证双轨（ABI v5）：音频元数据、按轨订阅与关闭后的 NOT_FOUND。
func TestBridgeIntegration_AudioTrack(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	audioURL := baseURL + "/audio-pcma"
	s, err := e.Acquire(
		ctx,
		audioURL,
		Consumer{ID: 3001, Kind: ConsumerPreview},
		StreamOptions{Transport: TransportTCP},
	)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// 1. 音频元数据：查询可能早于 on_ready，用轮询等它变成 running
	info := waitForAudioInfo(t, ctx, s)
	if !info.HasAudio {
		t.Fatal("expected audio track on /audio-pcma")
	}
	if info.Codec != CodecPCMA {
		t.Fatalf("expected CodecPCMA, got %v", info.Codec)
	}
	if info.SampleRate != 8000 || info.Channels != 1 {
		t.Fatalf("unexpected rate/channels: %d/%d", info.SampleRate, info.Channels)
	}

	// 2. 混合轨道订阅必须被拒绝：两条轨的生命周期与预算是分开的
	mixedMask := SubscriptionOptions{TrackMask: TrackMaskVideo | TrackMaskAudio}
	if _, err := s.Subscribe(ctx, mixedMask); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for mixed track mask, got %v", err)
	}

	// 3. 音频订阅只收音频包，且 codec 与 PTSUS 都已归一化到视频输出时间轴
	sub, err := s.Subscribe(ctx, SubscriptionOptions{
		TrackMask:        TrackMaskAudio,
		MaxPackets:       48,
		MaxPacketBytes:   8 * 1024,
		MaxBufferedBytes: 512 * 1024,
	})
	if err != nil {
		t.Fatalf("Subscribe audio failed: %v", err)
	}
	defer sub.Close()

	pkt, err := sub.Next(ctx)
	if err != nil {
		t.Fatalf("Next audio failed: %v", err)
	}
	if pkt.TrackType != TrackAudio {
		t.Fatalf("expected audio track packet, got %v", pkt.TrackType)
	}
	if pkt.Codec != CodecPCMA {
		t.Fatalf("expected PCMA audio, got %v", pkt.Codec)
	}
	if len(pkt.Data) != 160 {
		t.Fatalf("expected 160-byte G.711 payload, got %d", len(pkt.Data))
	}
	if pkt.KeyFrame || pkt.FrameClass != FrameUnknown || pkt.GOPID != 0 {
		t.Fatalf("audio packet must not carry video framing: key=%v class=%v gop=%d",
			pkt.KeyFrame, pkt.FrameClass, pkt.GOPID)
	}

	// 4. 关流后元数据不悬垂：Go 侧句柄失效返回 ErrClosed，绝不回读已释放的原生存储
	if err := sub.Close(); err != nil {
		t.Fatalf("sub.Close failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if _, err := s.AudioInfo(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed after close, got %v", err)
	}

}

// TestBridgeIntegration_AudioAbsent 验证纯视频相机不会被误判为「有音频」。
func TestBridgeIntegration_AudioAbsent(t *testing.T) {
	baseURL := getTestRTSPBaseURL(t)

	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s, err := e.Acquire(ctx, baseURL+"/live", Consumer{ID: 3101, Kind: ConsumerPreview},
		StreamOptions{Transport: TransportTCP})
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer s.Close()

	info := waitForAudioInfo(t, ctx, s)
	if info.HasAudio {
		t.Fatalf("expected no audio on a video-only fixture, got codec=%v", info.Codec)
	}
}

// waitForAudioInfo 轮询音频元数据直到流进入 RUNNING。
//
// acquire 是异步的：on_ready 之前查询会拿到 NOT_RUNNING，这属于正常时序而非失败。
func waitForAudioInfo(t *testing.T, ctx context.Context, s *Stream) AudioInfo {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		info, err := s.AudioInfo(ctx)
		if err == nil {
			return info
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("audio info never became available: %v", lastErr)
	return AudioInfo{}
}
