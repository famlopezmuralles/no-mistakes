package steps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Exercise the real executor, configured shell checks, and guarded Push against
// a local bare upstream. The remote starts at base, so success requires an
// actual publication rather than an already-up-to-date push.
func TestPipelineFastModeRequiresReviewedConfiguredChecks(t *testing.T) {
	for _, tc := range []struct {
		name              string
		skipReview        bool
		skipLint          bool
		skipTest          bool
		automaticLintSkip bool
		mutate            func(*testing.T, *pipeline.StepContext)
		wantError         string
	}{
		{name: "explicit lint skip remains missing evidence", skipLint: true},
		{name: "explicit test skip remains missing evidence", skipTest: true},
		{name: "skipped review cannot publish", skipReview: true, wantError: "review skip prevents push"},
		{name: "automatic lint skip is not operator consent", automaticLintSkip: true, wantError: "automatic lint skip"},
		{name: "missing lint evidence", mutate: func(t *testing.T, sctx *pipeline.StepContext) {
			if err := sctx.DB.ClearStepFindings(fastPushStep(t, sctx, types.StepLint).ID); err != nil {
				t.Fatal(err)
			}
		}, wantError: "lint did not complete with recorded evidence"},
		{name: "stale test head", mutate: func(t *testing.T, sctx *pipeline.StepContext) {
			changeFastPushFindings(t, sctx, types.StepTest, func(f *types.Findings) { f.TestedHeadSHA = sctx.Run.BaseSHA })
		}, wantError: "test evidence is not bound"},
		{name: "wrong lint command", mutate: func(t *testing.T, sctx *pipeline.StepContext) {
			changeFastPushFindings(t, sctx, types.StepLint, func(f *types.Findings) { f.Tested = []string{"a different check"} })
		}, wantError: "lint commands do not match"},
		{name: "failed lint exit cannot publish", mutate: func(t *testing.T, sctx *pipeline.StepContext) {
			if err := sctx.DB.CompleteStep(fastPushStep(t, sctx, types.StepLint).ID, 7, 0, ""); err != nil {
				t.Fatal(err)
			}
		}, wantError: "lint did not pass"},
		{name: "later recorded descendant needs new checks", mutate: func(t *testing.T, sctx *pipeline.StepContext) {
			gitCmd(t, sctx.WorkDir, "commit", "--allow-empty", "-m", "later unvalidated change")
			head := gitCmd(t, sctx.WorkDir, "rev-parse", "HEAD")
			if err := sctx.DB.UpdateRunHeadSHA(sctx.Run.ID, head); err != nil {
				t.Fatal(err)
			}
			sctx.Run.HeadSHA = head
		}, wantError: "differs from review-approved head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := t.TempDir()
			gitCmd(t, upstream, "init", "--bare")
			dir, base, head := setupGitRepo(t)
			gitCmd(t, dir, "remote", "add", "origin", upstream)
			gitCmd(t, dir, "push", "origin", "main", base+":refs/heads/feature")
			findings := cleanReviewFindings()
			findings.ReviewedPaths = fullReviewCoverage(t, dir, base)
			payload, err := json.Marshal(findings)
			if err != nil {
				t.Fatal(err)
			}
			ag := &mockAgent{name: "test", runFn: func(context.Context, agent.RunOpts) (*agent.Result, error) {
				return &agent.Result{Output: payload}, nil
			}}
			sctx := newTestContextWithDBRecords(t, ag, dir, base, head, config.Commands{
				Test: "git rev-parse --verify HEAD", Lint: "git diff --exit-code",
			})
			sctx.Config.FastMode = true
			sctx.Repo.UpstreamURL = upstream
			if err := sctx.DB.SetRunFastMode(sctx.Run.ID); err != nil {
				t.Fatal(err)
			}
			sctx.Run.FastMode = true
			p := paths.WithRoot(t.TempDir())
			if err := p.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			gateDir := p.RepoDir(sctx.Repo.ID)
			if err := os.MkdirAll(filepath.Dir(gateDir), 0o755); err != nil {
				t.Fatal(err)
			}
			gitCmd(t, filepath.Dir(gateDir), "init", "--bare", filepath.Base(gateDir))
			var lint pipeline.Step = &LintStep{}
			if tc.automaticLintSkip {
				lint = &transitionStep{name: types.StepLint, execute: func(*pipeline.StepContext) (*pipeline.StepOutcome, error) {
					return &pipeline.StepOutcome{Skipped: true, SkipReason: "lint service unavailable"}, nil
				}}
			}
			checks := []pipeline.Step{&ReviewStep{}, &TestStep{}, &DocumentStep{}, lint}
			if tc.mutate != nil {
				checks = append(checks, &transitionStep{name: types.CustomGateStepName(types.StepLint, "publication-probe"), execute: func(current *pipeline.StepContext) (*pipeline.StepOutcome, error) {
					tc.mutate(t, current)
					return &pipeline.StepOutcome{}, nil
				}})
			}
			checks = append(checks, &PushStep{})
			executor := pipeline.NewExecutor(sctx.DB, p, sctx.Config, ag, checks, nil)
			skips := []types.StepName{types.StepDocument}
			if tc.skipReview {
				skips = append(skips, types.StepReview)
			}
			if tc.skipLint {
				skips = append(skips, types.StepLint)
			}
			if tc.skipTest {
				skips = append(skips, types.StepTest)
			}
			executor.SetSkippedSteps(skips)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err = executor.Execute(ctx, sctx.Run, sctx.Repo, dir)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("publication failed: %v", err)
				}
				if got := gitCmd(t, upstream, "rev-parse", "refs/heads/feature"); got != head {
					t.Fatalf("published head = %s, want %s", got, head)
				}
				if push := fastPushStep(t, sctx, types.StepPush); push.Status != types.StepStatusCompleted {
					t.Fatalf("Push status = %s", push.Status)
				}
				if review := fastPushStep(t, sctx, types.StepReview); review.Status != types.StepStatusCompleted {
					t.Fatalf("Review status = %s, want completed", review.Status)
				}
				if tc.skipLint && fastPushStep(t, sctx, types.StepLint).Status != types.StepStatusSkipped {
					t.Fatal("explicit missing lint evidence was not preserved")
				}
				if tc.skipTest && fastPushStep(t, sctx, types.StepTest).Status != types.StepStatusSkipped {
					t.Fatal("explicit missing test evidence was not preserved")
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("publication error = %v, want %q", err, tc.wantError)
				}
				if got := gitCmd(t, upstream, "rev-parse", "refs/heads/feature"); got != base {
					t.Fatalf("refused publication changed upstream from %s to %s", base, got)
				}
			}
			run, err := sctx.DB.GetRun(sctx.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.skipReview {
				if run.ReviewApprovedHeadSHA != nil || len(ag.calls) != 0 {
					t.Fatal("skipped review gained approval or invoked an agent")
				}
			} else if run.ReviewApprovedHeadSHA == nil || *run.ReviewApprovedHeadSHA != head || len(ag.calls) != 1 {
				t.Fatalf("publication needs one independent review of %s; approved=%v calls=%d", head, run.ReviewApprovedHeadSHA, len(ag.calls))
			}
		})
	}
}

func fastPushStep(t *testing.T, sctx *pipeline.StepContext, name types.StepName) *db.StepResult {
	t.Helper()
	results, err := sctx.DB.GetStepsByRun(sctx.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.StepName == name {
			return result
		}
	}
	t.Fatalf("missing %s step", name)
	return nil
}

func changeFastPushFindings(t *testing.T, sctx *pipeline.StepContext, name types.StepName, change func(*types.Findings)) {
	t.Helper()
	result := fastPushStep(t, sctx, name)
	if result.FindingsJSON == nil {
		t.Fatalf("%s has no findings evidence", name)
	}
	findings, err := types.ParseFindingsJSON(*result.FindingsJSON)
	if err != nil {
		t.Fatal(err)
	}
	change(&findings)
	payload, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	if err := sctx.DB.SetStepFindings(result.ID, string(payload)); err != nil {
		t.Fatal(err)
	}
}
