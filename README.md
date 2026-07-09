# Cortex

**An evidence-guided agent kernel for software-engineering agents.**

Cortex is a small, local-first runtime that sits between an LLM and a set of specialist tools. It
does not replace the model's planning or coding ability — it supplies the parts models are
consistently bad at preserving across long tool-using tasks:

- a stable task state,
- explicit evidence and uncertainty,
- disciplined tool selection,
- bounded changes,
- verification tied to user-visible behavior,
- durable, structured memory,
- secret-safe execution.

The result is a model that is forced into a better reasoning loop:

```
orient → investigate → form hypotheses → declare a boundary → change → verify → preserve evidence
```

> More tools without structure = more ways to get lost. Specialized tools **+ a kernel** =
> accumulated engineering judgment.

---

## Why

A capable coding model often fails not because it cannot write a function, but because it cannot
maintain epistemic discipline through a multi-step task:

```
user report → broad search → read a few files → infer a likely cause →
edit several files → run one command → interpret partial success as proof →
lose the trail of why each decision was made
```

Cortex makes tool use **stateful, bounded, inspectable, and evidence-driven** — without becoming
an autonomous black box. The model stays the planner and author; Cortex ensures its claims have
an evidence trail and its actions occur inside a declared operating envelope.

## What it does

Cortex exposes **six cognitive actions** instead of dozens of overlapping raw tools:

| Action | What it enforces |
|---|---|
| **start** | opens a durable case file; orients on git identity and tool health |
| **investigate** | routes a question through discovery (vecgrep) then structure (codemap); records evidence with provenance — search output is a *candidate*, not proof |
| **plan** | requires a testable hypothesis **with a disproof path**, a change boundary, and a verification plan — plans without a disproof path are **rejected** |
| **verify** | runs the required verifiers (structural review + browser/terminal specs), detects scope drift, and writes a receipt per claim — a claim with no verifier is `not_run`, never `passed` |
| **remember** | persists the outcome and completes the task — **completion is impossible without a verification receipt** (or an explicit "not possible" acknowledgment) |
| **status** | phase, unresolved hypotheses, scope drift, missing verification, tool health |

These are structural invariants enforced by a **phase machine**, not by prompting. A model can't
skip the disproof path by restating a hypothesis, or call a change "done" without proof.

## Install

```bash
go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
# or, from a clone:
task build   # → ./bin/cortex
```

Cortex is a single pure-Go binary (`CGO_ENABLED=0`). The specialist tools it composes
(`codemap`, `vecgrep`, `cairn`, `glyph`, `fcheap`, `vidtrace`, `tvault`) are **optional at runtime** — every
adapter degrades safely when its tool is absent, and Cortex never fabricates a missing tool's
output.

## Quick start (CLI)

```bash
# 1. Open a case and orient
cortex start "Fix post-login checkout redirect" --surface code --surface browser
#   → task_06FK…  [investigating]

# 2. Investigate (routes vecgrep → codemap, records evidence)
cortex investigate task_06FK… "where is the OAuth return URL handled"

# 3. Plan — a hypothesis WITH a disproof path is mandatory
cortex plan task_06FK… \
  --hypothesis "returnTo is dropped before callback :: run login-from-checkout browser flow" \
  --file src/auth/callback.ts --symbol HandleCallback \
  --uncertainty "unsure whether state signing also strips it"

# 4. …make your edits within the boundary, then verify
cortex verify task_06FK… \
  --claim "the OAuth callback preserves the return URL" \
  --browser-spec specs/cairntrace/checkout_return.yml

# 5. Complete — needs a verification receipt to succeed
cortex remember task_06FK… "returnTo was dropped from signed state; fixed and browser-verified" \
  --tag auth --tag oauth

# anytime: inspect the case
cortex status task_06FK… --detail full
cortex list                 # tasks in the current workspace

# audit & monitor across EVERY repo (central XDG store)
cortex sessions             # all sessions, any repo (--repo/--active/--stale filters)
cortex show task_06FK…      # full one-screen view of a session — from ANY directory
cortex overview             # cross-repo rollup: completion, verification, where work sits
cortex timeline task_06FK…  # a session's phases + evidence + tool calls + verification, time-sorted
cortex metrics task_06FK…   # outcome & evidence metrics, incl. time-in-phase
cortex studio               # live board of ALL sessions across repos, w/ loop stepper (Charm v2 TUI)
cortex doctor               # environment + session snapshot + specialist tool health
```

