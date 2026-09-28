# Cortex Deck

A desktop console for **reviewing and operating [Cortex](../README.md)** — the evidence-guided
agent kernel. It is a thin, honest shell over the kernel: every screen is a projection of
`cortex --json` output or of the case files on disk. The deck never invents state, never runs a
free-form shell, and never treats prose as verification.

```
npm install     # once, inside desktop/
npm start       # launch the app
npm test        # 124 unit tests (node --test, no browser needed)
npm run smoke   # headless Electron pass: renders every view, fails on any console
                # error, and writes a screenshot per view to desktop/screenshots/
npm run icon    # re-render the app icon set from docs/public/cortex-mark.svg
```

The app icon is not forked artwork: `scripts/make-icon.mjs` renders the repository's brand mark
(`docs/public/cortex-mark.svg`, the same file the docs site and favicon use) into
`build/icon*.png`, `build/icon.icns`, and the 256² copy the shell shows in the rail and boot
splash (`src/renderer/assets/icon.png`). Re-run `npm run icon` whenever the mark changes.

Requires the cortex binary. Resolution order: Settings → `<workspace>/bin/cortex` →
`<cwd>/bin/cortex` → `PATH`. `task build` at the repository root produces `./bin/cortex`.

---

## Why this exists

Cortex removed its terminal Studio board in v0.19.0; the CLI (`--json`) and the MCP server
(`cortex serve`) are the two shipped surfaces. The deck is a third, human-first surface over the
same kernel: it can browse **every session in the central XDG store**, open any case and read all
of its projections, run **every command** through a form with a live argv preview, operate the
**MCP server** directly, and read the **case files** (ledgers, snapshots, `raw/`) without a
terminal.

## Feature inventory — what is integrated, and where

Everything below is reachable in the running app. The **Features** view renders this same
catalog from the live command registry, so the map cannot drift from the code.

### 1. Task loop (the kernel’s reasoning loop)

| Feature | Command(s) | Where in the deck |
|---|---|---|
| Open / idempotent resume, immutable acceptance criteria, actor/parent linkage, seeds | `open` | Workspace ▸ New case · Console · palette |
| Deliberately fresh case | `start` | Workspace ▸ Fresh case |
| Discovery → structure → evidence; depth, module scope, survey fan-out, async jobs, bug-video | `investigate` | Case ▸ Actions/Survey · Console |
| Planning gate: hypotheses **with disproof paths**, boundary, uncertainty, required verifiers | `plan` | Case ▸ Plan/Actions · Console |
| Bounded change ownership (lease acquire) | `begin-change` | Case ▸ Loop (primary action per phase) |
| Lease renew / release | `lease renew`, `lease release` | Case ▸ Loop lease panel · Actions |
| Typed claims → surfaces → verifiers → receipts; scope drift; no-op ack; drift ack | `verify` | Case ▸ Verification/Actions · Console |
| Hypothesis resolution without erasing history | `resolve` | Case ▸ Hypotheses (per-card Resolve) |
| Completion gate + explicit partial/failed acknowledgments | `remember` | Case ▸ Actions · Console |
| Abort without deleting evidence | `abort` | Case ▸ Actions · Workspace row actions |
| Agent checkpoint (phase, drift, missing proof, actions) | `status --detail full` | Case ▸ Loop |
| One-screen human view | `show` | Case ▸ Overview |

### 2. Sessions & observability

| Feature | Command(s) | Where |
|---|---|---|
| Workspace task list | `list` | Workspace · Dashboard |
| Cross-repo audit view with repo/active/stale/archived/query filters | `sessions` | Sessions |
| Cross-repo rollup: totals, rates, per-repo breakdown | `overview` | Dashboard |
| Chronological audit trail (phases, evidence, tool calls, receipts) | `timeline` | Case ▸ Timeline |
| Outcome & evidence-trail metrics, per-tool contribution, workspace aggregate | `metrics` | Case ▸ Metrics |
| Recovery checkpoint + deltas after a cursor | `resume` | Case ▸ Resume |
| Archive / unarchive / prune (dry-run first) / permanent delete (dry-run first) | `archive`, `unarchive`, `prune`, `rm` | Sessions row actions · Case ▸ Actions |

