package algorithm_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
)

func setupServiceTest(t *testing.T) (*algorithm.AlgorithmComponents, *database.Store, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	comp := algorithm.NewAlgorithmComponents(
		dbStore,
		algorunner.Runtime{AlgorithmsDir: tempDir},
		x86Host(),
		zap.NewNop(),
	)
	return comp, dbStore, tempDir
}

func TestServiceDeleteAlgorithmCleansUpTasks(t *testing.T) {
	comp, dbStore, _ := setupServiceTest(t)
	ctx := context.Background()

	// 1. Create an algorithm
	algo := &algorithm.Algorithm{
		ID:           "algo_fire_01",
		Name:         "fire_detect",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategoryFire,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  20,
		MemoryMB:     128,
		Status:       algorithm.StatusDisabled,
	}
	if err := comp.Store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	// 2. Create analysis tasks referencing this algorithm directly in DB
	if err := dbStore.DB().Exec(`
		INSERT INTO analysis_tasks (id, camera_id, enabled, created_at, updated_at) VALUES 
			('task_cam1', 'cam_01', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('task_cam2', 'cam_02', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps, config_json, created_at, updated_at) VALUES 
			('sub_cam1_fire', 'task_cam1', 'algo_fire_01', 1, 5, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('sub_cam2_fire', 'task_cam2', 'algo_fire_01', 1, 5, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`).Error; err != nil {
		t.Fatalf("insert test tasks failed: %v", err)
	}

	// 3. Delete Algorithm
	if err := comp.Service.DeleteAlgorithm(ctx, "algo_fire_01"); err != nil {
		t.Fatalf("delete algorithm failed: %v", err)
	}

	// 4. Verify algorithm is gone
	_, err := comp.Store.GetAlgorithm(ctx, "algo_fire_01")
	if err == nil {
		t.Fatal("expected algorithm to be deleted, but still found")
	}

	// 5. Verify task_algorithms for this algorithm are also gone
	var count int64
	dbStore.DB().Table("task_algorithms").Where("algorithm_id = ?", "algo_fire_01").Count(&count)
	if count != 0 {
		t.Errorf("expected 0 task_algorithms remaining, got: %d", count)
	}
}

func TestNewAlgorithmComponentsDoesNotCreateInstallDirectory(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "not-created-yet")
	host := algorithm.DetectHostPlatform("rknn")
	if comp := algorithm.NewAlgorithmComponents(nil, algorunner.Runtime{AlgorithmsDir: filepath.Join(dataDir, "algorithms")}, host, zap.NewNop()); comp.Verifier.HostPlatform().Target != "rknn" {
		t.Fatalf("expected configured host target to reach verifier, got %q", comp.Verifier.HostPlatform().Target)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "algorithms")); !os.IsNotExist(err) {
		t.Fatalf("expected constructor to leave install directory absent, stat error = %v", err)
	}
}

func TestServiceErrorApperrClassifications(t *testing.T) {
	comp, _, _ := setupServiceTest(t)
	ctx := context.Background()

	// 1. GetAlgorithm not found -> apperr.KindNotFound
	_, err := comp.Service.GetAlgorithm(ctx, "non_existent_id")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound || appErr.Code != "ALGORITHM_NOT_FOUND" {
		t.Fatalf("expected ALGORITHM_NOT_FOUND apperr on GetAlgorithm, got: %v", err)
	}

	// 2. DeleteAlgorithm not found -> apperr.KindNotFound
	err = comp.Service.DeleteAlgorithm(ctx, "non_existent_id")
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound || appErr.Code != "ALGORITHM_NOT_FOUND" {
		t.Fatalf("expected ALGORITHM_NOT_FOUND apperr on DeleteAlgorithm, got: %v", err)
	}

	// 3. ResetAlgorithm not in crash loop -> apperr.KindConflict
	algo := &algorithm.Algorithm{
		ID: "algo_reset", Name: "reset_test", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		TargetArch: "x86_64", TargetDevice: "onnx", ComputeCost: 10, MemoryMB: 64,
		Status: algorithm.StatusActive,
	}
	if err := comp.Store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}
	err = comp.Service.ResetAlgorithm(ctx, algo.ID)
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindConflict || appErr.Code != "ALGORITHM_NOT_IN_CRASH_LOOP" {
		t.Fatalf("expected ALGORITHM_NOT_IN_CRASH_LOOP apperr on ResetAlgorithm, got: %v", err)
	}
}

