package daemon

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestFastModeSkipsPreserveOperatorSelection(t *testing.T) {
	requested := []types.StepName{types.StepReview, types.StepPush}
	original := slices.Clone(requested)
	got := fastModeSkips(requested, true)
	if !slices.Equal(got, []types.StepName{types.StepReview, types.StepPush, types.StepDocument}) {
		t.Fatalf("resolved skips = %v", got)
	}
	if !slices.Equal(requested, original) {
		t.Fatal("operator skip slice mutated")
	}
	if !slices.Equal(fastModeSkips(requested, false), requested) {
		t.Fatal("ordinary skip selection changed")
	}
}

func TestFastModeNewRunsPersistDefaultSkips(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(fmt.Sprintf("fast=%t", fast), func(t *testing.T) {
			p, d := startTestDaemonWithSteps(t, func() []pipeline.Step {
				var checks []pipeline.Step
				for _, name := range types.AllSteps() {
					checks = append(checks, &mockPassStep{name: name})
				}
				return checks
			})
			global, err := os.ReadFile(p.ConfigFile())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p.ConfigFile(), append([]byte(fmt.Sprintf("fast_mode: %t\n", fast)), global...), 0o600); err != nil {
				t.Fatal(err)
			}
			_, head := setupTestGitRepo(t, p, d, "fast-policy")
			client, err := ipc.Dial(p.Socket())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var result ipc.PushReceivedResult
			if err := client.Call(ipc.MethodPushReceived, &ipc.PushReceivedParams{
				Gate: p.RepoDir("fast-policy"), Ref: "refs/heads/main",
				Old: "0000000000000000000000000000000000000000", New: head,
				SkipSteps: []types.StepName{types.StepPush},
			}, &result); err != nil {
				t.Fatal(err)
			}
			run := waitForRunTerminalState(t, d, result.RunID)
			if run.FastMode != fast {
				t.Fatalf("persisted fast mode = %v, want %v", run.FastMode, fast)
			}
			records, err := d.GetStepsByRun(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != len(types.AllSteps()) {
				t.Fatalf("step count = %d", len(records))
			}
			for _, record := range records {
				want := types.StepStatusCompleted
				if record.StepName == types.StepPush || fast && (record.StepName == types.StepReview || record.StepName == types.StepDocument) {
					want = types.StepStatusSkipped
				}
				if record.Status != want {
					t.Errorf("%s status = %s, want %s", record.StepName, record.Status, want)
				}
			}
		})
	}
}

func TestFastModeRecoveryUsesAdmissionPolicyNotCurrentProfile(t *testing.T) {
	p := paths.WithRoot(t.TempDir())
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(p.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repo, head := setupTestGitRepo(t, p, d, "fast-recovery")
	mgr := NewRunManager(d, p, nil)
	for _, fast := range []bool{false, true} {
		run, err := d.InsertRun(repo.ID, fmt.Sprintf("fast-%t", fast), head, head)
		if err != nil {
			t.Fatal(err)
		}
		if fast {
			if err := d.SetRunFastMode(run.ID); err != nil {
				t.Fatal(err)
			}
		}
		run, err = d.GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		workDir := p.WorktreeDir(repo.ID, run.ID)
		gitCmd(t, p.RepoDir(repo.ID), "worktree", "add", "--detach", workDir, head)
		// The shared repo fixture explicitly disables Test repair. Use Rebase,
		// whose budget is inherited from global config, to isolate policy binding.
		if err := os.WriteFile(p.ConfigFile(), []byte(fmt.Sprintf("fast_mode: %t\nauto_fix:\n  rebase: 3\n", !fast)), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := mgr.loadRecoveredConfig(context.Background(), run, repo, workDir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.FastMode != fast {
			t.Fatalf("recovery changed fast mode from %v to %v", fast, cfg.FastMode)
		}
		wantBudget := 3
		if fast {
			wantBudget = 0
		}
		if cfg.AutoFixLimit(types.StepRebase) != wantBudget {
			t.Fatalf("recovered rebase fix budget = %d, want %d", cfg.AutoFixLimit(types.StepRebase), wantBudget)
		}
	}
}
