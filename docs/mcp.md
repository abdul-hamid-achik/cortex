# MCP server

Cortex speaks the Model Context Protocol over stdio so agents can drive the kernel directly.

```bash
cortex serve                 # alias: cortex mcp; agent profile by default
cortex serve --profile all   # full 24-tool operator surface
```

The transport is **newline-delimited JSON-RPC** (the go-sdk `StdioTransport`). All diagnostic
logging goes to stderr, so stdout stays pure protocol. Workspace-scoped tools take an optional
`workspace` argument (a kernel is built per call), so one server process serves any
workspace. Handoff and timeline first locate central sessions by task ID; supply `workspace` as a
fallback when a case uses a repo-local or custom `cases_dir`.

## Exposure profiles

`--profile agent` is the default. It exposes 17 tools for the task lifecycle, bounded human
collaboration, evidence/artifact access, and prior-case recall. Cross-repository monitoring and
session administration stay outside the model's default tool context.

`--profile all` exposes 24 tools by adding exactly seven operator operations: `list_tasks`,
`sessions`, `timeline`, `metrics`, `overview`, `archive`, and `unarchive`. Profiles change exposure
only; both call the same kernel and use the same case files.

## Tools

| Tool | Profile | Purpose |
|---|---|---|
| `cortex_start_task` | `agent`, `all` | deliberately create a fresh case; orient on git identity + tool health |
| `cortex_open_task` | `agent`, `all` | preferred retry-safe entry: idempotency key returns the same case; otherwise resume newest active normalized goal/mode/workspace/branch match or start once; a newly created case accepts actor and parent linkage |
| `cortex_investigate` | `agent`, `all` | route a question causally — bounded discovery (vecgrep/vidtrace) first, top candidates fed into codemap; structural evidence carries `derivedFrom` provenance |
| `cortex_plan` | `agent`, `all` | the planning gate — hypotheses (with disproof and optional per-hypothesis evidence IDs), boundary, verification plan |
| `cortex_begin_change` | `agent`, `all` | atomically acquire the actor's expiring change lease and enter `changing`; same-owner retries are safe |
| `cortex_verify` | `agent`, `all` | run planned verifiers, detect scope drift, and bind typed `claimSpecs` to an exact surface/verifier/contract; leased tasks require the owner actor; intentional no-diff changes require `noOpAcknowledged` |
| `cortex_remember` | `agent`, `all` | persist the outcome and complete; normal completion requires the canonical assessment to be `verified`, while explicit `verificationNotPossible` / `acceptFailed` acknowledgments preserve non-green outcomes |
| `cortex_status` | `agent`, `all` | phase, case revision/actor/linkage/lease, pending decision, scope, structured actions, and canonical `verified / partial / failed / unverified` assessment |
| `cortex_resolve` | `agent`, `all` | mark a hypothesis confirmed/challenged/rejected as evidence accumulates (history retained) |
| `cortex_note` | `agent`, `all` | append redacted human/agent/reviewer context as provenance-bearing `human_report`; never satisfies verification alone |
| `cortex_request_decision` | `agent`, `all` | pause on one bounded human question with at least two options and explicit consequences |
| `cortex_answer_decision` | `agent`, `all` | record the selected option and resume the exact paused phase; `resume=true` repairs an already-persisted answer after a crash |
| `cortex_handoff` | `agent`, `all` | transfer packet (128 KiB hard cap): state plus revision/actor/linkage/lease, plan, hypotheses, 20 recent shareable evidence facts, current verifier runs and carried same-state named claims, decisions, assessment, and executable actions; sensitive record content and raw output omitted; optional workspace fallback |
| `cortex_abort_task` | `agent`, `all` | stop without deleting evidence (reason required) |
| `cortex_read_evidence` | `agent`, `all` | full evidence record by ID |
| `cortex_read_artifact` | `agent`, `all` | bounded preview of a task-owned raw ref or task-referenced fcheap ref; safe relative `path`; 32 KiB default/128 KiB cap; discovery ≤512 entries/100 files; binary refused unless `allowBinary` |
| `cortex_recall_cases` | `agent`, `all` | recall prior resolved cases (rejected/challenged hypotheses + definitive receipts) related to a query, cross-repo or scoped — prior disproofs to read before re-deriving a theory |
| `cortex_list_tasks` | `all` | list all tasks in the workspace (newest first) |
| `cortex_sessions` | `all` | **cross-repo**: every session everywhere — id, goal, phase, repo, verified/required, active, timestamps (filter by `repo`/`active`) |
| `cortex_timeline` | `all` | a session's time-sorted activity — phases, evidence, tool calls, receipts; optional workspace fallback finds repo-local/custom cases |
| `cortex_metrics` | `all` | observability metrics — a task's evidence trail + time-in-phase, or the workspace aggregate |
| `cortex_overview` | `all` | **cross-repo** rollup — completion/verified rates, mean time to complete, per-repo breakdown |
| `cortex_archive` | `all` | archive a terminal session — move it out of the active tree to the archive (reversible, nothing deleted); refuses in-flight sessions (located by task ID, any repo) |
| `cortex_unarchive` | `all` | restore an archived session back into the active tree (located by task ID, any repo) |

The agent profile has no separate lease-renew/release tools. Retrying `cortex_begin_change` as the
same actor with `ttl` is the MCP heartbeat; `cortex_remember` and `cortex_abort_task` release active
ownership.
Human operators can use the CLI's explicit `cortex lease renew|release` commands.

