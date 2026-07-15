# AGENTS.md

Instructions for AI agents (and humans) working on the **cortex** codebase. This is the
canonical source-of-truth doc; `CLAUDE.md` defers to it. `README.md` is the public-facing
intro. Architecture, behavior, and contributor rules that must remain durable belong here.
Design rationale and working notes belong in the Obsidian vault at `~/notes/projects/cortex/`,
**not** the repo.

## Project Overview

cortex is a local-first, evidence-guided **agent kernel** for software-engineering agents. It
sits between an LLM and a set of specialist tools (Bob, codemap, vecgrep, cairntrace, glyphrun,
fcheap, vidtrace, tvault, veclite) and enforces a stateful reasoning loop:

```
orient → investigate → form hypotheses → declare a boundary → change → verify → preserve evidence
```

Cortex does not replace the model's planning or coding ability. It supplies what models are bad
at preserving across long tool-using tasks: a durable **case file**, explicit **evidence** and
uncertainty, disciplined **tool routing**, **bounded** changes, and **verification** tied to
user-visible behavior.

Three surfaces over one kernel (the ecosystem pattern — cf. codemap/vecgrep):

- **CLI** — human commands *and* `--json` machine output for agents (Cobra + Charm v2 lipgloss).
- **MCP server** — `cortex serve` (stdio), a 17-tool `agent` profile by default;
  `--profile all` exposes the full 24-tool operator surface.
- **studio TUI** — `cortex studio` (Charm v2 bubbletea), a live, read-only board of **all** sessions
  across every repo: the session list plus the selected case's canonical verification assessment,
  pending decision, first structured action, loop stepper, hypotheses, and bounded recent
  evidence/receipts. Auto-refreshes; `--repo`/`--active` filters; `a` toggles active-only.

## Directory Structure

