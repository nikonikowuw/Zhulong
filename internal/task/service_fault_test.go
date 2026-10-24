package task_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestServiceUpdateTaskPropagatesWriteFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_fault", "fault_detector", 10)
	seeded := seedTask(t, store, "cam_update_fault", "algo_fault")

	svc := task.NewService(store, nil, nil, zap.NewNop())

	fault := errors.New("injected: update failure")
	injectFault(t, db, faultUpdate, 1, fault)

	_, err := svc.UpdateTask(ctx, seeded.ID, &task.UpdateTaskRequest{Name: "renamed"})
	assertAppErrCode(t, err, "TASK_UPDATE_FAILED")
	if !errors.Is(err, fault) {
		t.Fatalf("expected the cause to stay in the error chain, got %v", err)
	}
}

func TestServiceDeleteTaskPropagatesWriteFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := task.NewService(store, nil, nil, zap.NewNop())

	t.Run("driver failure", func(t *testing.T) {
		seeded := seedTask(t, store, "cam_delete_fail")

		fault := errors.New("injected: delete failure")
		injectFault(t, db, faultDelete, 1, fault)

		err := svc.DeleteTask(ctx, seeded.ID)
		assertAppErrCode(t, err, "TASK_DELETE_FAILED")
		if !errors.Is(err, fault) {
			t.Fatalf("expected the cause to stay in the error chain, got %v", err)
		}
	})

	t.Run("row vanished between read and delete", func(t *testing.T) {
		seeded := seedTask(t, store, "cam_delete_race")

		// DELETE 变成空操作：读取成功但删除影响 0 行，必须映射成 404 而不是 500。
		neutralizeDeletes(t, db)
		assertAppErrCode(t, svc.DeleteTask(ctx, seeded.ID), "TASK_NOT_FOUND")
	})
}

func TestHandlerListTasksReportsStoreFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	svc := task.NewService(store, nil, nil, zap.NewNop())
	handler := task.NewHandler(svc)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	closeUnderlyingDB(t, db)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	var body httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data != nil {
		t.Fatalf("expected null data on error, got %v", body.Data)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("database is closed")) {
		t.Fatal("the raw driver error must not reach the client")
	}
}

// closeUnderlyingDB 关掉底层连接池，模拟"数据库已关闭但进程仍在服务"的运行期故障。
func closeUnderlyingDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
}
