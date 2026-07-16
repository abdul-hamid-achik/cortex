# Concepts

Cortex is an **agent kernel**: the runtime contract between a model and a tool ecosystem. It owns
task lifecycle, state, tool-routing policy, result normalization, safety gates, evidence scoring,
and verification policy. It does **not** own language understanding, coding style, or the
internals of downstream tools.

## The reasoning loop

Every non-trivial task moves through the same loop, and Cortex enforces it:

```
orient → investigate → form hypotheses → declare a boundary → change → verify → preserve evidence
```

This reduces four expensive failure modes of tool-rich agents:

1. **Tool-context dilution** — too many overlapping tools burn context. Cortex exposes a compact,
   state-aware workflow instead.
2. **Evidence collapse** — search results and logs treated as transient chat text. Cortex records
   them as durable, provenance-bearing evidence.
3. **Hypothesis/proof confusion** — editing before a falsifiable explanation exists. The planning
   gate requires a disproof path.
4. **Verification substitution** — a green compile mistaken for proof that the browser works.
   Verification is matched to the user-visible surface.

## The phase machine

A task lives in exactly one phase. Transitions are legal only along a fixed graph, and each is
guarded by a data precondition.

```
new → orienting → investigating → planned → changing → verifying → persisting → complete

terminal alternatives: blocked | abandoned
resumable pause: needs_human_decision → the exact phase recorded in pausedFrom
```

| Transition | Requires |
|---|---|
| new → orienting | a goal and workspace exist |
| orienting → investigating | repository identity and tool health are known |
| investigating → planned | at least one hypothesis **with a disproof path** + a verification plan |
| planned → changing | a boundary exists; the standard `begin-change` path also claims an actor lease |
| changing → verifying | a diff/change record exists, or an intentional no-op is explicitly acknowledged |
| verifying → persisting | required verification passed, **or** failure is explicitly recorded |
| persisting → complete | summary, evidence references, and uncertainty are saved |

Investigate-only tasks may skip `changing` (planned → verifying). A failed verify can loop back
(verifying → changing). These rules live in `internal/domain/case.go` and are covered by tests —
they are structural, not advisory.

For compatibility, `verify` can still advance a legacy unleased task directly from `planned`.
Agent workflows should use `begin-change` so ownership is explicit and competing writers are
guarded.

## Retry-safe opening and coordination

`open` is the normal entry point for agents. An explicit idempotency key returns the same case even
after completion, which makes a lost MCP response safe to retry. Without a key, Cortex normalizes
the goal and resumes the newest active case with the same mode, workspace, and current branch;
`start` always creates a fresh case.

A case can record a stable actor and a same-workspace parent. The child stores `parentTaskId`; the
parent stores the child's ID. These links describe delegation without merging the two evidence
ledgers.

Before editing, `begin-change` atomically claims a time-bounded lease. The default TTL is 15 minutes
(minimum one second, maximum one hour). A same-owner retry is idempotent and an explicit TTL renews
the heartbeat; another actor is rejected until the lease is released or expires. CLI operators can
renew or release with `cortex lease`. Completion and abort release an active lease.

Every `case.json` snapshot has an optimistic `revision`. A save compares the caller's revision to
disk and increments only on success. Lease and parent-link updates reload after bounded conflicts,
so concurrent processes cannot silently overwrite a newer case or both acquire an empty lease.
Verifier facts, bounded raw output, receipts, and the verifying case revision commit as one
recoverable bundle; behavioral annotations happen only after that bundle is bound. Status and
handoff stream evidence instead of loading an unbounded history. Auto-refreshing Show and Studio
retain the 200 newest evidence/command/phase records plus exact totals from one task-locked
composite snapshot; explicit evidence and timeline commands remain the drill-down path.

## Core objects

### Case file

The durable state of one task: goal, repository identity, evidence records, hypotheses, the plan,
verification receipts, and the outcome. It is **working memory, not a transcript**. See
[The case file](/case-file).

### Evidence

A structured claim backed by a locatable source. *A model statement without a source is an
assertion, not evidence.* Every record carries a kind, source/provenance, a human-readable claim,
a location or artifact reference, a confidence band, and a sensitivity flag.

Confidence is a **policy band**, not the model's rhetorical certainty:

| Band | Meaning |
|---|---|
| high | direct evidence confirms the claim |
| medium | evidence strongly suggests it but one layer is unverified |
| low | a plausible lead (e.g. a search hit) needing more evidence |
| unknown | only a user report or model inference |

