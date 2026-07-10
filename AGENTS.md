# AGENTS.md

Instructions for AI agents (and humans) working on the **cortex** codebase. This is the
canonical source-of-truth doc; `CLAUDE.md` defers to it. `README.md` is the public-facing
intro. `SPEC.md` is the design specification the implementation follows. Design rationale and
working notes belong in the Obsidian vault at `~/notes/projects/cortex/`, **not** the repo.

## Project Overview

cortex is a local-first, evidence-guided **agent kernel** for software-engineering agents. It
sits between an LLM and a set of specialist tools (codemap, vecgrep, cairntrace, glyphrun,
fcheap, tvault) and enforces a stateful reasoning loop:

```
orient → investigate → form hypotheses → declare a boundary → change → verify → preserve evidence
```

Cortex does not replace the model's planning or coding ability. It supplies what models are bad
at preserving across long tool-using tasks: a durable **case file**, explicit **evidence** and
uncertainty, disciplined **tool routing**, **bounded** changes, and **verification** tied to
user-visible behavior. See `SPEC.md` for the full design.

Three surfaces over one kernel (the ecosystem pattern — cf. codemap/vecgrep):

- **CLI** — human commands *and* `--json` machine output for agents (Cobra + Charm v2 lipgloss).
- **MCP server** — `cortex serve` (stdio), seventeen `cortex_*` tools for agents.
- **studio TUI** — `cortex studio` (Charm v2 bubbletea), a live, read-only board of **all** sessions
  across every repo: the session list plus the selected case's loop stepper, hypotheses, evidence,
  and receipts. Auto-refreshes; `--repo`/`--active` filters; `a` toggles active-only.

## Directory Structure

```
cortex/
├── cmd/cortex/               # Cobra CLI, split per-command. Each RunE is THIN → builds a
│                             #   kernel (kernelFor) → calls internal/kernel. Files carry the
│                             #   header `/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */`.
│   ├── main.go               #   root command, persistent --workspace/-C and --json flags
│   ├── start / investigate / plan / verify / remember / status / doctor / serve / studio .go
│   └── render.go             #   lipgloss v2 styled view + --json emit (TTY-gated color)
├── internal/
│   ├── domain/               # core types — NO deps on adapters/store/transport
│   │   ├── case.go           #   CaseFile + Phase machine (transitions, invariants)
│   │   ├── evidence.go       #   Evidence, EvidenceKind, Confidence, Sensitivity
│   │   ├── hypothesis.go     #   Hypothesis + Disproof (the disproof-path gate)
│   │   ├── plan.go           #   Plan + planning-gate validation
│   │   ├── verification.go   #   VerificationRecord + statuses
│   │   ├── envelope.go       #   the shared MCP/CLI result envelope
│   │   └── policy.go         #   routing matrix, budget, surface→verifier map
│   ├── kernel/               # SHARED SERVICE LAYER — CLI + MCP both call this
│   │   ├── kernel.go         #   Kernel struct, evidence stamping, phase transition helper
│   │   ├── orient.go         #   StartTask (git identity + tool health)
│   │   ├── investigate.go    #   Investigate (route → discovery → candidates → structural expansion → record evidence)
│   │   ├── plan.go           #   Plan (rejects no-disproof / no-boundary plans)
│   │   ├── verify.go         #   Verify (review + behavioral specs + scope drift → receipts)
│   │   ├── persist.go        #   Remember (durable memory + summary.md + completion invariant)
│   │   ├── resolve.go        #   Resolve (confirm/challenge/reject a hypothesis; SPEC §9.3)
│   │   ├── recall.go         #   Cross-case disproof recall: index hooks + recall (SPEC §15.4)
│   │   ├── status.go         #   Status / AbortTask / ReadEvidence / ListTasks
│   │   └── scope.go          #   scope-drift detection vs the declared boundary
│   ├── adapters/             # one file per tool; flat package sharing exec/redact plumbing
│   │   ├── adapter.go        #   Adapter interface, Request/Result/Fact, Capability/Status
│   │   ├── exec.go           #   runner (fakeable), timeout, redaction, ErrToolMissing
│   │   ├── registry.go       #   Registry + concurrent Health probe
│   │   ├── codemap.go vecgrep.go fcheap.go cairntrace.go glyphrun.go vidtrace.go tvault.go veclite.go
│   │   └── util.go           #   pluralize / decodeJSON / clip helpers
│   ├── store/
│   │   ├── casefs/           #   JSON/JSONL case-file persistence ($XDG_STATE_HOME/cortex/sessions/<repo>/<id>/)
│   │   └── redact/           #   secret-shape redaction (last-line filter before model output)
│   ├── mcp/server.go         # stdio MCP server — THIN pass-through to internal/kernel (18 tools)
│   ├── tui/board.go          # Charm v2 bubbletea studio — live cross-workspace board + loop stepper
│   ├── config/               # XDG path resolution (paths.go) + cortex.yaml loader (budget/redact/cases_dir) + CORTEX_* env
│   ├── ids/                  # time-sortable Crockford-base32 IDs (task_/ev_/hyp_/vr_)
│   ├── eval/scenarios.go     # SPEC §18.3 evaluation harness (8 benchmark scenarios + scoring)
│   ├── forge/forge.go        # PR review action (ModeReview: PR fetch + APPROVE/REQUEST CHANGES verdict)
│   └── version/version.go    # Version/Commit/Date (ldflags-injected)
├── docs/                     # VitePress site (product docs ONLY) → deploy to Vercel
├── specs/                    # glyphrun E2E specs (*.yml)
├── .github/workflows/        # ci.yml (test+race+build+lint) · release.yml (goreleaser on tags)
├── Taskfile.yml .golangci.yml .goreleaser.yaml
└── README.md AGENTS.md CLAUDE.md SPEC.md LICENSE
```

