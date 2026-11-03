package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const syncReleaseRef = "refs/no-mistakes/upstream-releases/v1.1.0"

type syncReleaseFixture struct {
	t        *testing.T
	repo     string
	upstream string
	script   string
	env      []string
	release  string
	fork     string
	stable   string
}

func TestSyncUpstreamReleasePreservesForkAndAncestry(t *testing.T) {
	f := newSyncReleaseFixture(t)
	output, err := f.run()
	if err != nil {
		t.Fatalf("synchronize release: %v\n%s", err, output)
	}
	head := f.git(f.repo, "rev-parse", "HEAD")
	if head == f.fork || head == f.stable {
		t.Fatalf("expected merge preserving both histories, got %s", head)
	}
	f.git(f.repo, "merge-base", "--is-ancestor", f.fork, "HEAD")
	f.git(f.repo, "merge-base", "--is-ancestor", f.stable, "HEAD")
	assertFileContent(t, filepath.Join(f.repo, "fork.txt"), "personal fork content\n")
	assertFileContent(t, filepath.Join(f.repo, "upstream.txt"), "new stable release\n")
	if got := f.git(f.repo, "rev-parse", syncReleaseRef+"^{commit}"); got != f.stable {
		t.Fatalf("release ref resolves to %s, want %s", got, f.stable)
	}
	if got := f.git(f.repo, "tag", "--list", "v1.1.0"); got != "" {
		t.Fatalf("release should use the private namespace, not a local tag: %s", got)
	}
	if got := f.git(f.repo, "remote"); got != "origin" {
		t.Fatalf("synchronization changed remotes: %s", got)
	}
	f.assertRecord(output, "merged", f.fork, head)

	refs := f.git(f.repo, "show-ref")
	output, err = f.run()
	if err != nil {
		t.Fatalf("repeat synchronization: %v\n%s", err, output)
	}
	if got := f.git(f.repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("already-contained release moved HEAD: %s -> %s", head, got)
	}
	if got := f.git(f.repo, "show-ref"); got != refs {
		t.Fatalf("already-contained release changed refs:\n%s\nwant:\n%s", got, refs)
	}
	f.assertRecord(output, "no-op", head, head)
}

func TestSyncUpstreamReleaseAlreadyContainedBeforeFirstSync(t *testing.T) {
	f := newSyncReleaseFixture(t)
	f.git(f.repo, "fetch", "--no-tags", f.upstream, "refs/tags/v1.1.0:refs/fixture/stable")
	f.git(f.repo, "merge", "--no-edit", f.stable)
	before := f.git(f.repo, "rev-parse", "HEAD")
	output, err := f.run()
	if err != nil {
		t.Fatalf("synchronize already-contained release: %v\n%s", err, output)
	}
	if got := f.git(f.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("contained upstream moved HEAD: %s -> %s", before, got)
	}
	f.assertRecord(output, "no-op", before, before)
}

