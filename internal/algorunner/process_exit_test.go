package algorunner

import (
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
)

// TestExitFailureStagesHaveDiagnoses pins the two tables together.
//
// The failure stage comes from two channels (the runner's stderr marker and its
// exit code) and is logged through a third table. A code added to the first table
// without a row in the second would be recorded but reported as an "unexpected
// exit" — the exact misclassification that hides "broker unavailable" behind "the
// process exited".
func TestExitFailureStagesHaveDiagnoses(t *testing.T) {
	if len(exitFailureStages) == 0 {
		t.Fatal("exitFailureStages is empty: the secondary failure channel is gone")
	}
	for code, stage := range exitFailureStages {
		if _, ok := failureStageDiagnoses[stage]; !ok {
			t.Fatalf("exit code %d maps to stage %q, which has no diagnosis row", code, stage)
		}
	}
}

// TestFailureStageDiagnosesCoverSkeletonStages pins the diagnosis table to the
// stages the package skeleton actually emits, so a new stage cannot be accepted by
// the log allowlist and then have nowhere to be reported from.
func TestFailureStageDiagnosesCoverSkeletonStages(t *testing.T) {
	skeletonStages := []string{
		algoipc.FailureStageBrokerConnect,
		algoipc.FailureStageWorkerException,
		algoipc.FailureStageWorkerStalled,
	}
	for _, stage := range skeletonStages {
		if _, ok := failureStageDiagnoses[stage]; !ok {
			t.Fatalf("skeleton failure stage %q has no diagnosis row", stage)
		}
		if !validRunnerStage(stage) {
			t.Fatalf("skeleton failure stage %q is rejected by the runner log allowlist", stage)
		}
	}
}
