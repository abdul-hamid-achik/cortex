# CLI

The `cortex` binary is one of two surfaces over the kernel. Every read command supports `--json`
for machine consumption; output is styled at a TTY and plain when piped.

## Global flags

| Flag | Meaning |
|---|---|
| `-C, --workspace <dir>` | workspace/repository directory (defaults to cwd) |
| `--json` | emit machine-readable JSON instead of the styled view |

## Shell completion

Install completion with cobra's built-in command, e.g. `cortex completion zsh > "${fpath[1]}/_cortex"`
(or `bash` / `fish`). Every command that takes a `<taskId>` then **tab-completes task IDs** — with
the goal shown as the description — reading across every repo, so you never type a base32 ID by hand:

```bash
cortex show <TAB>       # → task_06FK… (fix cart total)  task_06FM… (add coupon codes)
```

## Commands

### `cortex start <goal>`

Open a case and orient (git identity + tool health). Lands in the `investigating` phase.

```bash
cortex start "Fix post-login checkout redirect" --surface code --surface browser --risk medium
```

| Flag | Default | Meaning |
|---|---|---|
| `--mode` | `change` | `change` \| `investigate` \| `review` |
| `--risk` | `medium` | `low` \| `medium` \| `high` |
| `--surface` (repeatable) | `code` | `code`, `browser`, `terminal`, `artifact`, `secret` |

### `cortex investigate <taskId> <question>`

Route a question through discovery then structure, recording evidence.

```bash
cortex investigate task_06FK… "where is the OAuth return URL handled"
```

| Flag | Meaning |
|---|---|
| `--surface` (repeatable) | override the routing surfaces |
| `--depth` | `quick` \| `standard` \| `deep` |

### `cortex plan <taskId>`

The planning gate. Rejects plans with no disproof path, and change tasks with no boundary.

```bash
cortex plan task_06FK… \
  --hypothesis "returnTo is dropped :: run login-from-checkout browser flow" \
  --file src/auth/callback.ts --symbol HandleCallback \
  --uncertainty "unsure whether state signing also strips it"
```

| Flag | Meaning |
|---|---|
| `--hypothesis` (repeatable) | a statement; supports the `statement :: disproof` shorthand |
| `--disprove` (repeatable) | disproof for the matching `--hypothesis` (by position) |
| `--confidence` | band for the hypotheses (default `low`) |
| `--file` / `--symbol` (repeatable) | the change boundary |
| `--boundary-reason` | why these are the expected change set |
| `--verify` (repeatable) | required verifiers (e.g. `codemap_review`, `cairntrace_flow`) |
| `--uncertainty` | explicit statement of what remains uncertain (required) |

### `cortex verify <taskId>`

Run the required verifiers, detect scope drift, and write receipts.

```bash
cortex verify task_06FK… \
  --claim "the OAuth callback preserves the return URL" \
  --browser-spec specs/cairntrace/checkout_return.yml
```

| Flag | Meaning |
|---|---|
| `--claim` (repeatable) | a user-facing claim to prove |
| `--changed-file` (repeatable) | override changed files (derived from git otherwise) |
| `--browser-spec` | cairntrace spec path (proves browser claims) |
| `--terminal-spec` | glyphrun spec path (proves terminal claims) |

### `cortex remember <taskId> <outcome>`

Persist the outcome to durable memory and complete the task.

```bash
cortex remember task_06FK… "returnTo was dropped; fixed and browser-verified" --tag auth
```

| Flag | Meaning |
|---|---|
| `--importance` | `0..1` importance for durable memory (default `0.5`) |
| `--tag` (repeatable) | tags for recall |
| `--unverified` | record explicitly that verification was not possible (required if no definitive receipt exists) |
| `--accept-failed` | complete despite only *failed* receipts (no pass) — records a failed outcome, not a green one |

### `cortex status <taskId>`

Phase, unresolved hypotheses, scope drift, missing verification, and (with `--detail full`) tool
health.

### `cortex review`

Evidence-backed review of a branch or pull request. Resolves the diff (`base…HEAD`), gathers
structural + semantic context, runs the verifiers over the change (structural review plus the
behavioral specs that cover it), and completes with a verdict — **approve / request-changes /
needs-verification** — where every claim is backed by a receipt.

```bash
cortex review                     # current branch vs its fork point with the default branch
cortex review --base release/2.1  # against a specific base
cortex review --pr 42             # fetch + review a PR (GitHub or Bitbucket)
cortex review --surface browser   # also auto-run the browser specs covering the change
```

