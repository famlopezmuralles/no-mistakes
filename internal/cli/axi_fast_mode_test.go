package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestDriveRunYesLeavesFastModeGateBlocked(t *testing.T) {
	for _, findings := range []string{
		`{"findings":[{"id":"failure","severity":"error","description":"command failed","action":"auto-fix"}]}`,
		`{"findings":[],"summary":"required test command missing"}`,
	} {
		t.Run(findings, func(t *testing.T) {
			parked := &ipc.RunInfo{
				ID: "run-fast", Status: types.RunRunning, FastMode: true,
				Steps: []ipc.StepResultInfo{{StepName: types.StepTest, Status: types.StepStatusAwaitingApproval, FindingsJSON: &findings}},
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
			// A nil client is deliberate: no response transport is needed or
			// permitted when --yes reaches a fast-mode gate.
			run, ready, err := driveRunWithReconciler(ctx, &progress, nil, reconciler, parked.ID, true)
			if err != nil || run != parked || ready {
				t.Fatalf("drive result = %+v ready=%v err=%v", run, ready, err)
			}
			if !strings.Contains(progress.String(), "fast_mode") {
				t.Fatalf("missing policy explanation: %s", progress.String())
			}
		})
	}
}

func TestFastModeGateHelpDoesNotOfferRejectedResponses(t *testing.T) {
	gate := stepView{
		Name:         string(types.StepTest),
		Status:       string(types.StepStatusAwaitingApproval),
		FindingsJSON: `{"findings":[{"id":"failed","severity":"error","action":"ask-user","description":"configured test failed"}]}`,
	}
	for _, inspectionOnly := range []bool{false, true} {
		out := axiDoc((runView{FastMode: true}).gateFields(gate, "run-fast", inspectionOnly)...)
		if strings.Contains(out, "--action approve") || strings.Contains(out, "--action fix") {
			t.Fatalf("fast-mode help offers a response its policy rejects:\n%s", out)
		}
		if inspectionOnly && strings.Contains(out, "--action skip") {
			t.Fatalf("inspection-only gate offered a mutation:\n%s", out)
		}
	}
}
