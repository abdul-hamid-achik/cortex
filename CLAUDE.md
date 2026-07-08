# CLAUDE.md

**Source of truth: `AGENTS.md`. Read it first.** This file adds Claude-specific orientation and
the handful of things that are easy to get wrong.

## What cortex is

A local-first **agent kernel**: a small runtime between an LLM and the specialist tool ecosystem
(codemap, vecgrep, cairntrace, glyphrun, fcheap, tvault). It gives a task a durable **case file**
and forces a reasoning loop — orient → investigate → plan → change → verify → preserve — through
a **phase machine** with hard invariants. Two surfaces over one kernel: a CLI (`--json` for
agents) and an MCP server (`cortex serve`, 8 tools).

Surfaces / key files:
- CLI: `cmd/cortex/` — cobra, split per-command; each `RunE` is thin → `kernelFor()` → `internal/kernel`.
- Shared service layer (everything routes here): `internal/kernel/` (orient/investigate/plan/verify/persist/status/scope).
- MCP server (thin, 8 tools): `internal/mcp/server.go`.
- Domain (no internal deps): `internal/domain/` (case + phase machine, evidence, hypothesis, plan, verification, policy, envelope).
- Adapters (flat, one file per tool): `internal/adapters/`.
- Storage: `internal/store/casefs` (JSON/JSONL) + `internal/store/redact` (secret masking).

## Two documentation surfaces — do not mix them

- `docs/` → VitePress **product docs**, deployed to **Vercel** (no GitHub Pages).
- `~/notes/projects/cortex/` → Obsidian vault for **working notes / handoffs**, via the
  `obsidian-cli` skill. **Never** write scratch `.md` into the repo. Repo root `.md` is limited
  to: README, AGENTS, CLAUDE, SPEC.

## Gotchas (learned the hard way)

- **MCP stdio framing must be newline-delimited JSON-RPC, NOT Content-Length.** Use the go-sdk
  `StdioTransport` as-is. Content-Length makes Claude Code report "Failed to connect" (this exact
  bug hit `glyph`). Keep **all** logging on stderr so stdout stays pure JSON-RPC.
- **Charm v2 lives on vanity module paths**: import `charm.land/lipgloss/v2` — **not**
  `github.com/charmbracelet/lipgloss`. Color must be **TTY-gated** (`detectColor` in
  `cmd/cortex/render.go`): without it, piping to a non-TTY emits per-grapheme escape sequences
  (a real bug we hit). `--json` output is always plain.
- **Persist the case before stamping orientation evidence.** `stampEvidence`'s append creates the
  task directory via `MkdirAll`; if `store.Create` runs *after* that, it sees the dir and refuses
  ("case already exists"). `StartTask` creates the skeleton first, then orients. (We hit this.)
- **Cortex must git-ignore its own state.** The kernel writes `<workspace>/.agent/.gitignore`
  (`*`) on init — otherwise every case-file write shows up as a workspace change and floods
  scope-drift + `codemap review`. (We hit this too.)
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
  boundary is the invariant (SPEC §6.3 #4). The kernel redactor is seeded from
  `config.RedactLiterals`.
- **`fcheap save --json` emits the manifest FLAT** (`{"id":…, "tool":…, "files":…}`), not wrapped
  in `{"manifest":{…}}` — parse `id` at the top level (we hit this; the stash link silently fell
  back to the ephemeral runDir until fixed).
- **Behavioral runs are three-way, not two.** cairn/glyph exit codes distinguish pass (0) / fail
  (1) / errored (2+, incl. contract-hash mismatch). An errored run is `inconclusive` at medium
  confidence — never a high-confidence FAILED verdict (SPEC §11.4). See `behavioralStatus`.
- **Adapters degrade, never fabricate.** A missing binary → `Health` returns `ErrToolMissing`;
  `Execute` → `unavailable` with a `tool_unavailable` fact. An unparseable output → `degraded`
  (first line only, kept as raw). Both are honest signals, not errors.

## Validate your work

`task check` (fmt + lint + test) before every commit · `task race` for concurrent code (the
registry health probe) · `task build` · `task flows` (glyphrun) when specs change. Sibling tools
are optional at runtime; the test suite fakes them (`fakeRunner`, `fakeAdapter`) and uses a real
temp git repo where git behavior matters.

## Related projects

`~/projects/*`: **codemap** (closest convention match — copy it when in doubt), **vecgrep**,
**cairntrace**, **glyphrun**, **file.cheap**, **tinyvault**, **mcphub**. Cortex composes them;
it does not replace mcphub.