```
cortex/
├── cmd/cortex-trajectory/    # opt-in trusted empirical harness; separate from runtime/release CLI
├── cmd/cortex/               # Cobra CLI, split per-command. Each RunE is THIN → builds a
│                             #   kernel (kernelFor) → calls internal/kernel. Files carry the
│                             #   header `/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */`.
│   ├── main.go               #   root command, persistent --workspace/-C and --json flags
│   ├── open / start / investigate / plan / change / verify / remember / status .go
│   ├── note / decision / handoff / doctor / serve / studio .go
│   └── render.go             #   lipgloss v2 styled view + --json emit (TTY-gated color)
├── internal/
│   ├── domain/               # core types — NO deps on adapters/store/transport
│   │   ├── case.go           #   CaseFile + Phase machine (transitions, invariants)
│   │   ├── acceptance.go     #   optional immutable criteria + transport-independent bounds
│   │   ├── evidence.go       #   Evidence, EvidenceKind, Confidence, Sensitivity
│   │   ├── hypothesis.go     #   Hypothesis + Disproof (the disproof-path gate)
│   │   ├── plan.go           #   Plan + planning-gate validation
│   │   ├── verification.go   #   typed claims + VerificationRecord + statuses
│   │   ├── lease.go decision.go # change ownership + resumable human decisions
│   │   ├── envelope.go       #   the shared MCP/CLI result envelope
│   │   └── policy.go         #   routing matrix, budget, surface→verifier map
│   ├── kernel/               # SHARED SERVICE LAYER — CLI + MCP both call this
│   │   ├── kernel.go         #   Kernel struct, evidence stamping, phase transition helper
│   │   ├── orient.go open.go #   new task + idempotent open/resume
│   │   ├── investigate.go    #   Investigate (route → discovery → candidates → structural expansion → record evidence)
│   │   ├── plan.go           #   Plan (rejects no-disproof / no-boundary plans)
│   │   ├── change.go lease.go #  explicit begin-change + bounded ownership
│   │   ├── verify.go assessment.go # typed verification + canonical task assessment
│   │   ├── persist.go        #   Remember (durable memory + summary.md + completion invariant)
│   │   ├── resolve.go        #   Resolve (confirm/challenge/reject a hypothesis)
│   │   ├── recall.go         #   Cross-case disproof recall: index hooks + recall
│   │   ├── observe.go decision.go handoff.go actions.go artifact.go # human/agent collaboration + projections
│   │   ├── status.go         #   Status / AbortTask / ReadEvidence / ListTasks
│   │   └── scope.go          #   scope-drift detection vs the declared boundary
│   ├── adapters/             # one file per tool; flat package sharing exec/redact plumbing
│   │   ├── adapter.go        #   Adapter interface, Request/Result/Fact, Capability/Status
│   │   ├── exec.go           #   runner (fakeable), timeout, redaction, ErrToolMissing
│   │   ├── registry.go       #   Registry + concurrent Health probe
│   │   ├── bob.go codemap.go vecgrep.go fcheap.go cairntrace.go glyphrun.go vidtrace.go tvault.go veclite.go command.go
│   │   └── util.go           #   pluralize / decodeJSON / clip helpers
│   ├── store/
│   │   ├── casefs/           #   JSON/JSONL case-file persistence ($XDG_STATE_HOME/cortex/sessions/<repo>/<id>/)
│   │   └── redact/           #   secret-shape redaction (last-line filter before model output)
│   ├── mcp/server.go         # stdio MCP server — THIN pass-through (17 agent / 24 all)
│   ├── tui/board.go          # Charm v2 bubbletea studio — live cross-workspace board + loop stepper
│   ├── config/               # XDG paths + cortex.yaml (budget/redact/cases_dir/recall/verifiers) + env
│   ├── ids/                  # time-sortable Crockford-base32 IDs (task_/ev_/hyp_/vr_/dec_/raw_)
│   ├── eval/                 # deterministic scorecard + separate opt-in trajectory runner contract
│   ├── forge/forge.go        # PR review action (ModeReview: PR fetch + APPROVE/REQUEST CHANGES verdict)
│   └── version/version.go    # Version/Commit/Date (ldflags-injected)
├── docs/                     # VitePress site (product docs ONLY) → deploy to Vercel
├── contracts/v1/             # public deterministic JSON/MCP conformance schema + fixtures
├── evaluations/              # trusted empirical manifests, repository fixtures, and independent oracles
├── specs/                    # glyphrun E2E specs (*.yml)
├── .github/workflows/        # ci.yml (test+race+build+lint) · release.yml (goreleaser on tags)
├── Taskfile.yml .golangci.yml .goreleaser.yaml
└── README.md AGENTS.md CLAUDE.md CHANGELOG.md LICENSE
```

**Package boundaries are part of the contract.** Dependency direction is one-way:
`cmd → kernel → {adapters, store, config, domain, ids}`; `domain` depends on nothing internal.
The `mcp` and CLI RunE handlers are *thin* and call `internal/kernel`. **Never put business
logic in `mcp` or `cmd`.** (Same rule codemap/glyphrun document for their own MCP packages.)

## The reasoning loop (what the kernel enforces)

The recommended change path is retry-safe and makes change ownership explicit:

| Action | Phase move | Gate the kernel enforces |
|---|---|---|
| `open` | new → orienting → investigating, or resume | idempotency key wins; otherwise newest active normalized goal/mode/workspace/branch/acceptance-contract match resumes; keyed retries cannot change criteria |
| `investigate` | (stays investigating) | search output recorded as *candidates*, not proof |
| `plan` | investigating → planned | every hypothesis has a **disproof path**; change tasks declare a **boundary**; uncertainty stated |
| `begin-change` | planned → changing | an actor acquires the bounded, expiring lease; competing actors lose the CAS race |
| `verify` | changing → verifying | typed claim→surface→verifier/contract receipts; registered IDs require their exact stored statement; leased tasks require the owner actor; no-diff changes require an explicit no-op acknowledgment |
| `remember` | verifying → persisting → complete | normal completion requires `verified`; registered criteria always require current bound proof; legacy tasks may preserve non-green outcomes explicitly |
| `status` / `show` | — | canonical `verified / partial / failed / unverified` assessment, bounded claim-proof manifest, decisions, lease, scope, and structured actions |

