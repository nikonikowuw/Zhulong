package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"go.uber.org/zap"
)

func TestService_AutoCleanupAudit(t *testing.T) {
	dbStore, repo, closeDB := newTestRepository(t)
	defer closeDB()

	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, zap.NewNop(), audit.Config{FlushInterval: 10 * time.Millisecond})
	auditCtx, auditCancel := context.WithCancel(context.Background())
	defer auditCancel()
	if err := auditSvc.Start(auditCtx); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = auditSvc.Stop(stopCtx)
	}()

	svc := NewService(ServiceDeps{Repo: repo, Logger: zap.NewNop()})
	svc.SetAuditor(auditSvc)
	svc.writeAutoCleanupAudit(&CleanupSummary{
		TriggerReason:  PruneTriggerEmergency,
		DeletedFiles:   2,
		FreedBytes:     4096,
		ProtectedFiles: 1,
		ProtectedBytes: 128,
		DurationMs:     42,
		TargetStatus:   PruneStatusCompleted,
		StopReason:     PruneStopTargetReached,
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		logs, _, err := auditStore.List(context.Background(), audit.Filter{
			Action: ActionStorageAutoCleanup,
			Limit:  1,
		})
		if err != nil {
			t.Fatalf("list audit logs: %v", err)
		}
		if len(logs) == 1 {
			detail := logs[0].Detail
			for _, want := range []string{
				"trigger=emergency",
				"deleted=2",
				"protectedFiles=1",
				"stopReason=target_reached",
			} {
				if !strings.Contains(detail, want) {
					t.Fatalf("audit detail %q does not contain %q", detail, want)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("auto cleanup audit entry was not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