**Package boundaries are part of the contract.** Dependency direction is one-way:
`cmd → kernel → {adapters, store, config, domain, ids}`; `domain` depends on nothing internal.
The `mcp` and CLI RunE handlers are *thin* and call `internal/kernel`. **Never put business
logic in `mcp` or `cmd`.** (Same rule codemap/glyphrun document for their own MCP packages.)

## The reasoning loop (what the kernel enforces)

The six cognitive actions map 1:1 to CLI subcommands and MCP tools:

| Action | Phase move | Gate the kernel enforces |
|---|---|---|
| `start` | new → orienting → investigating | a goal exists; git identity + tool health recorded |
| `investigate` | (stays investigating) | search output recorded as *candidates*, not proof |
| `plan` | investigating → planned | every hypothesis has a **disproof path**; change tasks declare a **boundary**; uncertainty stated |
| `verify` | planned → changing → verifying | claim→verifier receipts; a claim with no verifier is `not_run`, never `passed`; scope drift surfaced |
| `remember` | verifying → persisting → complete | **cannot complete** without a verification receipt or an explicit `--unverified` acknowledgment |
| `status` | — | phase, unresolved hypotheses, scope drift, missing verification, tool health |

These are structural invariants (see `internal/domain/case.go` `transitions`, and the `Validate`
methods). They are enforced by state, not by prompting — the model can't skip the disproof path
by restating a hypothesis.

## Development Commands (Taskfile, version 3)

```
task                 # list tasks
task doctor          # check go/task/glyph/bun + which sibling tools are on PATH
task build           # build → ./bin/cortex (ldflags inject version)
task test            # go test ./...
task race            # CGO_ENABLED=1 go test -race ./...
task lint            # golangci-lint v2 (or go vet + gofmt -l)
task fmt             # gofmt -s -w .
task check           # fmt + lint + test  (aliases: ci, verify)
task flows           # glyph run specs/*.yml  (E2E; local only — not run in CI)
task docs            # VitePress dev server (Bun)  ·  task docsbuild / task docsdeps
task ship            # check + race + build + flows
task install         # go install ./cmd/cortex
```

## Prerequisites

