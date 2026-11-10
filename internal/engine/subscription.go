package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
	"sync"
	"unsafe"
)

// Subscription 封装对物理流视频数据包的按需订阅。
//
// 队列语义：包数 + 字节双预算，任一超限立即以 ErrBackpressure 终止订阅并唤醒读者，
// 绝不阻塞原生工作线程。
type Subscription struct {
	stream     *Stream
	id         C.Zhulong_subscription_id
	token      cgoToken
	generation uint64

	limits subscriptionLimits
	// mask 是订阅的轨道掩码，仅用于诊断与断言；路由本身由原生侧按轨道完成。
	mask TrackMask

	core          *queueCore[Packet]
	closeOnce     sync.Once
	cancelControl context.CancelFunc
}

// inboundPacket 承载一次原生回调携带的全部字段。
//
// 之所以用结构体而不是继续加位置参数：ABI v3 之后字段已达 13 个，长参数表一旦错序
// （例如把 dts_us 传成 pts_us）编译期不会报错，只会让预览端时间戳静默错乱。
type inboundPacket struct {
	TrackType  TrackType
	Codec      Codec
	PTS        int64
	DTS        int64
	HasPTS     bool
	HasDTS     bool
	TimeBase   Rational
	KeyFrame   bool
	Data       []byte // 借用的原生内存，仅在回调期间有效
	Size       int
	PTSUS      int64
	DTSUS      int64
	FrameClass FrameClass
	GOPID      uint32
	Flags      PacketFlag
}

// toPacket 把回调视图深拷贝为 Go 拥有的 Packet。
//
// ⚠️ 借用指针只在同步回调期间有效，必须在这里完成拷贝，绝不外传。
func (in inboundPacket) toPacket() Packet {
	var data []byte
	if in.Size > 0 {
		data = make([]byte, in.Size)
		copy(data, in.Data)
	}
	return Packet{
		TrackType:  in.TrackType,
		Codec:      in.Codec,
		PTS:        in.PTS,
		DTS:        in.DTS,
		HasPTS:     in.HasPTS,
		HasDTS:     in.HasDTS,
		TimeBase:   in.TimeBase,
		KeyFrame:   in.KeyFrame,
		Data:       data,
		PTSUS:      in.PTSUS,
		DTSUS:      in.DTSUS,
		FrameClass: in.FrameClass,
		GOPID:      in.GOPID,
		Flags:      in.Flags,
	}
}

// onPacket 由 CGO 回调在 Native 工作线程同步调用。
func (s *Subscription) onPacket(packet *C.Zhulong_packet_view) {
	if packet == nil {
		return
	}

	size := int(packet.size)
	if size < 0 || (size > 0 && packet.data == nil) {
		s.core.closeWithErr(ErrInvalidArgument)
		return
	}

	var borrowed []byte
	if size > 0 {
		borrowed = unsafe.Slice((*byte)(unsafe.Pointer(packet.data)), size)
	}

	s.onPacketData(inboundPacket{
		TrackType:  TrackType(packet.track_type),
		Codec:      Codec(packet.codec),
		PTS:        int64(packet.pts),
		DTS:        int64(packet.dts),
		HasPTS:     packet.has_pts != 0,
		HasDTS:     packet.has_dts != 0,
		TimeBase:   Rational{Num: int32(packet.time_base_num), Den: int32(packet.time_base_den)},
		KeyFrame:   packet.key_frame != 0,
		Data:       borrowed,
		Size:       size,
		PTSUS:      int64(packet.pts_us),
		DTSUS:      int64(packet.dts_us),
		FrameClass: FrameClass(packet.frame_class),
		GOPID:      uint32(packet.gop_id),
		Flags:      PacketFlag(packet.pkt_flags),
	})
}

func (s *Subscription) onPacketData(in inboundPacket) {
	// 双保险：原生侧已按 track_mask 分流，这里再拦一次，因为串轨的后果是
	// 音频包被当成视频喂给解码器（黑屏/花屏），而这类错位极难从现象反推。
	if !s.mask.Accepts(in.TrackType) {
		return
	}
	if in.Size < 0 || in.Size > s.limits.maxPacketBytes {
		s.core.closeWithErr(ErrPacketTooLarge)
		return
	}
	// 回调视图必须自洽：声明了长度就必须有同样长（或更长）的借用内存。
	if in.Size > len(in.Data) {
		s.core.closeWithErr(ErrInvalidArgument)
		return
	}
	// 订阅已终止时直接丢弃，省掉一次无谓的深拷贝。
	if s.core.isClosed() {
		return
	}
	s.core.pushBounded(
		in.toPacket(),
		queueBudget{maxItems: s.limits.maxPackets, maxBytes: s.limits.maxBufferedBytes},
		ErrBackpressure,
	)
}

// Next 阻塞等待获取下一个视频压缩包，支持 Context 取消。
func (s *Subscription) Next(ctx context.Context) (Packet, error) {
	return s.core.next(ctx)
}

// Done 返回订阅结束的通知通道。
func (s *Subscription) Done() <-chan struct{} {
	return s.core.Done()
}

// Err 返回订阅终态错误（若正常关闭则为 nil）。
func (s *Subscription) Err() error {
	return s.core.Err()
}

// reportCallbackPanic 把回调内的 panic 转成订阅终态，避免它变成「静默丢帧」。
func (s *Subscription) reportCallbackPanic(recovered any) {
	if recovered == nil {
		return
	}
	s.core.closeWithErr(nativeCallbackPanic("packet_subscription", recovered))
}

// Close 关闭订阅并同步排空（Drain）底层 Native 回调。
func (s *Subscription) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancelControl != nil {
			s.cancelControl()
		}

		// 本地构造的订阅（测试接缝）没有原生 id，只能就地释放 token。
		hasNativeSubscription := s.stream != nil && s.stream.engine != nil && s.id > 0
		if hasNativeSubscription {
			closeErr = s.stream.engine.unsubscribeWith(
				s.generation,
				"unsubscribe",
				func(handle C.Zhulong_engine_h) C.Zhulong_status_t {
					return C.Zhulong_stream_unsubscribe(handle, s.stream.id, s.id)
				},
				s.token.release,
			)
		} else {
			s.token.release()
		}

		s.core.closeWithErr(nil)
		s.core.dropQueued()

		if s.stream != nil {
			s.stream.onSubClosed(s)
		}
	})
	return closeErr
}

// runControlLoop 监控订阅生命周期与底层物理流状态。
func (s *Subscription) runControlLoop(ctx context.Context) {
	s.core.watch(ctx, s.stream, func() {
		// 控制循环的退出路径无处上报注销错误：订阅终态已由 core 记录，
		// 资源回收失败由原生侧日志兜底。
		_ = s.Close()
	})
}