`model_inference` and `human_report` evidence **cannot satisfy a verification requirement on their
own**.

#### Repository contract is guidance, not proof

When a workspace contains `bob.yaml`, Cortex can consume Bob's compact repository contract during
orientation and classify a bounded set of planned paths. These direct facts use the dedicated
`repository_contract` kind and may be `high` confidence about the recipe, desired repository state,
whole-file ownership, or an extension point.

That confidence has a narrow meaning. A Bob fact cannot satisfy application-code correctness,
browser, terminal, artifact, secret, or general behavior verification. Bob saying a path is outside
its ownership is not a safety claim, and Bob saying desired state is clean is not proof that the
change works. Cortex preserves this boundary in policy instead of asking a model to remember it.

Path classifications inform the declared boundary without changing it. Bob-owned, reserved,
manifest-controlled, and unsafe paths produce warnings; a human may still make an explicit choice
while retaining the risk. A returned extension point stays warning-free, and Cortex recommends a
playbook only when Bob supplied the exact ID. Missing or invalid Bob context degrades explicitly
but does not stop unrelated Cortex work.

### Hypothesis

A falsifiable explanation. It must state the proposed cause, its supporting evidence, a confidence
band, and — critically — **what result would disprove it**. The planning gate rejects hypotheses
with no disproof path.

As evidence accumulates, `cortex resolve` marks a hypothesis **confirmed**, **challenged**, or
**rejected**. History is retained — the prior status and the reason are appended to the evidence
ledger rather than silently overwritten, so a later agent can learn from a failed line of
reasoning.

### Change boundary

The declared set of files and symbols expected to change. It is a reasoning and review guardrail,
not a security boundary. After a change, Cortex compares the real diff against it and flags
**scope drift** — accidental expansion becomes visible instead of silent.

### Verification surface & receipts

The user-visible layer a change affects picks the verifier:

| Surface | Verifier |
|---|---|
| code graph / change impact | codemap |
| browser / UI behavior | cairntrace |
| terminal CLI/TUI behavior | glyphrun |
| artifact content | fcheap |
| secret-dependent runtime | tvault-assisted execution |

A typed claim declares its statement, surface, and required exact contract (a browser/terminal
spec path, configured check, artifact reference, or capability selector); the verifier may be
omitted to use the surface default. This avoids guessing a surface from words such as “login” or
“build.” Legacy free-text claims remain accepted, but agents should prefer typed claims.

A **verification receipt** names the exact claim, purpose (`verifier_run` or `named_claim`),
requirement/contract, actor, verifier, and the HEAD + dirty-diff digest it supports. Its status is
`passed`, `failed`, `inconclusive`, `blocked`, `not_applicable`, or `not_run`. **`not_run` is never
rendered as `passed`.** A verify call commits one batch. If the case, lease owner, HEAD, or dirty
tree changes while it runs, definitive results are downgraded to inconclusive and the newest
unbound batch masks older proof.

All human and machine views derive one task-level assessment from current receipts:

| Assessment | Meaning |
|---|---|
| `verified` | at least one current proof passed and every required verifier and named claim is satisfied |
| `partial` | some proof passed, but a requirement or named claim remains non-passing |
| `failed` | a current verifier run or named claim failed |
| `unverified` | no adequate current proof passed |

An intentional no-diff result must be acknowledged explicitly (`--no-op` /
`noOpAcknowledged`). The acknowledgment only lets the task enter verification; it does not produce
a receipt or upgrade the assessment.

## Human decisions and transfer

Observations from people, agents, and reviewers are stored as redacted `human_report` evidence with
provenance. They can constrain reasoning but cannot satisfy verification. When work genuinely needs
a choice, a decision request records one bounded question, at least two option IDs, and each
option's consequence, then pauses in `needs_human_decision`. Answering records the selected option
as evidence and resumes exactly `pausedFrom`; a recovery operation repairs the narrow crash window
between persisting the answer and resuming the case.

A handoff is a bounded projection, not a transcript: current plan and hypotheses, at most the 20
most recent evidence facts, latest current receipts, decisions, assessment, and structured next
actions. Raw output is excluded and remains addressable through evidence/artifact references. The
serialized packet cannot exceed 128 KiB; if legacy free-form fields would cross that budget,
Cortex retains transfer-critical identity, the pending decision, and the leading continuation and
adds an explicit bounding warning. Records marked sensitive are not exported: the packet reports
their omission and retains only a sensitive pending decision's non-content identity.