func TestSyncUpstreamReleaseRefusesUnsafeInputsBeforeMutation(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*syncReleaseFixture)
		args  []string
		want  string
	}{
		{"tracked-dirty", func(f *syncReleaseFixture) { f.write(f.repo, "shared.txt", "dirty\n") }, nil, "working tree must be clean"},
		{"staged-dirty", func(f *syncReleaseFixture) {
			f.write(f.repo, "shared.txt", "staged\n")
			f.git(f.repo, "add", "shared.txt")
		}, nil, "working tree must be clean"},
		{"untracked", func(f *syncReleaseFixture) { f.write(f.repo, "untracked.txt", "not committed\n") }, nil, "working tree must be clean"},
		{"detached", func(f *syncReleaseFixture) { f.git(f.repo, "checkout", "--detach") }, nil, "detached HEAD"},
		{"main", func(f *syncReleaseFixture) { f.git(f.repo, "checkout", "-B", "main") }, nil, "default branch"},
		{"master", func(f *syncReleaseFixture) { f.git(f.repo, "checkout", "-b", "master") }, nil, "default branch"},
		{"discovered-default", func(f *syncReleaseFixture) {
			f.git(f.repo, "checkout", "-b", "release-default")
			f.git(f.repo, "update-ref", "refs/remotes/origin/release-default", "HEAD")
			f.git(f.repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/release-default")
		}, nil, "default branch"},
		{"wrong-origin", func(f *syncReleaseFixture) {
			f.git(f.repo, "remote", "set-url", "origin", "https://github.com/kunchenguid/no-mistakes.git")
		}, nil, "personal fork"},
		{"wrong-push-origin", func(f *syncReleaseFixture) {
			f.git(f.repo, "remote", "set-url", "--push", "origin", "https://github.com/other/no-mistakes.git")
		}, nil, "personal fork"},
		{"missing-origin", func(f *syncReleaseFixture) { f.git(f.repo, "remote", "remove", "origin") }, nil, "personal fork"},
		{"unauthenticated", func(f *syncReleaseFixture) { f.env = append(f.env, "SYNC_AUTH_FAIL=1") }, nil, "authenticate gh"},
		{"release-api-failure", func(f *syncReleaseFixture) { f.env = append(f.env, "SYNC_API_FAIL=1") }, nil, "discovery failed"},
		{"no-stable-release", func(f *syncReleaseFixture) { f.writeRelease("") }, nil, "no valid stable"},
		{"prerelease-tag", func(f *syncReleaseFixture) { f.writeRelease("v1.2.0-rc.1\n") }, nil, "no valid stable"},
		{"malformed-tag", func(f *syncReleaseFixture) { f.writeRelease("refs/heads/main\n") }, nil, "no valid stable"},
		{"multiple-tags", func(f *syncReleaseFixture) { f.writeRelease("v1.1.0\nv1.2.0\n") }, nil, "no valid stable"},
		{"arguments", func(f *syncReleaseFixture) {}, []string{"v1.0.0"}, "no arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncReleaseFixture(t)
			tc.setup(f)
			before := f.git(f.repo, "rev-parse", "HEAD")
			refs := f.git(f.repo, "show-ref")
			status := f.git(f.repo, "status", "--porcelain=v1", "--untracked-files=all")
			output, err := f.run(tc.args...)
			if err == nil || !strings.Contains(output, tc.want) {
				t.Fatalf("want refusal containing %q, got err=%v\n%s", tc.want, err, output)
			}
			if got := f.git(f.repo, "rev-parse", "HEAD"); got != before {
				t.Fatalf("unsafe input moved HEAD: %s -> %s", before, got)
			}
			if got := f.git(f.repo, "show-ref"); got != refs {
				t.Fatalf("unsafe input changed refs:\n%s\nwant:\n%s", got, refs)
			}
			if got := f.git(f.repo, "status", "--porcelain=v1", "--untracked-files=all"); got != status {
				t.Fatalf("unsafe input changed worktree/index: %q -> %q", status, got)
			}
		})
	}
}

func TestSyncUpstreamReleaseLeavesRealConflictsForOperator(t *testing.T) {
	f := newSyncReleaseFixture(t)
	f.write(f.repo, "shared.txt", "fork side\n")
	f.git(f.repo, "commit", "-am", "fork edit")
	before := f.git(f.repo, "rev-parse", "HEAD")
	f.write(f.upstream, "shared.txt", "upstream side\n")
	f.git(f.upstream, "commit", "-am", "upstream edit")
	f.git(f.upstream, "tag", "-f", "v1.1.0")
	f.stable = f.git(f.upstream, "rev-parse", "HEAD")

	output, err := f.run()
	if err == nil {
		t.Fatalf("expected real merge conflict, got success:\n%s", output)
	}
	f.assertRecord(output, "conflict", before, before)
	if got := f.git(f.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("conflicting merge moved HEAD: %s -> %s", before, got)
	}
	if got := f.git(f.repo, "rev-parse", "MERGE_HEAD"); got != f.stable {
		t.Fatalf("merge state lost: MERGE_HEAD=%s, want %s", got, f.stable)
	}
	unmerged := f.git(f.repo, "ls-files", "--unmerged")
	if !strings.Contains(unmerged, "shared.txt") {
		t.Fatalf("expected real unmerged index entries, got %q", unmerged)
	}
	assertFileContent(t, filepath.Join(f.repo, "fork.txt"), "personal fork content\n")

	output, err = f.run()
	if err == nil || !strings.Contains(output, "unfinished merge/rebase") {
		t.Fatalf("expected refusal of unresolved merge: %v\n%s", err, output)
	}
	if got := f.git(f.repo, "ls-files", "--unmerged"); got != unmerged {
		t.Fatalf("second invocation changed unresolved index: %q -> %q", unmerged, got)
	}
	f.git(f.repo, "merge", "--abort")
	if got := f.git(f.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("operator abort lost fork HEAD: %s -> %s", before, got)
	}
}

