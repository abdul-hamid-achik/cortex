# CLI

The `cortex` binary is one of three surfaces over the kernel: CLI, MCP, and the human-facing
[Studio](/studio). Every non-interactive read command supports `--json` for machine consumption;
output is styled at a TTY and plain when piped. Studio is an interactive TUI and rejects `--json`;
use `sessions --json` or `show --json` instead.

## Global flags

| Flag | Meaning |
|---|---|
| `-C, --workspace <dir>` | workspace/repository directory (defaults to cwd) |
| `--json` | emit machine-readable JSON instead of the styled view |

## Shell completion

Install completion with cobra's built-in command, e.g. `cortex completion zsh > "${fpath[1]}/_cortex"`
(or `bash` / `fish`). Every command that takes a `<taskId>` then **tab-completes task IDs** — with
the goal shown as the description — reading the central cross-repo store plus any repo-local/custom
case store selected with `-C`, so you never type a base32 ID by hand:

```bash
cortex show <TAB>       # → task_06FK… (fix cart total)  task_06FM… (add coupon codes)
```

## Commands

### `cortex open <goal>`

Preferred entry point for agent-driven work. It resumes matching work or starts one case, so a
retry after a lost response is safe.

```bash
cortex open "Fix post-login checkout redirect" \
  --surface code --surface browser \
  --actor agent-auth --idempotency-key checkout-redirect
```

An `--idempotency-key` is the strongest identity and returns its existing case even after
completion. Without one, Cortex resumes the newest active case with the same normalized goal,
mode, workspace, and current branch. When a new case is created, `--parent` links delegated work
to a case in the same workspace and both parent and child records are updated. Resuming returns the
existing metadata unchanged.

| Flag | Default | Meaning |
|---|---|---|
| `--mode` | `change` | `change` \| `investigate` \| `review` |
| `--risk` | `medium` | `low` \| `medium` \| `high` |
| `--surface` (repeatable) | `code` | `code`, `browser`, `terminal`, `artifact`, `secret` |
| `--actor` | — | stable, non-secret person/agent identifier |
| `--parent` | — | parent task ID for same-workspace delegated work |
| `--idempotency-key` | — | stable, non-secret retry identity |

### `cortex start <goal>`

Always create a fresh case and orient it (git identity + tool health). It remains useful for manual
work that intentionally must not resume an existing case; retrying it can create another case.

```bash
cortex start "Fix post-login checkout redirect" --surface code --surface browser --risk medium
```

| Flag | Default | Meaning |
|---|---|---|
| `--mode` | `change` | `change` \| `investigate` \| `review` |
| `--risk` | `medium` | `low` \| `medium` \| `high` |
| `--surface` (repeatable) | `code` | `code`, `browser`, `terminal`, `artifact`, `secret` |

### `cortex investigate <taskId> <question>`

Route a question causally: bounded discovery first, then the top file/symbol candidates are
expanded through codemap, recording provenance (`derivedFrom`) on the structural evidence.

```bash
cortex investigate task_06FK… "where is the OAuth return URL handled"
```

| Flag | Meaning |
|---|---|
| `--surface` (repeatable) | override the routing surfaces |
| `--depth` | `quick` \| `standard` \| `deep` |

Depth and surface overrides are validated before Cortex invokes an adapter. Unknown values fail
explicitly instead of silently falling back to a different route or investigation cost.

### `cortex route [question]`

Export the executable routing matrix for agents and gateway instructions:

```bash
cortex --json route
```

Pass a question to resolve one decision, or repeat `--surface` to override the detected
surface. Unknown surfaces fail instead of silently falling through.

```bash
cortex --json route --surface browser "the login flow is wrong"
```

### `cortex recall-cases <query>`

Search the cross-case recall index (veclite) for prior resolved hypotheses and definitive
receipts related to a query — the prior disproofs to read before re-deriving a theory.
Best-effort: no veclite configured → empty, never an error.

```bash
cortex recall-cases "where is the login redirect handled" --repo liftclub --limit 5
```

| Flag | Meaning |
|---|---|
| `--repo` | scope to a repository name (empty = cross-repo) |
| `--limit` | max prior cases to return (default 5) |

### `cortex reindex-cases`

Backfill the cross-case recall index from active central sessions under
`$XDG_STATE_HOME/cortex/sessions/**`:

```bash
cortex --json reindex-cases
```

