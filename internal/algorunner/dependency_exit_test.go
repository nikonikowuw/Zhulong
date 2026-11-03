package algorunner

import (
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
)

// TestIsDependencyFailureClassifiesOnlyBrokerLoss pins the boundary of the
// relaxed restart policy: only "the frame broker is gone" is an environment
// condition. A worker exception or a stalled worker is a package defect and must
// keep consuming the crash quota.
func TestIsDependencyFailureClassifiesOnlyBrokerLoss(t *testing.T) {
	cases := []struct {
		stage string
		want  bool
	}{
		{algoipc.FailureStageBrokerConnect, true},
		// mid-run 断连是干净退出（码 0），只有 stage 标记能把它识别成依赖故障。
		{algoipc.FailureStageBrokerLost, true},
		{algoipc.FailureStageWorkerException, false},
		{algoipc.FailureStageWorkerStalled, false},
		{"", false},
		{"something-new", false},
	}
	for _, tc := range cases {
		if got := isDependencyFailure(tc.stage); got != tc.want {
			t.Errorf("isDependencyFailure(%q) = %v, want %v", tc.stage, got, tc.want)
		}
	}
}

// TestDependencyBackoffLadder pins the restart cadence for an unavailable
// dependency: it must grow, stay bounded, and never be so short that a long
// camera outage becomes a tight restart loop.
func TestDependencyBackoffLadder(t *testing.T) {
	const max = 30 * time.Second

	want := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for attempt, expected := range want {
		if got := dependencyBackoff(attempt+1, max); got != expected {
			t.Fatalf("attempt %d: dependencyBackoff = %v, want %v", attempt+1, got, expected)
		}
	}

	// 零值/负的上限不能被当成「立即重启」。
	if got := dependencyBackoff(1, 0); got != 5*time.Second {
		t.Fatalf("dependencyBackoff with unset max = %v, want the 5s base", got)
	}
}
