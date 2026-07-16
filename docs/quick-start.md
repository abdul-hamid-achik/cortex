# Quick Start

## Install

```bash
go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
# or from a clone:
task build   # → ./bin/cortex
```

Cortex is a single pure-Go binary, but **Git is required** for repository identity, diffs, scope
drift, and revision-bound verification. The specialist tools it composes are **optional** — check
what's available with:

```bash
cortex doctor
```

If you are developing Cortex from a clone, `task doctor` separately checks the development
toolchain (Go, Task, Bun, Glyphrun, and lint tooling).

Anything missing simply degrades: the corresponding adapter reports `tool_unavailable` instead of
fabricating output.

## A full task, open to finish

Cortex tracks work as a **case**. Every action advances a phase machine and appends to the case
file under `$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/` by default. Set `cases_dir` /
`CORTEX_CASES_DIR` only when you want repo-local or custom storage; `cortex config` prints the
resolved path.

### 1. Open — resume safely or start once

```bash
cortex open "Fix post-login checkout redirect" \
  --surface code --surface browser \
  --actor agent-auth --idempotency-key checkout-redirect \
  --criterion 'checkout_return=Login started at checkout returns to checkout'
```

Cortex returns an existing case for the idempotency key, so retrying after a lost response cannot
duplicate work. Without a key it resumes the newest active case matching the normalized goal, mode,
workspace, branch, and acceptance contract. Criteria are immutable and must later be proven with
the same claim ID and exact statement. Only a real first call records git identity, probes tool health, and creates
the `taskId`; use `cortex start` when you intentionally want a new case.

### 2. Investigate — discover, then resolve structure

```bash
cortex investigate task_06FK… "where is the OAuth return URL handled"
```

The router sends vague behavioral questions to **vecgrep** (discovery) then **codemap**
(structure) — discovery runs first, and what it found feeds into **codemap** (structure) as
candidates. Each result is recorded as evidence with a confidence band — search hits are
`low`/`medium` **candidates**, never proof. Low-value hits (heading-only, bare imports,
punctuation fragments) are filtered, and an all-weak round honestly reports "no strong candidates"
with zero facts instead of recording noise.

### 3. Plan — the gate

```bash
cortex plan task_06FK… \
  --hypothesis "returnTo is dropped before callback :: run login-from-checkout browser flow" \
  --file src/auth/callback.ts --symbol HandleCallback \
  --uncertainty "unsure whether state signing also strips it"
```

The planning gate **rejects** any plan whose hypotheses lack a disproof path, and any change task
that declares no boundary. The `statement :: disproof` shorthand pairs a hypothesis with how it
could be falsified (or use paired `--hypothesis` / `--disprove` flags).

### 4. Begin change — claim the writer lease

```bash
cortex begin-change task_06FK… --actor agent-auth
```

The task enters `changing` under a 15-minute lease. A same-actor retry is safe; another actor is
rejected while the lease is active. Renew long work with `cortex lease renew … --actor agent-auth`
or release it deliberately with `cortex lease release … --actor agent-auth`.

### 5. Edit, then verify an exact claim

Make your edits within the declared boundary, then:

```bash
cortex verify task_06FK… \
  --claim "Login started at checkout returns to checkout" \
  --claim-id checkout_return \
  --claim-surface browser \
  --claim-verifier cairntrace \
  --claim-contract specs/cairntrace/checkout_return.yml \
  --actor agent-auth \
  --browser-spec specs/cairntrace/checkout_return.yml
```

The surface and contract make the proof obligation exact instead of guessing from the sentence.
Verify runs the planned checks and detects **scope drift**. Each named claim gets a receipt; a claim
whose exact verifier/contract did not run is `not_run`, never `passed`. Receipts bind to the current
HEAD and dirty-tree digest, so later edits make them stale.

If the planned change intentionally produces no diff, pass `--no-op`. This acknowledges the no-op
so verification may proceed; it does not create a pass or make the task verified.

### 6. Remember — complete the task

```bash
cortex remember task_06FK… \
  "returnTo was dropped from signed state; fixed and browser-verified" \
  --tag auth --tag oauth
```

Completion uses one canonical assessment: `verified`, `partial`, `failed`, or `unverified`.
Normal completion requires `verified`. If adequate proof could not be completed and the assessment
is `partial` or `unverified`, use `--unverified`; if it is `failed`, use `--accept-failed`. Those
acknowledgments do not bypass registered acceptance criteria. Cortex preserves the real assessment rather
than letting an incomplete or failed outcome masquerade as a clean pass.

## Inspect anytime

```bash
cortex status task_06FK… --detail full   # phase, hypotheses, scope drift, missing verification, tool health
cortex show task_06FK…                   # assessment, pending decision, first structured action
cortex list                              # all tasks, newest first
cortex read-evidence task_06FK… ev_06FK… # a full evidence record
cortex handoff task_06FK…                # bounded transfer packet for another person or agent
cortex sessions --query "billing partial" # shared AND-search across repo/state/outcome
cortex studio                            # live board; press / to search across repos
```

Add `--json` to any non-interactive read command for machine output. Output is styled at a terminal
and plain when piped. Studio is interactive; use `sessions --json` or `show --json` instead.
