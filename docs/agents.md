# For agents

Cortex is designed to make a weaker or context-constrained model more effective. It does not
increase base intelligence — it reduces the number of failure opportunities between thought and
verified outcome.

## The contract

Work through the task workflow. Cortex enforces the discipline; you supply the judgment.

1. **Open retry-safely.** Prefer `cortex_open_task`. Supply a stable, non-secret `actor` and an
   `idempotencyKey` when a harness may retry after losing a response. Without a key, Cortex resumes
   the newest active case matching normalized goal, mode, workspace, branch, and acceptance
   contract. When the goal already has explicit success rules, register `acceptanceCriteria`; they
   are immutable. Use `cortex_start_task` only when you deliberately need a fresh case.
2. **Treat search output as candidates, not proof.** `cortex_investigate` records vecgrep/codemap
   results as evidence with a confidence band. A `low`/`medium` hit is a lead, not a conclusion.
3. **Before editing, plan.** `cortex_plan` requires a testable hypothesis **with a disproof
   path**, a change boundary, and a verification plan. It rejects plans without a disproof path —
   restating a hypothesis more confidently will not get you through the gate. In a Bob-managed
   repository, also read any bounded ownership warning attached to a declared file; it is planning
   guidance, not behavioral proof or an automatic rejection.
4. **Claim change ownership before editing.** `cortex_begin_change` atomically acquires an
   expiring lease for the actor and moves the task to `changing`. A same-owner retry is safe; a
   different active owner is rejected. Pass the same actor to `cortex_verify` while the lease is
   active.
5. **Prove exact claims with the right verifier.** Prefer `claimSpecs`: every statement declares a
   `surface` and required exact `contract` such as a spec path or configured check; the verifier
   may default from the surface. A claim with no matching run is `not_run`, never passed. Receipts
   bind to HEAD plus the dirty-tree digest; edit again and `cortex_status` marks them stale. A
   registered acceptance criterion additionally requires the same claim ID and exact statement;
   read the bounded `claimProofs` manifest instead of inferring proof from prose.
   Repository-configured command checks are arbitrary local code and run only when the trusted
   launcher set `CORTEX_APPROVE_COMMANDS=1`; without it Cortex records `blocked`.
6. **Stay in the boundary.** Cortex compares your diff to the declared boundary and reports scope
   drift. If scope genuinely grew, expand the plan — don't let it drift silently.
7. **Preserve evidence and state uncertainty.** `cortex_remember` completes the task and writes a
   durable memory. Normal completion requires the canonical assessment to be `verified`; explicit
   `verificationNotPossible` / `acceptFailed` acknowledgments preserve non-green outcomes for
   legacy tasks but never bypass a registered acceptance contract.
8. **Never request or expose secret values.** Use `tvault` capability checks and scoped execution
   only.

## Bob-aware repositories

When `bob.yaml` exists and Bob exposes the compatible BOB-5 schema published in v0.4.0,
orientation records a compact `repository_contract` fact. Planning then checks a deduplicated,
capped set of concrete boundary files. A missing binary, invalid manifest, unsupported future
schema, or malformed result appears as a degraded warning and corrective continuation; it does not
manufacture context or stop unrelated work.

Treat Bob facts as authoritative only about Bob's repository contract. Even a `high`-confidence
ownership fact cannot prove code, browser, terminal, artifact, or secret-dependent behavior. Keep
the disproof path and verification plan intact.

If Cortex returns `bob_path`, invoke the read-only action with its exact `workspace` and `path`; if
it returns `bob_playbook`, use only the supplied ID with `operation: "show"`. These actions project
the direct commands below and are not Cortex MCP tools:

```text
bob --json path --workspace <absolute-workspace> -- <relative-path>
bob --json playbook show <id> <absolute-workspace>
```

Never infer a playbook ID, rewrite the Cortex plan from Bob output, or turn a read-only continuation
into `bob apply`. Any repository mutation remains an explicit human-authorized operation outside
this adapter.