Every read command supports `--json` for machine consumption. Output is styled at a TTY and plain
when piped.

## MCP server

Cortex speaks the Model Context Protocol over stdio (newline-delimited JSON-RPC):

```bash
cortex serve
```

It exposes seventeen tools: `cortex_start_task`, `cortex_investigate`, `cortex_plan`,
`cortex_verify`, `cortex_remember`, `cortex_status`, `cortex_list_tasks`, `cortex_sessions`,
`cortex_timeline`, `cortex_metrics`, `cortex_overview`, `cortex_archive`, `cortex_unarchive`,
`cortex_resolve`, `cortex_abort_task`, `cortex_read_evidence`, `cortex_read_artifact`.

Register it with [mcphub](https://github.com/abdul-hamid-achik/mcphub):

```bash
mcphub add cortex cortex serve
mcphub sync --write
```

In gateway mode the agent sees only mcphub, which proxies Cortex's tools as `cortex__<tool>` and
keeps the raw specialist tools available as an expert escape hatch.

## The case file

Each non-trivial task gets a durable, human-readable case file under `.cortex/cases/<taskId>/`
by default (override with `cases_dir` / `CORTEX_CASES_DIR` to keep the repo clean):

```
.cortex/cases/task_06FK…/
  case.json          # goal, workspace identity, phase, change boundary, required verification
  evidence.jsonl     # append-only ledger of claims with provenance and confidence
  hypotheses.json    # falsifiable explanations + disproof paths
  plan.json          # the planning gate
  verification.json  # receipts: which claim, which verifier, passed/failed/not_run
  commands.jsonl     # non-sensitive audit trail of tool invocations
  summary.md         # the readable outcome
```

It is the kernel's working memory, not a transcript. Workspace-local state is gitignored
(`.cortex/.gitignore`); you can also store cases under `~/.cortex/cases/…` so nothing lands in the repo.

## The ecosystem it composes

| Tool | Role in Cortex |
|---|---|
| [codemap](https://github.com/abdul-hamid-achik/codemap) | structural code graph — impact, callers, diff review |
| [vecgrep](https://github.com/abdul-hamid-achik/vecgrep) | semantic/keyword discovery + cross-session memory |
| [cairntrace](https://github.com/abdul-hamid-achik/cairntrace) | browser behavior verification |
| [glyphrun](https://github.com/abdul-hamid-achik/glyphrun) | terminal/TUI behavior verification |
| [file.cheap](https://github.com/abdul-hamid-achik/file.cheap) (`fcheap`) | durable evidence stash + search |
| [tinyvault](https://github.com/abdul-hamid-achik/tinyvault) (`tvault`) | secret-safe execution boundary (values never enter model context) |
| [mcphub](https://github.com/abdul-hamid-achik/mcphub) | MCP gateway that exposes Cortex as the default agent interface |

## Documentation

Full docs (built with VitePress) live in [`docs/`](./docs). Run locally with `task docs`. The
design specification is in [`SPEC.md`](./SPEC.md); architecture and contributor guidance in
[`AGENTS.md`](./AGENTS.md).

## Status

**v0.1 (MVP).** The kernel, both surfaces (CLI + MCP), all eight adapters, the case-file store,
redaction, scope-drift detection, and the verification policy are implemented and tested. See
`SPEC.md` §21 for the milestone roadmap.

## License

MIT © 2026 Abdul Hamid Achik
