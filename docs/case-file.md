# The case file

Each non-trivial task gets a durable, human-readable **case file** — the kernel's working memory,
not a transcript. By default it lives in the central XDG store at
`$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/`, so every session across every repo is auditable
in one place (repo-local `.cortex/cases` is opt-in via `cases_dir` / `CORTEX_CASES_DIR` — see
[Configuration](/configuration)).

```
$XDG_STATE_HOME/cortex/sessions/<repo>/task_06FK…/
  case.json          # goal, immutable criteria, workspace, phase, boundary, required verification
  decisions.json     # bounded human questions, options/consequences, and answers
  evidence.jsonl     # append-only ledger of claims (provenance + confidence)
  hypotheses.json    # falsifiable explanations + disproof paths
  plan.json          # the planning gate (hypotheses + boundary + verification + uncertainty)
  verification.json  # receipts: claim/verifier/status + verified HEAD and dirty-tree digest
  commands.jsonl     # non-sensitive audit trail of tool invocations
  phases.jsonl       # phase-transition history (feeds `cortex timeline` + time-in-phase metrics)
  summary.md         # the readable outcome (written at completion)
  raw/               # redacted raw tool output, one blob per tool call (evidence rawRef → here)
  refs/              # artifact references
```

Compact facts stay in `evidence.jsonl`. Facts with stored tool output use a `/raw/` `rawRef`,
retrievable on demand with `cortex read-artifact` (or `cortex_read_artifact`) so the model-visible
envelope stays small without losing detail. The embedded task ID must exactly match the requested
case; a reference cannot cross case boundaries. Self-pointing `/evidence/` refs are provenance IDs
and do not claim separate raw output.

The central XDG default lives outside every repo, so the working tree stays clean. When cases are
opted **repo-local** (`cases_dir`), Cortex writes a `.cortex/.gitignore` (`*`) so its own state
never registers as a workspace change — otherwise it would pollute scope-drift detection and diff
review.

## `case.json`

```json
{
  "schemaVersion": 1,
  "revision": 7,
  "id": "task_06FK…",
  "createdAt": "2026-07-06T14:00:00Z",
  "goal": "Fix post-login checkout return URL",
  "mode": "change",
  "status": "verifying",
  "actor": "agent-auth",
  "parentTaskId": "task_06FJ…",
  "childTaskIds": ["task_06FL…"],
  "risk": "medium",
  "workspace": {
    "root": "/Users/abdul/projects/liftclub",
    "repository": "liftclub",
    "branch": "fix/oauth-return-url",
    "commitBefore": "7e1f4d2"
  },
  "surfaces": ["code", "browser"],
  "acceptanceCriteria": [
    {
      "id": "checkout_return",
      "statement": "Login started at checkout returns to checkout"
    }
  ],
  "changeBoundary": {
    "files": ["src/auth/callback.ts", "src/auth/return-url.ts"],
    "symbols": ["HandleCallback", "ResolveReturnURL"]
  },
  "changeLease": {
    "actor": "agent-auth",
    "acquiredAt": "2026-07-06T14:10:00Z",
    "renewedAt": "2026-07-06T14:20:00Z",
    "expiresAt": "2026-07-06T14:35:00Z"
  },
  "verificationRequired": ["codemap_review", "cairntrace_flow"]
}
```

`revision` is the optimistic-concurrency version. Each successful snapshot save increments it;
a stale writer receives a revision conflict and must reload instead of overwriting newer state.
Actor and parent/child fields are optional coordination metadata. `changeLease` is time-bounded;
released leases keep a `releasedAt` timestamp for audit and expired/released leases may be replaced.
While a human decision is pending, `status` is `needs_human_decision` and `pausedFrom` stores the
exact phase to resume.

`acceptanceCriteria` is optional for backward compatibility and immutable after creation. A case
may register at most 64 unique stable IDs (128 bytes) with exact statements (4 KiB). Store-level
compare-and-swap and transaction paths reject later mutation. Verification must produce a current,
bound named-claim receipt with the same ID and exact statement for every criterion; explicit
partial/failed completion acknowledgments cannot bypass this contract.

## `evidence.jsonl`

One JSON object per line, appended in order:

```json
{
  "id": "ev_06FK…",
  "timestamp": "2026-07-06T14:03:00Z",
  "kind": "code_graph",
  "source": { "tool": "codemap", "uri": "codemap://symbol/HandleCallback" },
  "claim": "HandleCallback redirects to '/' when returnTo is missing",
  "location": { "file": "src/auth/callback.ts", "startLine": 42, "endLine": 61, "symbol": "HandleCallback" },
  "confidence": "high",
  "sensitivity": "normal",
  "rawRef": "case://task_06FK…/raw/raw_06FK…",
  "derivedFrom": ["ev_06FJ…"]
}
```

`derivedFrom` links structurally-expanded evidence back to the discovery candidate(s) that led to
it — causal routing records the symptom → candidate → structure chain on the evidence itself.

## `verification.json`

An array of receipts, each naming the exact claim it supports:

```json
[
  {
    "id": "vr_06FK…",
    "batchId": "vb_06FJ…",
    "claim": "browser flow specs/cairntrace/checkout_return.yml",
    "surface": "browser",
    "purpose": "verifier_run",
    "requirement": "cairntrace_flow",
    "actor": "agent-auth",
    "tool": "cairntrace",
    "verifierVersion": "0.8.1",
    "status": "passed",
    "evidence": ["ev_06FK…"],
    "artifact": "fcheap://stash/fc_019",
    "revision": "74c6e03d…",
    "dirtyDigest": "sha256:9be0…",
    "binding": "bound",
    "timestamp": "2026-07-06T14:27:00Z"
  },
  {
    "id": "vr_06FL…",
    "batchId": "vb_06FJ…",
    "claimId": "checkout_return",
    "claim": "Login started at checkout returns to checkout",
    "surface": "browser",
    "purpose": "named_claim",
    "contract": "specs/cairntrace/checkout_return.yml",
    "actor": "agent-auth",
    "tool": "cairntrace",
    "status": "passed",
    "revision": "74c6e03d…",
    "dirtyDigest": "sha256:9be0…",
    "binding": "bound",
    "timestamp": "2026-07-06T14:27:00Z"
  }
]
```

`status` is one of `passed`, `failed`, `inconclusive`, `blocked`, `not_applicable`, `not_run`.
**`not_run` is never rendered as `passed`** in a summary.

Verifier-run receipts (`purpose: verifier_run`) record what actually executed and the planning
requirement it addresses. Named-claim receipts (`purpose: named_claim`) map one user-facing claim to
that run through a required exact `contract`. The commit and dirty digest bind proof to the
workspace state; a later edit makes it stale. One verify call commits one `batchId`. Its receipts
are `bound` only when the case, owner, HEAD, and dirty tree remain stable throughout the run;
otherwise definitive results become inconclusive and that latest unbound batch masks older proof.
Status, metrics, sessions, review, remember, Show, and
Studio all interpret current receipts through the same `verified | partial | failed | unverified`
assessment. `cortex status --json` adds a bounded `claimProofs` manifest for these stable claim
IDs, including statement digests, receipt/batch binding, revision/diff identity, and safe evidence
references. Full criterion statements remain in `case.json`; the proof projection is sized for
model transports and reports every omission explicitly.

## `decisions.json`

Decisions are durable, bounded pauses rather than chat prompts:

```json
[
  {
    "id": "dec_06FM…",
    "question": "Which migration should we use?",
    "options": [
      {"id": "safe", "label": "Safe migration", "consequence": "More rollout time"},
      {"id": "fast", "label": "Fast migration", "consequence": "Higher rollback risk"}
    ],
    "requester": "agent-auth",
    "requestedAt": "2026-07-06T14:08:00Z",
    "status": "pending"
  }
]
```

An answer records the option ID, responder, timestamp, and the evidence ID of the redacted
`human_report` it creates. A pending decision prevents lifecycle progress but remains non-terminal.

## Bounded artifact previews

Raw blobs remain in `raw/`; fcheap stashes remain external references. `cortex read-artifact` and
`cortex_read_artifact` return a redacted preview, not an unbounded dump. Raw refs are readable only
by their owning task; fcheap refs are readable only after that task records the ref in artifact
evidence or a verification receipt. `path` must be a safe relative path; absolute paths, parent
traversal, and symlinks are rejected. An empty path discovers files, but walks at most 512 entries
and returns at most 100 regular files. Previews default to 32 KiB and stop at 128 KiB. Binary is
refused unless MCP `allowBinary` or CLI `--allow-binary` is explicit; allowed binary is returned as
bounded, sensitive base64. Results report `encoding`, `sensitive`, `truncated`, `maxBytes`, and
`bytesReturned`.

## Storage design

- **Files, not a database** (v0.1). The layout is intentionally readable and inspectable. Do not
  hand-edit an active snapshot: optimistic revisions and cross-file ledgers are kernel-managed.
- Snapshot documents (`case.json`, `plan.json`, `hypotheses.json`, `verification.json`,
  `decisions.json`) are
  rewritten atomically (temp + rename) so a crash mid-write can't corrupt them.
- A verifier run stages facts, redacted/bounded raw blobs, receipts, and its case revision, then
  publishes all of them in one recoverable revision-guarded transaction. A lost case/lease race
  writes none of that attempted proof; its audited command remains visible. Public readers acquire
  the same lock and recover an interrupted rename sequence before exposing receipts, evidence, or
  raw content. Behavioral annotations run only after a bound bundle commits.
- Ledgers (`evidence.jsonl`, `commands.jsonl`, `phases.jsonl`) are append-only. A phase event is
  appended only after the corresponding `case.json` snapshot commits, so a failed CAS or summary
  write cannot leave timeline/latency views ahead of durable task state.
- No secret value is ever written to a case file — the redactor filters tool output first, and
  `commands.jsonl` records capability and result, never secret contents.
- New directories and files use owner-only permissions (`0700` / `0600` on POSIX). User/model
  free text and collection counts, individual ledger records, and JSON snapshot reads/writes are
  bounded. Status counts evidence as a stream, handoff retains only its newest shareable window,
  and auto-refreshing human views retain bounded recent ledgers plus exact totals in one task-locked
  snapshot. General handoffs stop at 128 KiB; complete verified handoffs reserve 90 KiB for their
  actual primary JSON and keep the full non-sensitive named-claim/verifier proof closure or omit
  receipts atomically with a warning.
- `commands.jsonl` notes include retry attempt counts/final cause when a read-only tool call was
  retried and still failed (transient spawn/transport errors only; exits are data, never replayed).