### 3. Human/agent collaboration

| Feature | Command(s) | Where |
|---|---|---|
| Provenance-bearing notes (human/agent/reviewer, sensitivity) | `note` | Case ▸ Notes · header button |
| Bounded human decisions: request / answer / crash recovery | `decision request|answer|resume` | Case ▸ Decisions (click an option to answer) |
| Bounded handoff packets (128 KiB general / 90 KiB proof-closure budget), compact mode, file export | `handoff` | Case ▸ Handoff |

### 4. Long-running work

| Feature | Command(s) | Where |
|---|---|---|
| Survey coverage ledger per module | `coverage` | Case ▸ Survey & jobs · Long-running |
| Findings backlog: add / list / triage / dismiss (reason kept) / convert to child case | `finding …` | Case ▸ Findings · Long-running |
| Repository dossier: add / list / refresh freshness | `dossier …` | Case ▸ Survey & jobs · Long-running |
| Campaigns: add / list / claim next as linked child case | `workplan …` | Case ▸ Survey & jobs · Long-running |
| Detached background jobs: list / cancel | `job …` | Case ▸ Survey & jobs · Long-running |

### 5. Evidence & memory

| Feature | Command(s) | Where |
|---|---|---|
| Full evidence record by id | `read-evidence` | Case ▸ Evidence (per-record Read) · Evidence view |
| Bounded artifact / `raw/` / fcheap previews | `read-artifact` | Case ▸ Evidence · Evidence view |
| Cross-case recall of prior disproofs and definitive receipts | `recall-cases` | Evidence view |
| Rebuild the recall index | `reindex-cases` | Evidence view |
| Direct ledger browsing (no projection): evidence/commands/phases JSONL, snapshots, `summary.md`, `raw/` | — | Case ▸ Case files · Evidence view store browser |

### 6. Review

| Feature | Command(s) | Where |
|---|---|---|
| Diff-scoped branch/PR review with verdict + receipts; base/head/pr/surfaces/extra claims | `review` | Review view |

### 7. Environment & configuration

| Feature | Command(s) | Where |
|---|---|---|
| Environment + concurrent specialist-tool health, gateway probe | `doctor` | Environment ▸ Doctor · Dashboard |
| Workspace readiness with the exact fix per gap; trust configured verifiers | `setup` | Environment ▸ Readiness · Workspace |
| Resolved config, budgets, verifiers, recall, redaction | `config` | Environment ▸ Configuration |
| Starter `cortex.yaml` with detected test runner | `init` | Environment ▸ Configuration |
| Legacy `~/.cortex` migration (dry run first, all-or-nothing) | `migrate` | Environment ▸ Migration |
| Routing matrix + per-question resolution | `route` | Environment ▸ Routing |
| Shell completion script | `completion` | Console |

### 8. MCP server (the agent surface)

| Feature | Command(s) | Where |
|---|---|---|
| Run `cortex serve` (`agent` 23 tools / `all` 30 tools), inspect every tool’s JSON schema, call tools, watch newline-delimited JSON-RPC traffic and stderr diagnostics | `serve` | MCP server view |

### 9. Repository tooling (this project’s own surface)

| Feature | Where |
|---|---|
| Product docs (`docs/*.md`, README/AGENTS/CHANGELOG) rendered with the built-in markdown renderer | Repository ▸ Docs |
| glyphrun E2E specs (`specs/*.yml`) | Repository ▸ Specs |
| Public conformance corpus (`contracts/v1` manifest, schema, fixtures grouped by class) | Repository ▸ Contracts |
| Evaluation manifests & fixtures (`evaluations/`) | Repository ▸ Evaluations |
| Codebase shape: packages by size, Go/test file counts, module + go version, `.tool-versions` | Repository ▸ Codebase |
| Taskfile task list with descriptions | Repository ▸ Taskfile |
| Curated developer tasks with live streaming output (`task check/test/lint/race/flows/eval/docs/ship/…`), stop, run history | Dev tasks |

