package steps

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/git"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

// runFastChecks validates the pinned checkout using configured commands only.
// It neither repairs code nor turns a missing command into a passing check.
func runFastChecks(sctx *pipeline.StepContext, step types.StepName) (*pipeline.StepOutcome, error) {
	if sctx.Fixing {
		return nil, fmt.Errorf("fast mode does not run %s repair agents; fix the change outside the run and validate it again", step)
	}
	name := string(step)
	command := sctx.Config.Commands.Lint
	if step == types.StepTest {
		command = sctx.Config.Commands.Test
	}
	command = strings.TrimSpace(command)
	findings := Findings{
		Items:          []Finding{},
		TestingSummary: fmt.Sprintf("Fast mode validates configured %s commands only; no AI or live-product scenario validation was performed.", name),
	}
	if command == "" && len(sctx.Config.CommandOverrides[name].Additional) == 0 {
		findings.Items = append(findings.Items, Finding{
			ID:          "fast-mode-missing-" + name + "-command",
			Severity:    types.FindingSeverityError,
			Action:      types.ActionAskUser,
			Description: fmt.Sprintf("Fast mode requires commands.%s or a machine-local additional %s check. No check ran; agent discovery is disabled.", name, name),
		})
		findings.Summary = "Required validation command is not configured."
		payload, err := json.Marshal(findings)
		if err != nil {
			return nil, err
		}
		return &pipeline.StepOutcome{NeedsApproval: true, Findings: string(payload)}, nil
	}
	if err := ensurePrepared(sctx, step); err != nil {
		return nil, fmt.Errorf("prepare fast %s checks: %w", name, err)
	}
	if err := requireFastCheckCheckout(sctx, step); err != nil {
		return nil, err
	}
	declareStepCommandOverrides(sctx, name)
	sctx.Log(fmt.Sprintf("fast mode: running configured %s checks without an agent", name))
	output, results, err := runConfiguredChecks(sctx, name, command)
	projectedOutput := logConfiguredCommandOutput(sctx, output, step)
	if err != nil {
		return nil, fmt.Errorf("run fast %s checks: %w", name, err)
	}
	if err := requireFastCheckCheckout(sctx, step); err != nil {
		return nil, err
	}
	findings.Tested = make([]string, len(results))
	findings.TestedHeadSHA = sctx.Run.HeadSHA
	exitCode := 0
	for i, result := range results {
		findings.Tested[i] = result.Command
		if result.ExitCode == 0 {
			continue
		}
		if exitCode == 0 {
			exitCode = result.ExitCode
		}
		category := ""
		if step == types.StepTest {
			category = types.FindingCategoryTestCommand
		}
		findings.Items = append(findings.Items, Finding{
			Severity:    types.FindingSeverityError,
			Action:      types.ActionAskUser,
			Category:    category,
			Description: result.description(name),
		})
	}
	if exitCode == 0 {
		findings.Summary = fmt.Sprintf("Configured %s checks passed in fast mode; no agent was invoked.", name)
	} else {
		findings.Summary = fmt.Sprintf("Configured %s checks failed in fast mode.\n%s", name, projectedOutput)
	}
	payload, err := json.Marshal(findings)
	if err != nil {
		return nil, err
	}
	return &pipeline.StepOutcome{
		NeedsApproval: exitCode != 0,
		Findings:      string(payload),
		ExitCode:      exitCode,
	}, nil
}

// A command's success certifies only the recorded, unchanged checkout. A test
// or linter that edits files must not validate unpublished or unreviewed work.
func requireFastCheckCheckout(sctx *pipeline.StepContext, step types.StepName) error {
	head, err := git.HeadSHA(sctx.Ctx, sctx.WorkDir)
	if err != nil {
		return fmt.Errorf("resolve fast %s check head: %w", step, err)
	}
	if head != sctx.Run.HeadSHA {
		return fmt.Errorf("fast %s checks require the recorded head %s, but checkout HEAD is %s; no check is certified", step, sctx.Run.HeadSHA, head)
	}
	status, err := git.Run(sctx.Ctx, sctx.WorkDir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("inspect fast %s checkout: %w", step, err)
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("fast %s checks require an unchanged checkout; no check is certified:\n%s", step, status)
	}
	return nil
}
