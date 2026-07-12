# Cortex Agent Kernel
## Specification for a Local-First Evidence-Guided Engineering Runtime

**Status:** Draft v0.1  
**Primary implementation language:** Go  
**Deployment model:** Local-first CLI + MCP server  
**Target reasoning models:** MiniMax M3 and compatible coding-agent models  
**Primary integration surface:** `mcphub` gateway  
**Last updated:** 2026-07-06

---

## 0. Abstract

Cortex is an **agent kernel** for software-engineering agents. It is a small, local-first runtime positioned between an LLM and a set of specialist tools. Cortex does not attempt to replace the model's planning or coding ability. It supplies the parts models are consistently bad at preserving across long tool-using tasks:

- a stable task state;
- explicit evidence and uncertainty;
- disciplined tool selection;
- bounded changes;
- verification tied to user-visible behavior;
- durable, structured memory;
- secret-safe execution.

The result is a model that does not merely have more tools. It is forced into a better reasoning loop:

```text
orient → investigate → form hypotheses → declare a boundary → change → verify → preserve evidence
```

Cortex is designed to compose an existing local-first tool ecosystem:

- **mcphub**: MCP gateway, agent-harness synchronization, lazy tool exposure, usage telemetry, and scoped routing.
- **codemap**: structural code graph, symbols, call paths, impact analysis, test relationships, annotations, and index freshness.
- **vecgrep**: semantic and hybrid code search, similarity retrieval, related-file discovery, and semantic memory.
- **cairntrace**: behavioral browser specifications and browser-run evidence.
- **glyphrun**: black-box terminal/TUI specifications and artifact packs.
- **file.cheap / fcheap**: durable evidence stash, search, restore, diff, and codebase connection.
- **tinyvault / tvault**: local secret management and least-privilege secret injection.

Cortex is intentionally narrower than a fully autonomous agent framework. It is a **reasoning-control and evidence-control layer** for agentic coding work.

---

## 1. Problem Statement

### 1.1 The failure mode of tool-rich coding agents

A capable coding model often fails not because it cannot write a function, but because it cannot maintain reliable epistemic discipline through a multi-step task.

Common behavior:

```text
user report
  → broad search
  → read a few files
  → infer a likely cause
  → edit several files
  → run one command
  → interpret partial success as proof
  → lose the trail of why each decision was made
```

This behavior creates four expensive problems:

1. **Tool-context dilution**: exposing every MCP tool forces the model to choose among too many overlapping actions and burns context on schemas it will not use.
2. **Evidence collapse**: search results, logs, screenshots, and test output are treated as transient chat text rather than durable evidence.
3. **Hypothesis/proof confusion**: the model edits before it has established a falsifiable explanation of the failure.
4. **Verification substitution**: green compilation or unit tests are mistaken for proof that browser or terminal behavior works.

### 1.2 The goal

Cortex must make tool use **stateful, bounded, inspectable, and evidence-driven** without becoming an autonomous black box.

The model remains the planner and author. Cortex ensures that the model's claims have an evidence trail and that its actions occur inside a declared operating envelope.

---

## 2. Design Goals

### 2.1 Functional goals

Cortex SHALL:

1. Create a durable case file for each non-trivial engineering task.
2. Normalize disparate tool outputs into one evidence schema.
3. Route each question to the smallest appropriate tool set.
4. Keep discovery, structural reasoning, execution, and persistence as distinct phases.
5. Require a change boundary before file mutation is considered verified work.
6. Select verification based on the visible system surface:
   - code structure;
   - browser behavior;
   - terminal behavior;
   - artifacts/pipelines;
   - mixed systems.
7. Preserve useful evidence and outcome summaries across sessions.
8. Keep secret values outside model context.
9. Work locally and degrade safely when optional tools are unavailable.
10. Expose a compact, stable MCP interface suitable for weaker or context-constrained models.

### 2.2 Non-goals

Cortex SHALL NOT, in v0.1:

- be a replacement for `mcphub`;
- be a generic workflow scheduler for arbitrary businesses;
- require a cloud database, hosted queue, or external account;
- silently commit, deploy, publish, or modify a remote system;
- substitute semantic retrieval for structural code analysis;
- treat a model's natural-language explanation as evidence;
- store raw secrets in case files, tool outputs, artifact metadata, or model-visible logs;
- become a giant “one tool does everything” wrapper that hides useful underlying tool affordances.

---

## 3. Core Concepts

### 3.1 Agent kernel

An **agent kernel** is the runtime contract between the model and the tool ecosystem.

It owns:

- task lifecycle;
- state and persistence;
- tool-routing policy;
- result normalization;
- safety gates;
- evidence scoring;
- verification policy;
- model-visible summaries.

It does **not** own:

- language understanding;
- coding style decisions;
- architectural taste;
- the source-of-truth implementation details of downstream tools.

### 3.2 Case file

A **case file** is the durable state of one task. It contains the task goal, repository identity, branch/commit context, evidence records, hypotheses, planned changes, verification records, artifact references, and final outcome.

A case file is the kernel's working memory. It is not a transcript.

### 3.3 Evidence

Evidence is a structured claim backed by a locatable source. A model statement without a source is an **assertion**, not evidence.

Evidence can come from:

- structural code graph results;
- code search result snippets;
- source locations;
- test output;
- browser-run assertions;
- terminal-run assertions;
- artifacts;
- git diff/review analysis;
- human-provided facts.

### 3.4 Hypothesis

A hypothesis is a testable explanation for a task or failure. It must state:

- the proposed cause;
- supporting evidence;
- confidence;
- what result would disprove it.

### 3.5 Change boundary

A change boundary is the declared set of files, symbols, configuration keys, or contracts expected to change. It prevents the agent from quietly widening scope.

A boundary is not a hard security boundary. It is a reasoning and review guardrail.

### 3.6 Verification surface

The verification surface is the user-visible system layer affected by a change:

| Surface | Primary verifier |
|---|---|
| code graph / change impact | codemap |
| browser interaction or UI behavior | cairntrace |
| terminal CLI/TUI behavior | glyphrun |
| artifact content and reproducibility | fcheap |
| secret-dependent runtime | tvault-assisted execution |

---

## 4. System Architecture

### 4.1 Component topology

```text
                               ┌─────────────────────┐
                               │  MiniMax / Agent     │
                               │  planner + author    │
                               └──────────┬──────────┘
                                          │ MCP
                         small stable interface only
                                          │
                               ┌──────────▼──────────┐
                               │       Cortex         │
                               │    agent kernel      │
                               │──────────────────────│
                               │ task state           │
                               │ phase machine        │
                               │ evidence ledger      │
                               │ routing policy       │
                               │ verification policy  │
                               │ redaction            │
                               └───────┬───────┬──────┘
                                       │       │
                       ┌───────────────┘       └───────────────┐
                       ▼                                       ▼
              ┌─────────────────┐                     ┌─────────────────┐
              │ intelligence    │                     │ execution       │
              │ codemap         │                     │ cairntrace      │
              │ vecgrep         │                     │ glyphrun        │
              └─────────────────┘                     └─────────────────┘
                       │                                       │
                       └──────────────┬────────────────────────┘
                                      ▼
                         ┌─────────────────────┐
                         │ persistence/security │
                         │ fcheap + tvault      │
                         └─────────────────────┘
                                      │
                         ┌────────────▼─────────────┐
                         │ mcphub ingress / gateway  │
                         │ sync, lazy tools, metrics │
                         └──────────────────────────┘
```

### 4.2 Responsibility boundaries

#### MiniMax or the agent harness

The model SHALL:

- understand the user's intent;
- choose among Cortex's high-level actions;
- interpret returned evidence;
- propose hypotheses and implementation changes;
- explain uncertainty;
- write or modify code through its normal environment.

The model SHALL NOT be expected to remember every raw tool result or manually coordinate all specialist tools.

#### Cortex

Cortex SHALL:

- maintain the case file;
- choose or recommend tool sequences;
- normalize results;
- enforce phase transitions;
- evaluate the declared boundary against observed diff impact;
- choose verification required for a claim;
- persist summaries and references;
- redact sensitive material before a result returns to the model.

#### mcphub

`mcphub` SHALL remain the MCP gateway and agent-config control plane.

It SHALL:

- synchronize MCP configurations across harnesses;
- proxy selected downstream MCP servers;
- support direct and gateway modes;
- support lazy exposure and pinned tools;
- record gateway usage statistics;
- scope access to configured server/tool subsets;
- inject secrets into downstream server processes via `tvault` where configured.

`mcphub` SHALL NOT own task lifecycle, case-file semantics, or verification logic.

#### Specialist tools

Specialist tools SHALL retain their domains:

- `vecgrep` discovers by semantic/keyword meaning.
- `codemap` explains structure, ownership, call paths, and impact.
- `cairntrace` proves browser-visible behavior.
- `glyphrun` proves terminal-visible behavior.
- `fcheap` stores and searches evidence packs.
- `tvault` releases narrowly scoped secrets to execution processes without showing values to the model.

---

## 5. Why This Makes a Model More Effective

Cortex does not increase a model's base intelligence. It reduces the number of failure opportunities between thought and verified outcome.

### 5.1 It reduces choice overload

Instead of selecting from dozens of raw tools, the model follows a small task workflow:

```text
open or deliberately start a task
investigate
plan
begin change
verify
remember
inspect / decide / hand off when needed
```

The kernel then translates the action into specialized calls.

### 5.2 It separates retrieval from proof

Semantic search is good at finding candidates. It is not proof of ownership or behavior.

```text
vecgrep finds likely code
  ↓
codemap resolves structure and impact
  ↓
cairntrace / glyphrun proves behavior
```

This reduces the common “I found a string, therefore I fixed the system” hallucination.

### 5.3 It makes claims falsifiable

Each important hypothesis must include a disproof test. The model is no longer allowed to merely say “this is probably the issue.”

### 5.4 It bounds scope

A declared boundary makes unrelated edits visible as scope drift instead of invisible agent improvisation.

### 5.5 It turns ephemeral runs into memory

A failed browser run, a terminal transcript, and a relevant code symbol become linked evidence rather than three unrelated blobs that vanish from the context window.

---

## 6. Task Lifecycle

### 6.1 States

```text
new
  ↓
orienting
  ↓
investigating
  ↓
planned
  ↓
changing
  ↓
verifying
  ↓
persisting
  ↓
complete

Terminal alternatives:
blocked | abandoned

Resumable pause:
needs_human_decision → the exact active phase stored in pausedFrom
```

### 6.2 Transition rules

| From | To | Required condition |
|---|---|---|
| `new` | `orienting` | a goal and workspace/repository reference exist |
| `orienting` | `investigating` | repository identity and tool health are known or explicitly unavailable |
| `investigating` | `planned` | at least one hypothesis and a verification plan exist |
| `planned` | `changing` | change boundary is declared; the standard path is `begin-change`, which atomically leases ownership to an actor |
| `changing` | `verifying` | diff/change record exists, or an intentional no-op is explicitly acknowledged |
| `verifying` | `persisting` | required verification has passed or failure is explicitly recorded |
| `persisting` | `complete` | summary, evidence references, and uncertainty are saved |
| any non-terminal | `needs_human_decision` | one bounded question with at least two consequential options is persisted; `pausedFrom` records the exact resume target |
| `needs_human_decision` | `pausedFrom` | the selected option and responder are persisted as evidence; crash recovery may finish this transition after an already-written answer |
| any | `blocked` | a required dependency, permission, or secret is missing and the case cannot safely continue |

For backward compatibility, `verify` may still advance an unleased legacy task from `planned` to
`changing` and then `verifying`. New agent workflows SHALL call `begin-change` explicitly so actor
ownership is visible and competing writers are rejected.

### 6.3 Invariants

Cortex SHALL preserve these invariants:

1. A task cannot be `planned` without a hypothesis and disproof path.
2. Normal completion requires the canonical assessment to be `verified`; a partial/unverified/failed completion requires an explicit, durable acknowledgment and must retain that non-green assessment.
3. Every evidence record must have an origin and timestamp.
4. No secret value may enter an evidence record.
5. A verification pass must name the exact claim it supports.
6. A code mutation outside the declared boundary must trigger a scope-drift warning.
7. A typed claim bound to a contract cannot pass unless that exact verifier/contract ran.
8. A no-op acknowledgment permits verification of an intentional no-diff task but is not proof.
9. Every task view SHALL derive `verified | partial | failed | unverified` from the same current receipts.
10. Case snapshot writes use optimistic revisions; a stale writer cannot overwrite a newer snapshot.
11. At most one unexpired, unreleased change lease may own a case, and verification of a leased case must name its actor.

---

## 7. Tool Routing Policy

### 7.1 Routing matrix

| User/task signal | First tool | Follow-up tool | Why |
|---|---|---|---|
| vague feature/behavior description | vecgrep | codemap | discover by meaning, then resolve structure |
| known function/type/route/file | codemap | tests / verifier | graph questions are structural |
| “what breaks if I change this?” | codemap impact/callers/tests | review | needs blast radius, not semantic similarity |
| visual/browser bug | cairntrace + vecgrep | codemap | prove observed failure, then map UI evidence to code |
| CLI/TUI issue | glyphrun + vecgrep | codemap | prove terminal behavior, then map to implementation |
| old trace/video/log/screenshot | fcheap search/connect | vecgrep/codemap | recover prior evidence and link it to code |
| secret-dependent operation | tvault availability/use | tool execution | model must not receive secret values |
| changed diff needs review | codemap review/impact | behavioral verifier | structural review precedes behavior claim |

Routing is causal: discovery (vecgrep/vidtrace) runs first, the top deduplicated file/symbol
candidates are deduplicated and fed into the structural follow-up, and the structural evidence
records `derivedFrom` provenance linking it back to the discovery candidate that produced it.
When discovery yields no locatable candidates, the question itself falls through to codemap.

### 7.2 Explicit negative routing rules

Cortex SHOULD avoid:

- calling `vecgrep` when a known symbol can be resolved directly with `codemap`;
- calling a browser verifier when the behavior is terminal-only;
- calling a terminal verifier when the behavior is browser-only;
- generating a full semantic index when a precise keyword or symbol query suffices;
- stashing every trivial command output into `fcheap`;
- fetching secret values into model-visible output;
- activating a large MCP server merely to call one low-cost local tool.

### 7.3 Tool budget

Each workflow receives a budget. The purpose is not cost accounting alone; it prevents frantic indiscriminate tool use.

Default v0.1 budget:

```yaml
budget:
  max_parallel_calls: 3
  max_investigation_rounds: 3
  max_raw_output_bytes_per_tool: 32768
  max_evidence_items_returned: 12
  max_candidate_files_returned: 8
  max_auto_retries_per_tool: 1
```

Cortex MAY exceed the budget only when the model or user explicitly requests deeper investigation, and the case file MUST record the reason.

---

## 8. Data Model

### 8.1 Case file layout

```text
$XDG_STATE_HOME/cortex/            # ~/.local/state/cortex by default
  sessions/
    <repo-slug>/                   # e.g. cortex, vecgrep, myapp
      task_01J9Q5Y8B0M6D2/
        case.json
        evidence.jsonl
        hypotheses.json
        plan.json
        verification.json
        decisions.json
        commands.jsonl
        phases.jsonl
        summary.md
        raw/
        refs/
          artifacts.json
          annotations.json
```

Sessions default to a **central, XDG-organized** location —
`$XDG_STATE_HOME/cortex/sessions/<repo-slug>/` — so every session across every repository is
visible and auditable in one place (case files record machine-local workspace paths and git refs,
so they are XDG *state*, not portable data). Cortex's config and cache follow the XDG spec too
(`$XDG_CONFIG_HOME/cortex`, `$XDG_CACHE_HOME/cortex`); `$CORTEX_HOME` or a pre-existing `~/.cortex`
collapses all three into one directory for single-dir installs.

Repository-local storage stays available as an opt-in: set `cases_dir: .cortex/cases` (or
`CORTEX_CASES_DIR`) to keep a project's evidence next to its code, and a pre-existing
`<workspace>/.cortex/cases` is honored automatically so upgrading never strands active work.
Long-term archives may be copied or stashed through `fcheap`.

### 8.2 Case file schema