## Structured next actions

Envelopes keep human-readable `nextActions` and also return machine-readable `actions`. An action
names the MCP `tool`, CLI `command`, known `arguments`, missing `inputs`, why it is appropriate, and
anything in `blockedBy`. Agents and UIs can therefore offer or invoke the next step without parsing
English strings. Every task action carries its originating `workspace`; its CLI command is also
rendered with `-C` and shell-safe quoting for people who copy it from Status, Show, or a handoff.

Bob boundary guidance uses the same action envelope with read-only `bob_path` and `bob_playbook`
continuations. They are recommendations for an approved local tool registry, not tools registered
by Cortex's MCP server. Cortex never turns one into `bob apply` or any hidden mutation.

## Four layers of memory

A proven behavior is preserved in four places so it survives beyond the current context window:

| Layer | Where | What |
|---|---|---|
| working | the case file | the current task's state, evidence, and receipts |
| structural | codemap annotations | the proven/failed behavior attached to its owning code symbol |
| semantic | vecgrep memory | a compact, cross-session recall of the outcome |
| cross-case | veclite recall index | prior resolved hypotheses (rejected/challenged are the gold) and definitive receipts, recalled as prior disproofs |

After a definitive browser or terminal verification, Cortex annotates the code symbols the task
declared it would change with the behavior and its evidence reference — so the next agent asking
codemap about that symbol sees what it's known to do. A failed behavioral run is also archived to
fcheap and linked on its receipt, turning an ephemeral run into durable, discoverable evidence.

