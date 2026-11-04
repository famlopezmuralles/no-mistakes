package steps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// Both command checks pass on the current head. Neither ancestry nor a cached
// in-memory approval can substitute for the durable review of that exact head.
func TestReviewStepFastModeRequiresDurableExactHeadAuthority(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ancestor approval", "memory-only approval"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ag := newStaticReviewAgent(coverageFindingJSON([]string{"feature.txt", "feature2.txt"}))
			sctx, p, upstream := newFastReviewFixture(t, ag)
			executor := pipeline.NewExecutor(sctx.DB, p, sctx.Config, ag, []pipeline.Step{&ReviewStep{}, &TestStep{}, &LintStep{}}, nil)
			if err := executor.Execute(context.Background(), sctx.Run, sctx.Repo, sctx.WorkDir); err != nil {
				t.Fatalf("review and configured checks: %v", err)
			}
			assertFastReviewInvocation(t, ag)
			run, err := sctx.DB.GetRun(sctx.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if run.ReviewApprovedHeadSHA == nil || *run.ReviewApprovedHeadSHA != sctx.Run.HeadSHA {
				t.Fatalf("durable review approval = %v, want exact head %s", run.ReviewApprovedHeadSHA, sctx.Run.HeadSHA)
			}
			if sctx.Run.ReviewApprovedHeadSHA == nil || *sctx.Run.ReviewApprovedHeadSHA != sctx.Run.HeadSHA {
				t.Fatal("executor did not retain the successful review's exact-head authority")
			}
			for _, step := range []types.StepName{types.StepTest, types.StepLint} {
				result := fastPushStep(t, sctx, step)
				if result.Status != types.StepStatusCompleted || result.FindingsJSON == nil {
					t.Fatalf("%s did not record completed command evidence: %+v", step, result)
				}
				findings, err := types.ParseFindingsJSON(*result.FindingsJSON)
				if err != nil || findings.TestedHeadSHA != sctx.Run.HeadSHA {
					t.Fatalf("%s evidence is not current: %+v, %v", step, findings, err)
				}
			}
			approval := sctx.Run.BaseSHA
			if name == "memory-only approval" {
				approval = ""
			}
			if err := sctx.DB.UpdateRunReviewApprovedHeadSHA(sctx.Run.ID, approval); err != nil {
				t.Fatal(err)
			}
			// Keep the in-memory approval untouched: Push must consult the DB.
			if outcome, err := (&PushStep{}).Execute(sctx); err == nil || outcome != nil || !strings.Contains(strings.ToLower(err.Error()), "review") {
				t.Fatalf("Push with %s = %+v, %v; want refusal", name, outcome, err)
			}
			assertFastReviewUnpublished(t, sctx, upstream)
			assertFastReviewInvocation(t, ag)
		})
	}
}

func TestReviewStepFastModeMalformedOutputDoesNotRetryOrPublish(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		output string
		err    error
	}{
		{name: "malformed JSON", output: `{"findings":[`},
		{name: "missing risk assessment", output: `{"findings":[],"reviewed_paths":["feature.txt","feature2.txt"]}`},
		{name: "adapter schema rejection", err: rejectedStructuredOutputError{message: "review adapter rejected risk_level"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			ag := &mockAgent{name: "independent-reviewer", runFn: func(context.Context, agent.RunOpts) (*agent.Result, error) {
				calls++
				if calls == 1 {
					if tc.err != nil {
						return nil, tc.err
					}
					return &agent.Result{Output: json.RawMessage(tc.output)}, nil
				}
				// A forbidden retry could turn the invalid review into approval.
				return &agent.Result{Output: json.RawMessage(coverageFindingJSON([]string{"feature.txt", "feature2.txt"}))}, nil
			}}
			sctx, p, upstream := newFastReviewFixture(t, ag)
			executor := pipeline.NewExecutor(sctx.DB, p, sctx.Config, ag, []pipeline.Step{&ReviewStep{}, &TestStep{}, &LintStep{}, &PushStep{}}, nil)
			if err := executor.Execute(context.Background(), sctx.Run, sctx.Repo, sctx.WorkDir); err == nil {
				t.Fatal("malformed reviewer output passed the publication pipeline")
			}
			assertFastReviewInvocation(t, ag)
			assertFastReviewHasNoAuthority(t, sctx)
			if review := fastPushStep(t, sctx, types.StepReview); review.Status != types.StepStatusFailed {
				t.Fatalf("invalid Review status = %s, want failed", review.Status)
			}
			assertFastReviewUnpublished(t, sctx, upstream)
		})
	}
}

