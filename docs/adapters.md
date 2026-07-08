# Adapters & ecosystem

Cortex composes an existing local-first tool ecosystem. Each downstream tool is normalized behind
an **adapter** so the kernel only ever sees one result envelope. Adapters validate input, apply
timeouts, redact secrets, and mark whether a result is authoritative, partial, or unavailable —
they **never fabricate** a missing tool's output.

## The tools

| Adapter | Binary | Capability | Used for |
|---|---|---|---|
| git | `git` | structure | workspace identity + changed-files (scope drift) |
| codemap | `codemap` | structure | impact/blast-radius, callers, diff review, behavior annotations |
| vecgrep | `vecgrep` | discover | semantic/keyword search, similarity, memory |
| cairntrace | `cairn` | browser | browser behavior verification |
| glyphrun | `glyph` | terminal | terminal/TUI behavior verification |
| fcheap | `fcheap` | artifacts | durable evidence stash + search + connect |
| vidtrace | `vidtrace` | artifacts | bug-video → timestamped evidence → owning code |
| tvault | `tvault` | secrets | secret-safe execution boundary |

## Degraded modes

Every specialist tool is **optional at runtime**. When a binary is absent or unhealthy:

- `Health()` returns `ErrToolMissing`;
- `Execute()` returns a result with `status: unavailable` and a single `tool_unavailable` fact;
- verification depending on that surface is marked `blocked`/`not_run`, never invented.

When a tool runs but returns output Cortex can't parse, the result is `partial` (`degraded`): the
first line of the tool's message becomes a warning and the raw (redacted) output is retained as
evidence. This keeps Cortex honest under version skew rather than crashing or guessing.

## Flag dialects (they differ)

The adapters intentionally speak each tool's real dialect:

| Tool | Machine output | Limit | Health |
|---|---|---|---|
| codemap | `--json` (bool) | `--top`, `--depth` | `codemap doctor` |
| vecgrep | `-f json` (enum) | `-n` | `vecgrep --version` (no `doctor`) |
| fcheap | `--json` (persistent) | `--limit` | `fcheap doctor` |
| cairn | `--json` / `--format` | — | `cairn doctor --format json` |
| glyph | `--format json` (precedes sub-flags) | — | `glyph doctor` |
| tvault | `--json` (persistent) | — | `tvault doctor --json` |

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
   and redacts stdout/stderr.
3. Parse the tool's machine output into `Fact`s with an appropriate confidence band (search hits
   are `low`; structural results are `high` only when the tool reports precise resolution).
4. Degrade to `unavailable` / `degraded` on any failure — never fabricate.

See `internal/adapters/` — one file per tool, sharing the `tool` helper.
