package steps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func executeFastStep(step types.StepName, sctx *pipeline.StepContext) (*pipeline.StepOutcome, error) {
	if step == types.StepTest {
		return (&TestStep{}).Execute(sctx)
	}
	return (&LintStep{}).Execute(sctx)
}

func fastCheckContext(t *testing.T, step types.StepName, command string) (*pipeline.StepContext, *mockAgent) {
	t.Helper()
	dir, baseSHA, headSHA := setupGitRepo(t)
	ag := &mockAgent{name: "must-not-run", runFn: func(context.Context, agent.RunOpts) (*agent.Result, error) {
		t.Fatal("a fast command check invoked a validation agent")
		return nil, nil
	}}
	commands := config.Commands{}
	if step == types.StepTest {
		commands.Test = command
	} else {
		commands.Lint = command
	}
	sctx := newTestContext(t, ag, dir, baseSHA, headSHA, commands)
	sctx.Config.FastMode = true
	return sctx, ag
}

func TestFastMode_ConfiguredChecksRetainExitStatusWithoutAnAgent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		step    types.StepName
		command string
		exit    int
	}{
		{"test passes", types.StepTest, "go env GOOS", 0},
		{"test fails", types.StepTest, "exit 7", 7},
		{"lint passes", types.StepLint, "go env GOOS", 0},
		{"lint fails", types.StepLint, "exit 9", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sctx, ag := fastCheckContext(t, tc.step, tc.command)
			outcome, err := executeFastStep(tc.step, sctx)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.ExitCode != tc.exit || outcome.NeedsApproval != (tc.exit != 0) || outcome.AutoFixable {
				t.Fatalf("outcome = %+v, want exit %d with failures blocked and no repair", outcome, tc.exit)
			}
			findings, err := types.ParseFindingsJSON(outcome.Findings)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings.Tested) != 1 || findings.Tested[0] != tc.command || findings.TestedHeadSHA != sctx.Run.HeadSHA {
				t.Fatalf("command evidence is not bound to this check and head: %+v", findings)
			}
			if len(findings.Scenarios) != 0 || len(ag.calls) != 0 {
				t.Fatal("command-only validation must not invent live scenarios or invoke an agent")
			}
			if tc.exit != 0 && !hasBlockingFindings(findings.Items) {
				t.Fatal("a nonzero check exit must block publication")
			}
		})
	}
}

func TestFastMode_MissingCommandsRemainUnvalidated(t *testing.T) {
	t.Parallel()
	for _, step := range []types.StepName{types.StepTest, types.StepLint} {
		t.Run(string(step), func(t *testing.T) {
			t.Parallel()
			sctx, ag := fastCheckContext(t, step, " \n ")
			outcome, err := executeFastStep(step, sctx)
			if err != nil {
				t.Fatal(err)
			}
			if !outcome.NeedsApproval || outcome.AutoFixable {
				t.Fatalf("missing command was not blocked without repair: %+v", outcome)
			}
			findings, err := types.ParseFindingsJSON(outcome.Findings)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings.Tested) != 0 || findings.TestedHeadSHA != "" || !hasBlockingFindings(findings.Items) || len(ag.calls) != 0 {
				t.Fatalf("missing check fabricated evidence or fell back to an agent: %+v", findings)
			}
		})
	}
}

func TestFastMode_MachineLocalFailureCannotBeHiddenByPassingRepositoryCheck(t *testing.T) {
	t.Parallel()
	sctx, _ := fastCheckContext(t, types.StepTest, "go env GOOS")
	sctx.Config.CommandOverrides = map[string]config.CommandOverride{
		"test": {Additional: []string{"exit 12"}},
	}
	outcome, err := (&TestStep{}).Execute(sctx)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ExitCode != 12 || !outcome.NeedsApproval || outcome.AutoFixable {
		t.Fatalf("passing repository command hid the local failure: %+v", outcome)
	}
	findings, err := types.ParseFindingsJSON(outcome.Findings)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings.Tested) != 2 || findings.Tested[1] != "exit 12" {
		t.Fatalf("local failure was not retained in command evidence: %+v", findings)
	}
}

func TestFastMode_DoesNotCertifyCommandsThatChangeTheCheckout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		command string
	}{
		{"tracked file", "git rm -f tracked-input"},
		{"untracked file", "git show HEAD:tracked-input > untracked-input"},
		{"commit", "git commit --allow-empty -m unexpected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sctx, _ := fastCheckContext(t, types.StepTest, tc.command)
			input := filepath.Join(sctx.WorkDir, "tracked-input")
			if err := os.WriteFile(input, []byte("input\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitCmd(t, sctx.WorkDir, "add", "tracked-input")
			gitCmd(t, sctx.WorkDir, "commit", "-m", "add checked input")
			sctx.Run.HeadSHA = gitCmd(t, sctx.WorkDir, "rev-parse", "HEAD")
			outcome, err := (&TestStep{}).Execute(sctx)
			if err == nil || outcome != nil {
				t.Fatalf("changed checkout was certified: outcome=%+v error=%v", outcome, err)
			}
			switch tc.name {
			case "tracked file":
				if _, err := os.Stat(input); !os.IsNotExist(err) {
					t.Fatalf("expected command to delete tracked input, got %v", err)
				}
			case "untracked file":
				content, err := os.ReadFile(filepath.Join(sctx.WorkDir, "untracked-input"))
				if err != nil || string(content) != "input\n" {
					t.Fatalf("expected actual untracked output, got %q, %v", content, err)
				}
			case "commit":
				if head := gitCmd(t, sctx.WorkDir, "rev-parse", "HEAD"); head == sctx.Run.HeadSHA {
					t.Fatal("fixture did not actually change HEAD")
				}
			}
		})
	}
}

func TestFastMode_DoesNotAttestLiveValidationFromCommandSuccess(t *testing.T) {
	t.Parallel()
	sctx, _ := fastCheckContext(t, types.StepTest, "go env GOOS")
	outcome, err := (&TestStep{}).Execute(sctx)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.NeedsApproval || outcome.ExitCode != 0 {
		t.Fatalf("configured command did not pass: %+v", outcome)
	}
	steps := []*db.StepResult{{
		ID:           "fast-test",
		StepName:     types.StepTest,
		Status:       types.StepStatusCompleted,
		FindingsJSON: &outcome.Findings,
	}}
	if live := attestedLiveValidation(steps, nil, sctx.Run.HeadSHA); live != nil {
		t.Fatalf("command-only checks falsely attested live-product validation: %+v", live)
	}
}