```json
{
  "schemaVersion": 1,
  "revision": 7,
  "id": "task_01J9Q5Y8B0M6D2",
  "createdAt": "2026-07-06T14:00:00Z",
  "goal": "Fix post-login checkout return URL",
  "mode": "change",
  "status": "verifying",
  "actor": "agent-auth",
  "parentTaskId": "task_01J9Q4",
  "childTaskIds": ["task_01J9QC"],
  "idempotencyKey": "checkout-redirect",
  "workspace": {
    "root": "/Users/abdul/projects/liftclub",
    "repository": "liftclub",
    "branch": "fix/oauth-return-url",
    "commitBefore": "7e1f4d2"
  },
  "surfaces": ["code", "browser"],
  "changeBoundary": {
    "files": [
      "src/auth/callback.ts",
      "src/auth/return-url.ts",
      "src/auth/callback.test.ts"
    ],
    "symbols": ["HandleCallback", "ResolveReturnURL"],
    "reason": "Return URL state is produced and consumed here."
  },
  "changeLease": {
    "actor": "agent-auth",
    "acquiredAt": "2026-07-06T14:10:00Z",
    "renewedAt": "2026-07-06T14:20:00Z",
    "expiresAt": "2026-07-06T14:35:00Z"
  },
  "verificationRequired": [
    "codemap_review",
    "command:unit",
    "cairntrace_flow"
  ]
}
```

`revision` is a compare-and-swap version incremented on every successful case snapshot update.
Actor, idempotency, parent/child, and lease fields are optional coordination metadata; older cases
without them remain valid. While a decision is pending, `status` is `needs_human_decision` and
`pausedFrom` stores the active phase that must be restored.

### 8.3 Evidence record

```json
{
  "id": "ev_01J9Q6",
  "timestamp": "2026-07-06T14:03:00Z",
  "kind": "code_symbol",
  "source": {
    "tool": "codemap",
    "runId": "cm_84dc",
    "uri": "codemap://symbol/HandleCallback"
  },
  "claim": "HandleCallback redirects to '/' when signed state lacks returnTo.",
  "location": {
    "file": "src/auth/callback.ts",
    "startLine": 42,
    "endLine": 61,
    "symbol": "HandleCallback"
  },
  "confidence": "high",
  "sensitivity": "normal",
  "rawRef": "case://task_01J9Q5Y8B0M6D2/raw/raw_01J9Q6"
}
```

### 8.4 Hypothesis schema

```json
{
  "id": "hyp_01J9Q7",
  "statement": "OAuth state does not retain returnTo, so HandleCallback applies its '/' fallback.",
  "supports": ["ev_01J9Q6", "ev_01J9Q8"],
  "confidence": "medium",
  "disproveBy": {
    "kind": "behavioral_run",
    "tool": "cairntrace",
    "contract": "login-from-checkout-returns-to-checkout"
  },
  "status": "active"
}
```

### 8.5 Verification record

```json
{
  "id": "vr_01J9QA",
  "batchId": "vb_01J9Q9",
  "claimId": "checkout-return",
  "claim": "After OAuth login initiated from checkout, the browser returns to checkout.",
  "surface": "browser",
  "purpose": "named_claim",
  "contract": "specs/checkout-return.yml",
  "actor": "agent-auth",
  "tool": "cairntrace",
  "status": "passed",
  "revision": "74c6e03d…",
  "dirtyDigest": "sha256:9be0…",
  "binding": "bound",
  "timestamp": "2026-07-06T14:27:00Z"
}
```

Verifier-run receipts use `purpose: "verifier_run"`, name the planning `requirement`, and carry
direct evidence/artifact/version fields. Named-claim receipts use `purpose: "named_claim"` and map
one stable `claimId` to the verifier and required exact `contract`. Both bind to the verified HEAD
and dirty-tree digest so later edits make the proof stale. Every new `verify` call writes one
identified receipt batch atomically. A batch is `bound` only when the case revision, change owner,
HEAD, and dirty-tree digest stay stable from verifier start through commit. Otherwise definitive
pass/fail results are downgraded to inconclusive and the latest unbound batch masks older proof.
Facts and bounded raw blobs produced by that call SHALL be staged with the receipts and verifying
case revision; a failed case/lease CAS publishes none of them. Audit commands retain the actor
captured when each invocation began. Public evidence/receipt/raw readers SHALL recover interrupted
bundle transactions under the task lock before returning data. Code annotations derived from a
behavioral run SHALL execute only after the bundle commits with `binding: "bound"`.

#### Decision record

`decisions.json` is a snapshot list. Each request contains a stable ID, one question, requester,
timestamp, and at least two unique `{id, label, consequence}` options. An answered record adds the
selected option ID, responder, answer timestamp, and the `human_report` evidence ID created from the
choice. Decision text is redacted and may be marked sensitive; it can inform reasoning but cannot
satisfy verification by itself.

### 8.6 Confidence policy

Confidence describes the strength of a conclusion, not the model's rhetorical certainty.

| Confidence | Meaning | Minimum support |
|---|---|---|
| high | direct evidence confirms the claim | a primary source plus successful relevant verification |
| medium | evidence strongly suggests the claim but one relevant layer remains unverified | code evidence or partial behavioral evidence |
| low | plausible lead requiring more evidence | discovery/search result or untested reasoning |
| unknown | no reliable conclusion | only user report or model inference |

Cortex MUST NOT convert a low-confidence hypothesis into high confidence merely because the model restates it.

---

## 9. Evidence Ledger and Provenance

### 9.1 Evidence requirements

Every evidence record SHALL include:

- a stable ID;
- timestamp;
- source tool or human origin;
- human-readable claim;
- a location, artifact URI, or output reference;
- confidence;
- sensitivity classification.

### 9.2 Evidence classes

```text
code_location
code_graph
semantic_search
browser_run
terminal_run
unit_test
build
lint
artifact
human_report
model_inference
```

`model_inference` is permitted in a case file but MUST NOT satisfy a verification requirement by itself.

### 9.3 Contradiction handling

Cortex SHALL allow evidence to contradict a hypothesis.

When a contradiction occurs:

1. retain the prior hypothesis;
2. mark it `challenged` or `rejected`;
3. append the contradicting evidence;
4. request a revised hypothesis rather than silently overwriting history.

This preserves the actual investigation path and lets later tools learn from failed lines of reasoning.

---

## 10. MCP Surface

### 10.1 Principles

The Cortex MCP server SHALL expose a small, profiled public surface. The default `agent` profile
contains 17 lifecycle, collaboration, evidence, artifact-preview, and recall tools. The `all`
profile contains 24 tools by adding exactly seven cross-repository operator operations:
`list_tasks`, `sessions`, `timeline`, `metrics`, `overview`, `archive`, and `unarchive`.

### 10.2 Public tools

#### `cortex_start_task`

Deliberately creates a fresh case file and performs lightweight orientation. Retry-sensitive
agents SHOULD use `cortex_open_task` instead.

```json
{
  "goal": "Fix post-login checkout redirect",
  "workspace": "/Users/abdul/projects/liftclub",
  "mode": "change",
  "surfaces": ["code", "browser"],
  "risk": "medium"
}
```

Returns task ID, workspace identity, detected capability health, and recommended next action.

#### `cortex_open_task`

Idempotently resumes matching work or starts it once. `idempotencyKey` is the strongest identity
and returns its case even after completion. Without a key, Cortex resumes the newest active case
with the same normalized goal, mode, workspace, and current branch. On creation, optional `actor`
and `parentTaskId` record coordination and same-workspace delegation; a resume returns existing
metadata unchanged.

```json
{
  "goal": "Fix post-login checkout redirect",
  "workspace": "/Users/abdul/projects/liftclub",
  "mode": "change",
  "surfaces": ["code", "browser"],
  "actor": "agent-auth",
  "parentTaskId": "task_01J9Q4",
  "idempotencyKey": "checkout-redirect"
}
```

#### `cortex_investigate`

Routes a question through the appropriate discovery and structural tools, records returned evidence, and returns a bounded investigation summary.

```json
{
  "taskId": "task_01J9Q5Y8B0M6D2",
  "question": "Where is the return URL written and consumed during OAuth login?",
  "surfaces": ["code"],
  "depth": "standard"
}
```

#### `cortex_plan`

Stores hypotheses, change boundary, and verification plan. This is a planning gate, not a code generator.

```json
{
  "taskId": "task_01J9Q5Y8B0M6D2",
  "hypotheses": [
    {
      "statement": "returnTo is discarded before callback completion",
      "supports": ["ev_01J9Q6"],
      "disproveBy": "Run login-from-checkout browser contract"
    }
  ],
  "files": ["src/auth/callback.ts", "src/auth/return-url.ts"],
  "symbols": ["HandleCallback", "ResolveReturnURL"],
  "boundaryReason": "Return URL state is produced and consumed here.",
  "verification": [
    "codemap_review",
    "command:unit",
    "cairntrace_flow"
  ],
  "uncertainty": "State signing may also strip returnTo"
}
```