- **Go 1.25+** (module pins `1.25.5`, matching the ecosystem).
- **Task** (`go install github.com/go-task/task/v3/cmd/task@latest`).
- **Bun** for docs; **glyph** (glyphrun) for E2E specs; **golangci-lint** for lint.
- Sibling tools (`codemap`, `vecgrep`, `cairn`, `glyph`, `fcheap`, `tvault`, `mcphub`) are
  **optional at runtime** — every adapter degrades safely when its binary is absent
  (`Health` returns `ErrToolMissing`; `Execute` returns a `tool_unavailable` fact). Cortex
  never fabricates a missing tool's output.

## Architecture Notes

### Adapters (SPEC §11)
- Flat `internal/adapters` package, one file per tool, sharing the unexported `tool` helper
  (binary name + fakeable `runner` + `redact.Redactor` + timeout). This deliberately deviates
  from SPEC §22's per-tool subdirectories so adapters share exec/redact plumbing without
  exporting internals — the layout is the implementer's call (SPEC §11.1).
- **Flag dialects differ and matter.** codemap/fcheap/cairn/tvault use a boolean `--json`;
  **vecgrep uses `-f json` and `-n N`** (not `--json`/`--top`); **glyph uses `--format json`**
  and that flag must **precede** subcommand flags. `cairn`/`glyph` MCP subcommand is bare
  (`cairn mcp`, `glyph mcp`); `fcheap`/`mcphub` use `mcp serve`. See each adapter's doc comment.
- **vecgrep has no `doctor`** — health is `vecgrep --version`. Search/similar/memory outputs are
  **bare JSON arrays**, not wrapped objects.
- Every adapter returns a normalized `Result{Status, Facts, Artifacts, Warnings, Raw}`. `Status`
  is authoritative | partial | unavailable | error. Raw (redacted) output is retained for the
  case file but **not** returned to the model by default (SPEC §10.4).
- `tvault` is an execution boundary, not a content provider: it answers only permitted questions
  (project/key **availability**, capability) and **never** emits secret values (SPEC §12.7).

### Storage (SPEC §8, §24 #1)
- Case files are JSON/JSONL — files, not a DB, in v0.1 — under a **central, XDG-organized** root
  by default: `$XDG_STATE_HOME/cortex/sessions/<repo-slug>/<taskID>/` (path resolution in
  `internal/config/paths.go`, mirroring codemap). This keeps every session across every repo
  visible/auditable in one place and the workspace tree clean. Append-oriented ledgers
  (`evidence.jsonl`, `commands.jsonl`, `phases.jsonl`) plus snapshot documents (`case.json`, `plan.json`,
  `hypotheses.json`, `verification.json`, `summary.md`).
- Config/cache follow XDG too (`$XDG_CONFIG_HOME/cortex`, `$XDG_CACHE_HOME/cortex`); `$CORTEX_HOME`
  or a legacy `~/.cortex` collapses config+state+cache into one dir. Repo-local storage is opt-in
  via `cases_dir` / `CORTEX_CASES_DIR`, and a pre-existing `<workspace>/.cortex/cases` is honored
  so upgrades never strand active work.
- `writeJSON` is atomic (temp + rename) so a crash mid-write can't corrupt `case.json`.
- Only when cases are workspace-local (opt-in) does the kernel write `<workspace>/.cortex/.gitignore`
  (`*`) so Cortex's own state never registers as a workspace change. The central XDG default lives
  outside every repo, so no in-repo ignore file is needed.

### Redaction (SPEC §16)
- `store/redact` masks secret shapes (AWS/GitHub/Stripe/JWT/bearer/`KEY=secret`) before any
  text reaches model-visible output or a case file. It favors precision — a false positive that
  masks ordinary code is its own failure — and preserves the key name on assignments
  (`API_KEY="«redacted»"`). It is the last-line filter *behind* tvault's boundary.

### MCP server (`internal/mcp/server.go`)
- SDK: `github.com/modelcontextprotocol/go-sdk/mcp` (v1.6.1). Build with `sdkmcp.NewServer`,
  register with `sdkmcp.AddTool`, typed input structs using `json:"…,omitempty"` +
  `jsonschema:"description"` (a field **without** `omitempty` is required). Transport:
  `&sdkmcp.StdioTransport{}`.
