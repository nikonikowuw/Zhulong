package algorunner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
)

// TaskRegionProvider supplies host-owned region snapshots separately from plugin parameters.
type TaskRegionProvider interface {
	TaskRegionsFor(ctx context.Context, algorithmID string) (map[uint64]string, error)
}

// TaskConfigProvider resolves task-level algorithm parameters, keyed by engine
// stream ID. Each stream may have different parameters, so these travel over
// task IPC rather than the process-wide --config argument.
type TaskConfigProvider interface {
	TaskConfigsFor(ctx context.Context, algorithmID string) (map[uint64]string, error)
}

// TaskConfigBroadcaster pushes per-stream configurations to the FrameBrokers.
//
// The Broker caches the last payload per stream and replays it on handshake, so a
// push is durable for processes that start later or restart: they receive the
// configuration before their first frame instead of waiting for the next push.
type TaskConfigBroadcaster struct {
	broadcaster AIMessageBroadcaster
	logger      *zap.Logger
}

// NewTaskConfigBroadcaster creates a TaskConfigBroadcaster.
func NewTaskConfigBroadcaster(broadcaster AIMessageBroadcaster, logger *zap.Logger) *TaskConfigBroadcaster {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &TaskConfigBroadcaster{
		broadcaster: broadcaster,
		logger:      logger.Named("algorithm.task_config_broadcaster"),
	}
}

// BroadcastAlgorithm pushes one MSG_TASK_CONFIG per stream.
//
// Failures are collected rather than aborting on the first one: a single
// stream whose broker is gone must not stop the other streams from receiving
// their configuration. The aggregated error is returned so the caller can log it
// once per algorithm instead of once per stream.
func (b *TaskConfigBroadcaster) BroadcastAlgorithm(
	ctx context.Context,
	algorithmID string,
	configs map[uint64]string,
) error {
	return b.broadcast(
		ctx,
		algorithmID,
		configs,
		algoipc.MsgTaskConfig,
	)
}

// BroadcastRegions sends complete snapshots; {} explicitly clears all regions.
func (b *TaskConfigBroadcaster) BroadcastRegions(
	ctx context.Context,
	algorithmID string,
	configs map[uint64]string,
) error {
	return b.broadcast(
		ctx,
		algorithmID,
		configs,
		algoipc.MsgTaskRegions,
	)
}

func (b *TaskConfigBroadcaster) broadcast(
	ctx context.Context,
	algorithmID string,
	configs map[uint64]string,
	msgType uint16,
) error {
	if b.broadcaster == nil || len(configs) == 0 {
		return nil
	}

	// Deterministic order: map iteration order would make the log sequence (and
	// any test asserting it) depend on the runtime's hash seed.
	streamIDs := make([]uint64, 0, len(configs))
	for streamID := range configs {
		streamIDs = append(streamIDs, streamID)
	}
	slices.Sort(streamIDs)

	var firstErr error

	// fail records the first failure and logs it against the stream that caused
	// it. The loop keeps going: one broken stream must not starve the others.
	fail := func(streamID uint64, message string, err error) {
		if firstErr == nil {
			firstErr = err
		}
		b.logger.Warn(message,
			zap.String("algorithm_id", algorithmID),
			zap.Uint64("stream_id", streamID),
			zap.Error(err),
		)
	}

	for _, streamID := range streamIDs {
		configJSON := configs[streamID]
		if !json.Valid([]byte(configJSON)) {
			// A malformed config would otherwise be applied by the package's own
			// tolerant parser *or* silently ignored there. Reject it here, where the
			// value originated from our own storage.
			b.logger.Warn("skipping task config that is not valid JSON",
				zap.String("algorithm_id", algorithmID),
				zap.Uint64("stream_id", streamID),
			)
			continue
		}

		payload, err := algoipc.EncodeTaskConfigPayload(algorithmID, []byte(configJSON))
		if err != nil {
			fail(streamID, "failed to encode task config",
				fmt.Errorf("encode task config for stream %d: %w", streamID, err),
			)
			continue
		}
		if err := b.broadcaster.BroadcastAIMessage(
			ctx,
			streamID,
			msgType,
			payload,
		); err != nil {
			fail(streamID, "failed to broadcast task config",
				fmt.Errorf("broadcast task config for stream %d: %w", streamID, err),
			)
		}
	}
	return firstErr
}
