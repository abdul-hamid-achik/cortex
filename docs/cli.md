# CLI

The `cortex` binary is one of two surfaces over the kernel. Every read command supports `--json`
for machine consumption; output is styled at a TTY and plain when piped.

## Global flags

| Flag | Meaning |
|---|---|
| `-C, --workspace <dir>` | workspace/repository directory (defaults to cwd) |
| `--json` | emit machine-readable JSON instead of the styled view |

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

### Other

| Command | Purpose |
|---|---|
| `cortex resolve <taskId> <hypId> --status … --reason …` | mark a hypothesis confirmed/challenged/rejected (history retained) |
| `cortex metrics [taskId]` | observability: per-task outcome + evidence trail, or the workspace aggregate (SPEC §18) |
| `cortex list` (`ls`) | all tasks in the workspace, newest first |
| `cortex doctor` | environment + specialist tool health (JSON with `--json`) |
| `cortex config` | resolved configuration + which `cortex.yaml` files were applied |
| `cortex studio` (`studio`, `tui`) | interactive read-only case browser (Charm v2 TUI) |
| `cortex abort <taskId> <reason>` | stop a task without deleting evidence |
| `cortex read-evidence <taskId> <evidenceId>` | print a full evidence record (with its `rawRef`) |
| `cortex read-artifact <taskId> <ref>` | resolve an evidence `rawRef` to the raw tool output |
| `cortex serve` (`mcp`) | run the MCP server over stdio |

## Exit behavior

An operational error returns a non-zero exit and prints `Error: …` to stderr. Rejected gates
(e.g. a plan with no disproof path) return a non-zero exit with a clear reason — the phase is left
unchanged so you can correct and retry.