func TestServiceDeleteStopsRestartDuringCrashBackoff(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)
	algo := &algorithm.Algorithm{
		ID: "supervisor_test", Name: "supervisor_test", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		TargetArch: "x86_64", TargetDevice: "onnx", ComputeCost: 1,
		MemoryMB: 64, Status: algorithm.StatusDisabled,
	}
	if err := store.CreateAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	counterPath := filepath.Join(t.TempDir(), "runner-count")
	t.Setenv("RUN_COUNT_FILE", counterPath)
	// 每次启动都记一笔并退出 1 的包入口：崩溃/退避行为要能在没有原生 runner 的情况下观察
	script := "#!/bin/sh\nprintf 'run\\n' >> \"$RUN_COUNT_FILE\"\nexit 1\n"
	installStubPackage(t, filepath.Join(dataDir, "algorithms"), algo.ID, algo.Version, script)

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 250 * time.Millisecond
	options.MaxBackoff = time.Second
	supervisor := algorunner.NewSupervisor(
		store,
		algorunner.Runtime{AlgorithmsDir: filepath.Join(dataDir, "algorithms")},
		options,
		zap.NewNop(),
	)
	service := algorithm.NewService(
		store,
		nil,
		supervisor,
		algorunner.Runtime{AlgorithmsDir: filepath.Join(dataDir, "algorithms")},
		zap.NewNop(),
	)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo.ToPackageMeta()); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForStatus := func(id, want string) {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if supervisor.GetStatus(id) == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("status did not reach %s; got %s", want, supervisor.GetStatus(id))
	}
	waitForStatus(algo.ID, algorithm.StatusCrashed)

	if err := service.DeleteAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("delete crashed algorithm: %v", err)
	}
	time.Sleep(350 * time.Millisecond)

	countBytes, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("read runner count: %v", err)
	}
	if count := strings.Count(string(countBytes), "run\n"); count != 1 {
		t.Fatalf("expected deleted algorithm not to restart, runner executed %d times", count)
	}
}

type staticBrokerResolver struct {
	paths []string
}

func (r *staticBrokerResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	return r.paths, nil
}

func TestEnableAlgorithmWithZeroTasksEntersStandby(t *testing.T) {
	comp, _, _ := setupServiceTest(t)
	ctx := context.Background()

	algo := &algorithm.Algorithm{
		ID:           "algo_standby_01",
		Name:         "standby_test",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategoryGeneral,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusDisabled,
	}
	if err := comp.Store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	comp.Supervisor.SetBrokerSubscriptionResolver(&staticBrokerResolver{paths: []string{}})

	// 1. First enable with 0 tasks -> should succeed in standby (not error or crash)
	if err := comp.Service.EnableAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("enable algorithm in standby failed: %v", err)
	}

	found, err := comp.Store.GetAlgorithm(ctx, algo.ID)
	if err != nil {
		t.Fatalf("get algorithm: %v", err)
	}
	if found.Status != algorithm.StatusActive {
		t.Fatalf("expected DB status to be ACTIVE, got %s", found.Status)
	}
	if live := comp.Supervisor.GetStatus(algo.ID); live != algorithm.StatusDisabled {
		t.Fatalf("expected supervisor to have no watcher (standby), got %s", live)
	}

	// 2. Calling Enable again should be idempotent
	if err := comp.Service.EnableAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("idempotent enable failed: %v", err)
	}
}

