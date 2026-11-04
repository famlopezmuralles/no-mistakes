<h1 align="center"><code>git push no-mistakes</code></h1>
<p align="center">
  <a href="https://github.com/kunchenguid/no-mistakes/actions/workflows/release.yml"
    ><img
      alt="Release"
      src="https://img.shields.io/github/actions/workflow/status/kunchenguid/no-mistakes/release.yml?style=flat-square&label=release"
  /></a>
  <a href="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-blue?style=flat-square"
    ><img
      alt="Platform"
      src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-blue?style=flat-square"
  /></a>
  <a href="https://x.com/kunchenguid"
    ><img
      alt="X"
      src="https://img.shields.io/badge/X-@kunchenguid-black?style=flat-square"
  /></a>
  <a href="https://discord.gg/Wsy2NpnZDu"
    ><img
      alt="Discord"
      src="https://img.shields.io/discord/1439901831038763092?style=flat-square&label=discord"
  /></a>
</p>

<p align="center">
  <a href="https://trendshift.io/repositories/27829?utm_source=repository-badge&amp;utm_medium=badge&amp;utm_campaign=badge-repository-27829" target="_blank" rel="noopener noreferrer"><img src="https://trendshift.io/api/badge/repositories/27829" alt="kunchenguid%2Fno-mistakes | Trendshift" width="250" height="55"/></a>
</p>

<h3 align="center">Kill all the slop. Raise clean PR.</h3>

<p align="center"><strong>English</strong> · <a href="README.zh-CN.md">简体中文</a></p>

<p align="center">
  <img src="https://raw.githubusercontent.com/kunchenguid/no-mistakes/main/demo.gif" alt="no-mistakes demo" width="800" />
</p>

`no-mistakes` puts a local git proxy in front of your real remote.
Push to `no-mistakes` instead of `origin`, and it spins up a disposable worktree, runs an AI-driven validation pipeline, forwards the branch to the configured push target only after every check passes, and opens a clean PR automatically.

- **Non-blocking** - the pipeline runs in an isolated worktree without disrupting your work.
- **Agent-agnostic** - `claude`, `codex`, `grok`, `rovodev`, `opencode`, `pi`, `copilot`, `antigravity`, or `cursor` / `devin` / `acp:<target>` via `acpx`, with ordered fallbacks; every gate requires a runnable configured pipeline agent.
- **Agent-native** - `/no-mistakes` lets your coding agent do a task and gate it, or gate existing committed work: it runs the pipeline, has the pipeline apply safe fixes, and escalates the rest to you.
- **Human stays in charge** - auto-fix or review findings, your call.
- **Clean PRs by default** - push, open PR, watch CI, and auto-fix failures in one shot.

Full documentation: <https://kunchenguid.github.io/no-mistakes/>

## Personal fast policy (this fork)

This fork adds an opt-in, operator-owned `fast_mode` for lean **agentic code
review**. It removes duplicate work, not the independent review. The normal
upstream policy remains the default.

The [personal preset](profiles/famlopezmuralles.yaml):

- keeps an independent reviewer for the full reviewable change, with the existing
  trusted instructions and configured reviewer model;
- makes one initial analyzer pass instead of replaying the review to repair
  malformed output or automatically completing missing coverage; invalid output
  fails and incomplete coverage stays blocked until an explicit focused
  `complete-coverage` request reviews the remaining paths;
- skips Document and runs configured Test/Lint commands without validation agents
  or automatic repairs, including when the driver uses `--yes`;
- requires durable review approval and configured checks for the exact published
  commit; skipping Review cannot authorize Push;
- blocks missing commands, nonzero exits, and checks that change the pinned
  checkout instead of declaring them passed;
- keeps real CI mandatory, bounds missing-check registration to 60 seconds, and
  releases the run when checks pass rather than waiting for a human merge;
- pins the policy on new runs, so changing the global profile does not convert
  an existing run during recovery.

The preset uses the existing 10-minute review budget and 20-minute still-working
cap, with low reasoning effort and no executor crash replay. These are failure
bounds, not promised latency. Native adapter transport retries remain within the
invocation budget; one analyzer pass is not a promise of one provider request.
PR drafting may also use an agent. Neither source review nor command checks claim
live-product validation. Explicitly skipped Test/Lint checks remain visible
omissions. CI jobs that have registered retain their normal execution budget;
an empty check list is never green.

The inherited attestation workflow's owner exemption follows
`github.repository_owner`, rather than the upstream author's account. It reports
an owner exemption, not a compliant AI pipeline; the real Go/test CI still runs.

For an isolated Linux/WSL build, from this fork's checkout:

```sh
mkdir -p "$HOME/.local/bin" "$HOME/.no-mistakes-fam"
go build -o "$HOME/.local/bin/no-mistakes-fam" ./cmd/no-mistakes
cp -n profiles/famlopezmuralles.yaml "$HOME/.no-mistakes-fam/config.yaml"
NM_HOME="$HOME/.no-mistakes-fam" "$HOME/.local/bin/no-mistakes-fam" doctor
```

The copy preserves an existing isolated config. Do not replace the installed
binary/config or reinitialize a checkout while another installation owns active
runs. For a new checkout, `init --skip-user-skill` avoids replacing the existing
user-level agent skill. Build from this fork for these options; the upstream
one-line installer below installs upstream.

