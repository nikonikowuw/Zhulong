package storage

import (
	"context"
	"errors"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
)

const defaultRemoteBatchSize = 50

// RemotePruneUnit identifies one SQLite-indexed remote media item (either a
// snapshot unit or a remote recording clip) eligible for retention or quota cleanup.
type RemotePruneUnit struct {
	ID         string
	SnapshotID string
	ClipID     string
	Keys       []string
	SizeBytes  int64
	CreatedAt  time.Time
}

// RemotePrunePlan describes retention-expired and excess-quota remote items
// derived from SQLite indexes without listing objects from S3.
type RemotePrunePlan struct {
	Expired     []RemotePruneUnit
	QuotaExcess []RemotePruneUnit
	ExcessBytes int64
}

// RemotePruneProvider queries SQLite indexes to produce a RemotePrunePlan.
type RemotePruneProvider interface {
	BuildRemotePrunePlan(ctx context.Context, cfg *StorageConfig, now time.Time) (RemotePrunePlan, error)
}

// RemoteClipReconciler cleans up or marks SQLite clip records after their
// remote objects have been deleted.
type RemoteClipReconciler func(ctx context.Context, clipIDs []string) error

// RemotePruneRequest parameterizes a remote cleanup cycle on CleanerEngine.
type RemotePruneRequest struct {
	Config   *StorageConfig
	Trigger  PruneTrigger
	Store    filestore.Store
	Provider RemotePruneProvider
}

// RunRemotePrune deletes retention-expired and over-quota remote media objects in
// bounded batches using SQLite-indexed keys and the fail-closed EvidenceGuard,
// avoiding remote bucket listing and ".lock" sidecar lookups.
func (c *CleanerEngine) RunRemotePrune(
	ctx context.Context,
	req RemotePruneRequest,
) (*CleanupSummary, []string, error) {
	if c == nil {
		return nil, nil, errors.New("cleaner engine is nil")
	}
	if req.Config == nil {
		return nil, nil, errors.New("storage config cannot be nil")
	}
	if req.Store == nil {
		return nil, nil, errors.New("remote filestore cannot be nil")
	}
	if !c.running.CompareAndSwap(false, true) {
		return nil, nil, ErrCleanupInProgress
	}
	defer c.running.Store(false)

	startTime := time.Now()
	summary := &CleanupSummary{
		TriggerReason: req.Trigger,
		StopReason:    PruneStopCandidatesExhausted,
	}

	if req.Provider == nil {
		summary.finish(startTime, PruneStatusCompleted)
		return summary, nil, nil
	}

	plan, err := req.Provider.BuildRemotePrunePlan(ctx, req.Config, time.Now().UTC())
	if err != nil {
		return nil, nil, err
	}

	batchSize := c.batchSize
	if batchSize <= 0 {
		batchSize = defaultRemoteBatchSize
	}

	guard := c.currentEvidenceGuard()
	deletedUnitIDs := make(map[string]struct{}, len(plan.Expired))
	var deletedClipIDs []string
	remainingExcess := max(plan.ExcessBytes, int64(0))

	if stopReason := c.runRemoteBatches(
		ctx,
		plan.Expired,
		remoteBatchParams{
			batchSize:       batchSize,
			guard:           guard,
			store:           req.Store,
			summary:         summary,
			deletedClipIDs:  &deletedClipIDs,
			deletedUnitIDs:  deletedUnitIDs,
			remainingExcess: &remainingExcess,
			stopOnTarget:    false,
		},
	); stopReason != "" {
		summary.StopReason = stopReason
		summary.finish(startTime, PruneStatusCompleted)
		return summary, deletedClipIDs, nil
	}

	if remainingExcess > 0 {
		quotaQueue := make([]RemotePruneUnit, 0, len(plan.QuotaExcess))
		for _, unit := range plan.QuotaExcess {
			if _, seen := deletedUnitIDs[unit.ID]; seen {
				continue
			}
			quotaQueue = append(quotaQueue, unit)
		}
		stopReason := c.runRemoteBatches(
			ctx,
			quotaQueue,
			remoteBatchParams{
				batchSize:       batchSize,
				guard:           guard,
				store:           req.Store,
				summary:         summary,
				deletedClipIDs:  &deletedClipIDs,
				deletedUnitIDs:  deletedUnitIDs,
				remainingExcess: &remainingExcess,
				stopOnTarget:    true,
			},
		)
		if stopReason != "" {
			summary.StopReason = stopReason
		} else if remainingExcess <= 0 {
			summary.StopReason = PruneStopTargetReached
		}
	} else if plan.ExcessBytes > 0 {
		summary.StopReason = PruneStopTargetReached
	}

	summary.finish(startTime, PruneStatusCompleted)
	return summary, deletedClipIDs, nil
}

