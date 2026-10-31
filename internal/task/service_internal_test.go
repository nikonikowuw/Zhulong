package task

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// 错误映射是纯函数：拿合成 error 直接验，不需要数据库，也不需要 mock ORM。
func TestMapWriteError(t *testing.T) {
	cause := errors.New("sqlite: disk I/O error")

	tests := []struct {
		name     string
		err      error
		wantCode string
		wantKind apperr.Kind
	}{
		{
			name:     "compute budget",
			err:      fmt.Errorf("wrapped: %w", ErrComputeBudgetExceeded),
			wantCode: "COMPUTE_BUDGET_EXCEEDED",
			wantKind: apperr.KindConflict,
		},
		{
			name:     "camera already taken",
			err:      ErrTaskAlreadyExists,
			wantCode: "TASK_ALREADY_EXISTS",
			wantKind: apperr.KindConflict,
		},
		{
			name:     "missing algorithm",
			err:      ErrNotFound,
			wantCode: "ALGORITHM_NOT_FOUND",
			wantKind: apperr.KindNotFound,
		},
		{
			name:     "unknown store failure falls back",
			err:      cause,
			wantCode: "TASK_SAVE_FAILED",
			wantKind: apperr.KindInternal,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := mapWriteError(test.err, "TASK_SAVE_FAILED", "failed to save analysis task")

			var appErr *apperr.Error
			if !errors.As(mapped, &appErr) {
				t.Fatalf("expected *apperr.Error, got %T", mapped)
			}
			if appErr.Code != test.wantCode || appErr.Kind != test.wantKind {
				t.Fatalf(
					"expected %v/%s, got %v/%s",
					test.wantKind, test.wantCode, appErr.Kind, appErr.Code,
				)
			}
			// 底层 cause 必须留在错误链上供服务端日志使用。
			if !errors.Is(mapped, test.err) {
				t.Errorf("expected the mapped error to wrap %v", test.err)
			}
		})
	}
}

func TestMapTaskError(t *testing.T) {
	cause := errors.New("sql: database is closed")

	notFound := mapTaskError(ErrNotFound)
	var appErr *apperr.Error
	if !errors.As(notFound, &appErr) || appErr.Code != "TASK_NOT_FOUND" {
		t.Fatalf("expected TASK_NOT_FOUND, got %v", notFound)
	}

	internalErr := mapTaskError(cause)
	if !errors.As(internalErr, &appErr) || appErr.Code != "INTERNAL_ERROR" {
		t.Fatalf("expected INTERNAL_ERROR, got %v", internalErr)
	}
	if !errors.Is(internalErr, cause) {
		t.Error("expected the internal error to wrap the cause")
	}
}

// recordingListener 记录广播到的算法 ID。
type recordingListener struct {
	ids []string
}

func (l *recordingListener) OnTaskChanged(_ context.Context, algorithmID string) {
	l.ids = append(l.ids, algorithmID)
}

// TestNotifyTasksChangedFiltersEmptyAndDuplicateIDs 钉住广播契约：空 ID 不外发，重复 ID 只广播一次。
func TestNotifyTasksChangedFiltersEmptyAndDuplicateIDs(t *testing.T) {
	listener := &recordingListener{}
	svc := NewService(
		nil,
		nil,
		[]TaskChangeListener{listener, nil},
		nil,
	)

	svc.notifyTasksChanged(context.Background(), []string{"algo_a", "", "algo_a", "algo_b", ""})

	want := []string{"algo_a", "algo_b"}
	if len(listener.ids) != len(want) {
		t.Fatalf("expected %v, got %v", want, listener.ids)
	}
	for i := range want {
		if listener.ids[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, listener.ids)
		}
	}
}

func TestCollectAlgorithmIDs(t *testing.T) {
	taskA := &Task{Algorithms: []TaskAlgorithm{{AlgorithmID: "a1"}, {AlgorithmID: "a2"}}}
	taskB := &Task{Algorithms: []TaskAlgorithm{{AlgorithmID: "b1"}}}

	got := collectAlgorithmIDs(taskA, taskB)
	want := []string{"a1", "a2", "b1"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
	if ids := collectAlgorithmIDs(); len(ids) != 0 {
		t.Fatalf("expected no IDs for an empty input, got %v", ids)
	}
}

func TestAddComputePoints(t *testing.T) {
	const maxPoints int64 = 1000

	tests := []struct {
		name        string
		total       int64
		computeCost int
		sampleFPS   int
		wantPoint   int64
		wantErr     bool
	}{
		{name: "normal", total: 100, computeCost: 10, sampleFPS: 5, wantPoint: 150},
		{name: "zero fps costs nothing", total: 100, computeCost: 10, sampleFPS: 0, wantPoint: 100},
		{name: "exactly at the limit", total: 950, computeCost: 10, sampleFPS: 5, wantPoint: 1000},
		{name: "overflows the limit", total: 960, computeCost: 10, sampleFPS: 5, wantErr: true},
		{name: "negative running total", total: -1, computeCost: 10, sampleFPS: 5, wantErr: true},
		{name: "negative compute cost", total: 0, computeCost: -10, sampleFPS: 5, wantErr: true},
		{name: "negative sample fps", total: 0, computeCost: 10, sampleFPS: -5, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := addComputePoints(
				test.total,
				test.computeCost,
				test.sampleFPS,
				maxPoints,
			)
			if test.wantErr {
				if !errors.Is(err, ErrComputeBudgetExceeded) {
					t.Fatalf("expected ErrComputeBudgetExceeded, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.wantPoint {
				t.Fatalf("expected %d points, got %d", test.wantPoint, got)
			}
		})
	}
}
