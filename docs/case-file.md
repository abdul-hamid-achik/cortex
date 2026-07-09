# The case file

Each non-trivial task gets a durable, human-readable **case file** — the kernel's working memory,
not a transcript. By default it lives in the central XDG store at
`$XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>/`, so every session across every repo is auditable
in one place (repo-local `.cortex/cases` is opt-in via `cases_dir` / `CORTEX_CASES_DIR` — see
[Configuration](/configuration)).

```
$XDG_STATE_HOME/cortex/sessions/<repo>/task_06FK…/
  case.json          # goal, workspace identity, phase, boundary, required verification
  evidence.jsonl     # append-only ledger of claims (provenance + confidence)
  hypotheses.json    # falsifiable explanations + disproof paths
  plan.json          # the planning gate (hypotheses + boundary + verification + uncertainty)
  verification.json  # receipts: which claim, which verifier, passed/failed/not_run
  commands.jsonl     # non-sensitive audit trail of tool invocations
  phases.jsonl       # phase-transition history (feeds `cortex timeline` + time-in-phase metrics)
  summary.md         # the readable outcome (written at completion)
  raw/               # redacted raw tool output, one blob per tool call (evidence rawRef → here)
  refs/              # artifact references
```

Compact facts stay in `evidence.jsonl`; each fact's `rawRef` points at the underlying tool output
in `raw/`, retrievable on demand with `cortex read-artifact` (or `cortex_read_artifact`) so the
model-visible envelope stays small without losing detail.

The central XDG default lives outside every repo, so the working tree stays clean. When cases are
opted **repo-local** (`cases_dir`), Cortex writes a `.cortex/.gitignore` (`*`) so its own state
never registers as a workspace change — otherwise it would pollute scope-drift detection and diff
review.

## `case.json`

```json
{
  "schemaVersion": 1,
  "id": "task_06FK…",
  "createdAt": "2026-07-06T14:00:00Z",
  "goal": "Fix post-login checkout return URL",
  "mode": "change",
  "status": "verifying",
  "risk": "medium",
  "workspace": {
    "root": "/Users/abdul/projects/liftclub",
    "repository": "liftclub",
    "branch": "fix/oauth-return-url",
    "commitBefore": "7e1f4d2"
  },
  "surfaces": ["code", "browser"],
  "changeBoundary": {
    "files": ["src/auth/callback.ts", "src/auth/return-url.ts"],
    "symbols": ["HandleCallback", "ResolveReturnURL"]
  },
  "verificationRequired": ["codemap_review", "cairntrace_flow"]
}
```

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
  "confidence": 0.93,
  "sensitivity": "normal",
  "rawRef": "case://task_06FK…/evidence/ev_06FK…"
}
```

## `verification.json`

An array of receipts, each naming the exact claim it supports:

```json
[
  {
    "id": "vr_06FK…",
    "claim": "After OAuth login from checkout, the browser returns to checkout",
    "surface": "browser",
    "tool": "cairntrace",
    "status": "passed",
    "evidence": ["ev_06FK…"],
    "artifact": "fcheap://stash/fc_019",
    "timestamp": "2026-07-06T14:27:00Z"
  }
]
```

`status` is one of `passed`, `failed`, `inconclusive`, `blocked`, `not_applicable`, `not_run`.
**`not_run` is never rendered as `passed`** in a summary.

## Storage design

- **Files, not a database** (v0.1). The layout is intentionally readable so a case can be
  inspected or hand-edited.
- Snapshot documents (`case.json`, `plan.json`, `hypotheses.json`, `verification.json`) are
  rewritten atomically (temp + rename) so a crash mid-write can't corrupt them.
- Ledgers (`evidence.jsonl`, `commands.jsonl`, `phases.jsonl`) are append-only.
- No secret value is ever written to a case file — the redactor filters tool output first, and
  `commands.jsonl` records capability and result, never secret contents.
