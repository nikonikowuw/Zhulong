package algorithm_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"gorm.io/gorm"
)

// mustExec 执行一条 SQL，失败即终止测试。
func mustExec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// setupRouterWithDB 在 setupTestRouter 的基础上额外返回 *gorm.DB，方便
// 测试直接播种数据（Service 刻意不暴露 Store）。
func setupRouterWithDB(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	router, _, db := newAlgorithmTestApp(t)
	return router, db
}

// TestDeletionImpactReportsBindingsAndEvidence 验证影响面统计的每一项。
//
// 这是「卸载前提示」的数据来源：用户点卸载时前端先拿这份数据，才能知道
// 会停掉几路任务、多少条证据会失去算法归属。
func TestDeletionImpactReportsBindingsAndEvidence(t *testing.T) {
	router, db := setupRouterWithDB(t)

	const algoID = "algo_impact"
	// 1. 算法 + 两台相机 + 两个任务绑定
	mustExec(t, db, `INSERT INTO algorithms (id, name, version, task_type, category,
		target_arch, target_device, compute_cost, memory_mb, description, manifest_json,
		config_schema_json, status, previous_version, created_at, updated_at)
		VALUES (?, '安全帽检测', '2.1.0', 'detection', 'safety', 'aarch64', 'rknn',
		10, 64, '', '{}', '{}', 'DISABLED', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, algoID)
	mustExec(t, db, `INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
		VALUES ('cam_a', 'A', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
		VALUES ('cam_b', 'B', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO analysis_tasks (id, camera_id, enabled, name, created_at, updated_at)
		VALUES ('task_a', 'cam_a', 1, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO analysis_tasks (id, camera_id, enabled, name, created_at, updated_at)
		VALUES ('task_b', 'cam_b', 1, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps,
		config_json, region_config_json, created_at, updated_at)
		VALUES ('ta_a', 'task_a', ?, 1, 5, '{}', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, algoID)
	mustExec(t, db, `INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps,
		config_json, region_config_json, created_at, updated_at)
		VALUES ('ta_b', 'task_b', ?, 1, 5, '{}', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, algoID)

	// 2. 历史证据：2 条识别 / 3 条告警 / 1 条抓拍
	for i, id := range []string{"r1", "r2"} {
		mustExec(t, db, `INSERT INTO recognitions (id, camera_id, algorithm_id, algorithm_name,
			algorithm_version, pts, captured_at, created_at)
			VALUES (?, 'cam_a', ?, '安全帽检测', '2.1.0', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			id, algoID, i+1)
	}
	for i, id := range []string{"a1", "a2", "a3"} {
		mustExec(t, db, `INSERT INTO alarms (id, camera_id, algorithm_id, algorithm_name,
			algorithm_version, alarm_type, pts, captured_at, created_at)
			VALUES (?, 'cam_a', ?, '安全帽检测', '2.1.0', 'no_helmet', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			id, algoID, i+1)
	}
	mustExec(t, db, `INSERT INTO captures (id, camera_id, algorithm_id, algorithm_name,
		algorithm_version, target_type, pts, captured_at, created_at)
		VALUES ('c1', 'cam_a', ?, '安全帽检测', '2.1.0', 'person', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		algoID)

	// 3. 调用影响面接口
	req := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/"+algoID+"/deletion-impact", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Code string                   `json:"code"`
		Data algorithm.DeletionImpact `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope.Code != "OK" {
		t.Fatalf("expected OK envelope, got %q", envelope.Code)
	}

	impact := envelope.Data
	if impact.AlgorithmName != "安全帽检测" || impact.Version != "2.1.0" {
		t.Errorf("identity mismatch: name=%q version=%q", impact.AlgorithmName, impact.Version)
	}
	if impact.BoundTaskCount != 2 {
		t.Errorf("expected 2 bound tasks, got %d", impact.BoundTaskCount)
	}
	// 相机 ID 必须按字典序稳定输出：前端会直接 join 展示，顺序乱跳很难读。
	if len(impact.AffectedCameraIDs) != 2 ||
		impact.AffectedCameraIDs[0] != "cam_a" || impact.AffectedCameraIDs[1] != "cam_b" {
		t.Errorf("unexpected affected cameras: %v", impact.AffectedCameraIDs)
	}
	if impact.RecognitionCount != 2 || impact.AlarmCount != 3 || impact.CaptureCount != 1 {
		t.Errorf("evidence counts mismatch: rec=%d alarm=%d cap=%d",
			impact.RecognitionCount, impact.AlarmCount, impact.CaptureCount)
	}
	if !impact.HasEvidence() {
		t.Error("HasEvidence must be true when records exist")
	}
	// DISABLED 状态不需要停机
	if impact.RequiresStop {
		t.Error("disabled algorithm must not require a stop")
	}

	// 4. 关键断言：这个接口是只读的，不能有任何副作用。
	var remaining int64
	db.Table("algorithms").Where("id = ?", algoID).Count(&remaining)
	if remaining != 1 {
		t.Errorf("impact query must not delete the algorithm, %d rows left", remaining)
	}
	db.Table("task_algorithms").Where("algorithm_id = ?", algoID).Count(&remaining)
	if remaining != 2 {
		t.Errorf("impact query must not detach tasks, %d bindings left", remaining)
	}
	db.Table("recognitions").Where("algorithm_id = ?", algoID).Count(&remaining)
	if remaining != 2 {
		t.Errorf("impact query must not touch evidence, %d recognitions left", remaining)
	}
}

// TestDeletionImpactNotFound 验证未知算法的错误语义。
func TestDeletionImpactNotFound(t *testing.T) {
	router, _ := setupRouterWithDB(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/no_such/deletion-impact", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDeletionImpactZeroWhenClean 验证从未产生证据的算法返回全零而不是错误。
func TestDeletionImpactZeroWhenClean(t *testing.T) {
	router, db := setupRouterWithDB(t)

	mustExec(t, db, `INSERT INTO algorithms (id, name, version, task_type, category,
		target_arch, target_device, compute_cost, memory_mb, description, manifest_json,
		config_schema_json, status, previous_version, created_at, updated_at)
		VALUES ('algo_fresh', '新算法', '1.0.0', 'detection', 'general', 'aarch64', 'rknn',
		10, 64, '', '{}', '{}', 'DISABLED', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/algo_fresh/deletion-impact", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var envelope struct {
		Data algorithm.DeletionImpact `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope.Data.BoundTaskCount != 0 || envelope.Data.HasEvidence() {
		t.Errorf("expected a clean impact, got %+v", envelope.Data)
	}
	// 空数组而不是 null：前端 `impact.affectedCameraIds.join()` 遇到 null 会崩。
	if envelope.Data.AffectedCameraIDs == nil {
		t.Error("affectedCameraIds must be an empty array, not null")
	}
}

// TestDeleteAlgorithmKeepsEvidenceAfterImpact 串起「预览 → 执行」，确认提示
// 与真实后果一致：任务解绑、证据保留并保留快照。
func TestDeleteAlgorithmKeepsEvidenceAfterImpact(t *testing.T) {
	router, db := setupRouterWithDB(t)

	const algoID = "algo_del"
	mustExec(t, db, `INSERT INTO algorithms (id, name, version, task_type, category,
		target_arch, target_device, compute_cost, memory_mb, description, manifest_json,
		config_schema_json, status, previous_version, created_at, updated_at)
		VALUES (?, '入侵检测', '1.2.3', 'detection', 'security', 'aarch64', 'rknn',
		10, 64, '', '{}', '{}', 'DISABLED', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, algoID)
	mustExec(t, db, `INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
		VALUES ('cam_x', 'X', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO analysis_tasks (id, camera_id, enabled, name, created_at, updated_at)
		VALUES ('task_x', 'cam_x', 1, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps,
		config_json, region_config_json, created_at, updated_at)
		VALUES ('ta_x', 'task_x', ?, 1, 5, '{}', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, algoID)
	mustExec(t, db, `INSERT INTO alarms (id, camera_id, algorithm_id, algorithm_name,
		algorithm_version, alarm_type, pts, captured_at, created_at)
		VALUES ('alm_x', 'cam_x', ?, '入侵检测', '1.2.3', 'intrusion', 1,
		CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, algoID)

	// 执行卸载
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/algorithms/"+algoID, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
	}

	// 任务绑定被解除（这是提示里说的「2 路任务会失去该算法」）
	var bindings int64
	db.Table("task_algorithms").Where("algorithm_id = ?", algoID).Count(&bindings)
	if bindings != 0 {
		t.Errorf("expected task bindings removed, %d left", bindings)
	}
	// 但主任务本身保留（其他算法不受影响）
	var tasks int64
	db.Table("analysis_tasks").Where("id = ?", "task_x").Count(&tasks)
	if tasks != 1 {
		t.Errorf("analysis task must survive algorithm uninstall, %d left", tasks)
	}

	// 证据保留，引用置空，快照仍在 —— 与提示文案「保留但失去归属」一致
	var alarm struct {
		AlgorithmID      *string
		AlgorithmName    string
		AlgorithmVersion string
	}
	if err := db.Table("alarms").Select("algorithm_id, algorithm_name, algorithm_version").
		Where("id = ?", "alm_x").Scan(&alarm).Error; err != nil {
		t.Fatalf("load alarm: %v", err)
	}
	if alarm.AlgorithmID != nil {
		t.Errorf("algorithm_id must be NULL after uninstall, got %q", *alarm.AlgorithmID)
	}
	if alarm.AlgorithmName != "入侵检测" || alarm.AlgorithmVersion != "1.2.3" {
		t.Errorf("snapshot lost: name=%q version=%q", alarm.AlgorithmName, alarm.AlgorithmVersion)
	}

}
