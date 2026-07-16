# Adapters & ecosystem

Cortex composes an existing local-first tool ecosystem. Each downstream tool is normalized behind
an **adapter** so the kernel only ever sees one result envelope. Adapters validate input, apply
timeouts, redact secrets, and mark whether a result is authoritative, partial, or unavailable —
they **never fabricate** a missing tool's output.

## The tools

| Adapter | Binary | Capability | Used for |
|---|---|---|---|
| git | `git` | structure | workspace identity + changed-files (scope drift) |
| Bob | `bob` | repository contract | desired-state orientation + bounded path-ownership warnings |
| codemap | `codemap` | structure | impact/blast-radius, callers, diff review, behavior annotations |
| vecgrep | `vecgrep` | discover | semantic/keyword search, similarity, memory |
| cairntrace | `cairn` | browser | browser behavior verification |
| glyphrun | `glyph` | terminal | terminal/TUI behavior verification |
| fcheap | `fcheap` | artifacts | durable evidence stash + search + connect |
| vidtrace | `vidtrace` | artifacts | bug-video → timestamped evidence → owning code |
| tvault | `tvault` | secrets | secret-safe execution boundary |
| veclite | `veclite` | recall | prior disproofs and definitive receipts; embeddings use the configured Ollama endpoint |

## Degraded modes

Every specialist tool is **optional at runtime**. When a binary is absent or unhealthy:

- `Health()` returns `ErrToolMissing`;
- `Execute()` returns a result with `status: unavailable` and a single `tool_unavailable` fact;
- verification depending on that surface is marked `blocked`/`not_run`, never invented.

Bob is additionally gated by the repository: Cortex does not invoke it unless the workspace has a
`bob.yaml`. If that manifest exists but Bob is missing or its contract is invalid, orientation
records an explicit degraded warning and corrective action, then the normal Cortex workflow
continues. A repository without `bob.yaml` has no Bob degradation to report.

When a tool runs but returns output Cortex cannot parse, the adapter reports `partial` only when a
strict, useful subset remains; otherwise it reports `error`. Bounded redacted raw may be retained
case-only when it has valid provenance, but malformed output is never promoted into evidence. This
keeps Cortex honest under version skew rather than crashing or guessing.

Transient spawn/transport failures on read-only queries retry automatically up to
`budget.max_auto_retries_per_tool` (default 1; 0 disables). A still-failing call reports
`failed after N attempts … final cause: …` in its `tool_unavailable` fact. A non-zero exit is
data — never retried — so behavioral failures are never replayed.

## Discovery quality gates (vecgrep)

Search and similarity hits pass two gates before becoming facts, because a recorded claim like
"# Cartographer" is not a fact:

1. **Low-value chunks are dropped** — hits whose content is only markdown headings, bare
   import/require lines, or punctuation fragments. Dropping requires positive proof of noise: `#`
   marks a heading only in markdown files (elsewhere it is a comment or preprocessor directive and
   survives); import filtering is disabled when the question itself asks about
   imports/includes/requires/dependencies; and a hit with no content field (older binaries) is
   never dropped. Filtered counts appear as a warning.
2. **All-weak rounds record nothing** — when every remaining scored hit is below 0.10, the adapter
   returns zero facts and an explicit **"no strong candidates"** summary instead of a pile of weak
   leads. Unscored hits (older binaries, keyword mode) disable this gate; absence of a score is not
   evidence of weakness.

Kept hits stay `low` confidence — discovery is a candidate, not proof.

## Flag dialects (they differ)

The adapters intentionally speak each tool's real dialect:

| Tool | Machine output | Limit | Health |
|---|---|---|---|
| Bob | `--json` (persistent) | compact context + bounded path calls | `bob --json version` |
| codemap | `--json` (bool) | `--top`, `--depth` | `codemap doctor` |
| vecgrep | `-f json` (enum) | `-n` | `vecgrep --version` (no `doctor`) |
| fcheap | `--json` (persistent) | `--limit` | `fcheap doctor` |
| cairn | `--json` / `--format` | — | `cairn doctor --format json` |
| glyph | `--format json` (precedes sub-flags) | — | `glyph doctor` |
| tvault | `--json` (persistent) | — | `tvault doctor --json` |
| veclite | `--json` (persistent) | `--top-k` | `veclite version` (no `doctor`; embeddings via ollama) |

## Bob repository contract

The optional Bob adapter consumes the stable BOB-5 schema published with Bob v0.4.0. It is a
read-only local CLI integration, not an MCP client. Cortex calls direct argv with no shell:

```text
bob --json context <absolute-workspace> --profile compact
bob --json path --workspace <absolute-workspace> -- <relative-path>
```

At orientation, compact context records the recipe, repository state, and contract/context digest
as `repository_contract` evidence. During planning, Cortex deduplicates and caps declared file
paths before classifying them. A Bob-owned, reserved, manifest-controlled, or unsafe path produces
a warning; a human-owned extension point does not. Cortex preserves the declared boundary and only
suggests a playbook ID when Bob returned that exact ID.

Direct Bob facts may be `high` confidence because they are authoritative about Bob's desired state
and whole-file ownership. They do **not** prove application behavior and cannot satisfy browser,
terminal, code-correctness, artifact, secret, or general completion claims. “Outside Bob
ownership” likewise means only that Bob does not own the file; it is not a safety or correctness
verdict.

Bob's CLI envelope and inner schema are decoded strictly. Unknown future schemas, a mismatched
workspace, invalid state, timeout, or truncated required output degrade explicitly. Raw output is
bounded and redacted before optional case-file retention, and is never returned to the model by
default.

Plan responses can emit read-only structured continuations named `bob_path` and `bob_playbook`.
They carry the exact workspace/path or returned playbook ID for an approved local tool registry;
they are not Cortex MCP tools. Cortex never calls `bob apply`, recreates Bob's planner, renders
recipes, manages Bob's lock, or rewrites the plan automatically.

## Secret safety

`tvault` is treated as an **execution boundary, not a content provider**. The adapter answers only
the permitted, non-sensitive questions:

- Is a project available?
- Which secret **key names** exist (metadata only)?
- Is scoped injection granted (a capability)?

It never emits secret values, previews, or environment dumps. Behind it, a **redactor** masks
secret shapes (AWS/GitHub/Stripe/JWT/bearer tokens, `KEY=secret` assignments) in any tool output
before it reaches the model or a case file — a last-line filter, favoring precision so ordinary
code is left untouched.

## Writing a new adapter operation

1. Add a `case` to the adapter's `Execute` switch.
2. Shell out via the shared `tool.exec` helper — it checks the binary exists, applies a timeout,
   retries transient failures within the retry budget, and redacts stdout/stderr. Mutations must
   use `execOnce` (never retried).
3. Parse the tool's machine output into `Fact`s with an appropriate confidence band (search hits
   are `low`; structural results are `high` only when the tool reports precise resolution).
4. Degrade to `unavailable` / `degraded` on any failure — never fabricate.

See `internal/adapters/` — one file per tool, sharing the `tool` helper.
