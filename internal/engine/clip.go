package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <stdlib.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"
)

// ClipStatus 描述原生录像片段的完成状态（对应 ZHULONG_CLIP_*）。
type ClipStatus int32

const (
	ClipStatusCompleted ClipStatus = C.ZHULONG_CLIP_COMPLETED
	ClipStatusTruncated ClipStatus = C.ZHULONG_CLIP_TRUNCATED
	ClipStatusFailed    ClipStatus = C.ZHULONG_CLIP_FAILED
)

// RecorderConfig 配置某路流上的事件录像 GOP 环形缓冲与写队列。
type RecorderConfig struct {
	// PreMs 为事件前保留时长（毫秒）；0 表示关闭并释放缓冲。
	PreMs int64
	// ByteCap 为环形缓冲字节上限；<=0 使用原生默认（32 MiB）。
	ByteCap int64
	// QueueByteCap 为写队列字节上限；<=0 使用原生默认（16 MiB）。
	QueueByteCap int64
}

// ClipTriggerRequest 描述一次事件录像片段的触发请求。
type ClipTriggerRequest struct {
	// EventPTSUS 为事件帧在整形后时间轴上的微秒 PTS；<=0 时取缓冲最新包。
	EventPTSUS int64
	// PostMs 为事件后保留时长（毫秒）。
	PostMs int64
	// MaxMs 为单片段总长上限（毫秒）；<=0 使用原生默认（120s）。
	MaxMs int64
	// Path 为目标 MP4 的绝对路径（UTF-8）。
	Path string
}

// ClipTriggerResult 为触发片段的同步返回信息。
type ClipTriggerResult struct {
	Seq        uint64
	Created    bool
	Codec      Codec
	OffsetMs   int64
	StartPTSUS int64
}

// ClipEvent 为异步写线程完成一个片段后上报的事件。
type ClipEvent struct {
	Seq        uint64
	Status     ClipStatus
	Codec      Codec
	DurationMs int64
	SizeBytes  int64
	FirstPTSUS int64
	LastPTSUS  int64
}

// ConfigureRecorder 开启、调整或关闭该路流上的事件录像缓冲（PreMs == 0 时关闭）。
func (s *Stream) ConfigureRecorder(ctx context.Context, cfg RecorderConfig) error {
	if cfg.PreMs < 0 {
		return nativeErrorf("configure_recorder", StatusInvalidArgument, "negative pre_ms: %d", cfg.PreMs)
	}
	ref, err := s.lease(ctx)
	if err != nil {
		return err
	}

	var cCfg C.Zhulong_recorder_config
	cCfg.pre_ms = C.int64_t(cfg.PreMs)
	cCfg.byte_cap = C.int64_t(cfg.ByteCap)
	cCfg.queue_byte_cap = C.int64_t(cfg.QueueByteCap)

	return s.engine.configureRecorder(ref.generation, ref.id, &cCfg)
}

func (e *Engine) configureRecorder(
	gen uint64,
	streamID C.Zhulong_stream_id,
	cfg *C.Zhulong_recorder_config,
) error {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return err
	}
	defer release()

	status := C.Zhulong_stream_recorder_configure(lease.handle, streamID, cfg)
	return mapNativeStatus("configure_recorder", int32(status))
}

// TriggerClip 在该路流上发起或延长一个事件录像片段。
func (s *Stream) TriggerClip(ctx context.Context, req ClipTriggerRequest) (ClipTriggerResult, error) {
	if req.Path == "" || strings.IndexByte(req.Path, 0) >= 0 || !filepath.IsAbs(req.Path) {
		return ClipTriggerResult{}, nativeErrorf("trigger_clip", StatusInvalidArgument, "clip path must be absolute")
	}
	ref, err := s.lease(ctx)
	if err != nil {
		return ClipTriggerResult{}, err
	}

	cPath := C.CString(req.Path)
	defer C.free(unsafe.Pointer(cPath))

	var cReq C.Zhulong_clip_trigger_request
	cReq.event_pts_us = C.int64_t(req.EventPTSUS)
	cReq.post_ms = C.int64_t(req.PostMs)
	cReq.max_ms = C.int64_t(req.MaxMs)
	cReq.path = cPath

	return s.engine.triggerClip(ref.generation, ref.id, &cReq)
}

func (e *Engine) triggerClip(
	gen uint64,
	streamID C.Zhulong_stream_id,
	req *C.Zhulong_clip_trigger_request,
) (ClipTriggerResult, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return ClipTriggerResult{}, err
	}
	defer release()

	var cOut C.Zhulong_clip_trigger_result
	status := C.Zhulong_stream_clip_trigger(lease.handle, streamID, req, &cOut)
	if err := mapNativeStatus("trigger_clip", int32(status)); err != nil {
		return ClipTriggerResult{}, err
	}

	return ClipTriggerResult{
		Seq:        uint64(cOut.clip_seq),
		Created:    cOut.created != 0,
		Codec:      Codec(cOut.codec),
		OffsetMs:   int64(cOut.offset_ms),
		StartPTSUS: int64(cOut.start_pts_us),
	}, nil
}

const maxClipPollBatch = 256

// PollClipEvents 从原生引擎的完成队列中最多取出 max 条片段完成事件。
//
// 引擎 Stop 之后、Close 之前仍可调用以排空残留事件。
func (e *Engine) PollClipEvents(ctx context.Context, max int) ([]ClipEvent, error) {
	if ctx == nil {
		return nil, nativeErrorf("poll_clip_events", StatusInvalidArgument, "nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if max <= 0 {
		return nil, nil
	}
	if max > maxClipPollBatch {
		max = maxClipPollBatch
	}

	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning: false,
		deniedError:    ErrNotRunning,
	})
	if err != nil {
		return nil, err
	}
	defer release()

	buf := make([]C.Zhulong_clip_event, max)
	var outCount C.int32_t
	status := C.Zhulong_engine_clip_poll(lease.handle, &buf[0], C.int32_t(max), &outCount)
	runtime.KeepAlive(buf)
	if err := mapNativeStatus("poll_clip_events", int32(status)); err != nil {
		return nil, err
	}

	n := int(outCount)
	if n <= 0 {
		return nil, nil
	}
	if n > len(buf) {
		n = len(buf)
	}
	out := make([]ClipEvent, n)
	for i := 0; i < n; i++ {
		ev := buf[i]
		out[i] = ClipEvent{
			Seq:        uint64(ev.clip_seq),
			Status:     ClipStatus(ev.status),
			Codec:      Codec(ev.codec),
			DurationMs: int64(ev.duration_ms),
			SizeBytes:  int64(ev.size_bytes),
			FirstPTSUS: int64(ev.first_pts_us),
			LastPTSUS:  int64(ev.last_pts_us),
		}
	}
	return out, nil
}
