#!/usr/bin/env bash
# Merge the latest stable upstream release into a clean personal-fork branch.
set -euo pipefail

fail() {
  printf 'sync-upstream-release: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 0 ]] || fail 'usage: bash scripts/sync-upstream-release.sh (no arguments)'
command -v git >/dev/null || fail 'git is required'
command -v gh >/dev/null || fail 'authenticated GitHub CLI (gh) is required'
git rev-parse --show-toplevel >/dev/null 2>&1 || fail 'run inside the personal fork checkout'

for state in MERGE_HEAD rebase-merge rebase-apply; do
  [[ ! -e "$(git rev-parse --git-path "$state")" ]] || fail "unfinished merge/rebase ($state); resolve or abort it first"
done
branch=$(git symbolic-ref --quiet --short HEAD) || fail 'detached HEAD; switch to a non-default working branch'
default_ref=$(git symbolic-ref --quiet refs/remotes/origin/HEAD 2>/dev/null || true)
default_branch=${default_ref#refs/remotes/origin/}
[[ "$branch" != main && "$branch" != master && "$branch" != "$default_branch" ]] || fail 'refusing to synchronize on a default branch'
worktree_status=$(git status --porcelain=v1 --untracked-files=all) || fail 'could not inspect working tree status'
[[ -z "$worktree_status" ]] || fail 'working tree must be clean, including untracked files'

is_fork_url() {
  case "$1" in
    https://github.com/famlopezmuralles/no-mistakes|https://github.com/famlopezmuralles/no-mistakes.git|https://github.com/famlopezmuralles/no-mistakes/|git@github.com:famlopezmuralles/no-mistakes|git@github.com:famlopezmuralles/no-mistakes.git|ssh://git@github.com/famlopezmuralles/no-mistakes|ssh://git@github.com/famlopezmuralles/no-mistakes.git) return 0 ;;
    *) return 1 ;;
  esac
}
origin=$(git remote get-url --all origin 2>/dev/null) || fail 'origin must be the personal fork famlopezmuralles/no-mistakes'
push_origin=$(git remote get-url --push --all origin 2>/dev/null) || fail 'origin must push to the personal fork'
is_fork_url "$origin" && is_fork_url "$push_origin" || fail 'origin fetch and push URLs must be the personal fork famlopezmuralles/no-mistakes'

gh auth status --hostname github.com >/dev/null 2>&1 || fail 'authenticate gh with github.com before synchronizing'
# GitHub owns the stable/latest selection; do not scan the full release history
# or choose prereleases merely because they were published more recently.
tag=$(gh api --hostname github.com repos/kunchenguid/no-mistakes/releases/latest \
  --jq 'select(.draft == false and .prerelease == false) | .tag_name') \
  || fail 'GitHub stable release discovery failed; nothing was merged'
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'GitHub returned no valid stable vMAJOR.MINOR.PATCH release'

release_ref="refs/no-mistakes/upstream-releases/$tag"
# Use the canonical URL, not a configured upstream remote. Git permits updates
# outside refs/heads and refs/tags even without --force, so explicitly preserve
# any release ref that already exists.
upstream_url=https://github.com/kunchenguid/no-mistakes.git
if cached_object=$(git rev-parse --verify "$release_ref" 2>/dev/null); then
  remote_tag=$(git ls-remote --exit-code "$upstream_url" "refs/tags/$tag") \
    || fail "could not verify the existing release ref for $tag"
  [[ "$remote_tag" == "$cached_object"$'\t'"refs/tags/$tag" ]] \
    || fail "upstream tag $tag moved; refusing to replace the existing release ref"
else
  git fetch --no-tags --no-recurse-submodules --no-write-fetch-head "$upstream_url" \
    "refs/tags/$tag:$release_ref" || fail "could not fetch $tag"
fi
upstream_sha=$(git rev-parse --verify "$release_ref^{commit}") || fail "$tag does not resolve to a commit"
head_before=$(git rev-parse --verify HEAD)
printf 'tag=%s\nupstream_sha=%s\nhead_before=%s\n' "$tag" "$upstream_sha" "$head_before"

if git merge-base --is-ancestor "$upstream_sha" HEAD; then
  printf 'status=no-op\nhead_after=%s\n' "$head_before"
  exit 0
fi

if git -c merge.ff=true merge --no-edit --no-squash "$upstream_sha"; then
  printf 'status=merged\nhead_after=%s\n' "$(git rev-parse --verify HEAD)"
else
  merge_status=$?
  if [[ -n "$(git ls-files --unmerged)" ]]; then
    printf 'status=conflict\nhead_after=%s\n' "$(git rev-parse --verify HEAD)"
    printf 'sync-upstream-release: merge conflicts left in place; resolve and commit, or git merge --abort\n' >&2
  else
    printf 'status=merge-failed\nhead_after=%s\n' "$(git rev-parse --verify HEAD)"
    printf 'sync-upstream-release: merge failed; inspect Git state before proceeding\n' >&2
  fi
  exit "$merge_status"
fi
