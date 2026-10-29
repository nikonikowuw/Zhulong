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
	SubscribeAIResults(ctx context.Context) (*engine.AIResultSubscription, error)
	BrokerPath(ctx context.Context) (string, error)
	// BrokerClients 返回该路 AI 流上各算法包的帧投递统计（只读快照）。
	// 第二个返回值是当前客户端总数，大于 len(clients) 时表示结果被截断。
	BrokerClients(ctx context.Context) ([]engine.BrokerClientStats, uint32, error)
	SetPublishRate(ctx context.Context, fps uint32) error
	Snapshot(ctx context.Context, options engine.SnapshotOptions) ([]engine.SnapshotImage, error)
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
	s, err := a.engine.Acquire(ctx, uri, engine.Consumer{ID: consumerID, Kind: kind}, options)
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

func (s *streamAdapter) SubscribeAIResults(ctx context.Context) (*engine.AIResultSubscription, error) {
	return s.stream.SubscribeAIResults(ctx)
}

func (s *streamAdapter) BrokerPath(ctx context.Context) (string, error) {
	return s.stream.BrokerPath(ctx)
}

func (s *streamAdapter) BrokerClients(ctx context.Context) ([]engine.BrokerClientStats, uint32, error) {
	return s.stream.BrokerClients(ctx)
}

func (s *streamAdapter) SetPublishRate(ctx context.Context, fps uint32) error {
	return s.stream.SetPublishRate(ctx, fps)
}

func (s *streamAdapter) Snapshot(ctx context.Context, options engine.SnapshotOptions) ([]engine.SnapshotImage, error) {
	return s.stream.Snapshot(ctx, options)
}

func (s *streamAdapter) Close() error {
	return s.stream.Close()
}