#### `cortex_begin_change`

Atomically acquires an expiring lease and enters `changing`. `actor` is required; `ttl` defaults to
15 minutes and accepts one second through one hour. A same-owner retry is idempotent and an
explicit TTL renews its heartbeat. A different active owner is rejected.

```json
{
  "taskId": "task_01J9Q5Y8B0M6D2",
  "actor": "agent-auth",
  "ttl": "15m"
}
```

#### `cortex_verify`

Runs the required verification policy and returns whether the named claims are supported.

```json
{
  "taskId": "task_01J9Q5Y8B0M6D2",
  "actor": "agent-auth",
  "claimSpecs": [
    {
      "id": "checkout-return",
      "statement": "Users who login from checkout return to checkout.",
      "surface": "browser",
      "verifier": "cairntrace",
      "contract": "specs/checkout-return.yml"
    },
    {
      "statement": "Repository unit tests pass.",
      "surface": "code",
      "verifier": "command:unit",
      "contract": "unit"
    }
  ],
  "browserSpec": "specs/checkout-return.yml"
}
```

`claimSpecs` is preferred over legacy free-text `claims`. Each typed claim has an explicit surface
and required exact spec/check/capability contract; the verifier may default from the surface. A
typed claim is `not_run` unless that exact run occurred. Code claims may use only a
`command:<name>` declared in repository configuration. `noOpAcknowledged` permits an intentional
no-diff change to enter verification but creates no pass.

#### `cortex_remember`

Persists a concise, provenance-rich conclusion to the local semantic memory and optionally stores artifacts through `fcheap`.

```json
{
  "taskId": "task_01J9Q5Y8B0M6D2",
  "outcome": "OAuth return URL was dropped from signed state; fixed and browser-verified.",
  "importance": 0.8,
  "tags": ["liftclub", "auth", "oauth", "checkout"]
}
```

#### `cortex_status`

Returns task phase, unresolved hypotheses, scope drift, required verification, and tool health.

```json
{
  "taskId": "task_01J9Q5Y8B0M6D2",
  "detail": "standard"
}
```

Status includes case revision, actor/linkage/lease, pending decision, structured continuation
actions, stale and missing verification, and the canonical
`verified | partial | failed | unverified` outcome.

#### Collaboration, evidence, and recall tools (`agent`, `all`)

| Tool | Contract |
|---|---|
| `cortex_resolve` | confirm, challenge, or reject a hypothesis with a reason and optional evidence IDs; append history |
| `cortex_note` | append redacted observation/decision/constraint/handoff context as low/medium `human_report`; never proof by itself |
| `cortex_request_decision` | pause on one question with at least two option IDs, labels, and explicit consequences |
| `cortex_answer_decision` | persist an answer/responder and resume `pausedFrom`; `resume=true` repairs an already-written answer after a crash |
| `cortex_handoff` | return the canonical state projection with revision/actor/linkage/lease metadata, at most 20 recent non-sensitive evidence facts, current non-sensitive verifier runs and same-state named claims, decisions, assessment, and actions; sensitive record content is omitted with a warning while a pending sensitive decision retains only identity/status; the serialized packet has a 128 KiB hard cap and retains identity/pending decision/continuation when trimming; optional workspace fallback finds repo-local/custom stores; no raw output |
| `cortex_abort_task` | stop an active task without deleting evidence; release any active lease |
| `cortex_read_evidence` | return a full evidence record by ID |
| `cortex_read_artifact` | bounded task-owned/task-referenced preview; safe relative path; default 32768 bytes, hard cap 131072; binary requires opt-in |
| `cortex_recall_cases` | best-effort prior resolved cases/disproofs; returned as low-confidence leads |

#### Operator-only tools (`all`)

`cortex_list_tasks`, `cortex_sessions`, `cortex_timeline`, `cortex_metrics`, `cortex_overview`,
`cortex_archive`, and `cortex_unarchive` are intentionally excluded from the default agent profile.

### 10.3 Shared result envelope

Lifecycle and mutation tools SHALL return the same outer envelope. Read/index/operator tools may
return their documented structured projection directly.

```json
{
  "ok": true,
  "taskId": "task_01J9Q5Y8B0M6D2",
  "phase": "investigating",
  "summary": "Found two callback paths and one existing browser flow.",
  "facts": [
    {
      "id": "ev_01J9Q6",
      "claim": "HandleCallback applies '/' fallback when returnTo is missing.",
      "confidence": "high",
      "source": "codemap"
    }
  ],
  "hypotheses": [
    {
      "id": "hyp_01J9Q7",
      "statement": "OAuth state loses returnTo before callback completion.",
      "confidence": "medium",
      "status": "active"
    }
  ],
  "warnings": [],
  "nextActions": [
    "Run existing browser contract.",
    "Inspect signed state creation path."
  ],
  "actions": [
    {
      "tool": "cortex_plan",
      "command": "cortex plan task_01J9Q5Y8B0M6D2",
      "reason": "declare hypotheses, disproof paths, boundary, and verification",
      "arguments": {"taskId": "task_01J9Q5Y8B0M6D2", "workspace": "/Users/abdul/projects/liftclub"},
      "inputs": ["hypotheses", "uncertainty"],
      "blockedBy": ["insufficient evidence"]
    }
  ],
  "artifacts": [],
  "rawAvailable": true
}
```

`nextActions` is the human-readable compatibility field. `actions` is authoritative for machine
continuation: `tool` and `command` identify the operation, `arguments` contains known values,
`inputs` lists values the caller must still supply, `reason` explains the recommendation, and
`blockedBy` names unresolved gates. Clients SHALL NOT need to parse prose to continue a case.
Every task-scoped action SHALL carry the case workspace so it remains invokable outside the
originating cwd. Its human-facing CLI command SHALL pin that workspace and shell-quote
case-derived arguments. A begin-change action SHALL carry the explicit default lease TTL. Interrupted
`new`/`orienting` and half-committed decision request/answer states SHALL project a retry-safe
repair action using their durable inputs.
An MCP lifecycle rejection SHALL preserve this structured envelope with `ok: false` and set the
protocol result's `isError` flag. If a kernel error accompanies a populated envelope, the
transport SHALL retain the envelope rather than replacing it with unstructured error text.

### 10.4 Raw result retrieval

Raw downstream output SHALL NOT be sent by default. Cortex exposes:

```text
cortex_read_evidence(taskId, evidenceId)
cortex_read_artifact(taskId, ref, path?, maxBytes?, allowBinary?)
```

Artifact reads are previews, not dumps. A case raw ref SHALL embed the exact requested task ID; an
fcheap ref SHALL already appear in that task's artifact evidence or verification receipts. Paths
SHALL be canonical, relative, traversal-free, and symlink-free. Discovery SHALL reject more than
512 walked entries or 100 regular files. Text is redacted; binary SHALL be rejected unless
`allowBinary` is explicit, then returned as bounded base64 marked sensitive. `maxBytes` defaults to
32 KiB and is hard-capped at 128 KiB. The response reports the selected stash file, encoding,
truncation, and returned byte count.

---

## 11. Adapter Layer

### 11.1 Why adapters exist

Cortex must not tightly couple its workflow engine to a particular MCP schema or CLI flag. Each downstream integration is an adapter.

Adapters may use direct CLI JSON output in v0.1 and MCP transport in later versions. The workflow layer only knows the normalized result envelope.

### 11.2 Go interface

```go
package adapters

import "context"

type Capability string

const (
    CapabilityDiscover   Capability = "discover"
    CapabilityStructure  Capability = "structure"
    CapabilityBrowser    Capability = "browser"
    CapabilityTerminal   Capability = "terminal"
    CapabilityArtifacts  Capability = "artifacts"
    CapabilitySecrets    Capability = "secrets"
)

type Request struct {
    TaskID    string
    Operation string
    Input     map[string]any
}

type Result struct {
    Tool      string
    Operation string
    Status    string
    Summary   string
    Evidence  []Evidence
    Artifacts []ArtifactRef
    Warnings  []string
    RawRef    string
}

type Adapter interface {
    Name() string
    Capabilities() []Capability
    Health(context.Context) error
    Execute(context.Context, Request) (Result, error)
}
```