The command is idempotent. It uses each origin workspace's redaction policy and the same
sensitivity exclusions as live indexing. The JSON report separates `sessionLoadFailed` from
record-level `failed`, counts every active central session directory (including an unreadable
`case.json`), continues scanning after individual failures, then exits non-zero if either failure
count is non-zero. Archives and repository-local `cases_dir` overrides are intentionally outside
this central backfill.

### `cortex plan <taskId>`

The planning gate. Rejects plans with no disproof path, and change tasks with no boundary.

```bash
cortex plan task_06FK… \
  --hypothesis "returnTo is dropped :: run login-from-checkout browser flow" \
  --support 1=ev_06FJ… \
  --file src/auth/callback.ts --symbol HandleCallback \
  --uncertainty "unsure whether state signing also strips it"
```

| Flag | Meaning |
|---|---|
| `--hypothesis` (repeatable) | a statement; supports the `statement :: disproof` shorthand |
| `--disprove` (repeatable) | disproof for the matching `--hypothesis` (by position) |
| `--support` (repeatable) | strict one-based `hypothesis-index=evidence-id[,evidence-id...]`; IDs must belong to this task |
| `--confidence` | band for the hypotheses (default `low`) |
| `--file` / `--symbol` (repeatable) | the change boundary |
| `--boundary-reason` | why these are the expected change set |
| `--verify` (repeatable) | required verifiers (e.g. `codemap_review`, `cairntrace_flow`) |
| `--timeout` (repeatable) | strict `tool=duration` override; each tool may appear once |
| `--uncertainty` | explicit statement of what remains uncertain (required) |

Configured verifier names from `cortex.yaml` may be supplied as either `unit` or `command:unit`.
When `--verify` is omitted, every configured command verifier is included alongside the defaults
derived from the task's surfaces. Configured argv is still blocked by default: a trusted launcher
must set `CORTEX_APPROVE_COMMANDS=1`, otherwise verify records an honest `blocked` receipt.

### `cortex begin-change <taskId>`

Claim bounded ownership and enter `changing` before editing:

```bash
cortex begin-change task_06FK… --actor agent-auth --ttl 15m
```

The task must be a planned change with a declared boundary. The actor is required; TTL defaults to
15 minutes and must be between one second and one hour. A same-owner retry is idempotent (an
explicit TTL also renews the heartbeat). A competing active actor is rejected.

CLI operators can manage a long-lived lease directly:

```bash
cortex lease renew task_06FK… --actor agent-auth --ttl 30m
cortex lease release task_06FK… --actor agent-auth
```

An expired lease cannot be renewed; reacquire with `begin-change`. Completion and abort release an
active lease while retaining its audit record.

### `cortex verify <taskId>`

Run the required verifiers, detect scope drift, and write receipts.

```bash
cortex verify task_06FK… \
  --claim "the OAuth callback preserves the return URL" \
  --claim-surface browser \
  --claim-verifier cairntrace \
  --claim-contract specs/cairntrace/checkout_return.yml \
  --actor agent-auth \
  --browser-spec specs/cairntrace/checkout_return.yml
```

| Flag | Meaning |
|---|---|
| `--claim` (repeatable) | a user-facing claim to prove |
| `--claim-surface` (repeatable) | explicit `code`, `browser`, `terminal`, `artifact`, or `secret` surface; repeat once per claim |
| `--claim-verifier` (repeatable) | optional exact verifier (`codemap`, `cairntrace`, `glyphrun`, `fcheap`, `tvault`, or `command:<name>`); omit entirely or repeat once per claim |
| `--claim-contract` (repeatable) | required exact spec path/configured check/capability selector for each typed claim; repeat once per claim |
| `--changed-file` (repeatable) | override changed files (derived from git otherwise) |
| `--browser-spec` | cairntrace spec path (proves browser claims) |
| `--terminal-spec` | glyphrun spec path (proves terminal claims) |
| `--artifact-ref` | fcheap stash ID/URI for an artifact claim |
| `--secret-project` | tvault project for a value-free capability claim |
| `--no-auto-specs` | disable automatic selection of covering browser/terminal specs |
| `--no-op` | acknowledge that a change task intentionally produced no diff; does not create a pass |
| `--actor` | active change-lease owner, when leased |

