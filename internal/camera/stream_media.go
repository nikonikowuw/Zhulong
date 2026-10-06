package camera

import (
	"context"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

// PacketSource 抽象视频数据包订阅源（engine.Subscription 天然满足）。
type PacketSource interface {
	Next(ctx context.Context) (engine.Packet, error)
	Close() error
}

// MediaStream 抽象单个消费者对物理流的引用。
type MediaStream interface {
	Subscribe(ctx context.Context, options engine.SubscriptionOptions) (PacketSource, error)
	Close() error
}

// MediaEngine 抽象媒体拉流与连接池底座。
type MediaEngine interface {
	AcquireStream(ctx context.Context, uri string, consumerID uint64, kind engine.ConsumerKind, options engine.StreamOptions) (MediaStream, error)
}

// EngineMediaAdapter 将 *engine.Engine 适配为 MediaEngine 接口。
type EngineMediaAdapter struct {
	engine *engine.Engine
}

// NewEngineMediaAdapter 创建 *engine.Engine 的适配器。
func NewEngineMediaAdapter(e *engine.Engine) *EngineMediaAdapter {
	return &EngineMediaAdapter{engine: e}
}

func (a *EngineMediaAdapter) AcquireStream(
	ctx context.Context,
	uri string,
	consumerID uint64,
	kind engine.ConsumerKind,
	options engine.StreamOptions,
) (MediaStream, error) {
	if a.engine == nil {
		return nil, engine.ErrNotRunning
	}
	s, err := a.engine.Acquire(ctx, uri, consumerID, kind, options)
	if err != nil {
		return nil, err
	}
	return &streamAdapter{stream: s}, nil
}

type streamAdapter struct {
	stream *engine.Stream
}

func (s *streamAdapter) Subscribe(ctx context.Context, options engine.SubscriptionOptions) (PacketSource, error) {
	return s.stream.Subscribe(ctx, options)
}

func (s *streamAdapter) Close() error {
	return s.stream.Close()
}
