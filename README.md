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

Cortex exposes a small task workflow instead of dozens of overlapping raw tools:

| Action | What it enforces |
|---|---|
| **open** | retry-safely resumes matching work or starts one durable case; a new case can register an immutable acceptance contract and records actor/parent linkage when supplied |
| **investigate** | routes a question through discovery (vecgrep) then structure (codemap); records evidence with provenance — search output is a *candidate*, not proof |
| **plan** | requires a testable hypothesis **with a disproof path**, a change boundary, and a verification plan — plans without a disproof path are **rejected** |
| **begin-change** | atomically claims bounded change ownership for an actor before editing; leases expire, renew, and prevent competing writers |
| **verify** | binds typed claims to an explicit surface, optional verifier, and required exact contract; detects scope drift and atomically writes one revision-bound receipt batch |
| **remember** | persists the outcome and completes the task — normal completion requires the canonical assessment to be `verified`; explicit acknowledgments preserve partial/unverified/failed outcomes honestly |
| **status / show** | phase, structured next actions, decisions, scope drift, bounded claim-proof manifest, and the canonical `verified / partial / failed / unverified` assessment |

These are structural invariants enforced by a **phase machine**, not by prompting. A model can't
skip the disproof path by restating a hypothesis, or call a change "done" without proof.

## Install

```bash
go install github.com/abdul-hamid-achik/cortex/cmd/cortex@latest
# or, from a clone:
task build   # → ./bin/cortex
```

Cortex is a single pure-Go binary (`CGO_ENABLED=0`), with **Git required** for repository identity,
diffs, scope drift, and revision-bound verification. The specialist tools it composes
(`codemap`, `vecgrep`, `cairn`, `glyph`, `fcheap`, `vidtrace`, `tvault`, `veclite`) are **optional
at runtime** — every adapter degrades safely when its tool is absent, and Cortex never fabricates
a missing tool's output.

## Quick start (CLI)

```bash
# 1. Open or resume a case safely
cortex open "Fix post-login checkout redirect" \
  --surface code --surface browser --actor agent-auth --idempotency-key checkout-redirect \
  --criterion 'checkout_return=Login started at checkout returns to checkout'
#   → task_06FK…  [investigating]

# 2. Investigate (routes vecgrep → codemap, records evidence)
cortex investigate task_06FK… "where is the OAuth return URL handled"

# 3. Plan — a hypothesis WITH a disproof path is mandatory
cortex plan task_06FK… \
  --hypothesis "returnTo is dropped before callback :: run login-from-checkout browser flow" \
  --file src/auth/callback.ts --symbol HandleCallback \
  --uncertainty "unsure whether state signing also strips it"

# 4. Claim bounded change ownership, then edit
cortex begin-change task_06FK… --actor agent-auth

# 5. Verify a typed claim against the exact browser contract
cortex verify task_06FK… \
  --claim "Login started at checkout returns to checkout" \
  --claim-id checkout_return \
  --claim-surface browser --claim-verifier cairntrace \
  --claim-contract specs/cairntrace/checkout_return.yml \
  --actor agent-auth \
  --browser-spec specs/cairntrace/checkout_return.yml

# 6. Complete — needs an adequate verification assessment to succeed
cortex remember task_06FK… "returnTo was dropped from signed state; fixed and browser-verified" \
  --tag auth --tag oauth

# anytime: inspect the case
cortex status task_06FK… --detail full
cortex list                 # tasks in the current workspace

# audit & monitor across EVERY repo (central XDG store)
cortex sessions             # all sessions, any repo (--repo/--active/--stale/--query filters)
cortex sessions --query "billing partial" # AND-search goal/repo/state/outcome
cortex show task_06FK…      # full one-screen view of a session — from ANY directory
cortex overview             # cross-repo rollup: completion, verification, where work sits
cortex timeline task_06FK…  # a session's phases + evidence + tool calls + verification, time-sorted
cortex metrics task_06FK…   # outcome & evidence metrics, incl. time-in-phase
cortex studio               # live board; press / to search every repo/session
cortex doctor               # environment + session snapshot + specialist tool health
```

Every non-interactive read command supports `--json` for machine consumption. Output is styled at a
TTY and plain when piped. Studio is interactive and directs machines to `sessions --json` or
`show --json`.

Cortex has three surfaces over the same kernel: the CLI for direct operation and scripting, the MCP
server for agents, and **Studio** (`cortex studio`) for a live, read-only human view across sessions.
Humans and agents can also attach provenance-bearing notes, pause on bounded decisions with explicit
consequences, and export a compact handoff without copying raw transcripts. General handoffs are
capped at 128 KiB; complete verified handoffs use a 90 KiB primary-result budget and preserve their
entire non-sensitive named-claim/verifier proof closure or omit it explicitly as one unit.

## MCP server

Cortex speaks the Model Context Protocol over stdio (newline-delimited JSON-RPC):

```bash
cortex serve                 # compact agent profile (default, 17 tools)
cortex serve --profile all   # full operator profile (24 tools)
```