func TestSyncUpstreamReleaseDoesNotReplaceMovedReleaseRef(t *testing.T) {
	f := newSyncReleaseFixture(t)
	if output, err := f.run(); err != nil {
		t.Fatalf("initial synchronization: %v\n%s", err, output)
	}
	before := f.git(f.repo, "rev-parse", "HEAD")
	refs := f.git(f.repo, "show-ref")
	f.write(f.upstream, "upstream.txt", "retagged upstream content\n")
	f.git(f.upstream, "commit", "-am", "retag release")
	f.git(f.upstream, "tag", "-f", "v1.1.0")
	output, err := f.run()
	if err == nil || !strings.Contains(output, "refusing to replace") {
		t.Fatalf("expected moved-tag refusal: %v\n%s", err, output)
	}
	if got := f.git(f.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("retagged release moved HEAD: %s -> %s", before, got)
	}
	if got := f.git(f.repo, "show-ref"); got != refs {
		t.Fatalf("retagged release replaced refs:\n%s\nwant:\n%s", got, refs)
	}
}

func TestSyncUpstreamReleaseUsesCanonicalURLWithoutChangingUpstreamRemote(t *testing.T) {
	f := newSyncReleaseFixture(t)
	f.git(f.repo, "remote", "add", "upstream", filepath.Join(t.TempDir(), "not-a-repository"))
	remotes := f.git(f.repo, "remote", "-v")
	output, err := f.run()
	if err != nil {
		t.Fatalf("canonical upstream synchronization: %v\n%s", err, output)
	}
	f.git(f.repo, "merge-base", "--is-ancestor", f.stable, "HEAD")
	if got := f.git(f.repo, "remote", "-v"); got != remotes {
		t.Fatalf("synchronization changed remotes:\n%s\nwant:\n%s", got, remotes)
	}
}

func TestSyncUpstreamReleaseRefusesRealRebaseInProgress(t *testing.T) {
	f := newSyncReleaseFixture(t)
	f.write(f.repo, "shared.txt", "fork side\n")
	f.git(f.repo, "commit", "-am", "fork edit")
	f.write(f.upstream, "shared.txt", "upstream side\n")
	f.git(f.upstream, "commit", "-am", "upstream edit")
	f.git(f.repo, "fetch", "--no-tags", f.upstream, "HEAD:refs/fixture/rebase-target")
	if output, err := f.command(f.repo, "git", "rebase", "refs/fixture/rebase-target"); err == nil {
		t.Fatalf("fixture should create a real rebase conflict:\n%s", output)
	}
	before := f.git(f.repo, "rev-parse", "HEAD")
	unmerged := f.git(f.repo, "ls-files", "--unmerged")
	output, err := f.run()
	if err == nil || !strings.Contains(output, "unfinished merge/rebase") {
		t.Fatalf("expected refusal of unresolved rebase: %v\n%s", err, output)
	}
	if got := f.git(f.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("helper moved in-progress rebase HEAD: %s -> %s", before, got)
	}
	if got := f.git(f.repo, "ls-files", "--unmerged"); got != unmerged {
		t.Fatalf("helper changed rebase index: %q -> %q", unmerged, got)
	}
	f.git(f.repo, "rebase", "--abort")
}

