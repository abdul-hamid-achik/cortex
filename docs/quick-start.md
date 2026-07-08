# Quick Start

## Install

```bash
go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
# or from a clone:
task build   # → ./bin/cortex
```

Cortex is a single pure-Go binary. The specialist tools it composes are **optional** — check
what's available with:

```bash
task doctor
```

Anything missing simply degrades: the corresponding adapter reports `tool_unavailable` instead of
fabricating output.

## A full task, start to finish

Cortex tracks work as a **case**. Every action advances a phase machine and appends to the case
file under `.agent/cases/<taskId>/`.

### 1. Start — open a case and orient

```bash
cortex start "Fix post-login checkout redirect" --surface code --surface browser
```

Cortex records git identity (repo, branch, baseline commit) and probes tool health, then lands in
the `investigating` phase and prints the new `taskId`.

### 2. Investigate — discover, then resolve structure

```bash
cortex investigate task_06FK… "where is the OAuth return URL handled"
```

The router sends vague behavioral questions to **vecgrep** (discovery) then **codemap**
(structure). Each result is recorded as evidence with a confidence band — search hits are
`low`/`medium` **candidates**, never proof.

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

### 4. …edit, then verify

Make your edits within the declared boundary, then:

```bash
cortex verify task_06FK… \
  --claim "the OAuth callback preserves the return URL" \
  --browser-spec specs/cairntrace/checkout_return.yml
```

Verify runs a structural diff review (codemap), any provided behavioral specs (cairntrace /
glyphrun), and checks for **scope drift** against the boundary. Each claim gets a receipt; a claim
with no relevant verifier is recorded `not_run`, never `passed`.

### 5. Remember — complete the task

```bash
cortex remember task_06FK… \
  "returnTo was dropped from signed state; fixed and browser-verified" \
  --tag auth --tag oauth
```

Completion **requires** a verification receipt. If verification genuinely couldn't run, you must
say so explicitly with `--unverified` — Cortex never lets an unverified outcome masquerade as
verified. A `summary.md` is written and a durable memory is stored (via vecgrep).

## Inspect anytime

```bash
cortex status task_06FK… --detail full   # phase, hypotheses, scope drift, missing verification, tool health
cortex list                              # all tasks, newest first
cortex read-evidence task_06FK… ev_06FK… # a full evidence record
```

Add `--json` to any read command for machine output. Output is styled at a terminal and plain when
piped.