func TestEnableAlgorithmInCrashLoopReturnsConflict(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)

	algo := &algorithm.Algorithm{
		ID: "algo_crash_loop", Name: "crash_loop_test", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategoryGeneral,
		TargetArch: "x86_64", TargetDevice: "onnx", ComputeCost: 10,
		MemoryMB: 64, Status: algorithm.StatusDisabled,
	}
	if err := store.CreateAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	installStubPackage(t, dataDir, algo.ID, algo.Version, "#!/bin/sh\nexit 1\n")

	opts := algorunner.DefaultRecoveryOptions()
	opts.MaxCrashes = 1
	opts.InitialBackoff = 5 * time.Millisecond
	opts.CrashWindow = time.Second
	supervisor := algorunner.NewSupervisor(
		store,
		algorunner.Runtime{AlgorithmsDir: dataDir},
		opts,
		zap.NewNop(),
	)
	service := algorithm.NewService(
		store,
		nil,
		supervisor,
		algorunner.Runtime{AlgorithmsDir: dataDir},
		zap.NewNop(),
	)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
		if err := dbStore.Close(); err != nil {
			t.Logf("cleanup dbStore close: %v", err)
		}
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo.ToPackageMeta()); err != nil {
		t.Logf("StartAlgorithm returned error: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if supervisor.GetStatus(algo.ID) == algorithm.StatusCrashLoop {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusCrashLoop {
		t.Fatalf("expected CRASH_LOOP, got %s", got)
	}

	err := service.EnableAlgorithm(ctx, algo.ID)
	if err == nil {
		t.Fatal("expected EnableAlgorithm on CRASH_LOOP to return error")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperr.Error, got %T (%v)", err, err)
	}
	if appErr.Code != "ALGORITHM_IN_CRASH_LOOP" {
		t.Fatalf("expected code ALGORITHM_IN_CRASH_LOOP, got %s", appErr.Code)
	}
	if appErr.Kind != apperr.KindConflict {
		t.Fatalf("expected KindConflict, got %v", appErr.Kind)
	}
}

