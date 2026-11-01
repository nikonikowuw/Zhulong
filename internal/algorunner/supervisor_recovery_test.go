package algorunner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// TestSupervisorRollbackThenStopKeepsStatusConsistent is the black-box
// concurrency counterpart to the white-box R1 test. A version-sensitive runner
// crashes on 2.0.0 inside the rollback guard window, which triggers automatic
// rollback to 1.0.0; concurrently StopAlgorithm is hammered. The invariant:
// whenever the supervisor reports RUNNING, the persisted status must not be
// DISABLED (a running process must never be described as stopped in the DB).
//
// The deterministic white-box test
// (TestStopAlgorithmDoesNotDisableAReplacedWatcher) pins the exact defect; this
// test cross-checks it under real scheduling with -race. The invariant is
// asserted once the round has settled, to avoid flagging benign mid-flight
// interleavings where a subsequent status write is still in progress.
func TestSupervisorRollbackThenStopKeepsStatusConsistent(t *testing.T) {
	const rounds = 6
	ctx := context.Background()

	for round := 0; round < rounds; round++ {
		store := newMockLifecycleStore()
		algoV1 := supervisorTestPackage()
		algoV1.Version = "1.0.0"
		store.packages[algoV1.ID] = algoV1

		// 版本敏感的桩：新形态的 argv 里没有版本号，但入口路径自带版本目录
		// （<root>/<id>/<version>/<id>），所以按 $0 分支才是对等写法。
		scriptContent := `#!/bin/sh
case "$0" in
	*2.0.0*)
		exit 1
		;;
esac
trap 'exit 0' TERM INT
while true; do sleep 0.05; done
`

		options := algorunner.DefaultRecoveryOptions()
		options.RollbackGuardWindow = 3 * time.Second
		options.InitialBackoff = 5 * time.Millisecond
		options.MaxBackoff = 10 * time.Millisecond
		root := stubPackageRoot(t, algoV1, scriptContent)
		algoV2 := algoV1
		algoV2.Version = "2.0.0"
		stubPackageRootAt(t, root, algoV2, scriptContent)
		supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: root}, options, zap.NewNop())

		if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
			supervisor.StopAll(time.Second)
			t.Fatalf("round %d: start v1: %v", round, err)
		}
		waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

		algoV2.PreviousVersion = algoV1.Version
		store.packages[algoV2.ID] = algoV2

		if err := supervisor.UpgradeAlgorithm(ctx, algoV2, algoV1); err != nil {
			supervisor.StopAll(time.Second)
			t.Fatalf("round %d: upgrade: %v", round, err)
		}

		// Hammer StopAlgorithm while the crash-triggered rollback is in flight.
		stopDone := make(chan struct{})
		go func() {
			defer close(stopDone)
			for i := 0; i < 30; i++ {
				_ = supervisor.StopAlgorithm(ctx, algoV1.ID)
				time.Sleep(3 * time.Millisecond)
			}
		}()
		<-stopDone

		// Let any in-flight rollback finish before reading the settled state.
		time.Sleep(150 * time.Millisecond)

		if got := supervisor.GetStatus(algoV1.ID); got == algorunner.StatusRunning {
			pkg, err := store.GetAlgorithmPackage(ctx, algoV1.ID)
			if err == nil && pkg.Status == algorunner.StatusDisabled {
				supervisor.StopAll(time.Second)
				t.Fatalf("round %d: supervisor reports RUNNING while the stored status is DISABLED", round)
			}
		}

		supervisor.StopAll(time.Second)
	}
}