### 10. Deck-only capabilities

- **Command palette (⌘K)** over views, all 55 commands, sessions, dev tasks, and deck actions.
- **Universal command runner**: every registry command as a form with a live argv preview,
  confirmation for writes/destructive ops, and a receipt view (summary, facts, structured next
  actions, JSON, raw stdout/stderr). Kernel `actions[]` from any receipt open the matching command
  prefilled.
- **Activity inspector** (⌘J): every cortex run, task run, and MCP lifecycle event this session.
- **Trusted-launcher approvals**: `CORTEX_APPROVE_COMMANDS`, `CORTEX_APPROVE_REMOTE_RECALL`,
  `CORTEX_APPROVE_TRAJECTORY` toggles (off by default, confirmed when granted).
- Dark/light themes, comfortable/compact density, workspace switcher with recents, per-machine
  console run history.

---

## Architecture

```
desktop/
├── package.json            electron + electron-builder only; no bundler, no framework
├── scripts/
│   ├── syntax-check.js     node --check over every shipped JS file
│   └── smoke.mjs           launches the headless smoke pass
├── src/
│   ├── main/               plain node modules (unit-testable without Electron)
│   │   ├── index.js        app bootstrap, deck:// protocol, window, menu, smoke capture
│   │   ├── ipc.js          the only place electron IPC is wired
│   │   ├── registry.js     declarative spec of all 55 commands (args, flags, kinds, docs)
│   │   ├── cortex.js       spawn + --json normalization + approval env + timeouts
│   │   ├── casestore.js    read-only, path-confined access to the central XDG case store
│   │   ├── mcp.js          MCP stdio client (newline-delimited JSON-RPC)
│   │   ├── repo.js         workspace git info + bounded file reads + Taskfile parse
│   │   ├── runner.js       curated Taskfile runner with streaming output
│   │   └── settings.js     userData settings store (atomic, 0600)
│   ├── preload/preload.cjs sandboxed bridge with an explicit channel allowlist
│   └── renderer/           ESM over a custom deck:// scheme (no bundler)
│       ├── app.js          shell, rail, topbar, activity inspector, keys, smoke signal
│       ├── lib/            dom, icons, format, ui, forms, renderers, markdown, jsonview,
│       │                   router, palette, state, bus, api, modal
│       └── views/          one module per nav destination (14 views)
└── test/                   node --test suites + a minimal DOM stub for renderer logic
```

Design rules the code enforces:

- **One source of truth per fact.** Projections come from `cortex --json`; durable files are read
  from the case store; the repository is read from disk. Nothing is cached across a write.
- **No arbitrary execution.** The renderer can only invoke registry commands and the curated
  Taskfile list; `ipc.js` validates workspaces and paths; `casestore.js` and `repo.js` confine
  every read to their roots.
- **No markup injection.** The renderer builds DOM with `h()`; the markdown renderer emits nodes,
  never HTML strings; CSP is `default-src 'none'` plus `'self'` for scripts/styles.
- **Honest degradation.** Missing binaries, refused gates, corrupt ledger lines, and truncated
  reads are all rendered as what they are.

## Verification

- `npm test` — 124 tests: registry completeness against the CLI help tree, argv construction and
  shell quoting, JSON envelope normalization, approval-env gating, timeouts, case-store path
  confinement, JSONL corruption handling, MCP framing/handshake/errors, markdown structure and
  injection safety, and renderer shape normalizers.
- `npm run smoke` — real Electron run: boots the app against the real binary and the real central
  store, renders all 14 views plus a real case, fails on any renderer console error, exercises the
  bridge (list / sessions / status / MCP start+call+stop), and writes
  `screenshots/*.png` + `screenshots/smoke-report.json`.

Screenshots are gitignored; regenerate them locally with `npm run smoke`.