These are structural invariants (see `internal/domain/case.go` `transitions`, and the `Validate`
methods). They are enforced by state, not by prompting — the model can't skip the disproof path
by restating a hypothesis.

`Verify` retains a planned→changing→verifying compatibility path for old unleased clients. New
CLI/MCP agent flows use `begin-change`; do not remove or silently change the compatibility path
without a migration and contract tests.

Coordination metadata is durable: `case.json` carries an optimistic `revision`, optional `actor`,
`parentTaskId` / `childTaskIds`, and the current `changeLease`. `Store.Save` is compare-and-swap;
lease mutations reload and retry bounded revision conflicts, so two processes cannot both become
the writer. Cross-process task locks heartbeat while held and use owner tokens during stale-lock
recovery. Plan and hypothesis/evidence companion writes use revision-guarded transactions; verify
stages facts/raw/receipts until one revision-guarded bundle publishes them with the verifying case
snapshot, and marks it bound only if case/owner/HEAD/diff stay stable. Status and handoff stream
bounded evidence projections; Show/Studio retain bounded recent ledgers plus exact totals from one
task-locked composite snapshot. Transaction recovery runs before public evidence/receipt/raw reads,
and behavioral annotations occur only after a bound bundle wins. A
released or expired lease may be replaced. `cortex note`, `decision
request|answer|resume`, and `handoff` preserve provenance, bounded human choices, and transfer state
without treating prose as verification. Structured continuation actions always carry the case
workspace and render workspace-pinned, shell-safe human commands; begin-change actions also carry
the explicit 15-minute default TTL. General handoff JSON is hard-capped at 128 KiB while retaining
transfer-critical identity, pending decisions, and a continuation. Complete verified handoffs
budget the actual primary JSON at 90 KiB and retain the entire non-sensitive named-claim/verifier
proof closure or omit all receipts atomically with a warning. Interrupted
orientation and half-committed decision states project retry-safe repair actions. `show`,
`timeline`, and `handoff` locate central sessions by ID and accept an explicit workspace fallback
for repo-local/custom case stores.

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
task eval            # lifecycle scenarios + paired unassisted-baseline scorecard
task trajectory-validate MANIFEST=...  # validate a trajectory manifest without execution
CORTEX_APPROVE_TRAJECTORY=1 task trajectory MANIFEST=... LAUNCHER=...  # trusted opt-in run
task docs            # VitePress dev server (Bun)  ·  task docsbuild / docstest / docsdeps
task ship            # check + race + build + flows + docstest + docsbuild
task install         # go install ./cmd/cortex
```

## Prerequisites

- **Go 1.25+** (module pins `1.25.5`, matching the ecosystem).
- **Git** is the hard runtime dependency for workspace identity, diffs, scope drift, and
  revision-bound verification.
- **Task** (`go install github.com/go-task/task/v3/cmd/task@latest`).
- **Bun** for docs; **glyph** (glyphrun) for E2E specs; **golangci-lint** for lint.
- Sibling tools (`bob`, `codemap`, `vecgrep`, `cairn`, `glyph`, `fcheap`, `vidtrace`, `tvault`,
  `veclite`, `mcphub`) are
  **optional at runtime** — every adapter degrades safely when its binary is absent
  (`Health` returns `ErrToolMissing`; `Execute` returns a `tool_unavailable` fact). Cortex
  never fabricates a missing tool's output.

## Architecture Notes

### Adapters
- Flat `internal/adapters` package, one file per tool, sharing the unexported `tool` helper
  (binary name + fakeable `runner` + `redact.Redactor` + timeout). The flat layout lets adapters
  share exec/redact plumbing without exporting internals.
- **Flag dialects differ and matter.** codemap/fcheap/cairn/tvault use a boolean `--json`;
  **vecgrep uses `-f json` and `-n N`** (not `--json`/`--top`); **glyph uses `--format json`**
  and that flag must **precede** subcommand flags. `cairn`/`glyph` MCP subcommand is bare
  (`cairn mcp`, `glyph mcp`); `fcheap`/`mcphub` use `mcp serve`. See each adapter's doc comment.
- **vecgrep has no `doctor`** — health is `vecgrep --version`. Search first requests
  `-f json-envelope`: Cortex accepts schema v1 and the transitional schema-version-zero shape,
  rejects unknown explicit majors, and falls back to the legacy bare-array `-f json` output.
  Similar and memory outputs remain bare JSON arrays.
- **Bob v0.4.0/BOB-5 is optional and manifest-gated.** Health uses `bob --json version`. Cortex
  calls `bob --json context <absolute-workspace> --profile compact` only when `bob.yaml` exists,
  and planning classifies deduplicated, bounded paths with
  `bob --json path --workspace <absolute-workspace> -- <relative-path>`. Use direct argv, strict
  schema-v1 decoding, canonical-workspace checks, the shared timeout/redaction path, and bounded
  capture. Reject unknown future schemas; missing or invalid Bob degrades explicitly without
  stopping unrelated work.
- Bob output normalizes to `repository_contract` evidence. Direct ownership/context facts may be
  `high` confidence about Bob's desired state, but can never satisfy application-code, browser,
  terminal, artifact, secret, or general behavioral verification. Raw output is case-only when
  adapter policy permits and is never model-visible by default. Preserve digest-based retry
  identity so open/resume does not duplicate equivalent context.
- Bob plan checks warn on owned, reserved, manifest-controlled, or unsafe paths and stay silent for
  human-owned extension points. They do not rewrite or reject the plan automatically. Structured
  `bob_path` actions carry `{workspace,path}` and exact path argv; `bob_playbook` carries only a
  Bob-returned `{workspace,id,operation:"show"}` and exact read-only show argv. These are
  continuations for a local registry, not Cortex MCP tools. Never call `bob apply`, reproduce Bob's
  planner/lock/recipe renderer, or make Cortex an MCP client.
- Every adapter returns a normalized `Result{Status, Facts, Artifacts, Warnings, Raw}`. `Status`
  is authoritative | partial | unavailable | error. Raw (redacted) output is retained for the
  case file but **not** returned to the model by default.
- `tvault` is an execution boundary, not a content provider: it answers only permitted questions
  (project/key **availability**, capability) and **never** emits secret values.
- Repository command verifiers are the exception to external adapter discovery: only exact argv
  arrays declared under `verifiers:` in `cortex.yaml` may run. They use no shell, accept only
  `unit_test|build|lint` on the `code` surface, and fail configuration closed. Configured argv is
  arbitrary local code and remains blocked unless the trusted launcher sets
  `CORTEX_APPROVE_COMMANDS=1`; repository configuration cannot approve itself.

### Storage
- Case files are JSON/JSONL — files, not a DB, in v0.1 — under a **central, XDG-organized** root
  by default: `$XDG_STATE_HOME/cortex/sessions/<repo-slug>/<taskID>/` (path resolution in
  `internal/config/paths.go`, mirroring codemap). This keeps every session across every repo
  visible/auditable in one place and the workspace tree clean. Append-oriented ledgers
  (`evidence.jsonl`, `commands.jsonl`, `phases.jsonl`) plus snapshot documents (`case.json`, `plan.json`,
  `hypotheses.json`, `verification.json`, `decisions.json`, `summary.md`).
- Config/cache follow XDG too (`$XDG_CONFIG_HOME/cortex`, `$XDG_CACHE_HOME/cortex`); `$CORTEX_HOME`
  or a legacy `~/.cortex` collapses config+state+cache into one dir. Repo-local storage is opt-in
  via `cases_dir` / `CORTEX_CASES_DIR`, and a pre-existing `<workspace>/.cortex/cases` is honored
  so upgrades never strand active work.
- `writeJSON` is atomic (temp + rename) so a crash mid-write can't corrupt `case.json`.
- New case directories/files are owner-only (`0700`/`0600` on POSIX). Durable free text,
  collections, ledger records, and snapshot files have hard write/read bounds.
- `case.json` snapshots also use an optimistic revision check. Treat `casefs.ErrRevisionConflict`
  as retryable only after reloading; never overwrite a stale snapshot.
- Multi-file Plan/Resolve updates go through `CommitPlan` / `UpdateHypotheses`; do not reintroduce
  separate `SavePlan` + `SaveHypotheses` writes in kernel workflows. Verification batches use
  `AppendVerificationBatch`; a later unbound batch must mask older passing proof.
- Only when cases are workspace-local (opt-in) does the kernel write `<workspace>/.cortex/.gitignore`
  (`*`) so Cortex's own state never registers as a workspace change. The central XDG default lives
  outside every repo, so no in-repo ignore file is needed.

### Redaction
- `store/redact` masks secret shapes (AWS/GitHub/Stripe/JWT/bearer/`KEY=secret`) before any
  text reaches model-visible output or a case file. It favors precision — a false positive that
  masks ordinary code is its own failure — and preserves the key name on assignments
  (`API_KEY="«redacted»"`). It is the last-line filter *behind* tvault's boundary.

### Empirical evaluation boundary

- `cmd/cortex-trajectory` and `internal/eval/trajectory` are an opt-in trusted test harness, not a
  kernel, CLI/MCP, profile, or release-runtime path. Changes there must not alter Cortex semantics.
- Scenario YAML fixes the repository digest, acceptance/oracle coverage, model build/configuration,
  arms, and budgets. It cannot select a launcher, inject environment variables, or approve itself.
- The operator supplies a separate exact-argv launcher config whose executable is an absolute path,
  and must set `CORTEX_APPROVE_TRAJECTORY=1`. Cortex resolves and hashes that executable before any
  arm, runs the resolved path, and rechecks its digest per arm. Once approved, manifest oracle argv
  executes local fixture code; manifests and fixtures therefore require the same review as
  executable test harnesses.
- Processes use direct argv, process-group cancellation, bounded capture, stripped child approval
  variables, a frozen Cortex-config digest with private copies per phase/arm, per-arm Cortex/XDG
  state roots, and separate oracle environments reduced by sensitive names and known secret-value
  shapes. Private config mutation invalidates only that arm. Real execution fails closed where
  process-group containment is unavailable. Fixtures and workspace snapshots stream under
  per-file/total/cardinality/aggregate-path bounds.
- Independent oracle workspaces are rebuilt from the frozen fixture and receive only declared
  allowed changes. Out-of-bound or oracle-protected changes invalidate success and are never allowed
  to replace the frozen tests/spec dependencies. Launcher self-report, model inference,
  blocked/not-run/inconclusive results, and stale receipts cannot pass or contaminate the measured
  model diff.
- Owner-only reports and redacted traces stay in XDG trajectory state, outside public docs. A
  truncated trace is omitted atomically. Reports bind the manifest, fixture, copied oracle specs,
  frozen/per-use Cortex config digests, launcher config/binary, each available absolute oracle
  binary and digest, effective model, exact per-arm toolchain names/versions/binary digests, and arm
  requests by digest. Missing or changed oracle executables stay blocked while independent oracles
  continue. Cortex/Bob/local-agent executables are required only in the corresponding arms, and
  boundary/recovery metrics require trusted launcher instrumentation. Failed, blocked, timed-out,
  and incomplete arms remain in reports; unavailable cost is omitted rather than scored as zero.
- Real model/network runs never belong in ordinary unit CI. Deterministic tests validate schemas,
  process bounds, scoring, and failure handling without supporting an empirical uplift claim.

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
- The default `agent` profile exposes 17 lifecycle, collaboration, evidence, and recall tools.
  `all` adds exactly seven operator tools (`list_tasks`, `sessions`, `timeline`, `metrics`,
  `overview`, `archive`, `unarchive`) for 24 total. Update profile tests and docs with any change.

### CLI / Charm v2
- Cobra for commands; **Charm v2 lipgloss** (`charm.land/lipgloss/v2`, **not**
  `github.com/charmbracelet/...`) for the styled view. Color is **TTY-gated** (`detectColor`):
  piped/`--json` output is plain, so agents never see ANSI escapes. Every non-interactive read
  command supports `--json` for machine output; Studio rejects it and points callers to
  `sessions --json` / `show --json`.
- Public documentation does not duplicate the current version. Its `Latest release` navigation item
  follows GitHub's stable `/releases/latest` redirect. Vercel Git Integration owns documentation
  deployment from `main` using the normal VitePress build; the tag workflow only publishes binaries
  and Homebrew metadata and does not call Vercel or require Vercel credentials.

## mcphub registration

Cortex is registered like any other MCP server:

```
mcphub add cortex cortex serve
mcphub sync --write
```

This registers the default compact `agent` profile. Use
`mcphub add cortex cortex serve -- --profile all` only for clients that require the historical
full operator/admin surface. The `--` separates mcphub's flags from arguments passed to Cortex.

In `gateway` mode the agent sees only `mcphub`, which proxies Cortex tools as `cortex__<tool>`.
Recommended lazy pins: `cortex__cortex_open_task`, `_investigate`, `_plan`, `_begin_change`,
`_verify`, `_status`.

## Common Tasks for Agents

**Add a CLI command:** add a `*.go` in `cmd/cortex/` with a cobra command var + `init()`
registration + a thin `RunE` that builds a kernel and calls `internal/kernel`. Support `--json`.

**Add an MCP tool:** define a typed input struct (json + jsonschema tags) in
`internal/mcp/server.go`, register with `sdkmcp.AddTool`, delegate to `internal/kernel`. Thin.

**Add an adapter operation:** add a `case` in the adapter's `Execute` switch, shell out via the
shared `tool.exec` (which redacts + times out), parse the tool's `--json`/`-f json`/`--format
json` output into `Fact`s. Degrade to `unavailable`/`degraded` — never fabricate.

For Bob, preserve its stable v0.4.0 direct-argv forms and the repository-contract/behavioral-proof
boundary above; Bob's public BOB-5 fixtures are the consumer contract.

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
- Bob adapter/integration tests use Bob v0.4.0 BOB-5 public fixture envelopes. Cover exact argv,
  strict schemas, workspace identity, timeout/truncation/redaction, retry-stable orientation,
  bounded/deduplicated path calls, warning/action provenance, and the rule that Bob facts cannot
  satisfy verification. Never require a live Bob binary in ordinary CI.
- glyphrun specs in `specs/` are the E2E contract. Run with `task flows` (local only).
- Public JSON/MCP compatibility goldens live in `contracts/v1/` and are generated from kernel,
  handoff, and MCP test paths. Update them only with `CORTEX_UPDATE_CONTRACTS=1` and review every
  diff; IDs, timestamps, digests, private paths, and secrets must remain normalized or absent.
- `task eval` runs the eight authored lifecycle scenarios and the deterministic paired
  Cortex-versus-unassisted calibration scorecard. The paired fixtures validate scoring across
  evidence quality, disproof, scope, verification, completion honesty, recovery, and overhead;
  they are not statistical claims about model performance.
- Trajectory manifest validation and runner logic use local fixtures and fake process runners in
  unit tests. `task trajectory` is an explicit trusted integration operation, never a CI gate;
  every published interpretation must name the exact scenario digest, model build, budgets, failed
  or incomplete arms, and independent oracle outcome.

## Before Committing

`task check` (fmt + lint + test) → `task build` → `task flows` if specs changed →
`task docsbuild` when documentation or site assets changed. Keep docs
discipline: product docs in `docs/` (VitePress), design notes in `~/notes/projects/cortex/`; no
stray `.md` in the repo root beyond README/AGENTS/CLAUDE/CHANGELOG. Commit/push only when asked.

## Related projects (ecosystem)

Siblings under `~/projects`: **Bob** (repository desired-state contract and generated-file
ownership), **codemap** (structural code graph — the closest convention match:
Go CLI + config + MCP), **vecgrep** (semantic search + memory), **cairntrace** (browser specs),
**glyphrun** (terminal specs), **file.cheap**/`fcheap` (evidence stash), **vidtrace** (bug-video
evidence), **tinyvault**/`tvault` (secrets), **veclite** (cross-case recall), and **mcphub** (MCP
gateway). Cortex consumes Bob as read-only orientation/planning guidance; it does not replace Bob
or mcphub.