| Flag | Meaning |
|---|---|
| `--base <ref>` | base to diff from (default: merge-base with the default branch) |
| `--head <ref>` | ref to review (default: current branch) |
| `--pr <N>` | fetch and review a pull/merge request, host-agnostic by git ref |
| `--surface` (repeatable) | `code` (default), `browser`, `terminal` |
| `--risk` | `low` / `medium` (default) / `high` |
| `--claim` (repeatable) | an extra user-facing claim to prove |

A PR is fetched by git ref (GitHub `pull/N/head`, Bitbucket `pull-requests/N/from`) — no host CLI
required. When a host can't be fetched by ref (e.g. Bitbucket Cloud), Cortex tells you to check out
the branch and re-run with `--base`. Inspect the full review with `cortex status <taskId> --detail full`.

## Audit & monitor (across every repo)

Cortex stores sessions in a central, XDG-organized location
(`$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/` by default), so these commands see **all** your
work regardless of which repository it belongs to — one place to audit and monitor. All support
`--json`.

### `cortex sessions` (`sess`)

Every session across every repo, newest first: repo · phase · age · verification · goal.

```bash
cortex sessions                    # everything, everywhere
cortex sessions --repo billing     # only sessions whose repo/slug matches
cortex sessions --active           # only in-flight (non-terminal)
cortex sessions --stale            # in-flight but untouched beyond --stale-after (default 24h)
```

An in-flight session untouched beyond `--stale-after` renders its age with a `⚠` — a nudge toward
forgotten or stuck work. Add `--archived` to list retired sessions instead of active ones.

### `cortex archive <taskId>` / `cortex unarchive <taskId>`

Retire a finished session — **move** it (a *terminal* session: complete / abandoned / blocked) out of
the active tree into `$XDG_STATE_HOME/cortex/archive/`, so `sessions` / `overview` / `studio` stay
focused on live work as history accumulates. The data is preserved and reversible with `unarchive`;
**nothing is deleted**, and in-flight sessions are refused. View the archive with
`cortex sessions --archived`.

### `cortex show <taskId>` (`view`)

A full one-screen view of a single session: phase badge, loop stepper, hypotheses, verification
receipts, time-in-phase (with elapsed), and recent activity. Located by ID **from any directory**,
so you can inspect a task from another repo without `cd`-ing there (`cortex status` is
workspace-scoped; `show` is not). `--json` returns the whole view.

### `cortex overview` (`dash`)

A cross-repo rollup: totals, active/stale counts, completion and verified-completion rates, mean
time to complete, and a per-repo breakdown. The "how am I using cortex overall" dashboard.

### `cortex timeline <taskId>` (`activity`)

A session's chronological activity — phase transitions, evidence, audited tool calls, and
verification receipts — merged and time-sorted. Works from any directory (the session is located by
ID). This is the reader for a case's audit log.

### `cortex studio` (`board`, `tui`)

A live, read-only Charm v2 board of every session across every repo: the session list on the left,
and the selected case's **loop stepper** (`orient→…→preserve`, with a "you are here" marker),
hypotheses, evidence, and verification on the right. Auto-refreshes.

```bash
cortex studio               # all sessions, live
cortex studio --active      # only in-flight
cortex studio --repo api    # scope to a repo
```

Keys: `↑/↓` navigate · `g/G` jump · `a` active-only · `r` refresh · `q` quit.

### Other

| Command | Purpose |
|---|---|
| `cortex resolve <taskId> <hypId> --status … --reason …` | mark a hypothesis confirmed/challenged/rejected (history retained) |
| `cortex metrics [taskId]` | observability: per-task outcome + evidence trail (incl. **time-in-phase**), or the workspace aggregate (SPEC §18) |
| `cortex list` (`ls`) | all tasks in the **current workspace**, newest first (for cross-repo, use `cortex sessions`) |
| `cortex doctor` | environment + a **cross-repo session snapshot** + specialist tool health (JSON with `--json`) |
| `cortex config` | resolved configuration, the **XDG storage layout**, and which `cortex.yaml` files were applied |
| `cortex abort <taskId> <reason>` | stop a task without deleting evidence |
| `cortex read-evidence <taskId> <evidenceId>` | print a full evidence record (with its `rawRef`) |
| `cortex read-artifact <taskId> <ref>` | resolve an evidence `rawRef` to the raw tool output |
| `cortex serve` (`mcp`) | run the MCP server over stdio |

## Exit behavior

An operational error returns a non-zero exit and prints `Error: …` to stderr. Rejected gates
(e.g. a plan with no disproof path) return a non-zero exit with a clear reason — the phase is left
unchanged so you can correct and retry.
