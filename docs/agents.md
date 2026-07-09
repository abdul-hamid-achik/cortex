# For agents

Cortex is designed to make a weaker or context-constrained model more effective. It does not
increase base intelligence — it reduces the number of failure opportunities between thought and
verified outcome.

## The contract

Work through the six cognitive actions. Cortex enforces the discipline; you supply the judgment.

1. **Start or resume a task.** `cortex_start_task` opens a case and orients. Resume by passing the
   `taskId` to later tools.
2. **Treat search output as candidates, not proof.** `cortex_investigate` records vecgrep/codemap
   results as evidence with a confidence band. A `low`/`medium` hit is a lead, not a conclusion.
3. **Before editing, plan.** `cortex_plan` requires a testable hypothesis **with a disproof
   path**, a change boundary, and a verification plan. It rejects plans without a disproof path —
   restating a hypothesis more confidently will not get you through the gate.
4. **Prove user-visible behavior with the right verifier.** `cortex_verify` runs a structural
   review plus browser/terminal specs, fcheap artifact-manifest checks, and value-free tvault
   capability checks. A claim with no relevant input comes back `not_run` — never passed. New
   receipts bind to the full HEAD + dirty-tree digest; edit again and `cortex_status` marks them stale.
5. **Stay in the boundary.** Cortex compares your diff to the declared boundary and reports scope
   drift. If scope genuinely grew, expand the plan — don't let it drift silently.
6. **Preserve evidence and state uncertainty.** `cortex_remember` completes the task and writes a
   durable memory. It will not complete without a verification receipt (or an explicit
   `verificationNotPossible`).
7. **Never request or expose secret values.** Use `tvault` capability checks and scoped execution
   only.

## Why this helps

- **Fewer choices.** Six actions instead of dozens of overlapping raw tools.
- **Retrieval ≠ proof.** vecgrep finds candidates; codemap resolves structure; cairntrace/glyphrun
  prove behavior. Cortex keeps these distinct so "I found a string" never becomes "I fixed the
  system."
- **Falsifiable claims.** Every hypothesis carries a disproof test.
- **Bounded scope.** Unrelated edits show up as drift instead of invisible improvisation.
- **Ephemeral runs become memory.** A failed browser run, a terminal transcript, and a relevant
  symbol become linked evidence rather than three blobs that vanish from the context window.

## Reading raw detail

Investigation and verification results carry compact `facts` (claim + confidence + source), not
raw tool dumps. When you need the full record, call `cortex_read_evidence` with the `evidenceId`.
This protects your context window while keeping everything recoverable.

## Degraded tools

If a specialist tool is unavailable, Cortex tells you plainly (`tool_unavailable`) and marks the
dependent verification `blocked` — it never pretends the tool ran. Adjust your plan accordingly
(e.g. record `verificationNotPossible` if a required verifier genuinely can't run).