## Why this helps

- **Fewer choices.** A compact workflow instead of dozens of overlapping raw tools.
- **Retrieval ≠ proof.** vecgrep finds candidates; codemap resolves structure; cairntrace/glyphrun
  prove behavior. Cortex keeps these distinct so "I found a string" never becomes "I fixed the
  system."
- **Falsifiable claims.** Every hypothesis carries a disproof test.
- **Bounded scope.** Unrelated edits show up as drift instead of invisible improvisation.
- **Ephemeral runs become memory.** A failed browser run, a terminal transcript, and a relevant
  symbol become linked evidence rather than three blobs that vanish from the context window.

## Reading raw detail

Investigation and verification results carry compact `facts`, not raw dumps. Read the evidence
record, then pass its `/raw/` `rawRef` with the same `taskId`; Cortex rejects case refs owned by
another task. An fcheap ref is readable only when that task already references it in artifact
evidence or a verification receipt. `maxBytes` defaults to 32 KiB and stops at 128 KiB. `path`
must be safe and relative; discovery walks at most 512 entries and returns at most 100 regular
files. Binary is refused by default; set `allowBinary: true` to receive bounded, sensitive
base64.

## Structured continuation

Use `actions`, not prose parsing, to continue a run. Each action can carry `tool`, `command`, known
`arguments`, still-required `inputs`, a `reason`, and `blockedBy`. `nextActions` remains as the
human-readable compatibility field. Every task action includes `workspace`; pass it back so the
continuation is independent of the caller's cwd. Human-facing `command` values are also pinned with
`-C` and POSIX-shell quote every case-derived argument; machine clients should still prefer
`tool` + `arguments`. Begin-change actions include the explicit `15m` default TTL. A pending
decision action includes its exact `decisionId` and names the
answer/responder inputs still needed. If a process stopped between durable writes, Cortex instead
returns an executable repair: retry the stored decision request, resume its stored answer, or
retry-safe open a `new`/`orienting` case.

## Human collaboration

- `cortex_note` records a redacted observation, constraint, decision context, or handoff fact as
  provenance-bearing `human_report` evidence. It may inform reasoning but never proves a claim.
- `cortex_request_decision` pauses on one bounded question with at least two options and an explicit
  consequence for each. `cortex_answer_decision` records the chosen option and resumes the exact
  paused phase; its `resume` mode repairs the narrow crash window after an answer was persisted.
- `cortex_handoff` returns current state and coordination metadata (revision, actor, linkage,
  lease), the plan, hypotheses, the 20 most recent evidence facts, current verifier runs and named
  claims still bound to the same workspace state, decisions, and executable actions. Raw tool
  output is intentionally excluded. General JSON packets have a 128 KiB hard ceiling. Complete
  verified packets instead fit their actual primary JSON within 90 KiB, trimming non-proof detail
  before proof and retaining every non-sensitive named claim plus its referenced verifier batches;
  if the closure cannot fit, all receipts are omitted with an explicit warning. Sensitive evidence
  and receipts are omitted; a sensitive
  pending decision keeps only its ID/status plus an omission warning. Pass `workspace` for
  repo-local/custom case stores.

## Interpreting verification

All task views use the same assessment: `verified` only when some current proof passed and every
required verifier, named claim, and registered criterion is satisfied; `partial` when some proof passed but gaps remain;
`failed` when a current verifier or named claim failed; `unverified` when no adequate proof passed.
An intentional no-diff change must set `noOpAcknowledged`; that only permits verification to run —
it does not create a pass.

## Degraded tools

If a specialist tool is unavailable, Cortex tells you plainly (`tool_unavailable`) and marks the
dependent verification `blocked` — it never pretends the tool ran. Adjust your plan accordingly
(e.g. record `verificationNotPossible` if a required verifier genuinely can't run).