Supplying `--claim-surface` opts into typed claims and requires the matching `--claim-contract`.
Typed routing is exact: a claim is `not_run` unless that exact verifier/contract ran. Legacy
`--claim` without typed flags remains available but infers the surface heuristically.

Repository-configured checks execute only when the process launching Cortex explicitly sets
`CORTEX_APPROVE_COMMANDS=1`; `cortex.yaml` cannot authorize its own arbitrary argv. Without approval,
the requirement remains blocked and the canonical assessment stays non-green.

### `cortex remember <taskId> <outcome>`

Persist the outcome to durable memory and complete the task.

```bash
cortex remember task_06FK… "returnTo was dropped; fixed and browser-verified" --tag auth
```

| Flag | Meaning |
|---|---|
| `--importance` | `0..1` importance for durable memory (default `0.5`) |
| `--tag` (repeatable) | tags for recall |
| `--unverified` | explicitly accept a `partial` or `unverified` completion when adequate proof could not be completed |
| `--accept-failed` | explicitly accept a `failed` completion — records a failed outcome, not a green one |

### `cortex status <taskId>`

Phase, unresolved hypotheses, scope drift, missing verification, and (with `--detail full`) tool
health. JSON includes case `revision`, actor/parent/children, lease, pending decision, structured
`actions`, and one canonical `verificationOutcome`: `verified`, `partial`, `failed`, or
`unverified`.

`cortex show` and Studio use one task-locked composite projection. They retain the 200 newest
evidence, command, and phase ledger records and return exact `evidenceTotal` / `timelineTotal`
counts plus a truncation warning; use `read-evidence` or `timeline` for older detail.

### Human context, decisions, and handoff

Record provenance-bearing context without pretending it is proof:

```bash
cortex note task_06FK… "support confirmed this affects only invited users" \
  --kind constraint --origin human --actor alice --ref ticket://AUTH-42
```

`--kind` is `observation | decision | constraint | handoff`; `--origin` is
`human | agent | reviewer`; confidence is only `low | medium`. Notes are redacted
`human_report` evidence and cannot satisfy verification by themselves.

Pause on one bounded human choice, then resume the exact phase:

```bash
cortex decision request task_06FK… \
  --question "Which migration should we use?" --requester agent-auth \
  --option 'safe=Safe migration|More rollout time' \
  --option 'fast=Fast migration|Higher rollback risk'

cortex decision answer task_06FK… dec_06FM… --answer safe --responder alice
cortex decision resume task_06FK… # crash recovery only: answer persisted, phase did not resume
```

A request requires at least two unique option IDs and an explicit consequence for each. While the
case is `needs_human_decision`, normal lifecycle work is paused and the case remains active.

Export a bounded transfer packet as Markdown or JSON:

```bash
cortex handoff task_06FK…                 # Markdown to stdout
cortex handoff task_06FK… -o handoff.md   # Markdown file
cortex --json handoff task_06FK…          # structured packet
```

Handoff JSON is hard-capped at 128 KiB and excludes sensitive evidence/receipt content. Markdown
files are created owner-readable/writable only (`0600` on POSIX systems).

The packet contains current state, revision, actor/parent/children/lease coordination metadata,
plan, hypotheses, at most 20 recent evidence facts, the latest verifier runs plus named-claim
receipts still current for the same revision/diff, decisions, the verification assessment, and
executable actions. Raw tool output is excluded. Use `-C <workspace>` when the case lives in a
repo-local or custom `cases_dir`.

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

### `cortex rm <taskId>` (`delete`)

**Destructive, irreversible.** Permanently deletes a session's directory and everything under it —
this is the only destructive operation in Cortex. Prefer `cortex archive` if you just want a
finished session out of the way; archiving is reversible, `rm` is not.

Guards:

- **Terminal sessions only.** In-flight (non-complete/abandoned/blocked) sessions are refused —
  complete, abort, or archive one first.
- **Dry run by default.** Without `--force`, `rm` only prints the directory that would be deleted;
  nothing is removed. Pass `--force` to actually delete.
- Works on a session in **either** the active tree or the archive.

```bash
cortex rm <taskId>            # dry run — shows what would be deleted
cortex rm <taskId> --force    # permanently deletes it (no undo)
```

### `cortex show <taskId>` (`view`)