The preset includes explicit additional checks for this fork and Centinela.
Its Centinela commands use the owner's WSL Python environment at
`/home/lopezc/centinela/.venv/bin/python`; adjust that personal path on
another host. These additions never replace trusted repository commands. Other
repositories must configure their own real Test/Lint commands; fast mode refuses
to invent them. See the [global config reference](docs/src/content/docs/reference/global-config.md#fast_mode)
for policy and recovery details.

For maintained use of this fork, follow the
[land-the-plane runbook](PR-MERGE-RUNBOOK.md): refresh the latest stable upstream
source before each addition and before landing, merge through a reviewed PR,
then deploy the fork with verified backups, active-run protection and rollback.
Do not use the upstream self-updater to update the personal fork.


## How it works

```
        your branch
            │  git push no-mistakes
            ▼
   ┌────────────────────────────────────────────────┐
   │  disposable worktree — your work stays put     │
   │  review → test → docs → lint → push → PR → CI  │
   └────────────────────────────────────────────────┘
            │  every check green
            ▼
        clean PR, opened for you
```

Each step either passes on its own or stops with a **finding** for you to act on.
Safe, mechanical fixes are applied automatically; anything that touches your intent is escalated for you to **approve**, **fix**, or **skip**.
The initial change reaches the configured push target only after every local gate is green.

When CI itself fails, the pipeline repairs it and publishes that repair through the same guarded force-push path - but only when it can prove the repair builds on the head you already reviewed. When it cannot prove that, the repair goes back through Review before it is published, so unrelated history cannot replace the reviewed commit. Merge-conflict repairs rewrite history, so they always take that safer route.
Set [`ci.revalidate_repairs: true`](https://kunchenguid.github.io/no-mistakes/reference/repo-config/#cirevalidate_repairs) if *every* CI repair must itself be reviewed, at the cost of another full pass over your change each time CI is repaired.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/kunchenguid/no-mistakes/main/docs/install.sh | sh
```

Windows, Go install, Nix, and build-from-source instructions are in the [installation guide](https://kunchenguid.github.io/no-mistakes/start-here/installation/).

## Quick Start

```sh
$ no-mistakes init
  ✓ Gate initialized

    repo  /Users/you/src/my-repo
    gate  no-mistakes → /Users/you/.no-mistakes/repos/abc123def456.git
  remote  git@github.com:you/my-repo.git
   skill  /no-mistakes installed for agents at user level

  Push through the gate with:
  git push no-mistakes <branch>

$ git checkout my-branch

# do some work in the branch...

$ git push no-mistakes
  * Pipeline started

  Run no-mistakes to review.

$ no-mistakes
# opens the TUI for the active run
```

For GitHub fork contributions, keep `origin` pointed at the parent repository and initialize with `no-mistakes init --fork-url <your-fork-url>`.

From the TUI you act on each **finding**: **auto-fix** ones are applied for you (or approve to let them), **ask-user** ones are a judgement call you approve, fix, or skip.
Once every check is green, the gate forwards your branch to the configured push target and opens the PR for you, so there is no manual `git push origin` and no hand-written PR body.
Prefer to let your coding agent drive the same flow headlessly?
Use `/no-mistakes` (see below).

## Three ways to trigger the gate

Every change runs through the same pipeline. Pick the entry point that fits how you're working when the change is ready:

- **`git push no-mistakes`** - the explicit Git path. Push a committed branch to the gate remote instead of `origin`.
- **`no-mistakes`** - the TUI. Run it after making changes (no commit needed) and a wizard walks you through creating a branch, committing, and pushing through the gate, then attaches to the run. `no-mistakes -y` does all of that automatically.
- **`/no-mistakes`** - the agent skill. Tell the coding agent to do a task and gate it with `/no-mistakes <task>`, or use bare `/no-mistakes` to gate existing committed work. It runs the pipeline, has the pipeline apply safe fixes, and stops to ask you about anything that needs a human call.

`no-mistakes init` installs the `/no-mistakes` skill for Claude Code and other agents. Under the hood the skill drives `no-mistakes axi`, a non-interactive TOON interface to the same approval flow.

See the [quick start](https://kunchenguid.github.io/no-mistakes/start-here/quick-start/) for the full first-run walkthrough.

## Development

```sh
make build   # Build bin/no-mistakes with version info
make test    # Run go test -race ./... (excludes the e2e suite)
make e2e     # Run the tagged end-to-end agent journey suite
make e2e-record # Re-record e2e fixtures when agent wire formats change
make lint    # Check generated skill and Windows resource drift, run go vet ./...
make skill   # Regenerate committed no-mistakes skill files
make fmt     # Run gofmt -w .
make demo    # Regenerate demo.gif and demo.mp4 (needs vhs and ffmpeg)
make docs    # Build the Astro docs site in docs/dist
```

See `Makefile` for the full target list.

`make e2e-record` overwrites `internal/e2e/fixtures/` from the real `claude`, `codex`, `opencode`, and `antigravity` CLIs, spends real API quota, and should be reviewed before committing.

## Star History

<a href="https://www.star-history.com/?repos=kunchenguid%2Fno-mistakes&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=kunchenguid/no-mistakes&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=kunchenguid/no-mistakes&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=kunchenguid/no-mistakes&type=date&legend=top-left" />
 </picture>
</a>
