# Long-running work

A single case is shaped like a ticket: one goal, a handful of investigation rounds, a bounded
change, proof, done. "Understand this whole repository, then find bugs, improvements, and feature
ideas" is a different unit of work — it spans many sessions, several commits, possibly several
agents, and it has no natural end unless something measures it. Cortex gives that work seven
durable pieces. None of them adds autonomy: they are bookkeeping invariants, the same as the rest
of the kernel.

| Piece | What it makes durable | Where it lives |
|---|---|---|
| **Survey mode** + coverage ledger | which modules were visited, which were never seen | `coverage.json` in the case |
| **Repository dossier** | evidence-backed statements about modules that outlive cases | `$XDG_STATE_HOME/cortex/repos/<slug>/dossier.{json,md}` |
| **Findings** | bugs / improvements / features / questions awaiting a disposition | `findings.json` in the case |
| **Campaign work plan** | dependency-ordered child work handed to actors | `workplan.json` in the parent case |
| **Checkpoint + resume** | the packet a model reads after context loss | `checkpoint.md` in the case |
| **Background jobs** | detached investigation rounds that outlive an MCP call | `jobs.json` + `jobs/<id>.log` in the case |
| **Evidence freshness** | whether a located claim still describes the file as it is | the `commit` on every evidence record |

## Survey mode

Open the case with `--mode survey` (MCP: `mode: "survey"`). Orientation builds a **coverage
ledger** from codemap's architecture map (`codemap map --json`: subsystems with real fan-in) or,
when codemap is absent or unindexed, from the git tree (directories with file counts standing in for
fan-in — the warning says which). Every row is `unseen` until a round visits it.

```bash
cortex open "Understand the bob codebase end to end" --mode survey --actor agent-survey
cortex coverage task_06G6…            # unseen / explored / summarized, fan-in, rounds, next module
cortex investigate task_06G6… "entry points and invariants"            # takes the next unseen module
cortex investigate task_06G6… "how are manifests validated" --module internal/manifest
```

A survey round is scoped: discovery passes the module as vecgrep's `--dir` prefix, the literal
`git grep` fallback uses it as a pathspec, and a zero-dependency **module tree** fact set (tracked
files, counts, tests) anchors the round on real files even when no index exists. A round with no
`--module` takes the ledger's next module — the unseen one with the highest fan-in, so the most
depended-upon code is understood first. Rounds are budgeted **per module**, not per case; the
case-level counter only grows.

`status` reports coverage (`coverage.percent`, `next`) and offers the next module as a structured
action. `remember` refuses to complete a survey while modules remain unseen unless
`--accept-partial-coverage` (`acceptPartialCoverage`) states that explicitly. "Understood the
codebase" therefore always has a number behind it.

## Repository dossier

The dossier is memory **per repository**, not per case. An entry is one evidence-backed statement
about a module — its responsibility, an invariant, a hotspot, a convention, or an open question —
written from an active case so the evidence it cites stays resolvable after the case completes.

```bash
cortex dossier add task_06G6… --module internal/manifest --kind invariant \
  --title "Manifests are written atomically" \
  --summary "Load/LoadFileWithSource parse strict YAML and validate; writes go through a temp+rename path." \
  --file internal/manifest/manifest.go --evidence ev_06G6…A --evidence ev_06G6…B
cortex dossier list [--module internal/manifest] [--kind invariant] [--stale]
cortex dossier refresh
```

Each entry records the HEAD it was true at. Listing evaluates freshness live: when any file the
entry depends on changed since that commit (committed, uncommitted, or untracked), the entry is
**stale** with a reason. `refresh` persists the marks. Orientation stamps the freshest non-stale
entries as low-confidence `model_inference` evidence, so the second session on a repository starts
from what the first one proved instead of re-deriving the architecture — and stale entries never
orient a new case. Writing an entry also stamps a provenance-bearing record into the case ledger
and promotes the module to `summarized` in a survey.

The redactor gates every entry: text that looks like a secret is refused outright (excluded, not
masked), because repository memory is read by every later case.

## Findings

A finding is what a survey turns up: a bug, an improvement, a feature idea, or a question. It is
neither a hypothesis (which explains a symptom) nor a claim (which verification proves); it is a
backlog item with evidence and a disposition.

```bash
cortex finding add task_06G6… "manifestFailure encodes manifest error shapes inside internal/mcp" \
  --kind improvement --severity low --file internal/mcp/tools.go --symbol manifestFailure --evidence ev_06G6…
cortex finding list task_06G6… [--status open]
cortex finding triage task_06G6… fnd_06G6… --reason "confirmed, schedule after the parser change"
cortex finding dismiss task_06G6… fnd_06G6… --reason "matches the repo convention"
cortex finding convert task_06G6… fnd_06G6… --actor agent-fix [--mode change|investigate|review]
```