A full one-screen view of a single session: phase badge, loop stepper, hypotheses, verification
receipts, time-in-phase (with elapsed), and recent activity. Central sessions are located by ID
**from any directory**. For a repo-local or custom `cases_dir`, pass `-C <workspace>`; you still do
not need to `cd` there. (`cortex status` remains workspace-scoped.) `--json` returns the whole view.

### `cortex overview` (`dash`)

A cross-repo rollup: totals, active/stale counts, completion and verified-completion rates, mean
time to complete, and a per-repo breakdown. The "how am I using cortex overall" dashboard.

### `cortex timeline <taskId>` (`activity`)

A session's chronological activity — phase transitions, evidence, audited tool calls, and
verification receipts — merged and time-sorted. Central sessions work from any directory; pass
`-C <workspace>` for a repo-local or custom case store. This is the reader for a case's audit log.

### `cortex studio` (`board`, `tui`)

A live, read-only Charm v2 board of every session across every repo: the session list on the left,
and the selected case's **loop stepper** (`orient→…→preserve`, with a "you are here" marker),
canonical verification assessment and gaps, pending decision, first structured next action,
hypotheses, recent evidence, and recent receipts on the right. Auto-refreshes.

```bash
cortex studio               # all sessions, live
cortex studio --active      # only in-flight
cortex studio --repo api    # scope to a repo
```

Studio is interactive and rejects `--json`. Use `cortex sessions --json` for the board index or
`cortex show <taskId> --json` for one canonical session projection.

Keys: `↑/↓` navigate · `g/G` jump · `a` active-only · `r` refresh · `q` quit.

### Other

| Command | Purpose |
|---|---|
| `cortex resolve <taskId> <hypId> --status … --reason …` | mark a hypothesis confirmed/challenged/rejected (history retained) |
| `cortex metrics [taskId]` | observability: per-task outcome + evidence trail (incl. **time-in-phase**), or the workspace aggregate (SPEC §18) |
| `cortex list` (`ls`) | all tasks in the **current workspace**, newest first (for cross-repo, use `cortex sessions`) |
| `cortex doctor` | environment + a **cross-repo session snapshot** + specialist tool health (JSON with `--json`) |
| `cortex config` | resolved workspace/storage paths, budget, recall policy, safe verifier metadata (argv omitted), redaction count, and applied `cortex.yaml` sources |
| `cortex abort <taskId> <reason>` | stop a task without deleting evidence |
| `cortex read-evidence <taskId> <evidenceId>` | print a full evidence record (with its `rawRef`) |
| `cortex read-artifact <taskId> <ref> [--path file] [--max-bytes N] [--allow-binary]` | preview a task-owned raw ref or task-referenced fcheap ref; path must be safe/relative; discovery ≤512 entries/100 files; binary requires explicit opt-in; 32 KiB default/128 KiB cap |
| `cortex serve` (`mcp`) | run the MCP server over stdio; compact `agent` profile by default |

`cortex serve --profile agent` exposes 17 lifecycle, collaboration, evidence, and recall tools for
a model's normal working context. `cortex serve --profile all` exposes 24 tools by adding seven
cross-repository monitoring/session-administration operations for an operator-oriented MCP client.
See [MCP server](/mcp#exposure-profiles).

### `cortex migrate`

Moves a legacy `~/.cortex` (or `$CORTEX_HOME`-collapsed) tree onto the split XDG layout —
`config.yaml` → `$XDG_CONFIG_HOME/cortex`, `sessions/`/`archive/`/anything else →
`$XDG_STATE_HOME/cortex`, `cache/` → `$XDG_CACHE_HOME/cortex`. **Dry run by default** — it reports
every planned move without touching disk; pass `--apply` to actually perform them. It is
**all-or-nothing**: if any XDG destination already exists, the whole migration is blocked (nothing
moves) so it can't leave a half-migrated state where moved sessions become invisible under the
surviving `~/.cortex` — resolve the conflict and re-run. If `~/.cortex` ends up empty afterward,
it's removed. With `$CORTEX_HOME` still set, or with no legacy `~/.cortex` present, it's a no-op
that explains why (`--json` reports this as `note`, `applied: false`).

```bash
cortex migrate            # dry run — see what would move
cortex migrate --apply    # actually move config.yaml/sessions/archive/cache
```

## Exit behavior

An operational error returns a non-zero exit and prints `Error: …` to stderr. Rejected gates
(e.g. a plan with no disproof path) return a non-zero exit with a clear reason — the phase is left
unchanged so you can correct and retry.