- **CRITICAL: stdio MCP output must be newline-delimited JSON-RPC, not Content-Length.** The
  SDK's `StdioTransport` already does this — do not wrap or reframe it. (A sibling tool, `glyph`,
  reported "Failed to connect" in Claude Code purely because it used Content-Length framing.)
- **All logging goes to stderr** so stdout stays pure JSON-RPC (mcphub follows the same rule).
- Kernels are built **per-call** (`kernelFor`) from the tool's optional `workspace` arg, so one
  server process serves tasks in any workspace.

### CLI / Charm v2
- Cobra for commands; **Charm v2 lipgloss** (`charm.land/lipgloss/v2`, **not**
  `github.com/charmbracelet/...`) for the styled view. Color is **TTY-gated** (`detectColor`):
  piped/`--json` output is plain, so agents never see ANSI escapes. Every read command supports
  `--json` for machine output.

## mcphub registration

Cortex is registered like any other MCP server:

```
mcphub add cortex cortex serve
mcphub sync --write
```

In `gateway` mode the agent sees only `mcphub`, which proxies Cortex tools as `cortex__<tool>`.
Recommended lazy pins: `cortex__cortex_start_task`, `_investigate`, `_plan`, `_verify`, `_status`.

## Common Tasks for Agents

**Add a CLI command:** add a `*.go` in `cmd/cortex/` with a cobra command var + `init()`
registration + a thin `RunE` that builds a kernel and calls `internal/kernel`. Support `--json`.

**Add an MCP tool:** define a typed input struct (json + jsonschema tags) in
`internal/mcp/server.go`, register with `sdkmcp.AddTool`, delegate to `internal/kernel`. Thin.

**Add an adapter operation:** add a `case` in the adapter's `Execute` switch, shell out via the
shared `tool.exec` (which redacts + times out), parse the tool's `--json`/`-f json`/`--format
json` output into `Fact`s. Degrade to `unavailable`/`degraded` — never fabricate.

**Change the phase machine:** edit `internal/domain/case.go` `transitions` and add a test in
`case_test.go`. Keep the `Validate` invariants in sync (`plan.go`, `hypothesis.go`).

## Code Style

- `gofmt -s` + `golangci-lint` (config version 2; errcheck + staticcheck enabled).
- Error messages **lowercase, no trailing punctuation**; return errors, `os.Exit(1)` in `main`
  only.
- Small, testable functions; explicit error handling over panics.
- `cmd/` files carry the header `/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */`.

## Testing

- High unit coverage on the invariants: phase transitions (`domain/case_test.go`), the disproof
  and completion gates (`domain/validate_test.go`, `kernel/kernel_test.go`), routing
  (`domain/policy_test.go`), redaction (`store/redact`), scope drift and the full lifecycle
  (`kernel/kernel_test.go`, over a real temp git repo + fake adapters), case-file serialization
  (`store/casefs`).
- Adapter contract tests use a fake `runner` so no real binary is spawned; git tests use a real
  temp repo (git is a hard dependency).
- glyphrun specs in `specs/` are the E2E contract. Run with `task flows` (local only).

## Before Committing

`task check` (fmt + lint + test) → `task build` → `task flows` if specs changed. Keep docs
discipline: product docs in `docs/` (VitePress), design notes in `~/notes/projects/cortex/`; no
stray `.md` in the repo root beyond README/AGENTS/CLAUDE/SPEC. Commit/push only when asked.

## Related projects (ecosystem)

Siblings under `~/projects`: **codemap** (structural code graph — the closest convention match:
Go CLI + config + MCP), **vecgrep** (semantic search + memory), **cairntrace** (browser specs),
**glyphrun** (terminal specs), **file.cheap**/`fcheap` (evidence stash), **tinyvault**/`tvault`
(secrets), **mcphub** (MCP gateway). Cortex composes all seven; it does not replace mcphub.