### 11.3 Required adapters

| Adapter | Input domain | Output role |
|---|---|---|
| `codemap.Adapter` | known symbols, files, impacts, tests, diff | structural evidence |
| `vecgrep.Adapter` | concepts, behavior descriptions, similarity | candidate discovery |
| `cairntrace.Adapter` | browser contracts, runs, artifacts | browser verification evidence |
| `glyphrun.Adapter` | terminal contracts, runs, artifacts | terminal verification evidence |
| `fcheap.Adapter` | stash, search, diff, connect | durable artifact evidence |
| `tvault.Adapter` | availability and scoped execution | secret-safe capability decision |
| `git.Adapter` | branch, commit, diff, status | workspace and scope evidence |

### 11.4 Adapter discipline

Adapters MUST:

- validate inputs;
- apply timeouts;
- capture stderr separately from evidence;
- redact configured sensitive values;
- produce a machine-readable raw reference;
- return a bounded summary;
- identify whether a result is authoritative, partial, or unavailable.

Adapters MUST NOT:

- silently retry destructive actions;
- emit raw secrets;
- convert ambiguous output into a high-confidence conclusion;
- mutate the workspace unless explicitly called by a mutation-capable workflow.

---

## 12. Tool Integration Details

### 12.1 mcphub integration

Cortex should be registered in `mcphub.yaml` like any other MCP server. MiniMax should normally see Cortex directly, with raw specialist tools either hidden behind `mcphub` lazy exposure or selectively pinned.

Illustrative configuration:

```yaml
version: 1
expose: lazy

servers:
  cortex:
    command: cortex
    args: [mcp]
    enabled: true
    description: Evidence-guided agent kernel
    tags: [kernel, orchestration]

  codemap:
    command: codemap
    args: [serve]
    enabled: true
    description: Structural code intelligence
    tags: [code, structure]

  vecgrep:
    command: vecgrep
    args: [serve, --mcp]
    enabled: true
    description: Semantic code search
    tags: [code, retrieval]

  cairntrace:
    command: cairn
    args: [mcp]
    enabled: true
    description: Browser behavior verification
    tags: [browser, verification]

  glyphrun:
    command: glyph
    args: [mcp]
    enabled: true
    description: Terminal behavior verification
    tags: [terminal, verification]

  fcheap:
    command: fcheap
    args: [mcp, serve]
    enabled: true
    description: Artifact persistence and evidence search
    tags: [evidence, artifacts]

  tvault:
    command: tvault
    args: [mcp]
    enabled: false
    description: Secrets management; model-visible values forbidden
    tags: [secrets]

groups:
  engineering: [cortex, codemap, vecgrep, cairntrace, glyphrun, fcheap]
  code-intel: [codemap, vecgrep]
  evidence: [cairntrace, glyphrun, fcheap]
```

Recommended pins in lazy mode:

```text
cortex__cortex_open_task
cortex__cortex_investigate
cortex__cortex_plan
cortex__cortex_begin_change
cortex__cortex_verify
cortex__cortex_status
```

The model should be able to discover raw downstream tools when it has a legitimate exceptional need. Cortex should not artificially prevent expert use; it should make the default path sane.

### 12.2 codemap integration

Cortex shall use Codemap as the structural authority for:

- repository index freshness;
- project/symbol overview;
- callers and callees;
- call paths;
- impact/blast radius;
- related tests;
- diff review;
- annotations that link symbols to behavioral evidence.

When a behavior is discovered through browser or terminal evidence, Cortex should attach it to the owning code symbol using Codemap annotations where the symbol can be identified with reasonable confidence.

Cortex must mark index-dependent results as potentially stale whenever Codemap reports stale project state.

### 12.3 vecgrep integration

Cortex shall use Vecgrep for:

- natural-language code discovery;
- hybrid semantic + keyword searches;
- similarity retrieval;
- related-file lookup;
- compact semantic memory recall.

Vecgrep result ranks are candidates, not structural proof. Any candidate used to justify a code change must be validated by source inspection and/or Codemap structure.

Cortex should use `vecgrep` global memory sparingly for durable, reusable facts. Task-specific truth belongs in the case file first.

### 12.4 cairntrace integration

Cortex shall use Cairntrace when the relevant claim is browser-visible.

A browser verification plan should name:

- the user intent;
- the expected outcomes;
- the preconditions;
- whether an existing spec should be run or a draft should be created;
- artifact retention policy;
- which code symbols should be annotated after a successful or meaningful failed run.

Cortex should preserve a reference to the run bundle instead of embedding screenshots, videos, or large timelines in the case file.

### 12.5 glyphrun integration

Cortex shall use Glyphrun when the claim is terminal-visible.

A terminal verification plan should state the intended user workflow and its outcomes, not merely unit-level implementation facts. Specs should preserve the distinction between:

- **contract**: intent + outcomes;
- **repairable path**: interaction steps.

Failure context should be linked through run artifacts, agent context, final screen, diagnostics, or a stored `fcheap` stash.

### 12.6 fcheap integration

Cortex shall use Fcheap as a durable evidence archive.

Recommended artifact-stashing policy:

| Artifact type | Persist by default? | Reason |
|---|---|---|
| passing trivial unit-test output | no | low future value |
| failed browser run | yes | high debugging value |
| failing terminal run | yes | high debugging value |
| bug video / reproduction media | yes | expensive evidence |
| meaningful diff review | optional | save if used to justify a final decision |
| deployment logs with redacted metadata | conditional | useful if no secrets remain |
| large generated build outputs | no | unless they are the failure artifact |

Fcheap references should be stored in the case file as stable artifact IDs. Searches through archived evidence should be treated as historical leads, not proof that current code still behaves the same.

### 12.7 tvault integration

Cortex must treat TinyVault as an execution boundary, not as a content provider.

Permitted model-visible questions:

```text
Is project “github” available?
Can this workflow access the required secret names?
Was the secret injection request granted or denied?
Which non-sensitive capability labels are available?
```

Forbidden model-visible output:

```text
secret values
secret previews
raw environment dumps
command strings containing secrets
unredacted stderr that may contain secrets
```

Execution pattern:

```text
Cortex determines minimum required capability
  → requests scoped secret injection through tvault
  → starts downstream subprocess with injected environment
  → captures/redacts output
  → returns only capability result and non-sensitive evidence
```

---

## 13. Change Control

### 13.1 Planning gate

Before a task enters `changing`, Cortex MUST have:

- one or more active hypotheses;
- a declared change boundary;
- a verification plan;
- an explicit statement of uncertainty.

Change hypotheses SHOULD cite evidence; a missing support ID is surfaced as a warning rather than
a hard gate because a falsifiable hypothesis may precede formal evidence. New workflows then call
`begin-change` with a stable actor before editing.

The CLI attaches supports with the strict one-based repeatable syntax
`--support hypothesis-index=evidence-id[,evidence-id...]`. The kernel validates every supplied ID
against the task's evidence ledger, matching MCP's typed per-hypothesis `supports` field.

### 13.2 Scope-drift detection

After mutation, Cortex compares changed files/symbols with the declared boundary.

Possible results:

```json
{
  "scope": "within_boundary",
  "unexpectedFiles": [],
  "risk": "low"
}
```

```json
{
  "scope": "drift_detected",
  "unexpectedFiles": ["package-lock.json", "src/config/defaults.ts"],
  "risk": "medium",
  "action": "Require plan expansion or revert unrelated changes."
}
```

Scope drift does not automatically fail a task. It prevents accidental expansion from being invisible.

### 13.3 Diff review policy

For medium- and high-risk tasks, Cortex SHALL run structural diff review after changes.

Review questions:

- Which symbols changed?
- Which callers or dependents are affected?
- Which tests cover the changed symbols?
- Did an API, permission boundary, or public contract change?
- Did the change escape the planned boundary?
- Is additional browser/terminal verification now required?

### 13.4 Multi-agent coordination

`open` records an optional non-secret actor, idempotency key, and same-workspace parent. The child
stores `parentTaskId`; the parent stores the child ID. An explicit key returns its case on retry,
including after completion. Without one, only the newest active normalized goal/mode/workspace/
branch match may resume.

