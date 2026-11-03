package config

import (
	"fmt"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestFastModeGlobalOnlyAndDisablesEveryRepairBudget(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(fmt.Sprintf("fast=%t", fast), func(t *testing.T) {
			global, err := LoadGlobalFromBytes([]byte(fmt.Sprintf("fast_mode: %t\nauto_fix:\n  test: 7\n", fast)))
			if err != nil {
				t.Fatal(err)
			}
			// Repository policy cannot self-enable fast mode, even when trusted
			// commands are explicitly allowed. Unknown repo fields remain inert.
			repo, err := LoadRepoFromBytes([]byte("fast_mode: true\ngates:\n  - name: security\n    after: test\n    command: make security\nauto_fix:\n  test: 9\n  lint: 9\n  review: 9\n  document: 9\n  ci: 9\n  rebase: 9\n  gates:\n    security: 9\n"))
			if err != nil {
				t.Fatal(err)
			}
			cfg := Merge(global, EffectiveRepoConfig(repo, repo, true))
			if cfg.FastMode != fast {
				t.Fatalf("FastMode = %v, want %v", cfg.FastMode, fast)
			}
			if !fast {
				if got := cfg.AutoFixLimit(types.StepTest); got != 9 {
					t.Fatalf("ordinary test fix budget = %d, want 9", got)
				}
				return
			}
			for _, step := range append(types.AllSteps(), types.CustomGateStepName(types.StepTest, "security")) {
				if got := cfg.AutoFixLimit(step); got != 0 {
					t.Errorf("%s fix budget = %d, want 0", step, got)
				}
			}
			if cfg.AutoFix.Lint != 0 || cfg.AutoFix.Test != 0 || cfg.AutoFix.Review != 0 || cfg.AutoFix.Document != 0 || cfg.AutoFix.CI != 0 || cfg.AutoFix.Rebase != 0 || len(cfg.AutoFix.Gates) != 0 {
				t.Fatalf("resolved automatic repair budgets are not zero: %+v", cfg.AutoFix)
			}
		})
	}
	cfg := Merge(DefaultGlobalConfig(), &RepoConfig{})
	if cfg.FastMode {
		t.Fatal("fast mode must be opt-in")
	}
}

func TestCINoChecksTimeoutInheritanceAndTrust(t *testing.T) {
	for _, tc := range []struct {
		name, global, trusted, pushed string
		want                          time.Duration
	}{
		{"default", "{}", "{}", "{}", 0},
		{"global", "ci: {no_checks_timeout: 2m}", "{}", "{}", 2 * time.Minute},
		{"trusted_override", "ci: {no_checks_timeout: 2m}", "ci: {no_checks_timeout: 30s}", "ci: {no_checks_timeout: 1s}", 30 * time.Second},
		{"trusted_zero", "ci: {no_checks_timeout: 2m}", "ci: {no_checks_timeout: 0}", "{}", 0},
		{"pushed_ignored", "ci: {no_checks_timeout: 2m}", "{}", "ci: {no_checks_timeout: 0}", 2 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			global, err := LoadGlobalFromBytes([]byte(tc.global))
			if err != nil {
				t.Fatal(err)
			}
			trusted, err := LoadRepoFromBytes([]byte(tc.trusted))
			if err != nil {
				t.Fatal(err)
			}
			pushed, err := LoadRepoFromBytes([]byte(tc.pushed))
			if err != nil {
				t.Fatal(err)
			}
			for _, allow := range []bool{false, true} {
				got := Merge(global, EffectiveRepoConfig(pushed, trusted, allow)).CI.NoChecksTimeout
				if got != tc.want {
					t.Fatalf("allow_repo_commands=%v: timeout = %s, want %s", allow, got, tc.want)
				}
			}
		})
	}
}

func TestCINoChecksTimeoutRejectsInvalidDurations(t *testing.T) {
	for _, value := range []string{"-1s", "nonsense", ""} {
		data := []byte("ci:\n  no_checks_timeout: '" + value + "'\n")
		if _, err := LoadGlobalFromBytes(data); err == nil {
			t.Errorf("global accepted %q", value)
		}
		if _, err := LoadRepoFromBytes(data); err == nil {
			t.Errorf("repo accepted %q", value)
		}
	}
}
