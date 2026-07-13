# CLAUDE.md

**Source of truth: `AGENTS.md`. Read it first.** This file adds Claude-specific orientation and
the handful of things that are easy to get wrong.

## What cortex is

A local-first **agent kernel**: a small runtime between an LLM and the specialist tool ecosystem
(codemap, vecgrep, cairntrace, glyphrun, fcheap, vidtrace, tvault, veclite). It gives a task a
durable **case file** and forces a reasoning loop — orient → investigate → plan → change → verify →
preserve — through a **phase machine** with hard invariants. Three surfaces share one kernel: a CLI
(`--json` for agents), an MCP server (`cortex serve`, 17 agent-profile / 24 all-profile tools), and
the cross-workspace Studio board (`cortex studio`).

Surfaces / key files:
- CLI: `cmd/cortex/` — cobra, split per-command; each `RunE` is thin → `kernelFor()` → `internal/kernel`.
- Shared service layer (everything routes here): `internal/kernel/` (orient/investigate/plan/verify/persist/status/scope).
- MCP server (thin, 17-tool agent / 24-tool all profiles): `internal/mcp/server.go`.
- Studio (read-only): `internal/tui/board.go`.
- Domain (no internal deps): `internal/domain/` (case + phase machine, evidence, hypothesis, plan, verification, policy, envelope).
- Adapters (flat, one file per tool): `internal/adapters/`.
- Storage: `internal/store/casefs` (JSON/JSONL) + `internal/store/redact` (secret masking).

## Two documentation surfaces — do not mix them

- `docs/` → VitePress **product docs**, deployed to **Vercel** (no GitHub Pages).
- `~/notes/projects/cortex/` → Obsidian vault for **working notes / handoffs**, via the
  `obsidian-cli` skill. **Never** write scratch `.md` into the repo. Repo root `.md` is limited
  to: README, AGENTS, CLAUDE, CHANGELOG.

## Gotchas (learned the hard way)

- **MCP stdio framing must be newline-delimited JSON-RPC, NOT Content-Length.** Use the go-sdk
  `StdioTransport` as-is. Content-Length makes Claude Code report "Failed to connect" (this exact
  bug hit `glyph`). Keep **all** logging on stderr so stdout stays pure JSON-RPC.
- **Charm v2 lives on vanity module paths**: import `charm.land/lipgloss/v2` — **not**
  `github.com/charmbracelet/lipgloss`. Color must be **TTY-gated** (`detectColor` in
  `cmd/cortex/render.go`): without it, piping to a non-TTY emits per-grapheme escape sequences
  (a real bug we hit). `--json` output is always plain.
- **Persist the case before any ledger append.** Appending to *any* JSONL ledger — `phases.jsonl`
  (via `transition`), `evidence.jsonl` (via `stampEvidence`), `commands.jsonl` — creates the task
  directory via `MkdirAll`; if `store.Create` runs *after* that, it sees the dir and refuses ("case
  already exists"). `StartTask` calls `Create` first, *then* the `new→orienting` transition. (We hit
  this twice: once with evidence, again when phase-history recording moved into `transition`.)
- **Cortex sessions default to a central XDG tree, not the repo.** Cases live under
  `$XDG_STATE_HOME/cortex/sessions/<repo-slug>/<taskID>/` by default (resolved in
  `internal/config/paths.go`), so the workspace stays clean and every session is auditable in one
  place. Repo-local cases are opt-in (`cases_dir: .cortex/cases`); only then does the kernel write
  `<workspace>/.cortex/.gitignore` (`*`) to keep case writes out of scope-drift + `codemap review`.
  A pre-existing `<workspace>/.cortex/cases` is still honored automatically. Tests must isolate
  global dirs (`CORTEX_HOME=<temp>` or `$XDG_STATE_HOME`) or they write into your real home.
- **Adapter flag dialects are NOT uniform.** vecgrep = `-f json` / `-n N`; glyph = `--format
  json` (must precede sub-flags); everyone else = `--json`. `cairn mcp` / `glyph mcp` are bare;
  `fcheap mcp serve` / `mcphub mcp serve` are not. codemap `changed_files` is an array of
  **objects** (`{path,status,symbols}`), not strings — a naive `[]string` parse fails silently
  into the degraded path.
- **Secrets never leave tvault's boundary.** The tvault adapter answers availability/capability
  only; `redact` is the last-line filter behind it. No adapter should ever print a secret value.
- **Redaction runs at the evidence-record boundary, not only at the adapter.** `stampEvidence`
  redacts every fact's claim/URI before persisting, so human/model-supplied facts (e.g.
  `cortex resolve` reasons) are masked too — adapter output is *already* redacted, but the write
  boundary is the invariant. The kernel redactor is seeded from `config.RedactLiterals`.
- **`fcheap save --json` emits the manifest FLAT** (`{"id":…, "tool":…, "files":…}`), not wrapped
  in `{"manifest":{…}}` — parse `id` at the top level (we hit this; the stash link silently fell
  back to the ephemeral runDir until fixed).
- **Behavioral runs are three-way, not two.** cairn/glyph exit codes distinguish pass (0) / fail
  (1) / errored (2+, incl. contract-hash mismatch). An errored run is `inconclusive` at medium
  confidence — never a high-confidence FAILED verdict. See `behavioralStatus`.
- **Adapters degrade, never fabricate.** A missing binary → `Health` returns `ErrToolMissing`;
  `Execute` → `unavailable` with a `tool_unavailable` fact. An unparseable output → `degraded`
  (first line only, kept as raw). Both are honest signals, not errors.
- **Acceptance criteria are immutable case identity.** `open`/`start` may register up to 64 stable
  ID + exact-statement pairs. Store saves and transactions reject later mutation; verification
  must reuse the exact ID/statement, and non-green completion acknowledgments cannot bypass
  missing criterion proof. Status exposes only a bounded proof manifest; full statements stay in
  `case.json`.
- **Complete handoffs must fit local-agent honestly.** General packets retain the 128 KiB cap, but
  a complete verified packet measures the actual pretty MCP JSON against 90 KiB and keeps every
  non-sensitive named claim with its verifier-batch closure. If that atomic proof set cannot fit,
  return no receipts plus the explicit overflow warning—never a partial proof set.

## Validate your work

`task check` (fmt + lint + test) before every commit · `task race` for concurrent code (the
registry health probe) · `task build` · `task flows` (glyphrun) when specs change. Sibling tools
are optional at runtime; the test suite fakes them (`fakeRunner`, `fakeAdapter`) and uses a real
temp git repo where git behavior matters.

## Related projects

`~/projects/*`: **codemap** (closest convention match — copy it when in doubt), **vecgrep**,
**cairntrace**, **glyphrun**, **file.cheap**, **vidtrace**, **tinyvault**, **veclite**, **mcphub**.
Cortex composes them; it does not replace mcphub.