func createInstallablePackageArchive(t *testing.T, id, version string) string {
	t.Helper()
	manifest := algorithm.Manifest{
		ID:        id,
		Version:   version,
		Name:      id,
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return createTestTarGz(t, map[string][]byte{
		"manifest.json":      manifestBytes,
		id:                   installableEntry(1),
		"sample.jpg":         []byte("sample image bytes"),
		"config.schema.json": []byte(`{"type":"object"}`),
	})
}

func TestInstallAlgorithmUpgradePreservesStatusAndCreatedAt(t *testing.T) {
	tests := []struct {
		name           string
		initialStatus  string
		brokerPaths    []string
		nilSupervisor  bool
		wantDBStatus   string
		wantLiveStatus string
	}{
		{
			name:           "active running stays active and transitions runner",
			initialStatus:  algorithm.StatusActive,
			brokerPaths:    []string{"/tmp/zhulong_broker_upgrade_active.sock"},
			wantDBStatus:   algorithm.StatusActive,
			wantLiveStatus: algorithm.StatusRunning,
		},
		{
			name:           "active standby stays active without starting runner",
			initialStatus:  algorithm.StatusActive,
			brokerPaths:    []string{},
			wantDBStatus:   algorithm.StatusActive,
			wantLiveStatus: algorithm.StatusDisabled,
		},
		{
			name:           "disabled stays disabled",
			initialStatus:  algorithm.StatusDisabled,
			brokerPaths:    []string{"/tmp/zhulong_broker_upgrade_disabled.sock"},
			wantDBStatus:   algorithm.StatusDisabled,
			wantLiveStatus: algorithm.StatusDisabled,
		},
		{
			name:           "crash_loop resets to disabled on upgrade",
			initialStatus:  algorithm.StatusCrashLoop,
			brokerPaths:    []string{"/tmp/zhulong_broker_upgrade_crashloop.sock"},
			wantDBStatus:   algorithm.StatusDisabled,
			wantLiveStatus: algorithm.StatusDisabled,
		},
		{
			name:          "active with nil supervisor stays active without panic",
			initialStatus: algorithm.StatusActive,
			nilSupervisor: true,
			wantDBStatus:  algorithm.StatusActive,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			comp, _, tempDir := setupServiceTest(t)
			t.Cleanup(func() {
				comp.Supervisor.StopAll(time.Second)
			})
			ctx := context.Background()
			const algoID = "upgrade_status_algo"

			svc := comp.Service
			if tc.nilSupervisor {
				svc = algorithm.NewService(
					comp.Store,
					comp.Verifier,
					nil,
					algorunner.Runtime{AlgorithmsDir: tempDir},
					zap.NewNop(),
				)
			} else {
				comp.Supervisor.SetBrokerSubscriptionResolver(&staticBrokerResolver{paths: tc.brokerPaths})
			}

			// 1. 安装 1.0.0，固定早期 CreatedAt 并切到测试所需的初始状态
			v1Archive := createInstallablePackageArchive(t, algoID, "1.0.0")
			defer os.Remove(v1Archive)
			v1, err := svc.InstallAlgorithm(ctx, v1Archive)
			if err != nil {
				t.Fatalf("install v1.0.0: %v", err)
			}

			origCreatedAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
			v1.CreatedAt = origCreatedAt
			v1.Status = tc.initialStatus
			if err := comp.Store.UpdateAlgorithm(ctx, v1); err != nil {
				t.Fatalf("seed v1 initial state: %v", err)
			}
			if !tc.nilSupervisor && tc.initialStatus == algorithm.StatusActive && len(tc.brokerPaths) > 0 {
				if err := comp.Supervisor.StartAlgorithm(ctx, v1.ToPackageMeta()); err != nil {
					t.Fatalf("start v1 runner: %v", err)
				}
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && comp.Supervisor.GetStatus(algoID) != algorithm.StatusRunning {
					time.Sleep(20 * time.Millisecond)
				}
				if got := comp.Supervisor.GetStatus(algoID); got != algorithm.StatusRunning {
					t.Fatalf("v1 runner did not reach RUNNING, got %s", got)
				}
			}

			// 2. 升级到 1.1.0
			v2Archive := createInstallablePackageArchive(t, algoID, "1.1.0")
			defer os.Remove(v2Archive)
			upgraded, err := svc.InstallAlgorithm(ctx, v2Archive)
			if err != nil {
				t.Fatalf("upgrade to v1.1.0: %v", err)
			}

			if upgraded.Version != "1.1.0" || upgraded.PreviousVersion != "1.0.0" {
				t.Fatalf("unexpected versions: version=%q previous=%q", upgraded.Version, upgraded.PreviousVersion)
			}
			if upgraded.Status != tc.wantDBStatus {
				t.Fatalf("returned Status = %q, want %q", upgraded.Status, tc.wantDBStatus)
			}
			if !upgraded.CreatedAt.Equal(origCreatedAt) {
				t.Fatalf("returned CreatedAt = %v, want preserved %v", upgraded.CreatedAt, origCreatedAt)
			}

			stored, err := comp.Store.GetAlgorithm(ctx, algoID)
			if err != nil {
				t.Fatalf("reload upgraded algorithm from DB: %v", err)
			}
			if stored.Status != tc.wantDBStatus {
				t.Fatalf("stored DB Status = %q, want %q", stored.Status, tc.wantDBStatus)
			}
			if !stored.CreatedAt.Equal(origCreatedAt) {
				t.Fatalf("stored DB CreatedAt = %v, want preserved %v", stored.CreatedAt, origCreatedAt)
			}

			if !tc.nilSupervisor {
				if tc.wantLiveStatus == algorithm.StatusRunning {
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) && comp.Supervisor.GetStatus(algoID) != algorithm.StatusRunning {
						time.Sleep(20 * time.Millisecond)
					}
				}
				if got := comp.Supervisor.GetStatus(algoID); got != tc.wantLiveStatus {
					t.Fatalf("supervisor live status = %q, want %q", got, tc.wantLiveStatus)
				}
			}
		})
	}
}