`begin-change` acquires a `ChangeLease{actor, acquiredAt, renewedAt, expiresAt, releasedAt?}`. The
default TTL is 15 minutes, with a one-second minimum and one-hour maximum. The same owner may retry;
an explicit TTL renews the heartbeat. A different active owner is rejected. An expired or released
lease may be replaced. Verification of an active lease requires the matching actor; completion and
abort release it.

`case.json` carries an optimistic `revision`. Snapshot saves compare the caller's revision to disk,
then increment on success. Coordination mutations reload and retry bounded conflicts; an arbitrary
stale write MUST NOT silently win. Plan and hypothesis/evidence companion snapshots commit under
the same cross-process task lock and revision check. Lock owners heartbeat while live; stale-lock
recovery atomically reaps only the stale candidate and owner-token checks prevent an old process
from deleting a replacement lock.

Status SHALL stream evidence counts and handoff SHALL retain only its bounded newest shareable
window. Auto-refreshing Show and Studio views SHALL retain bounded recent ledgers and exact totals
from one task-locked composite snapshot; dedicated evidence/timeline commands provide drill-down.
Durable user/model text, collection counts, individual
ledger records, and JSON snapshot files SHALL have hard size bounds; new case state SHALL be
owner-only on platforms with POSIX permissions.

---

## 14. Verification Policy

### 14.1 Claim-to-proof mapping

Every user-facing claim must map to a relevant verifier.

Agents SHOULD submit typed claims with `statement`, explicit `surface`, optional stable `id`,
optional exact `verifier`, and required exact `contract`. The surface chooses the default verifier;
the contract makes the match exact. Legacy free-text claims remain compatible but use heuristic
surface inference and are not preferred.

| Claim | Minimum proof |
|---|---|
| “the function compiles” | compile/build result |
| “the intended handler is changed” | source location + structural review |
| “this refactor does not break callers” | Codemap impact + appropriate tests |
| “the page redirects correctly” | Cairntrace flow outcome |
| “the CLI interaction works” | Glyphrun flow outcome |
| “the artifact contains expected output” | Fcheap artifact inspection/diff |
| “the deployment can access credentials” | Tvault-backed execution result without secret disclosure |

Repositories may define trusted code checks in `cortex.yaml`:

```yaml
verifiers:
  unit:
    argv: ["go", "test", "./..."]
    kind: unit_test
    surface: code
    timeout: 2m
```

Only configured argv may execute: the caller selects `command:unit` but cannot submit or append
executable text. Cortex invokes argv directly without a shell. Names are restricted to
letters/digits/dash/underscore; kinds are `unit_test | build | lint`; v0.1 permits only `code` and
requires a positive timeout. Invalid verifier configuration fails kernel construction closed.
Configured checks join the default verification plan and can be bound by exact typed contracts.
Because repository-configured argv is arbitrary local code, configuration does not authorize
execution. The trusted process launching Cortex MUST set `CORTEX_APPROVE_COMMANDS=1` (or install an
equivalent explicit approver); otherwise Cortex records the requirement as `blocked`. A repository
MUST NOT be able to grant this approval to itself.

### 14.2 Verification statuses

```text
passed
failed
inconclusive
blocked
not_applicable
not_run
```

`not_run` must never be rendered as `passed` in a final summary.

The task-level assessment is separate from an individual receipt and is canonical across remember,
status, metrics, sessions/overview, review, Show, Studio, and handoff:

| Assessment | Rule over the latest current-revision receipts |
|---|---|
| `verified` | at least one proof passed and every required verifier plus named claim is satisfied |
| `partial` | some proof passed, but a required verifier or named claim remains non-passing |
| `failed` | a current verifier run or named claim failed |
| `unverified` | no adequate current proof passed |

A no-op acknowledgment is not evidence and does not alter these rules.

### 14.3 Verification receipts

A verification receipt contains:

- purpose (`verifier_run` or `named_claim`), claim, and optional stable claim ID;
- exact planning requirement and/or contract;
- actor when known;
- verifier and version if known;
- timestamp;
- result;
- artifact location;
- full code revision plus dirty-tree digest;
- notes on limitations.

---

## 15. Memory Strategy

### 15.1 Four layers of memory

| Layer | Storage | Purpose |
|---|---|---|
| working memory | The case file | the current task's state, evidence, and receipts |
| structural memory | Codemap annotations | code-symbol relationships and behavior ownership |
| semantic recall | Vecgrep memory / Fcheap search | cross-session discovery of prior conclusions and artifacts |
| cross-case recall | Veclite recall index | prior resolved hypotheses and definitive receipts as prior disproofs (§15.4) |

### 15.2 What should be remembered

Remember facts that are both reusable and grounded:

- owning symbols for a user-visible flow;
- known fragile integrations;
- the location and meaning of a useful behavioral spec;
- a confirmed failure pattern and its fix;
- environment constraints that reliably affect tests;
- durable decisions and their evidence.

Do not remember:

- raw command dumps;
- ambiguous guesses;
- secret-related details;
- temporary user preferences irrelevant to engineering;
- a large undigested transcript.

### 15.3 Memory item format

```text
repo=liftclub
area=auth
symbol=HandleCallback
behavior=post-login return URL
finding=returnTo must be persisted in signed OAuth state or callback defaults to '/'
evidence=case task_01J9Q5Y8B0M6D2; cairntrace artifact fc_019
confidence=high
commit=7e1f4d2..9c1ee0a
```

### 15.4 Cross-case disproof recall

Resolved hypotheses (rejected/challenged are the gold) and definitive verification receipts are
indexed into a veclite collection as a fourth memory layer, so a weak model stops re-forming the
same wrong theory every session. The kernel indexes at resolve time (immediately, with the
reason) and at remember time (a completion sweep of all resolved hypotheses + definitive
receipts). Every string is redaction-gated; records that trip the redactor or carry the
`sensitive` flag are **excluded** from the cross-repo index, not masked into it. At orient and
investigate, prior related cases surface as low-confidence `model_inference` evidence — prior
disproofs to read before re-deriving a theory. Recall is two-tier: repo-scoped first, then
cross-repo. Best-effort: a missing veclite or unreachable ollama degrades to a warning, never a
hard failure that blocks a phase.

---

## 16. Security and Privacy

### 16.1 Threat model

Cortex assumes that:

- the model can make unsafe or overly broad requests;
- tool output can accidentally contain secrets;
- command execution can have side effects;
- artifact packs can preserve sensitive data;
- local systems may have different trust levels.

### 16.2 Security controls

Cortex SHALL implement:

1. **Secret redaction** before model-visible return.
2. **Least privilege** through `tvault` project-level allowlists.
3. **Action classing**: read-only, workspace mutation, configured execution, external mutation,
   and secreted execution.
4. **Explicit approval integration point** for configured execution, external mutation, commit,
   push, deploy, and publish operations.
5. **Artifact sensitivity labels** to prevent accidental archival of secrets.
6. **No raw environment export** into case files.
7. **Audit entries** for secret-backed execution that record capability name and result, not secret contents.

### 16.3 Action classes

| Class | Examples | Default policy |
|---|---|---|
| read-only | search, inspect, status, graph query | allowed |
| local mutation | edit working tree, generate a spec | allowed only inside active task boundary |
| configured execution | repository-declared test/build/lint argv | requires trusted launcher/harness approval (`CORTEX_APPROVE_COMMANDS=1`) |
| external mutation | send, deploy, publish, remote write | requires explicit user/harness approval |
| secret-backed execution | private registry, authenticated integration | require tvault capability and redaction |

---

## 17. Failure Handling and Degraded Modes

### 17.1 Tool unavailable

If an optional tool is unavailable:

- record a `tool_unavailable` evidence event;
- mark affected verification as `blocked` or `inconclusive`;
- continue with safe alternatives where meaningful;
- never fabricate the unavailable tool's result.

Examples:

- Vecgrep down: use keyword/source navigation but lower discovery confidence.
- Codemap stale: request re-index or mark structural claims stale.
- Cairntrace unavailable: do not claim browser behavior verified.
- Fcheap unavailable: retain local artifact refs and warn that archival did not occur.
- Tvault unavailable: do not attempt secret-backed command execution.

### 17.2 Timeout policy

Default adapter timeouts:

```yaml
timeouts:
  health_check: 5s
  code_search: 15s
  structural_query: 20s
  browser_run: 180s
  terminal_run: 120s
  artifact_save: 60s
  secreted_execution: 120s
```

Timeouts may be overridden per task, but the override must be written to the case file.