func newSyncReleaseFixture(t *testing.T) *syncReleaseFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("fork landing helper supports Linux/WSL Bash")
	}
	for _, binary := range []string{"bash", "git"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is unavailable: %v", binary, err)
		}
	}
	root := t.TempDir()
	script, err := filepath.Abs(filepath.Join("scripts", "sync-upstream-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	f := &syncReleaseFixture{t: t, repo: filepath.Join(root, "fork"), upstream: filepath.Join(root, "upstream"), script: script, release: filepath.Join(root, "release-tag")}
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{bin, f.upstream, filepath.Join(root, "home")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Only the GitHub CLI boundary is replaced. Fetches, merges, conflicts,
	// refs, index changes and ancestry checks all use the real Git executable.
	// This fixture file represents gh's filtered, deterministic API result.
	writeExecutable(t, filepath.Join(bin, "gh"), `#!/bin/sh
case "$1" in
  auth) [ "${SYNC_AUTH_FAIL:-0}" = 0 ] ;;
  api)
    [ "${SYNC_API_FAIL:-0}" = 0 ] || exit 1
    cat "$SYNC_RELEASE_RESULT"
    ;;
  *) exit 2 ;;
esac
`)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "HOME" || key == "PATH" || key == "XDG_CONFIG_HOME" || strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "GH_") || strings.HasPrefix(key, "SYNC_") {
			continue
		}
		f.env = append(f.env, entry)
	}
	f.env = append(f.env,
		"HOME="+filepath.Join(root, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "home", ".config"),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true", "GIT_MERGE_AUTOEDIT=no",
		"GIT_AUTHOR_NAME=Release Test", "GIT_AUTHOR_EMAIL=release@example.invalid", "GIT_COMMITTER_NAME=Release Test", "GIT_COMMITTER_EMAIL=release@example.invalid",
		"SYNC_RELEASE_RESULT="+f.release,
	)
	f.git(f.upstream, "init")
	f.git(f.upstream, "symbolic-ref", "HEAD", "refs/heads/main")
	f.write(f.upstream, "shared.txt", "base\n")
	f.git(f.upstream, "add", "shared.txt")
	f.git(f.upstream, "commit", "-m", "base release")
	f.git(f.upstream, "tag", "v1.0.0")
	f.git(root, "clone", f.upstream, f.repo)
	f.git(f.repo, "remote", "set-url", "origin", "https://github.com/famlopezmuralles/no-mistakes.git")
	// URL rewriting provides a hermetic Git transport while the helper must
	// still request the canonical upstream URL rather than any remote alias.
	f.git(f.repo, "config", "url."+f.upstream+".insteadOf", "https://github.com/kunchenguid/no-mistakes.git")
	f.git(f.repo, "checkout", "-b", "sync-release")
	f.write(f.repo, "fork.txt", "personal fork content\n")
	f.git(f.repo, "add", "fork.txt")
	f.git(f.repo, "commit", "-m", "personal fork change")
	f.fork = f.git(f.repo, "rev-parse", "HEAD")
	f.write(f.upstream, "upstream.txt", "new stable release\n")
	f.git(f.upstream, "add", "upstream.txt")
	f.git(f.upstream, "commit", "-m", "new stable release")
	f.stable = f.git(f.upstream, "rev-parse", "HEAD")
	f.git(f.upstream, "tag", "-a", "v1.1.0", "-m", "annotated stable release")
	f.writeRelease("v1.1.0\n")
	return f
}

func (f *syncReleaseFixture) command(dir, name string, args ...string) (string, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = f.env
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (f *syncReleaseFixture) git(dir string, args ...string) string {
	f.t.Helper()
	output, err := f.command(dir, "git", args...)
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(output)
}

func (f *syncReleaseFixture) run(args ...string) (string, error) {
	f.t.Helper()
	return f.command(f.repo, "bash", append([]string{f.script}, args...)...)
}

func (f *syncReleaseFixture) write(dir, name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *syncReleaseFixture) writeRelease(content string) {
	f.t.Helper()
	if err := os.WriteFile(f.release, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *syncReleaseFixture) assertRecord(output, status, before, after string) {
	f.t.Helper()
	for _, line := range []string{"tag=v1.1.0", "upstream_sha=" + f.stable, "head_before=" + before, "status=" + status, "head_after=" + after} {
		if !strings.Contains("\n"+output, "\n"+line+"\n") {
			f.t.Errorf("missing landing record %q in output:\n%s", line, output)
		}
	}
}