Evidence ids must exist in the owning case (a rejection offers the real ids). Dismissing requires a
reason and indexes the finding for cross-case recall — "we looked, and it is not a bug" is as
valuable as a rejected hypothesis. `convert` opens (or resumes, retry-keyed) a **linked child
case** whose goal is the finding and, for change work, whose immutable acceptance criterion is the
finding itself: the child cannot complete green without proving the finding was addressed. Status,
handoff, and the checkpoint carry the backlog counts.

## Campaigns: the work plan

A big goal splits into delegable items with dependencies. Cortex keeps the plan and hands items
out; it never runs the agents that take them.

```bash
cortex workplan add task_06G6… "Refactor the manifest parser" --id parser --mode change --file internal/manifest/manifest.go
cortex workplan add task_06G6… "Update every caller" --id callers --mode change --after parser
cortex workplan list task_06G6…
cortex workplan next task_06G6… --actor agent-1     # opens the child case for `parser`
cortex workplan next task_06G6… --actor agent-2     # `callers` is blocked until `parser` completes
```

Item state is derived from the child case at read time: `pending`, `blocked` (a dependency has not
completed), `active`, `done`, or `failed`. `next` claims the first ready item by opening its child
case with the parent link, the actor, the item's criteria, and the idempotency key `parent/item`,
so a retried call returns the same child and two actors racing for one item resolve through the
parent's lock. The graph is validated on every write: unique ids, known dependencies, no cycles.
The parent's `status` and `remember` see the rollup (`remember` still refuses to complete over
in-flight children unless acknowledged).

## Checkpoint and resume

After every durable step — a phase move, an investigation round, a finding, a dossier entry, a
finished job — Cortex rewrites `checkpoint.md`: the compact handoff (goal, state, top claims, open
items, next command) plus survey coverage, the open backlog, and in-flight jobs. It is bounded at
64 KiB and meant to be injected into a model context.

```bash
cortex resume task_06G6…                                  # checkpoint + the most recent records
cortex resume task_06G6… --since 2026-09-02T08:00:00Z     # checkpoint + everything after the cursor
```

`resume` returns the checkpoint, the case revision, and the deltas since a cursor: evidence, phase
events, the pending decision, open findings, coverage, jobs, and stale evidence, plus a new
`cursor` to pass next time. The MCP instruction contract tells a model to call `cortex_resume`
first after compaction or when taking over another actor's case.

## Background jobs

Every foreground `investigate` round has a wall clock (20 s / 45 s / 90 s) so an MCP call cannot
hang. A survey over a hundred modules does not fit in one call. A **job** runs rounds in a detached
worker (`cortex job run`, a new session that survives the call that queued it); each round goes
through the ordinary `Investigate` path — redaction, budgets, junk filters, coverage marking — so
background evidence is indistinguishable from foreground evidence in the ledger.

```bash
cortex investigate task_06G6… "entry points and invariants" --async --module internal/engine
cortex investigate task_06G6… --fanout --max 5            # survey: one round per unseen module
cortex job list task_06G6…
cortex job cancel task_06G6… job_06G6…
```

One job per case at a time (rounds share the case snapshot; a conflicting foreground write is
retried). `job list` repairs honestly: a queued or running job whose process is gone is reported
as **failed** with the reason, never left "running". `remember` refuses to complete while a job is
in flight. The worker's log lives in the case directory (`jobs/<id>.log`) and is never returned to
the model.

## Evidence freshness

Every evidence record stamps the workspace HEAD at the moment it was written (`commit`). A
freshness pass asks git, once per distinct recording commit, which files changed since — committed
changes, uncommitted changes, and untracked files — and flags located records whose file is in
that set:

- `status` and `resume` list `staleEvidence` with a reason and warn;
- `verify` warns when an unresolved hypothesis rests on stale supporting evidence;
- the dossier uses the same probe for its entries.

A change case's own declared boundary is exempt once it is editing: those edits *are* the change,
not stale reads. Records without a commit or a file location (legacy, human, graph-only) are never
flagged. The pass is bounded to 16 distinct commits per read; older records are reported as
unchecked rather than silently fresh.

## What did not change

Cortex still does not run agents, apply patches, or call `bob apply`. Findings, dossier entries,
and work items are records with provenance; jobs run Cortex's own bounded rounds and nothing else.
Completion invariants only got stricter: a survey needs coverage, a converted finding becomes an
acceptance criterion, and an in-flight job blocks `remember`.