### 17.3 Retry policy

- Read-only idempotent queries: one automatic retry on transient process/transport failures.
- Browser/terminal runs: no automatic replay unless the tool identifies infrastructure failure rather than behavioral failure.
- Mutating operations: no automatic retry without an idempotency key or explicit approval.

---

## 18. Observability and Evaluation

### 18.1 What to measure

Cortex should measure outcomes, not merely tool-call volume.

Core metrics:

```text
case completion rate
verified completion rate
mean tools per successful task
tool calls before first evidence
scope-drift rate
verification coverage by surface
reopened/failed-after-complete rate
average raw-output bytes returned to model
unresolved hypothesis rate
memory reuse rate
time in phase (phase latency, from the phase-transition history)
mean time to complete
```

### 18.2 Relationship to mcphub telemetry

`mcphub` already records proxied call counts, errors, latency, and estimated token cost. Cortex should enrich—not duplicate—this by recording task-level meaning:

```text
mcphub: “codemap tool called 8 times”
Cortex: “codemap calls contributed evidence to 3 hypotheses; 2 were verified”
```

### 18.3 Evaluation scenarios

Cortex should be evaluated on a small benchmark of real repositories and task types:

1. known-symbol bug fix;
2. vague user-reported UI bug;
3. terminal/TUI regression;
4. safe refactor with broad impact;
5. old artifact/video investigation;
6. secret-backed local integration;
7. intentionally misleading semantic-search result;
8. stale code index.

Success should require a correct outcome **and** an adequate evidence trail.

### 18.4 Paired baseline evaluation

The scenario harness SHALL also support paired observations for Cortex and an unassisted baseline
on the same task and oracle. Every pair records its baseline protocol explicitly; the calibration
protocol is the same model with direct repository/shell tools and no Cortex case file, gates, or
recall. “Unassisted” MUST NOT remain an implicit moving target.

Each applicable dimension is normalized to 0–100, higher-is-better:

```text
evidence quality
disproof discipline
boundary / scope control
verifier correctness
completion honesty
recovery / resume
cost / latency overhead
```

All quality dimensions have equal default weight. Cost/latency is a lower-weight guardrail
(0.5) and raw Cortex-minus-baseline tool calls, latency, and estimated cost are reported separately.
The quality score excludes cost; overall includes it. Aggregation is a macro-average across cases
so a task with more claims cannot silently dominate the benchmark.

Deterministic fixtures calibrate formulas and scorecard output; they are **not** statistical product
claims. Real recorded repository trials populate the same paired model. The scorer MUST be capable
of reporting a Cortex regression (for example, equal quality with higher overhead). `task eval`
runs both the authored lifecycle scenarios and this paired calibration scorecard.

---

## 19. Reference Workflows

### 19.1 Workflow A: Browser redirect defect

User report:

```text
After OAuth login, a user who started from checkout lands on / instead of /checkout.
```

Kernel workflow:

```text
1. cortex_open_task
   goal: Fix post-login checkout redirect
   surfaces: code, browser
   actor: agent-auth
   idempotencyKey: checkout-redirect

2. orientation
   - inspect git branch and baseline commit
   - query codemap index status
   - check Cairntrace availability

3. investigation
   - Vecgrep: “OAuth callback return URL checkout”
   - Codemap: resolve candidate auth symbols
   - Codemap: inspect callers/callees/impact/tests
   - Fcheap: search prior auth artifacts

4. prove the reported failure
   - run existing Cairntrace flow or create a draft contract

5. plan
   - declare hypothesis
   - define specific files/symbols allowed to change
   - require structural review + unit tests + browser flow

6. begin change
   - cortex_begin_change atomically leases the case to agent-auth
   - agent edits only planned locations

7. verify
   - Codemap diff/impact review
   - configured command:unit check
   - Cairntrace checkout-return flow
   - typed browser claim binds to cairntrace + exact checkout-return spec

8. persist
   - stash meaningful browser artifact with Fcheap
   - annotate owning callback symbol in Codemap
   - write compact semantic memory
```

### 19.2 Workflow B: Terminal regression

User report:

```text
The command appears to finish but leaves the interactive TUI in the wrong state.
```

Kernel workflow:

```text
1. orient repository and target CLI
2. run Glyphrun existing contract or record/scaffold a new one
3. inspect agent_context and terminal frames from the artifact pack
4. use Vecgrep only to discover possible implementation areas
5. use Codemap to identify ownership and blast radius
6. plan a limited change
7. verify with Glyphrun + relevant unit tests + Codemap review
8. stash failure and passing artifacts if useful
```

### 19.3 Workflow C: Refactor with unknown blast radius

User request:

```text
Consolidate duplicated authentication middleware.
```

Kernel workflow:

```text
1. Codemap identifies all candidate symbols, callers, paths, and test relationships.
2. Vecgrep finds semantically similar implementations that may not share names.
3. Cortex creates a boundary and risk level.
4. Agent edits implementation and tests.
5. Codemap review detects unexpected consumers or changed public contract.
6. Browser/terminal verification is selected only if those surfaces are implicated.
```

### 19.4 Workflow D: Investigate a bug video

User provides a recording or old artifact bundle.

```text
1. Fcheap restores or finds the bundle.
2. Cairntrace / vidtrace-derived evidence identifies visible failure moment.
3. Fcheap connect / Vecgrep finds candidate code by UI text and behavior.
4. Codemap resolves candidate symbols and impact.
5. Cortex records provenance so a future agent knows the code conclusion arose from a specific external observation.
```

---

## 20. Model Instruction Contract

The recommended system/developer instruction for MiniMax or another agent should be short and operational.

```text
You are working through Cortex.

For non-trivial engineering work:
1. Open or resume with Cortex; use an idempotency key when a retry is possible.
2. Treat search output as candidates, not proof.
3. Before editing, state a testable hypothesis, change boundary, and verification plan.
4. Claim bounded change ownership with a stable actor before editing.
5. Prefer typed claims bound to the exact surface, verifier, and contract.
6. Do not claim a user-visible behavior works without the relevant behavioral verifier.
7. Keep changes within the declared boundary; expand the plan if scope changes.
8. Preserve important evidence and state uncertainty explicitly.
9. Never request or expose secret values. Use capability checks and scoped execution only.
```

This prompt is intentionally small. Cortex should enforce behavior through state and interfaces, not depend on the model remembering a 500-line sermon.

---

## 21. Implementation Plan

### Milestone 0: operational protocol, no new binary

**Goal:** Improve MiniMax immediately using existing tools and `AGENTS.md` guidance.

Deliverables:

- a repository-level `AGENTS.md` workflow contract;
- `mcphub` gateway mode enabled for the intended agent harness;
- lazy exposure enabled;
- a manually maintained `.cortex/cases/` layout;
- one reference browser workflow and one terminal workflow.

Acceptance criteria:

- agent uses Codemap before structurally risky edits;
- agent uses Cairntrace/Glyphrun for relevant visible claims;
- task summaries include evidence links and uncertainty.

### Milestone 1: Cortex core CLI

**Goal:** Introduce durable task state and normalized outputs.

Commands:

```text
cortex open
cortex start
cortex investigate
cortex plan
cortex begin-change / lease
cortex verify
cortex remember
cortex status
cortex note / decision / handoff
```

Implementation:

- Go CLI using Cobra or existing house style;
- central XDG case files with repo-local opt-in;
- Git adapter;
- initial Codemap and Vecgrep adapters;
- JSON output on every command;
- no direct code mutation through Cortex.

Acceptance criteria:

- `cortex investigate` produces evidence IDs;
- `cortex plan` rejects plans with no disproof path;
- `cortex status` detects missing required verification.

### Milestone 2: behavioral verification adapters

**Goal:** Connect execution evidence to the case file.

Implementation:

- Cairntrace adapter;
- Glyphrun adapter;
- artifact reference ingestion;
- optional Fcheap stash policy;
- Codemap annotation sink.

Acceptance criteria:

- a browser/terminal run produces a verification receipt;
- failed runs can be recovered from a case file;
- relevant symbols can be annotated with evidence references.

### Milestone 3: MCP server and mcphub integration

**Goal:** Provide the compact model-facing interface.

Implementation:

- `cortex mcp` stdio server;
- profiled public tools (17 in `agent`, 24 in `all`);
- shared result envelope;
- machine-readable structured actions;
- `mcphub` configuration template;
- agent scope presets for MiniMax and other harnesses.

