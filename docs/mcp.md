# MCP server

Cortex speaks the Model Context Protocol over stdio so agents can drive the kernel directly.

```bash
cortex serve   # alias: cortex mcp
```

The transport is **newline-delimited JSON-RPC** (the go-sdk `StdioTransport`). All diagnostic
logging goes to stderr, so stdout stays pure protocol. Each tool takes an optional `workspace`
argument (a kernel is built per call), so one server process serves any workspace.

## Tools

| Tool | Purpose |
|---|---|
| `cortex_start_task` | open a case; orient on git identity + tool health |
| `cortex_investigate` | route a question causally — bounded discovery (vecgrep/vidtrace) first, top candidates fed into codemap; structural evidence carries `derivedFrom` provenance |
| `cortex_plan` | the planning gate — hypotheses (with disproof), boundary, verification plan |
| `cortex_verify` | run verifiers, detect scope drift, write a receipt per claim. Browser/terminal use specs; artifact claims take `artifactRef` (fcheap stash); secret-capability claims take `secretProject` (value-free tvault availability) |
| `cortex_remember` | persist the outcome and complete (needs a *passing* receipt, or `verificationNotPossible` / `acceptFailed`) |
| `cortex_status` | phase, hypotheses, scope drift, missing/stale verification, tool health. New receipts are bound to full HEAD + dirty-tree digest and become stale after later edits |
| `cortex_list_tasks` | list all tasks in the workspace (newest first) |
| `cortex_sessions` | **cross-repo**: every session everywhere — id, goal, phase, repo, verified/required, active, timestamps (filter by `repo`/`active`) |
| `cortex_timeline` | a session's time-sorted activity — phases, evidence, tool calls, receipts (located by task ID, any repo) |
| `cortex_metrics` | observability metrics — a task's evidence trail + time-in-phase, or the workspace aggregate |
| `cortex_overview` | **cross-repo** rollup — completion/verified rates, mean time to complete, per-repo breakdown |
| `cortex_archive` | archive a terminal session — move it out of the active tree to the archive (reversible, nothing deleted); refuses in-flight sessions (located by task ID, any repo) |
| `cortex_unarchive` | restore an archived session back into the active tree (located by task ID, any repo) |
| `cortex_resolve` | mark a hypothesis confirmed/challenged/rejected as evidence accumulates (history retained) |
| `cortex_abort_task` | stop without deleting evidence (reason required) |
| `cortex_read_evidence` | full evidence record by ID |
| `cortex_read_artifact` | resolve an evidence `rawRef` (or artifact ref) to the raw tool output |

## The shared result envelope

Every tool returns the same outer shape, so a weaker model learns the interface once:

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
  "artifacts": [],
  "rawAvailable": true
}
```

Raw downstream output is **not** included by default — it is stored in the case file and fetched
on demand with `cortex_read_evidence`, protecting the model's context window.

`derivedFrom` on a fact links structurally-expanded evidence back to the discovery candidate(s)
that produced it (causal routing: symptom → candidate → structure).

## Registering with mcphub

```bash
mcphub add cortex cortex serve
mcphub sync --write
```

In `gateway` mode the agent sees only mcphub, which proxies Cortex's tools namespaced as
`cortex__<tool>`. Recommended lazy pins:

```
cortex__cortex_start_task
cortex__cortex_investigate
cortex__cortex_plan
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
1. Start or resume a Cortex task.
2. Treat search output as candidates, not proof.
3. Before editing, state a testable hypothesis, change boundary, and verification plan.
4. Do not claim a user-visible behavior works without the relevant behavioral verifier.
5. Keep changes within the declared boundary; expand the plan if scope changes.
6. Preserve important evidence and state uncertainty explicitly.
7. Never request or expose secret values. Use capability checks and scoped execution only.
```