The **cross-case** layer breaks the loop where a weak model re-forms the same wrong
theory every session. When a hypothesis is resolved (rejected/challenged) or a verification
definitively passes/fails, the case is redaction-gated (sensitive records are **excluded**, not
masked) and indexed into a veclite collection. At orient and investigate time, prior related
cases surface as low-confidence `model_inference` evidence — "PRIOR CASE task_x (repo Y):
hypothesis '…' was REJECTED — …" — so the model reads prior disproofs before re-deriving a theory.
Recall is two-tier: repo-scoped first (this project's prior disproofs are the strongest signal),
then cross-repo. Best-effort: a missing veclite or unreachable ollama degrades to a warning, never
a hard failure.

## Tool routing

Cortex routes questions to the smallest appropriate tool set rather than exposing everything:

| Signal | First tool | Then | Why |
|---|---|---|---|
| vague behavior | vecgrep | codemap | discover by meaning, then resolve structure |
| known symbol | codemap | codemap | a known symbol resolves directly in the graph |
| "what breaks if…" | codemap | codemap | blast radius is structural, not semantic |
| browser bug | cairntrace | codemap | prove the failure, then map to code |
| terminal bug | glyphrun | codemap | prove terminal behavior, then map to code |
| old artifact | fcheap | vecgrep | recover prior evidence, then link to code |
| secret-dependent | tvault | codemap | check capability without exposing values |

`cortex --json route` exports this ordered executable matrix. `cortex --json route <question>`
returns the selected row, so gateway instructions and agent prompts can consume policy data
without copying the keyword table.

Routing is **causal, not parallel**: bounded discovery (vecgrep/vidtrace) runs first, capped by
`max_candidate_files_returned`; the top deduplicated file/symbol candidates are then fed into
codemap as a second structural stage. Each structural fact records `derivedFrom` links back to the
discovery evidence whose candidate produced it, preserving symptom → candidate → structural
expansion provenance. When discovery yields no locatable candidates, the question itself falls
through to codemap (the previous behavior).

The summary is honest about that second stage. When the structural stage ran but resolved nothing,
the summary says so ("structural stage (codemap) returned no results") and warns that the evidence
is discovery-only. `tool_unavailable` records never count as structural facts, so a failed codemap
expansion cannot masquerade as successful vecgrep→codemap routing.

### Discovery quality gates

Discovery output is filtered before it becomes evidence — a search hit is a candidate, and a
worthless candidate is not worth recording. A vecgrep hit whose matched content is *only* markdown
headings, bare import/require lines, or punctuation fragments carries no evidentiary weight and is
dropped; the dropped count is reported as a warning. The filter demands positive proof of noise:

- A `#` line is a heading only in a markdown document. In shell, Python, or YAML it is a comment,
  and in C/C++ a preprocessor directive — substantive content that survives.
- When the question itself asks about imports/includes/requires/dependencies, import lines **are**
  the evidence the caller asked for and are kept.
- A hit with no content at all (older vecgrep binaries omit the field) is never treated as low
  value.

When every remaining scored hit falls below the usefulness floor (score 0.10), the round records
**zero facts** and reports **"no strong candidates"** — nothing found is stated honestly instead of
a pile of weak leads polluting the ledger. Treat that report as *nothing found*, not as evidence:
rephrase the question, name a specific symbol, or record that discovery came up empty. Unscored
hits (older binaries, keyword mode) are never gated on score — absence of a score is not evidence
of weakness.

### Deep-mode decomposition

At `deep` depth, a compound question ("where is the plan created, how is session state validated,
and where is the boundary enforced") is decomposed into up to **five** targeted sub-queries, each
searched separately — one giant embedding query averages every clause into mush and returns
doc-header noise. The split is a deliberate heuristic, not an LLM call: hard separators (`?`, `;`,
`:`) split only when followed by whitespace, so `std::sort`, URLs, and spaceless ternaries survive
intact; a comma or "and" splits when it introduces a new interrogative clause; and a clause whose
tail conjoins two short parallel objects ("enforce idempotency **and size limits**") additionally
yields one query per object, with the original clause always kept alongside them so a heuristic
miss can only add a query, never lose the real one. Fragments under three words are dropped. The
result lists the exact sub-queries searched ("deep decomposition: N targeted sub-queries
searched: …") so the split is reviewable. A question that does not decompose is searched
unchanged, and `quick` / `standard` depths never decompose.

Deep mode also reserves a slice of the evidence budget (a quarter, at least two items) for the
codemap structural stage whenever discovery defers to it, so a productive search can no longer
crowd the structural expansion out of the round, and evidence claims display each chunk's first
*substantive* line rather than a markdown heading.

## Action classes & approval

Every tool operation is classified by side-effect risk, and the class drives what's allowed:

| Class | Examples | Policy |
|---|---|---|
| `read_only` | search, inspect, status, graph query, behavioral verification | always allowed |
| `local_mutation` | write a durable memory, stash an fcheap bundle, add a codemap annotation | allowed within an active task |
| `external_mutation` | send, deploy, publish, push | **refused by default** — requires explicit approval |
| `secreted_execution` | run with injected secrets | requires the tvault capability (values stay redacted) |

The class is recorded in the case's command audit trail, so the security posture is inspectable.
A harness can install an approver to grant external mutations — the explicit approval integration
point. Cortex v0.1 issues no external mutations itself (it's an evidence layer; the agent edits
code), so the gate is a guard rail for future capabilities and any adapter that gains an
outward-facing verb.

## Budget

Each workflow gets a budget (parallel calls, investigation rounds, raw bytes per tool, evidence
items returned). Its purpose is not just cost — it prevents frantic, indiscriminate tool use.

The investigation-rounds budget (default 3) is tracked per case: each `cortex investigate` round
increments a counter, and exceeding the budget warns and nudges the agent to form a hypothesis and
plan. Exceeding is *allowed* — a legitimately deep investigation isn't blocked — but the reason is
recorded on the case, and `cortex status` surfaces the round count (`rounds N/budget`). Evidence
returned per call is likewise capped so a single query can't flood the model's context.

The retry budget `max_auto_retries_per_tool` is honored by every read-only adapter call: a
transient process failure (spawn/pipe/child-timeout, not a behavioral exit) retries up to the
budget, the attempt count and final cause are recorded on the degraded result and in
`commands.jsonl`, and mutating operations (memory writes, stashes, annotations) never retry.

## Paired evaluation

`task eval` runs the eight authored lifecycle scenarios and a paired Cortex-versus-unassisted
scorecard. Each pair names the baseline protocol explicitly (the same model with direct repository
and shell tools, without a Cortex case file, gates, or recall) and scores seven dimensions:
evidence quality, disproof discipline, boundary/scope control, verifier correctness, completion
honesty, recovery/resume, and cost/latency overhead.

Quality is reported separately from cost; overall adds cost as a lower-weight guardrail and also
shows raw tool-call, latency, and estimated-cost deltas. Cases are macro-averaged so a claim-heavy
case does not dominate. The checked-in deterministic pairs calibrate the scoring formulas and prove
the model can report a regression; they are not empirical claims about how much Cortex improves an
arbitrary agent. Real repository trials can populate the same paired observation model through the
separate, opt-in [empirical trajectory runner](/evaluation), which keeps launcher authority outside
scenario YAML and judges every arm with an independent oracle.
