# Land the personal no-mistakes fork

Project-specific companion to `/land-pr`. Target repository:
`famlopezmuralles/no-mistakes`; consume upstream source from
`kunchenguid/no-mistakes`, never publish there. Supported deployment host:
Linux/WSL with Bash, Git, authenticated `gh`, the repository's Go toolchain,
Make and Python 3's standard-library SQLite support.

A merge is not a deployment. Record the PR, reviewed head, upstream release and
commit, merged commit, installed binary identity, backup, service action and
verification. On a post-merge stop, record exactly what landed and what remains
undeployed. Never report a skipped AI phase or owner exemption as validation.

## 1. Refresh upstream before every new addition and again before landing

Start in a clean persistent clone whose `origin` is this personal fork. Fetch
`origin`, lease a Treehouse worktree and create a fresh feature branch from
`origin/main`. Discover the lease path; never hard-code a pool slot. Do not edit
tracked files or validate in the persistent clone.

```bash
git fetch origin
WT=$(treehouse get --lease --lease-holder "<holder>-fork-change" --base main)
git -C "$WT" switch -c <feature-branch> --no-track origin/main
# In that leased worktree, before editing:
bash scripts/sync-upstream-release.sh
```

The helper resolves GitHub's latest **stable published release**, fetches its
exact tag directly from the canonical upstream URL into a private ref, and
merges its commit without discarding fork changes. It does not add an
`upstream` remote: that name has separate meaning in no-mistakes' contributor
initialization flow. A release already contained in the fork is a no-op, not a
reason to downgrade. Upstream `main`, prereleases and downloaded upstream
binaries are not substitutes for this release policy.

Record the helper's tag and full commit IDs. Resolve actual conflicts in the
leased feature branch while preserving both upstream and the personal policy;
never reset, squash away upstream ancestry or force-push the fork's `main`.
Commit the addition, then rerun the helper before final validation/landing. If
that changes HEAD, push and obtain fresh review and CI for that new head.
If discovery/fetch fails, stop; an old cached release is not evidence of latest.

Upstream releases legitimately include generated `CHANGELOG.md` and
`.release-please-manifest.json`. The personal fork's CI permits these imports
only when the canonical latest stable release is newly present in HEAD (not
already in the PR base) and each changed generated blob exactly matches that
release. Hand edits, copied blobs without ancestry, old-release rewinds and
unverifiable imports still fail. Do not waive the guard or regenerate these
files manually to make a sync pass.

The policy is mandatory for future additions, not just upstream-only updates.
When a new release needs integration without another feature, use a dedicated
sync branch and PR through the same gates. No unattended direct merge into main.

## 2. Validate and review the exact PR head

- Run the smallest real checks covering the change. For the release helper and
  import guard: `go test . -run 'TestSyncUpstreamRelease|TestGuardGeneratedFilesWorkflow' -count=1`.
- Exercise the changed command or installation path, not just its tests.
- Use the personal fast policy only with explicit real Test/Lint commands. All
  applicable remote CI must pass; missing checks are not green. Docs deployment
  intentionally skipped on a PR is not a failed build.
- Inspect live PR details/reviews and checks with `gh-axi pr view <PR> -R
  famlopezmuralles/no-mistakes --reviews` and `gh-axi pr checks <PR> -R
  famlopezmuralles/no-mistakes`.
- Require open, non-draft, mergeable, no unresolved requested changes and a
  latest review covering the current full head SHA. Do not merge pending/red
  checks, stale review or newly changed HEAD. Do not manufacture a full AI
  attestation: the personal owner's configured exemption is explicit.

## 3. Merge through the appropriate owner and preserve ancestry

For a Firstmate worker task use the shared skill's existing owners, in order:
`fm-pr-merge.sh`, non-forced `fm-teardown.sh`, then `fm-fleet-sync.sh`. A refusal
is a stop, never an invitation to invent task metadata or bypass the owner.

For a captain-authorized session-direct PR with no Firstmate task, inspect
with `gh-axi`, then use native `gh` for the atomic head comparison that
`gh-axi pr merge` does not expose:

```bash
gh pr merge <PR> --repo famlopezmuralles/no-mistakes \
  --merge --match-head-commit <reviewed-full-head-sha>
```

Use a merge commit so upstream release ancestry survives. Verify `MERGED` and
record the forge's full merge commit. Fetch `origin`; prove that commit is on
`origin/main`. Refresh the clean persistent clone by fast-forward only; stop on
dirt/divergence, never stash/reset unrelated work. Return only this session's
clean, committed/published Treehouse lease; preserve other holders' work.

## 4. Resolve the registered installation and stage the landed build

Use the `no-mistakes` entry in `$FM_HOME/data/deployments.md` and log in
`$FM_HOME/data/landings.md`. If `FM_HOME` is unset, locate the existing Firstmate
home/registry before deployment; do not invent a different deployment owner or
logging location. Stop if the registered executable, application home or
service ownership differs from the actual installation.

On the registered host, resolve `command -v no-mistakes` and its real executable
path; preserve the launcher/symlink. Record `--version`, the actual daemon
executable and binary/config SHA-256. The deployed source must be this fork's
landed commit, not a feature branch or upstream installer.

Build from a clean Treehouse lease pinned to the recorded merge commit:

```bash
make build VERSION="fam-<full-merged-commit>" COMMIT="<full-merged-commit>"
bin/no-mistakes --version
```