## The shared result envelope

Lifecycle and mutation tools return the same outer envelope, so a weaker model learns the working
interface once. Read/index/operator tools return their documented structured projections directly.

```json
{
  "ok": true,
  "taskId": "task_06FK…",
  "phase": "investigating",
  "summary": "investigated … via vecgrep→codemap: 3 evidence items recorded",
  "facts": [
    { "id": "ev_06FK…", "claim": "HandleCallback redirects to '/' when returnTo is missing",
      "confidence": "medium", "source": "codemap", "kind": "code_graph",
      "derivedFrom": ["ev_06FJ…"] }
  ],
  "warnings": [],
  "nextActions": ["cortex plan — state a hypothesis with a disproof path"],
  "actions": [
    {
      "tool": "cortex_plan",
      "command": "cortex plan task_06FK…",
      "reason": "declare hypotheses, disproof paths, boundary, and verification",
      "arguments": {"taskId": "task_06FK…", "workspace": "/work/cortex"},
      "inputs": ["hypotheses", "uncertainty"],
      "blockedBy": ["insufficient evidence"]
    }
  ],
  "artifacts": [],
  "rawAvailable": true
}
```

`actions` is the machine-readable continuation contract; `nextActions` remains for humans and
older clients. Use known `arguments` directly, collect named `inputs`, and respect `blockedBy`
instead of parsing prose. Every task continuation carries its workspace, so another process can
invoke it without inheriting the original cwd. Its companion CLI `command` is workspace-pinned and
shell-safe for human copy/paste. `cortex_begin_change` actions also carry the
explicit default `ttl` (`15m`). Interrupted `new`/`orienting` cases project a retry-safe open; a
half-committed decision request or answer projects the exact request/resume repair.

A lifecycle rule rejection keeps this JSON envelope (`ok: false`, task ID, and any recovery
context) and also sets MCP `isError: true`. If an internal write fails after Cortex constructed an
error envelope, that structured envelope is retained rather than replaced by plain error text.
Clients may therefore use MCP error signaling without losing the fields needed to recover.

Raw downstream output is **not** included by default. Pass a `/raw/` ref only with its exact owning
`taskId`. An fcheap ref must already be present in that task's artifact evidence or verification
receipts. `maxBytes` defaults to 32768 and is clamped to 131072. `path` selects a safe relative
file; absolute paths, parent traversal, and symlinks are rejected. Discovery walks at most 512
entries and returns at most 100 regular files. Binary is rejected unless `allowBinary` is true,
then returned as bounded base64 with `sensitive: true`. Responses declare `encoding`, `truncated`,
and `bytesReturned`, so bounded retrieval is never mistaken for the complete artifact.

`derivedFrom` on a fact links structurally-expanded evidence back to the discovery candidate(s)
that produced it (causal routing: symptom → candidate → structure).

## Registering with mcphub

```bash
mcphub add cortex cortex serve
mcphub sync --write
```

That registration uses the default 17-tool `agent` profile. To expose the 24-tool operator surface:

```bash
mcphub add cortex cortex serve -- --profile all
mcphub sync --write
```

The `--` separator passes `--profile all` to Cortex instead of parsing it as an mcphub flag.

In `gateway` mode the agent sees only mcphub, which proxies Cortex's tools namespaced as
`cortex__<tool>`. Recommended lazy pins:

```
cortex__cortex_open_task
cortex__cortex_investigate
cortex__cortex_plan
cortex__cortex_begin_change
cortex__cortex_verify
cortex__cortex_status
```

The raw specialist tools stay discoverable as an expert escape hatch — Cortex makes the *default*
path sane without preventing expert use.

## Model instruction contract

The recommended system prompt is intentionally short — Cortex enforces behavior through state, not
a long sermon:

```
You are working through Cortex.

For non-trivial engineering work:
1. Open or resume with cortex_open_task; use an idempotency key when a retry is possible.
2. Treat search output as candidates, not proof.
3. Before editing, state a testable hypothesis, change boundary, and verification plan.
4. Claim the change with cortex_begin_change and keep the same actor through verification.
5. Prefer typed claimSpecs and bind important claims to the exact verifier contract.
6. Do not claim a user-visible behavior works without the relevant behavioral verifier.
7. Keep changes within the declared boundary; expand the plan if scope changes.
8. Preserve important evidence and state uncertainty explicitly.
9. Never request or expose secret values. Use capability checks and scoped execution only.
```

## Typed verification example

```json
{
  "taskId": "task_06FK…",
  "actor": "agent-auth",
  "claimSpecs": [
    {
      "id": "checkout-return",
      "statement": "Login initiated at checkout returns to checkout",
      "surface": "browser",
      "verifier": "cairntrace",
      "contract": "specs/checkout-return.yml"
    },
    {
      "statement": "Repository unit tests pass",
      "surface": "code",
      "verifier": "command:unit",
      "contract": "unit"
    }
  ],
  "browserSpec": "specs/checkout-return.yml"
}
```

The command verifier must already exist in `cortex.yaml`; callers cannot provide executable argv.
Because configured argv is arbitrary local code, it remains blocked unless the trusted launcher
starts the server with `CORTEX_APPROVE_COMMANDS=1 cortex serve`. Repository configuration cannot
approve itself; without approval Cortex records a `blocked` receipt.
If an exact contract did not run, the named claim receipt is `not_run`. `noOpAcknowledged: true`
only acknowledges an intentional no-diff change and does not make either claim pass.