The default `agent` profile includes the task loop, notes, bounded decisions, handoffs, evidence and
artifact previews, and prior-case recall. It hides seven cross-repository operator/session tools.
`--profile all` exposes those too for operator-oriented MCP clients.

Register it with [mcphub](https://github.com/abdul-hamid-achik/mcphub):

```bash
mcphub add cortex cortex serve
mcphub sync --write
```

Existing integrations that require the historical full surface can register it with:

```bash
mcphub add cortex cortex serve -- --profile all
mcphub sync --write
```

In gateway mode the agent sees only mcphub, which proxies Cortex's tools as `cortex__<tool>` and
keeps the raw specialist tools available as an expert escape hatch.

### local-agent compatibility

Cortex's compact profile is contract-tested with `local-agent`: its required
`cortex_open_task`, `cortex_status`, and `cortex_handoff` calls remain stable, while additive
acceptance criteria and proof manifests are optional. Use the normal MCPHub gateway process
(`mcphub mcp serve --agent local-agent`) or register a direct server named `cortex` with command
`cortex` and arguments `serve`. `cortex doctor --probe` performs a live gateway handshake; the
default Cortex registration should report 17 tools. Completed handoffs stay within local-agent's
96 KiB tool-result ceiling by budgeting the primary proof packet at 90 KiB.

## The case file

Each non-trivial task gets a durable, human-readable case file in the central XDG state store by
default. Override `cases_dir` / `CORTEX_CASES_DIR` only when you want repo-local or custom storage:

```
$XDG_STATE_HOME/cortex/sessions/<repo-slug>/task_06FK…/
  case.json          # goal, acceptance criteria, workspace, phase, boundary, required verification
  decisions.json     # bounded human questions, options, consequences, and answers
  evidence.jsonl     # append-only ledger of claims with provenance and confidence
  hypotheses.json    # falsifiable explanations + disproof paths
  plan.json          # the planning gate
  verification.json  # receipts: which claim, which verifier, passed/failed/not_run
  commands.jsonl     # non-sensitive audit trail of tool invocations
  phases.jsonl       # phase-transition history
  summary.md         # the readable outcome
  raw/               # redacted raw tool output, fetched on demand
```

It is the kernel's working memory, not a transcript. The central default keeps the working tree
clean. If you opt into repo-local storage, Cortex writes `.cortex/.gitignore` so its own state does
not appear as a workspace change. Run `cortex config` to see the resolved paths.

Case snapshots carry optimistic revisions, optional actor/parent/child metadata, and the active or
released change lease. Verification publishes its case revision, facts, bounded raw blobs, and
receipts as one recoverable transaction; losing plan/lease races leave no stray proof, and only a
bound behavioral batch can annotate code. Status and handoff stream bounded evidence projections,
while auto-refreshing Show/Studio views retain bounded recent ledgers and exact totals from one
task-locked snapshot. New state files and directories are owner-only. Artifact reads are task-scoped: a case raw reference must belong to the
requested task, and an fcheap reference must already appear in that task's artifact evidence or
verification receipts. Previews default to 32 KiB and stop at 128 KiB; fcheap paths must be safe
relative paths, directory discovery walks at most 512 entries and returns at most 100 regular
files, and binary content is refused unless explicitly allowed.

## The ecosystem it composes

| Tool | Role in Cortex |
|---|---|
| [codemap](https://github.com/abdul-hamid-achik/codemap) | structural code graph — impact, callers, diff review |
| [vecgrep](https://github.com/abdul-hamid-achik/vecgrep) | semantic/keyword discovery + cross-session memory |
| [cairntrace](https://github.com/abdul-hamid-achik/cairntrace) | browser behavior verification |
| [glyphrun](https://github.com/abdul-hamid-achik/glyphrun) | terminal/TUI behavior verification |
| [file.cheap](https://github.com/abdul-hamid-achik/file.cheap) (`fcheap`) | durable evidence stash + search |
| vidtrace | screen-recording evidence linked to owning code |
| [tinyvault](https://github.com/abdul-hamid-achik/tinyvault) (`tvault`) | secret-safe execution boundary (values never enter model context) |
| veclite | cross-case recall of prior disproofs and definitive receipts (embeddings via Ollama) |
| [mcphub](https://github.com/abdul-hamid-achik/mcphub) | MCP gateway that exposes Cortex as the default agent interface |

## Documentation

Full documentation is published at **[cortexai.tools](https://cortexai.tools)**; its isolated
VitePress source lives in [`docs/`](./docs) and runs locally with `task docs`. Architecture,
behavior, and contributor guidance live in [`AGENTS.md`](./AGENTS.md).

## Status

**v0.12.0.** The kernel, all three surfaces (CLI + MCP + Studio), the
adapter suite, case-file coordination, redaction, scope-drift detection, and revision-bound
verification policy are implemented and tested. `task eval` also prints a paired
Cortex-versus-unassisted calibration scorecard; its deterministic fixtures validate the
measurement model, not an empirical product claim. See `CHANGELOG.md` for the current release
scope and `internal/eval` for the executable evaluation contract.

## License

MIT © 2026 Abdul Hamid Achik