type remoteBatchParams struct {
	batchSize       int
	guard           EvidenceGuard
	store           filestore.Store
	summary         *CleanupSummary
	deletedClipIDs  *[]string
	deletedUnitIDs  map[string]struct{}
	remainingExcess *int64
	stopOnTarget    bool
}

func (c *CleanerEngine) runRemoteBatches(
	ctx context.Context,
	units []RemotePruneUnit,
	p remoteBatchParams,
) PruneStopReason {
	for start := 0; start < len(units); start += p.batchSize {
		if ctx.Err() != nil {
			return PruneStopContextCanceled
		}
		end := min(start+p.batchSize, len(units))
		batch := units[start:end]

		if stop := c.deleteRemoteBatch(ctx, batch, p); stop != "" {
			return stop
		}
		if p.stopOnTarget && *p.remainingExcess <= 0 {
			return PruneStopTargetReached
		}
	}
	return ""
}

func (c *CleanerEngine) deleteRemoteBatch(
	ctx context.Context,
	batch []RemotePruneUnit,
	p remoteBatchParams,
) PruneStopReason {
	var snapshotIDs []string
	for _, unit := range batch {
		if unit.SnapshotID != "" {
			snapshotIDs = appendUnique(snapshotIDs, unit.SnapshotID)
		}
	}

	var protected map[string]bool
	guardFailed := false
	if p.guard != nil && len(snapshotIDs) > 0 {
		var err error
		protected, err = p.guard(ctx, snapshotIDs)
		if err != nil {
			guardFailed = true
			c.logger.Warn("remote storage cleaner: evidence guard failed; protecting snapshot batch", zap.Error(err))
		}
	}

	for _, unit := range batch {
		if ctx.Err() != nil {
			return PruneStopContextCanceled
		}
		if unit.SnapshotID != "" && (guardFailed || protected[unit.SnapshotID]) {
			p.summary.ProtectedFiles += len(unit.Keys)
			if unit.SizeBytes > 0 {
				p.summary.ProtectedBytes += unit.SizeBytes
			}
			continue
		}

		unitDeleted := true
		deletedKeys := 0
		for _, key := range unit.Keys {
			if key == "" {
				continue
			}
			if err := p.store.Delete(ctx, key); err != nil {
				unitDeleted = false
				c.logger.Warn("remote storage cleaner: delete object failed",
					zap.String("key", key),
					zap.Error(err),
				)
				if ctx.Err() != nil {
					return PruneStopContextCanceled
				}
				break
			}
			deletedKeys++
		}
		if !unitDeleted {
			continue
		}

		p.deletedUnitIDs[unit.ID] = struct{}{}
		p.summary.DeletedFiles += deletedKeys
		if unit.SizeBytes > 0 {
			p.summary.FreedBytes += unit.SizeBytes
			*p.remainingExcess -= unit.SizeBytes
		}
		if unit.SnapshotID != "" {
			p.summary.DeletedSnapshotIDs = appendUnique(p.summary.DeletedSnapshotIDs, unit.SnapshotID)
		}
		if unit.ClipID != "" {
			*p.deletedClipIDs = appendUnique(*p.deletedClipIDs, unit.ClipID)
		}
		if p.stopOnTarget && *p.remainingExcess <= 0 {
			return PruneStopTargetReached
		}
	}
	return ""
}
