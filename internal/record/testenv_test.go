package record_test

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// 本文件是 record 域测试的共享夹具：真实 SQLite（跑完整迁移）、预置相机/算法/
// 人员这些被外键引用到的父行。
//
// 之前 store_test.go 与 handler_test.go 各写一份等价的开库 + 裸 SQL 播种，
// 结果改一次 schema 要同步两处，且两处会渐渐跑偏。

const insertCameraSQL = `
INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
VALUES (?, ?, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`

// insertAlgorithmSQL 插一行最小的在册算法：recognitions/captures/alarms 的
// algorithm_id 是外键，未卸载时必须有父行。
const insertAlgorithmSQL = `
INSERT INTO algorithms (
	id, name, version, task_type, category, target_arch, target_device,
	compute_cost, memory_mb, description, manifest_json, config_schema_json,
	status, previous_version, created_at, updated_at
) VALUES (
	?, ?, '1.0.0', 'person_structure', 'face', 'x86_64', 'cpu',
	10, 64, 'test', '{}', '{}', 'ENABLED', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`

// setupTestStore 打开一个跑完迁移的临时库并预置外键父行。
//
// 返回的 cleanup 关闭数据库；调用方用 defer cleanup()。
func setupTestStore(t *testing.T) (*record.Store, *database.Store, func()) {
	t.Helper()

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	db := dbStore.DB()

	for _, camera := range []struct{ id, name string }{
		{"cam_01", "Test Camera 1"},
		{"cam_02", "Test Camera 2"},
	} {
		if err := db.Exec(insertCameraSQL, camera.id, camera.name).Error; err != nil {
			t.Fatalf("insert camera %s: %v", camera.id, err)
		}
	}

	if err := db.Exec(insertAlgorithmSQL, "algo_01", "FaceReID").Error; err != nil {
		t.Fatalf("insert algorithm: %v", err)
	}

	if err := db.Exec(`INSERT INTO persons (id, name, created_at, updated_at)
		VALUES ('person_alice', 'Alice', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert person: %v", err)
	}

	cleanup := func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}
	return record.NewStore(dbStore), dbStore, cleanup
}

// strPtr / boolPtr 是可选列的构造器：两者在测试里用来表达「这一列有值」。
func strPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}
