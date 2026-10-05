package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Subscription 封装对物理流视频数据包的按需订阅。
type Subscription struct {
	stream     *Stream
	id         C.Zhulong_subscription_id
	token      uintptr
	generation uint64

	maxPackets       int
	maxPacketBytes   int
	maxBufferedBytes int

	mu            sync.Mutex
	queue         []Packet
	bufferedBytes int
	closed        bool
	err           error

	notify        chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
	tokenDeleted  atomic.Bool
	cancelControl context.CancelFunc
}

func (s *Subscription) deleteToken() {
	if s.token == 0 {
		return
	}
	if s.tokenDeleted.CompareAndSwap(false, true) {
		h := cgo.Handle(s.token)
		h.Delete()
	}
}

// onPacket 由 CGO 回调在 Native 工作线程同步调用。
func (s *Subscription) onPacket(packet *C.Zhulong_packet_view) {
	if packet == nil {
		return
	}

	size := int(packet.size)
	if size < 0 || (size > 0 && packet.data == nil) {
		s.mu.Lock()
		if !s.closed {
			s.err = ErrInvalidArgument
			s.closed = true
			close(s.done)
		}
		s.mu.Unlock()
		s.signal()
		return
	}

	var borrowedData []byte
	if size > 0 {
		borrowedData = unsafe.Slice((*byte)(unsafe.Pointer(packet.data)), size)
	}

	s.onPacketData(
		int32(packet.codec),
		int64(packet.pts),
		int64(packet.dts),
		packet.has_pts != 0,
		packet.has_dts != 0,
		Rational{Num: int32(packet.time_base_num), Den: int32(packet.time_base_den)},
		packet.key_frame != 0,
		borrowedData,
		size,
	)
}

func (s *Subscription) onPacketData(
	codec int32,
	pts, dts int64,
	hasPTS, hasDTS bool,
	timeBase Rational,
	keyFrame bool,
	borrowedData []byte,
	size int,
) {
	if size < 0 || size > s.maxPacketBytes {
		s.mu.Lock()
		if !s.closed {
			s.err = ErrPacketTooLarge
			s.closed = true
			close(s.done)
		}
		s.mu.Unlock()
		s.signal()
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}

	// 检查包数与字节预算
	if len(s.queue) >= s.maxPackets || s.bufferedBytes+size > s.maxBufferedBytes {
		s.err = ErrBackpressure
		s.closed = true
		close(s.done)
		s.mu.Unlock()
		s.signal()
		return
	}

	// 从借用指针深拷贝到 Go 内存中，实现生命周期彻底解耦
	var data []byte
	if size > 0 && len(borrowedData) > 0 {
		data = make([]byte, size)
		copy(data, borrowedData)
	}

	pkt := Packet{
		Codec:    Codec(codec),
		PTS:      pts,
		DTS:      dts,
		HasPTS:   hasPTS,
		HasDTS:   hasDTS,
		TimeBase: timeBase,
		KeyFrame: keyFrame,
		Data:     data,
	}

	s.queue = append(s.queue, pkt)
	s.bufferedBytes += len(data)
	s.mu.Unlock()
	s.signal()
}

func (s *Subscription) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Next 阻塞等待获取下一个视频压缩包，支持 Context 取消。
func (s *Subscription) Next(ctx context.Context) (Packet, error) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			pkt := s.queue[0]
			s.queue[0] = Packet{}
			s.queue = s.queue[1:]
			s.bufferedBytes -= len(pkt.Data)
			s.mu.Unlock()
			return pkt, nil
		}
		if s.closed {
			err := s.err
			s.mu.Unlock()
			if err == nil {
				return Packet{}, ErrClosed
			}
			return Packet{}, err
		}
		s.mu.Unlock()

		select {
		case <-s.notify:
		case <-ctx.Done():
			return Packet{}, ctx.Err()
		case <-s.done:
		}
	}
}

// Done 返回订阅结束的通知通道。
func (s *Subscription) Done() <-chan struct{} {
	return s.done
}

// Err 返回订阅终态错误（若正常关闭则为 nil）。
func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close 关闭订阅并同步排空（Drain）底层 Native 回调。
func (s *Subscription) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancelControl != nil {
			s.cancelControl()
		}

		if s.stream != nil && s.stream.engine != nil {
			closeErr = s.stream.engine.unsubscribe(s.generation, s.stream.id, s.id, s.deleteToken)
		} else {
			s.deleteToken()
		}

		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.done)
		}
		s.queue = nil
		s.bufferedBytes = 0
		s.mu.Unlock()

		s.signal()

		if s.stream != nil {
			s.stream.onSubClosed(s)
		}
	})
	return closeErr
}

// runControlLoop 监控订阅生命周期与底层物理流状态（每 250ms 轮询）。
func (s *Subscription) runControlLoop(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			if !s.closed && s.err == nil {
				s.err = ctx.Err()
			}
			s.mu.Unlock()
			_ = s.Close()
			return
		case <-s.done:
			_ = s.Close()
			return
		case <-ticker.C:
			status, err := s.stream.Status(ctx)
			if err != nil {
				s.mu.Lock()
				if !s.closed {
					s.err = err
					s.closed = true
					close(s.done)
				}
				s.mu.Unlock()
				s.signal()
				_ = s.Close()
				return
			}
			if status.State == StreamFailed {
				s.mu.Lock()
				if !s.closed {
					if status.Error != nil {
						s.err = status.Error
					} else {
						s.err = ErrIO
					}
					s.closed = true
					close(s.done)
				}
				s.mu.Unlock()
				s.signal()
				_ = s.Close()
				return
			}
		}
	}
}