Acceptance criteria:

- MiniMax can complete a standard investigation without directly invoking downstream raw tools;
- raw tools are still discoverable for expert escape hatches;
- mcphub telemetry reports Cortex usage cleanly.

### Milestone 4: scope control and review intelligence

**Goal:** Make changes auditable and less chaotic.

Implementation:

- Git diff adapter;
- Codemap diff/impact review integration;
- change-boundary comparison;
- scope-drift warnings;
- risk classification.

Acceptance criteria:

- unrelated edits are detected;
- verification requirements escalate when public contracts or large impacts are touched.

### Milestone 5: secret-safe execution and evaluation

**Goal:** Mature security and empirical quality checks.

Implementation:

- Tvault capability adapter;
- redaction test suite;
- task-level telemetry;
- real-repository evaluation corpus;
- paired unassisted-baseline scoring with quality and overhead dimensions;
- no-secret-leak regression tests.

Acceptance criteria:

- no secret value appears in model-visible output under test;
- Cortex can prove capability availability without displaying credentials;
- benchmark tasks show improved verification coverage and lower tool sprawl compared with raw-tool baseline.

---

## 22. Repository Structure

Current package layout (the dependency direction is `cmd/mcp → kernel → domain/adapters/store/config`):

```text
cortex/
├── cmd/cortex/              # thin Cobra commands; CLI rendering only
├── internal/
│   ├── domain/              # dependency-free case/evidence/plan/lease/decision/receipt types
│   ├── kernel/              # shared lifecycle, coordination, assessment, views, and actions
│   ├── adapters/            # flat adapters sharing exec/redaction plumbing
│   ├── store/casefs/        # revisioned JSON/JSONL case persistence
│   ├── store/redact/        # last-line secret-shape redaction
│   ├── config/              # XDG paths + cortex.yaml policy and command verifiers
│   ├── mcp/server.go        # thin profiled stdio MCP surface
│   ├── tui/board.go         # read-only Studio operator surface
│   └── eval/                # lifecycle scenarios + paired baseline scorecard
├── docs/                    # VitePress product documentation
├── specs/                   # Glyphrun end-to-end contracts
├── AGENTS.md                # contributor source of truth
├── SPEC.md                  # this design contract
└── Taskfile.yml
```

---

## 23. Test Strategy

### 23.1 Unit tests

Cover:

- phase transition rules;
- evidence schema validation;
- confidence handling;
- scope-drift detection;
- redaction;
- routing policy selection;
- retry and timeout logic;
- case-file serialization.
- idempotent open matching and parent/child linkage;
- optimistic revision conflicts and mutually exclusive change leases;
- typed claim/contract routing and canonical assessment outcomes;
- decision pause/answer/crash recovery, structured actions, and bounded artifact previews;

### 23.2 Adapter contract tests

Use fake processes or recorded fixtures for every downstream tool.

Validate that:

- raw output is normalized;
- unavailable tools degrade safely;
- timeouts are represented correctly;
- tool errors do not become false evidence;
- redaction is performed before model output.

### 23.3 End-to-end behavior tests

Use Glyphrun to test Cortex's own CLI/TUI behavior and Cairntrace for any Studio/web surface if added later.

Example acceptance flow:

```text
open a task idempotently
  → investigate with fake code evidence
  → attempt plan without disproof
  → observe rejection
  → submit valid plan
  → begin change with an actor lease
  → create unexpected diff
  → observe scope-drift warning
  → attach a passing verification receipt
  → complete task
```

### 23.4 Security tests

- seed fake credentials into a Tvault-backed process;
- force child stderr and environment-like output to contain them;
- verify model-visible Cortex output is redacted;
- verify case files and Fcheap manifests contain no values.

---

## 24. Open Decisions

The following decisions are intentionally deferred:

1. **Database vs files for active cases**: begin with JSON/JSONL case files; evaluate SQLite after case-link querying becomes painful.
2. **Automatic code mutation**: v0.1 leaves edits to the agent harness; Cortex only controls the evidence/verification lifecycle.
3. **Richer multi-agent roles**: v0.1 has optimistic revisions, actor metadata, parent/child links,
   and one expiring writer lease per case. Shared subplans, role-based permissions, and automatic
   merge/reconciliation remain deferred.
4. **Remote artifacts**: local Fcheap is the default. Remote synchronization should be optional and encrypted.
5. **Formal confidence calculus**: v0.1 uses policy bands, not probabilistic inference.
6. **Autonomous verifier selection**: use explicit routing rules first; learn policies from local telemetry only after enough real cases exist.
7. **Cross-repository case links**: keep repository identity mandatory and avoid broad global memory until relevance controls are proven.

---

## 25. Acceptance Criteria for v0.1

Cortex v0.1 is successful when all of the following are true:

1. A model can idempotently open, investigate, plan, lease/begin, verify, and complete a task through the 17-tool agent profile.
2. Each completed task has a readable case file with evidence IDs and verification receipts.
3. Semantic results are never presented as structural proof without an explicit qualifier.
4. Browser and terminal claims are marked unverified unless Cairntrace or Glyphrun evidence exists.
5. Scope drift is detected after a change.
6. Important evidence can be stashed through Fcheap and linked back to the case file.
7. Codemap annotations can connect behavior evidence to owning symbols.
8. Tvault-backed operations do not expose secret values to the model or case file.
9. `mcphub` can expose Cortex as the default agent interface while keeping specialist tools available through controlled discovery.
10. A real MiniMax workflow uses fewer irrelevant tool calls and produces a more inspectable explanation than the same workflow with raw tools alone.
11. Typed claims bind to exact verifier contracts, and every task view agrees on the canonical verification assessment.
12. Notes, bounded decisions, handoffs, and artifact previews preserve human oversight without turning prose or truncated content into proof.
13. Paired evaluation reports evidence/disproof/scope/verifier/completion/recovery quality together with raw cost/latency overhead against an explicit unassisted protocol.

---

## 26. Glossary

| Term | Meaning |
|---|---|
| agent kernel | runtime layer that governs stateful, evidence-driven tool use |
| case file | durable task record containing state, evidence, plan, verification, and outcome |
| evidence ledger | append-oriented list of claims with provenance |
| hypothesis | falsifiable proposed explanation |
| change boundary | declared set of expected modifications |
| change lease | expiring, actor-owned claim on bounded change work |
| scope drift | observed change outside declared boundary |
| verification receipt | structured proof record for a specific claim |
| verification assessment | canonical task outcome: verified, partial, failed, or unverified |
| structured action | machine-readable next operation with known arguments, missing inputs, reason, and blockers |
| decision | durable bounded human question with explicit options and consequences |
| cognitive action | compact model-facing operation such as investigate or verify |
| adapter | integration layer that normalizes a downstream tool |
| capability | a non-secret permission or available execution affordance |

---

## 27. Source Compatibility Notes

This design is intentionally grounded in the current capabilities of the existing tools:

- `mcphub` supports a single gateway, direct/gateway agent sync modes, lazy exposure, pinning, agent scoping, local SQLite usage intelligence, and Tvault-backed server spawning.
- `codemap` provides local code-graph navigation, impact reasoning, index freshness, `--json` machine output, annotations, and a combined CLI/MCP/TUI surface.
- `vecgrep` provides local-first hybrid semantic/keyword retrieval, related files, similarity search, MCP integration, and a separate semantic memory store.
- `cairntrace` provides browser behavior contracts, execution evidence, and integration points for connecting evidence to Codemap.
- `glyphrun` provides black-box PTY behavior contracts, deterministic terminal evaluation, artifact packs, agent context, and MCP support.
- `fcheap` provides local stash save/restore/search/analyze/diff/connect capabilities, with MCP tools and artifact resources.
- `tvault` provides local encrypted secrets management and MCP support designed to keep secret values out of model context.

The first implementation should adapt to actual command schemas rather than assume that all current CLIs share identical flags. Where an adapter invokes a CLI, it must validate availability via `doctor`, `help`, or machine-readable capability discovery before executing a workflow.

---

## 28. Final Design Rule

The point is not to give MiniMax every instrument in the workshop.

The point is to ensure it knows which instrument it needs, what claim it is trying to establish, what could prove it wrong, and whether the final result actually works in the surface a human uses.

```text
more tools without structure = more ways to get lost

specialized tools + a kernel = accumulated engineering judgment
```
