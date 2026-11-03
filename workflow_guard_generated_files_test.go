package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Execute the workflow's actual shell consumer against real Git histories.
// Release metadata is deterministic; blob identity and ancestry are not mocked.
func TestGuardGeneratedFilesWorkflow(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/guard-generated-files.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, step := range workflow.Jobs["check"].Steps {
		if step.Run != "" {
			commands = append(commands, step.Run)
		}
	}
	script := strings.Join(commands, "\n")

	cases := []struct {
		name       string
		repository string
		wantPass   bool
		prepare    func(*syncReleaseFixture, string) string
	}{
		{
			name: "ordinary-change-needs-no-upstream-api", wantPass: true,
			prepare: func(f *syncReleaseFixture, base string) string {
				f.env = append(f.env, "SYNC_API_FAIL=1")
				f.write(f.repo, "documentation.md", "ordinary addition\n")
				commitGeneratedGuardFixture(f, "ordinary addition")
				return base
			},
		},
		{
			name: "unchanged-generated-blobs-with-new-release-ancestry", wantPass: true,
			prepare: func(f *syncReleaseFixture, base string) string {
				mergeGeneratedGuardRelease(f)
				return base
			},
		},
		{
			name: "changed-generated-mode",
			prepare: func(f *syncReleaseFixture, base string) string {
				mergeGeneratedGuardRelease(f)
				f.git(f.repo, "update-index", "--chmod=+x", "CHANGELOG.md")
				f.git(f.repo, "commit", "-m", "alter generated artifact mode")
				return base
			},
		},
		{
			name: "hand-edited-release-import",
			prepare: func(f *syncReleaseFixture, base string) string {
				mergeGeneratedGuardRelease(f)
				f.write(f.repo, "CHANGELOG.md", "hand edited release notes\n")
				commitGeneratedGuardFixture(f, "modify generated artifact")
				return base
			},
		},
		{
			name: "matching-blobs-without-release-ancestry",
			prepare: func(f *syncReleaseFixture, base string) string {
				f.write(f.repo, "CHANGELOG.md", "canonical upstream release notes\n")
				f.write(f.repo, ".release-please-manifest.json", "{\".\":\"1.2.0\"}\n")
				commitGeneratedGuardFixture(f, "copy artifacts without importing release")
				return base
			},
		},
		{
			name: "old-release-cannot-authorize-a-later-artifact-rewind",
			prepare: func(f *syncReleaseFixture, _ string) string {
				mergeGeneratedGuardRelease(f)
				f.write(f.repo, "CHANGELOG.md", "later fork release notes\n")
				commitGeneratedGuardFixture(f, "later fork release")
				base := f.git(f.repo, "rev-parse", "HEAD")
				f.write(f.repo, "CHANGELOG.md", "canonical upstream release notes\n")
				commitGeneratedGuardFixture(f, "rewind generated artifact")
				return base
			},
		},
		{
			name: "upstream-discovery-failure",
			prepare: func(f *syncReleaseFixture, base string) string {
				mergeGeneratedGuardRelease(f)
				f.env = append(f.env, "SYNC_API_FAIL=1")
				return base
			},
		},
		{
			name: "exception-does-not-apply-to-another-repository", repository: "another/no-mistakes",
			prepare: func(f *syncReleaseFixture, base string) string {
				mergeGeneratedGuardRelease(f)
				return base
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncReleaseFixture(t)
			f.write(f.upstream, "CHANGELOG.md", "canonical upstream release notes\n")
			f.write(f.upstream, ".release-please-manifest.json", "{\".\":\"1.2.0\"}\n")
			f.git(f.upstream, "add", "CHANGELOG.md", ".release-please-manifest.json")
			f.git(f.upstream, "commit", "-m", "upstream generated release")
			f.git(f.upstream, "tag", "-a", "v1.2.0", "-m", "new stable release")
			f.writeRelease("v1.2.0\n")
			base := tc.prepare(f, f.fork)
			head := f.git(f.repo, "rev-parse", "HEAD")
			repository := tc.repository
			if repository == "" {
				repository = "famlopezmuralles/no-mistakes"
			}
			// Do not inherit the real CI repository identity or its token.
			var env []string
			for _, entry := range f.env {
				if !strings.HasPrefix(entry, "GITHUB_REPOSITORY=") && !strings.HasPrefix(entry, "GH_TOKEN=") {
					env = append(env, entry)
				}
			}
			f.env = append(env, "BASE_SHA="+base, "HEAD_SHA="+head, "GITHUB_REPOSITORY="+repository)
			output, err := f.command(f.repo, "bash", "-c", script)
			if (err == nil) != tc.wantPass {
				t.Fatalf("want pass=%v, got err=%v\n%s", tc.wantPass, err, output)
			}
			if got := f.git(f.repo, "rev-parse", "HEAD"); got != head {
				t.Fatalf("guard moved caller HEAD: %s -> %s", head, got)
			}
		})
	}
}

func commitGeneratedGuardFixture(f *syncReleaseFixture, message string) {
	f.t.Helper()
	f.git(f.repo, "add", ".")
	f.git(f.repo, "commit", "-m", message)
}

func mergeGeneratedGuardRelease(f *syncReleaseFixture) {
	f.t.Helper()
	f.git(f.repo, "fetch", "--no-tags", f.upstream, "refs/tags/v1.2.0:refs/fixture/guard-release")
	f.git(f.repo, "merge", "--no-edit", "refs/fixture/guard-release^{commit}")
}