func TestSupervisorUpgradeAlgorithmSuccess(t *testing.T) {
	store := newMockLifecycleStore()
	algoV1 := supervisorTestPackage()
	store.packages[algoV1.ID] = algoV1

	// 升级要拉起 2.0.0，所以两个版本的入口都得在盘上。
	root := stubPackageRoot(t, algoV1, idlePackageScript())
	algoV2 := algoV1
	algoV2.Version = "2.0.0"
	stubPackageRootAt(t, root, algoV2, idlePackageScript())

	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: root}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
		t.Fatalf("start v1 algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

	algoV2.PreviousVersion = algoV1.Version

	if err := supervisor.UpgradeAlgorithm(ctx, algoV2, algoV1); err != nil {
		t.Fatalf("upgrade algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algoV1.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected status running after upgrade, got %s", got)
	}
	if err := supervisor.StopAlgorithm(ctx, algoV1.ID); err != nil {
		t.Fatalf("stop upgraded algorithm: %v", err)
	}
}

func TestSupervisorUpgradeAlgorithmRollbackOnCrash(t *testing.T) {
	store := newMockLifecycleStore()
	algoV1 := supervisorTestPackage()
	algoV1.Version = "1.0.0"
	store.packages[algoV1.ID] = algoV1

	// Script that crashes immediately when receiving 2.0.0 in args, but stays alive for 1.0.0
	// 同上：按 $0 里的版本目录分支
	scriptContent := `#!/bin/sh
case "$0" in
	*2.0.0*)
		exit 1
		;;
esac
trap 'exit 0' TERM INT
while true; do sleep 0.1; done
`

	options := algorunner.DefaultRecoveryOptions()
	options.RollbackGuardWindow = 2 * time.Second
	root := stubPackageRoot(t, algoV1, scriptContent)
	algoV2 := algoV1
	algoV2.Version = "2.0.0"
	stubPackageRootAt(t, root, algoV2, scriptContent)
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: root}, options, zaptest.NewLogger(t))
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
		t.Fatalf("start v1 algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

	algoV2.PreviousVersion = algoV1.Version
	store.packages[algoV2.ID] = algoV2

	if err := supervisor.UpgradeAlgorithm(ctx, algoV2, algoV1); err != nil {
		t.Fatalf("upgrade algorithm: %v", err)
	}

	// Wait for automatic rollback to restore version 1.0.0 in store
	deadline := time.Now().Add(5 * time.Second)
	rolledBack := false
	for time.Now().Before(deadline) {
		pkg, err := store.GetAlgorithmPackage(ctx, algoV1.ID)
		if err == nil && pkg.Version == "1.0.0" {
			rolledBack = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !rolledBack {
		pkg, _ := store.GetAlgorithmPackage(ctx, algoV1.ID)
		t.Fatalf("expected version to be rolled back to 1.0.0, got %s", pkg.Version)
	}

	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)
}

func TestSupervisorStartFailsWithNoBrokerSocketsWhenResolverConfigured(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(&mockBrokerPathResolver{paths: []string{}})
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	err := supervisor.StartAlgorithm(context.Background(), algo)
	if !errors.Is(err, algorunner.ErrNoBrokerSockets) {
		t.Fatalf("expected ErrNoBrokerSockets, got %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected no watcher to be registered, got %s", got)
	}
}

func TestSupervisorRestartWithZeroBrokersRetiresRunnerToStandby(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolver := newMutableBrokerPathResolver([]string{"/tmp/stream1.sock"})
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := store.UpdateAlgorithmStatus(ctx, algo.ID, algorunner.StatusActive); err != nil {
		t.Fatalf("update status to active: %v", err)
	}
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algo.ID, algorunner.StatusRunning)

	// Now remove all tasks / broker sockets
	resolver.Set([]string{})
	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm with 0 brokers failed: %v", err)
	}

	// Runner should be gracefully retired, status in supervisor is DISABLED (standby)
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected supervisor status to be DISABLED (standby), got %s", got)
	}

	// Persisted status in DB must remain ACTIVE
	pkg, err := store.GetAlgorithmPackage(ctx, algo.ID)
	if err != nil {
		t.Fatalf("get package: %v", err)
	}
	if pkg.Status != algorunner.StatusActive {
		t.Fatalf("expected DB status to remain ACTIVE, got %s", pkg.Status)
	}
}