A `fam-` identity is intentionally not an upstream release version. Copy the
candidate outside the pool into the registered deployment/backup directory
before returning the lease. Verify its reported full commit and checksum.
**Do not use `make install`, `no-mistakes update`, `go install @latest` or the
upstream one-line installer**: these can restart without this procedure or
replace the personal fork with upstream code. New upstream releases enter only
through section 1 and a reviewed PR.

## 5. Back up, prove migration on a copy, then stop without force

Changes touching schema/migrations/data-modifying startup need the captain's
explicit approval before activating. Existing approval is not blanket approval
for a later migration. Never force-stop or cancel someone else's active run to
deploy this fork.

1. Create a new timestamped directory at the registry's backup location. Save
   the resolved old executable, exact config bytes, launcher/service identity,
   versions, checksums and the candidate identity. Preserve permissions. Back up
   the actual service definition and drop-ins privately; they can contain proxy
   credentials, so never dump their environment into a public landing log.
2. Make a consistent SQLite backup of `NM_HOME/state.sqlite` using SQLite's
   online backup API, **not** a raw copy of a live WAL database. Run
   `PRAGMA integrity_check` against the backup and require `ok`.
3. Migrate only a disposable copy with the candidate's existing database-only
   `stats` command; it opens the schema but does not start a daemon or recovery:

   ```bash
   env -u NM_DAEMON NM_HOME="$MIGRATION_HOME" \
     NO_MISTAKES_NO_UPDATE_CHECK=1 NO_MISTAKES_TELEMETRY=0 "$CANDIDATE" stats
   ```

   Reopen with the same command to prove idempotence. Compare existing run/step
   IDs, statuses, heads and counts with the untouched baseline; require database
   integrity and foreign-key checks, and verify the expected added schema.
   Never start a daemon over copied production records. Parse the intended
   global config too: `doctor` can report failed checks with exit status zero,
   so its exit code alone is not validation.
   Keep the pre-migration backup untouched.
4. Invoke the **old installed binary** with `daemon stop`, without `--force`.
   A refusal is a hard stop: leave live binary/config/database/service intact,
   log the active run IDs and report the staged-but-not-activated deployment.
   Do not spin, cancel runs, force, or claim deployment succeeded. Revisit only
   after the owners finish and a subsequent deployment attempt is authorized.
5. After a successful stop, ensure the old daemon and database users exited.
   Take a full quiescent copy of `NM_HOME`, plus any external durable paths the
   new startup can modify, and preserve the service definition/drop-ins. A
   SQLite-only rollback is insufficient after startup can migrate gate hooks
   or clean worktrees/evidence. Verify a final SQLite snapshot in that copy;
   save run identities to detect newly accepted work before rollback.
   Do not change schema/data with hand-written SQL.

The CLI's missing `--force` preserves its all-repository active-run guard; it
does not promise an unlimited graceful shutdown. Its native stop can escalate
after a timeout. Record the actual stop outcome and do not present a killed
process as a graceful exit. Approval gates and CI monitors count as active even
when no agent process is doing work.

## 6. Activate, verify and roll back on failure

Only after section 5's successful guarded stop:

1. Merge the approved keys from `profiles/famlopezmuralles.yaml` into the existing
   global config, preserving unrelated local configuration. Enable `fast_mode`,
   the bounded missing-check timeout, release-after-checks and the explicit
   repository checks; do not replace the whole file or remove other settings.
   Missing real commands on other repositories remain blocking, not AI discovery.
2. Atomically replace the resolved executable with the verified staged binary;
   do not overwrite the launcher/symlink. Start using the new installed binary's
   native `daemon start` so it owns service installation and migration.
3. Require native `daemon status` to report running, prove that PID is executing
   the installed fork build, and inspect the daemon's startup log for migration,
   config or recovery errors. Verify the live database's integrity. Record both
   the landed commit and the executable checksum, not just a version label.
4. Smoke the **installed executable** with a disposable Git/Go fixture in an
   isolated `NM_HOME` using a copy of the installed global config. Run real Test
   and Lint commands, prove a passing change reaches Push and broken code cannot
   publish even with `--yes`; inspect persisted fast-mode policy and zero
   validation-agent invocations. Explicitly skipping external PR/CI for this
   local transport smoke does not replace the real CI evidence from section 2.
   Stop only the fixture's daemon and remove its scaffolding afterward.
5. Inspect AXI against an existing initialized repository without reinitializing
   it. Verify the ordinary launcher reaches the healthy deployed daemon and
   existing history remains readable. Do not alter other projects or runs.

If update/start/verification fails, record the original error and use native
non-forced stop to quiesce any new daemon. Before restoring, prove no new work
was accepted since the quiescent snapshot; otherwise preserve all state and ask
the captain rather than discard it. Restore the matching quiescent application
home, executable, config and private service state. Never attach stale WAL/SHM
sidecars to a restored database: only after proving all database users stopped,
preserve the failed home/database/sidecars together, then restore into a clean
location. Do not replay archived PID/socket/lock artifacts as live ownership.
Use the old binary's native `daemon start` to restore its service definition.
If recovery also fails, leave it stopped, preserve evidence and report both
failures. A rollback is a failed deployment, never a clean landing.

## 7. Close the landing record

Record each PR and exact reviewed/merged commit, latest upstream release/SHA,
old/new executable identity, backups and integrity result, config changes,
service stop/start result, actual smoke results, intentional skips, migration
approval and any rollback/blocker. Update the deployment entry only to observed
state. Report whether it is **merged**, **staged**, **activated** and **verified**;
these are different claims.
