package daemon

import (
	"slices"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

// fastModeSkips is used only at admission, never when recovering an existing run.
// Execute records these through its existing durable skipped-step rows.
func fastModeSkips(requested []types.StepName, fast bool) []types.StepName {
	if !fast {
		return requested
	}
	resolved := slices.Clone(requested)
	for _, step := range []types.StepName{types.StepReview, types.StepDocument} {
		if !slices.Contains(resolved, step) {
			resolved = append(resolved, step)
		}
	}
	return resolved
}
