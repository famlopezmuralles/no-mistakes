package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestDriveRunYesLeavesFastModeGateBlocked(t *testing.T) {
	for _, tc := range []struct {
		step     types.StepName
		findings string
	}{
		{types.StepTest, `{"findings":[{"id":"failure","severity":"error","description":"command failed","action":"auto-fix"}]}`},
		{types.StepTest, `{"findings":[],"summary":"required test command missing"}`},
		{types.StepReview, `{"findings":[{"id":"defect","severity":"error","description":"zero violates the public contract","action":"auto-fix"}]}`},
		{types.StepReview, `{"findings":[],"reviewed_paths":[],"summary":"review coverage is incomplete"}`},
	} {
		t.Run(string(tc.step)+tc.findings, func(t *testing.T) {
			parked := &ipc.RunInfo{
				ID: "run-fast", Status: types.RunRunning, FastMode: true,
				Steps: []ipc.StepResultInfo{{StepName: tc.step, Status: types.StepStatusAwaitingApproval, FindingsJSON: &tc.findings}},
			}
			source := &scriptedRunStateSource{
				subscriptions: []scriptedSubscription{{events: make(chan ipc.Event)}},
				runs:          []*ipc.RunInfo{parked},
			}
			reconciler := newRunReconciler(source, parked.ID)
			defer reconciler.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var progress bytes.Buffer
			// A nil client is deliberate: no response transport is permitted.
			run, ready, err := driveRunWithReconciler(ctx, &progress, nil, reconciler, parked.ID, true)
			if err != nil || ready || run.Status != types.RunRunning || run.Steps[0].Status != types.StepStatusAwaitingApproval {
				t.Fatalf("drive result = %+v ready=%v err=%v", run, ready, err)
			}
		})
	}
}
