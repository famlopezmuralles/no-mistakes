package pipeline

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestExecutorFastModeRefusesAutomaticAndManualRepair(t *testing.T) {
	for _, name := range []types.StepName{types.StepTest, types.StepLint, types.StepCI, types.StepRebase} {
		t.Run(string(name), func(t *testing.T) {
			database, p, run, repo := setupTest(t)
			var calls, fixes atomic.Int32
			check := &adaptiveCallStep{name: name, fn: func(sctx *StepContext) (*StepOutcome, error) {
				calls.Add(1)
				if sctx.Fixing {
					fixes.Add(1)
				}
				return &StepOutcome{
					ExitCode: 1, NeedsApproval: true, AutoFixable: true,
					Findings: `{"findings":[{"id":"failed","severity":"error","description":"configured command failed","action":"auto-fix"}]}`,
				}, nil
			}}
			cfg := &config.Config{FastMode: true, AutoFix: config.AutoFix{Test: 5, Lint: 5, CI: 5, Rebase: 5}}
			executor := NewExecutor(database, p, cfg, nil, []Step{check}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			workDir := t.TempDir()
			go func() { done <- executor.Execute(ctx, run, repo, workDir) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("executor did not stop")
				}
			}()
			waitForStepStatus(t, database, run.ID, name, types.StepStatusAwaitingApproval)
			for _, action := range []types.ApprovalAction{types.ActionFix, types.ActionApprove} {
				if err := executor.Respond(name, action, []string{"failed"}); err == nil || !strings.Contains(err.Error(), "fast_mode") {
					t.Fatalf("%s response error = %v, want fast mode refusal", action, err)
				}
			}
			if calls.Load() != 1 || fixes.Load() != 0 {
				t.Fatalf("check calls=%d repair calls=%d", calls.Load(), fixes.Load())
			}
			records, err := database.GetStepsByRun(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].Status != types.StepStatusAwaitingApproval {
				t.Fatalf("refused response changed parked state: %+v", records)
			}
			if err := executor.Respond(name, types.ActionAbort, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExecutorFastModeRefusesApprovalOfInconclusiveNoopGate(t *testing.T) {
	executor := &Executor{config: &config.Config{FastMode: true}, waiting: true, waitingStep: types.StepTest}
	executor.waitingApprovalRefusal = executor.gateApprovalRefusal(types.StepTest, `{"findings":[]}`)
	if err := executor.Respond(types.StepTest, types.ActionApprove, nil); err == nil {
		t.Fatal("inconclusive check was approved")
	}
	if !executor.waiting {
		t.Fatal("refused approval released the gate")
	}
}