// Current command evidence cannot release a blocked Review. In particular, an
// otherwise clean partial review must park, not spend a second agent turn or
// allow manual approval to silently waive the missing coverage.
func TestReviewStepFastModeFindingsAndCoverageRemainBlocked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		output string
		gap    bool
	}{
		{name: "blocking defect", output: `{"findings":[{"id":"r-1","severity":"error","action":"auto-fix","description":"empty input dereferences a nil configuration","file":"feature.txt","line":1,"review_scope":"source"}],"risk_level":"high","risk_rationale":"nil dereference","risk_scope":"source-or-external","reviewed_paths":["feature.txt","feature2.txt"]}`},
		{name: "partial coverage", output: coverageFindingJSON([]string{"feature.txt"}), gap: true},
		{name: "unreported coverage", output: coverageFindingJSON(nil), gap: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			ag := &mockAgent{name: "independent-reviewer", runFn: func(context.Context, agent.RunOpts) (*agent.Result, error) {
				calls++
				if calls == 1 {
					return &agent.Result{Output: json.RawMessage(tc.output)}, nil
				}
				// Completion or repair would certify the head if invoked.
				return &agent.Result{Output: json.RawMessage(coverageFindingJSON([]string{"feature.txt", "feature2.txt"}))}, nil
			}}
			sctx, p, upstream := newFastReviewFixture(t, ag)
			// Put real deterministic evidence before Review so Push refusal cannot
			// be attributed to missing Test/Lint checks.
			executor := pipeline.NewExecutor(sctx.DB, p, sctx.Config, ag, []pipeline.Step{&TestStep{}, &LintStep{}, &ReviewStep{}, &PushStep{}}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- executor.Execute(ctx, sctx.Run, sctx.Repo, sctx.WorkDir) }()
			stopped := false
			stop := func() {
				if stopped {
					return
				}
				cancel()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					t.Error("parked review did not stop after cancellation")
				}
				stopped = true
			}
			t.Cleanup(stop)
			waitForReviewStatus(t, sctx.DB, sctx.Run.ID, types.StepStatusAwaitingApproval)
			review := fastPushStep(t, sctx, types.StepReview)
			if review.FindingsJSON == nil {
				t.Fatal("parked Review lost its structured findings")
			}
			findings, err := types.ParseFindingsJSON(*review.FindingsJSON)
			if err != nil {
				t.Fatal(err)
			}
			if tc.gap {
				if findings.ReviewCoverageScope == nil || !slices.Contains(findings.ReviewCoverageScope.Paths, "feature2.txt") || slices.Contains(findings.ReviewedPaths, "feature2.txt") {
					t.Fatalf("parked review did not retain the explicit coverage gap: %+v", findings)
				}
			} else if len(findings.Items) != 1 || findings.Items[0].ID != "r-1" {
				t.Fatalf("parked Review lost its blocking defect: %+v", findings.Items)
			}
			for _, action := range []types.ApprovalAction{types.ActionApprove, types.ActionFix} {
				if err := executor.Respond(types.StepReview, action, nil); err == nil || !strings.Contains(err.Error(), "fast_mode") {
					t.Fatalf("%s response = %v, want fast_mode refusal retaining the gate", action, err)
				}
			}
			if got := fastPushStep(t, sctx, types.StepReview).Status; got != types.StepStatusAwaitingApproval {
				t.Fatalf("refused response released Review: %s", got)
			}
			stop()
			assertFastReviewInvocation(t, ag)
			assertFastReviewHasNoAuthority(t, sctx)
			if outcome, err := (&PushStep{}).Execute(sctx); err == nil || outcome != nil || !strings.Contains(strings.ToLower(err.Error()), "review") {
				t.Fatalf("Push after blocked Review = %+v, %v; want refusal", outcome, err)
			}
			assertFastReviewUnpublished(t, sctx, upstream)
		})
	}
}

func newFastReviewFixture(t *testing.T, ag agent.Agent) (*pipeline.StepContext, *paths.Paths, string) {
	t.Helper()
	dir, base, head := twoFileRepo(t)
	upstream := t.TempDir()
	gitCmd(t, upstream, "init", "--bare")
	gitCmd(t, dir, "remote", "add", "origin", upstream)
	gitCmd(t, dir, "push", "origin", "main", base+":refs/heads/feature")
	sctx := newTestContextWithDBRecords(t, ag, dir, base, head, config.Commands{
		Test: "git rev-parse --verify HEAD", Lint: "git diff --exit-code",
	})
	sctx.Config.FastMode = true
	sctx.Run.FastMode = true
	sctx.Repo.UpstreamURL = upstream
	if err := sctx.DB.SetRunFastMode(sctx.Run.ID); err != nil {
		t.Fatal(err)
	}
	p := paths.WithRoot(t.TempDir())
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	gateDir := p.RepoDir(sctx.Repo.ID)
	if err := os.MkdirAll(filepath.Dir(gateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, filepath.Dir(gateDir), "init", "--bare", filepath.Base(gateDir))
	return sctx, p, upstream
}

func assertFastReviewInvocation(t *testing.T, ag *mockAgent) {
	t.Helper()
	if len(ag.calls) != 1 {
		t.Fatalf("agent invocations = %d, want one independent review and no retry, completion, or repair", len(ag.calls))
	}
	if call := ag.calls[0]; call.Purpose != "review" || call.Session != nil {
		t.Fatalf("review invocation = purpose %q session %+v, want an independent review", call.Purpose, call.Session)
	}
}

func assertFastReviewHasNoAuthority(t *testing.T, sctx *pipeline.StepContext) {
	t.Helper()
	run, err := sctx.DB.GetRun(sctx.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.ReviewApprovedHeadSHA != nil {
		t.Fatalf("blocked Review acquired durable approval: %s", *run.ReviewApprovedHeadSHA)
	}
}

func assertFastReviewUnpublished(t *testing.T, sctx *pipeline.StepContext, upstream string) {
	t.Helper()
	if got := gitCmd(t, upstream, "rev-parse", "refs/heads/feature"); got != sctx.Run.BaseSHA {
		t.Fatalf("refused review published %s, want upstream unchanged at %s", got, sctx.Run.BaseSHA)
	}
}
