# Changelog

All notable changes to Cortex are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/); versions follow semver.

## [Unreleased]

## [0.15.2] — 2026-07-16

### Fixed
- **Unicode panic in deep-mode decomposition** — `objectConjunctSplit` sliced the original
  question with a byte offset computed on a `ToLower` copy; runes whose lowercase changes UTF-8
  byte length (Ⱥ, the Kelvin sign, İ) panicked the whole `cortex_investigate` call or silently
  split queries mid-word. The conjunction is now located with an ASCII-case-insensitive scan over
  the original bytes.
- **Heuristic split misses can no longer lose the real query** — an object-conjunction split now
  keeps the original clause FIRST alongside the targeted sub-queries, so firing on a fixed phrase
  ("search and replace") adds a query instead of replacing the real one; hitting the sub-query cap
  can no longer silently drop the right conjunct's terms from the whole set. The right conjunct is
  bounded to 2–3 words, and the grafted query now keeps the verb ("…enforce idempotency and size
  limits" → "…enforce size limits", not "…ingress size limits").
- **Structural budget reserve collapse at tiny budgets** — with `max_evidence_items_returned` at or
  below the reserve, the reservation clamped discovery back to the full budget and silently
  re-cancelled the codemap stage — the exact bug it exists to prevent. The reserve is now capped at
  budget−1.
- **Memory recall can no longer crowd out discovery** — the three recall steps stamped first and
  could contribute 3×candLimit facts against the reduced discovery cap, evicting every real search
  hit. Recall is orientation: it is now bounded to 3 facts per step.
- **Truncation warnings name their cap** — "evidence truncated to 12 items" now says the number is
  the discovery cap (budget minus the structural reserve) instead of calling a number that matches
  no configured limit "budget".
- **Decomposition warning honesty** — the "targeted sub-queries searched" note now fires only when
  a search step was actually expanded (a route with no search step decomposes nothing), and the
  sub-query list is no longer mangled by nested quoting.
- **Stage-2 provenance misalignment** — when `structuralSteps` skipped a candidate (a dotfile whose
  query token is empty), the `derivedFrom` links for every following structural fact pointed at the
  wrong discovery evidence and the "expanded structurally" count was inflated. Steps and links are
  now built in lockstep, and the count reflects steps that ran.
- **Non-code candidates no longer feed the structural stage** — README/AGENTS.md headings and
  dotfiles like `.env` became `codemap impact`/`find` steps that could only return not_found noise
  and a misleading degraded flag; structural expansion is now limited to files codemap can resolve.

## [0.15.1] — 2026-07-16

### Fixed
- **Deep-mode decomposition now splits object conjunctions** — "how does X enforce idempotency
  and size limits?" previously did not decompose at all (the clause-boundary pass only split at
  `", "`/`" and "` followed by an interrogative), so discovery ran one averaged-out embedding
  query and returned doc mush. The rightmost `" and "` conjoining a short (2–4 word) trailing
  object now yields one targeted query per object, with the shared clause context preserved.
  Guards keep "drag and drop"-style phrases whole.
- **Structural stage no longer silently starved by discovery** — when discovery filled the entire
  evidence budget, `cortex_investigate` skipped the codemap stage without attempting it, so the
  empty-structural-stage warning never fired and the summary still claimed "via vecgrep→codemap".
  A slice of the budget (¼, min 2) is now reserved for structural facts whenever a codemap
  follow-up is deferred to stage 2.
- **Evidence claims show the chunk's first substantive line** — a markdown chunk opening with a
  heading ("# Security") but carrying a 24-line body rendered its claim as just the heading,
  making legitimate evidence read as noise. Claims now skip heading/import/trivial lines and fall
  back to the first non-empty line.

### Added
- **Deep decomposition is surfaced** — the investigate result now lists the exact sub-queries
  searched ("deep decomposition: 2 targeted sub-queries searched — …"), so an invisible split is
  distinguishable from no split.

## [0.15.0] — 2026-07-15

### Added
- **Deep-mode question decomposition** — `cortex_investigate` at deep depth splits compound
  questions into up to five targeted sub-queries (heuristic, no LLM dependency) and searches each
  separately. Splitting is whitespace-gated so code tokens (`std::sort`, URLs, spaceless ternaries)
  survive intact, and non-compound questions are unchanged.
- **Optional Bob repository-contract adapter** — workspaces with `bob.yaml` can consume Bob
  v0.4.0/BOB-5 compact context during orientation and a deduplicated, capped set of exact path
  classifications during planning. Strict schema/workspace validation, bounded redacted raw
  capture, digest-stable evidence identity, explicit missing/invalid degradation, and read-only
  `bob_path`/`bob_playbook` continuations preserve the integration boundary. Bob facts use the
  dedicated `repository_contract` kind and may be high confidence about desired state or ownership,
  but never satisfy behavioral verification; Cortex does not call `bob apply`, recreate Bob's
  planner/lock/renderer, or act as a Bob MCP client.
- **Opt-in empirical trajectory runner** — a separate trusted harness compares fixed repository
  scenarios across raw-tools, Cortex, Cortex+Bob, and structured-continuation arms with pinned
  model/budget inputs, digest-verified isolated workspaces, independent command/Glyphrun oracles,
  frozen-baseline/allowed-change oracle overlays, protected test inputs, honest failed/incomplete
  arms, scope and stale-proof checks, streaming size/cardinality bounds, process-group containment,
  frozen configuration plus private per-phase/per-arm copies, exact digest-bound oracle execution,
  per-arm mutable-state isolation, independently hashed effective-model/toolchain attestation,
  instrumented boundary measurement, reproducibility digests, and owner-only redacted traces.
  Scenario YAML cannot select a launcher or approve execution; real runs require a separate exact-
  argv launcher config and `CORTEX_APPROVE_TRAJECTORY=1`, and remain outside ordinary CI and product
  uplift claims.
- **Public conformance corpus** — 27 versioned, deterministic fixtures cover lifecycle successes,
  structural rejections, degraded and bounded states, exact shared-envelope text/structured parity,
  and a real MCP stdio handshake. Corpus tests reject future schemas, private paths, secrets,
  timestamps, nondeterministic IDs, and unreviewed golden drift.

### Changed
- MCP exposure remains the existing `agent` and `all` profiles. No `lite` profile was added because
  there are no reviewed empirical results comparing direct `agent`, six-pin MCPHub, a frozen
  candidate, and structured-action lazy resolution, and no design was explicitly selected.
  Deterministic fixtures do not count as model-uplift evidence; a future profile requires meaningful
  quality improvement or context savings without materially worse recovery or human collaboration.
- Public documentation no longer duplicates a manually maintained current-version string. Its
  `Latest release` navigation follows GitHub's stable release redirect, while Vercel Git Integration
  deploys `main` with the standard VitePress build and remains independent from the tag-triggered
  GitHub Release and Homebrew workflow.

### Fixed
- **Discovery evidence quality gates** — vecgrep hits that are only markdown headings, bare
  import/require lines, or punctuation fragments are no longer recorded as investigation facts.
  Filtering is markdown-aware (`#` code comments in shell/Python/YAML survive), keeps import lines
  when the question itself asks about imports, and when every remaining hit scores below 0.10 the
  investigation records zero facts and reports "no strong candidates" instead of low-value noise.
- **Structural-stage honesty** — investigation summaries now state when the codemap stage ran but
  returned no results, and `tool_unavailable` records no longer count as structural facts, so a
  failed codemap expansion can't masquerade as successful vecgrep→codemap routing.

## [0.14.1] — 2026-07-15

The tag that first shipped the strengthened public contracts: the Bob repository-contract
adapter, the opt-in empirical trajectory runner, and the public conformance corpus all landed
here — their full descriptions live under [0.15.0], which finalized them.

### Added
- **Public contracts and repository guidance** — release-bound documentation versioning, the
  versioned conformance fixture corpus under `contracts/v1` (with its manifest and corpus tests),
  the opt-in `cortex-trajectory` harness entrypoint, and bounded read-only Bob orientation and
  path-ownership guidance, while keeping any `lite` MCP profile evidence-gated.

### Fixed
- **Docs deployment decoupled from the release workflow** — the tag-triggered release no longer
  builds or validates the documentation site. The release-version stamping/check scripts
  (`release-version.mts`, `check-release-version.mts`, `check-built-release.mts`) were removed;
  CI checks the docs build and the stable `releases/latest` navigation link instead, and Vercel
  deploys `main` independently.

## [0.13.0] — 2026-07-13

### Added
- **Versioned vecgrep search envelopes** — the vecgrep adapter reads `schema_version` on the
  `json-envelope` search shape: version 1 and the transitional unversioned pre-v1 envelope are
  both accepted (dual-read during the compatibility window), and an unknown explicit major
  degrades to partial with a schema-version warning instead of misreading a future contract.
- The documentation site loads Vercel Analytics.

## [0.12.0] — 2026-07-12

### Added
- **Unified session search** — `sessions --query`, the all-profile `cortex_sessions.query`, and
  Studio's `/` search use one case-insensitive AND-token contract across task identity, goal,
  phase, mode, repository/workspace, and verification outcome. Studio keeps navigation responsive
  while a filter is pending and distinguishes requested, applied, failed, and empty states.
- **Immutable acceptance criteria and proof manifests** — `open`/`start` accept up to 64 optional
  `id=statement` criteria (also exposed as typed MCP input). Registered claims must retain the
  exact statement, every criterion needs current bound proof before a task is verified, and status
  exposes a compact `claimProofs` manifest that stays below local-agent's result budget.
- **Completion-safe proof handoffs** — a complete verified handoff carries every non-sensitive
  named claim together with its referenced verifier-run batches under the actual 90 KiB encoded
  local-agent limit. Cortex strips non-proof context first and, if the proof closure still cannot
  fit, omits all receipts atomically with an explicit warning instead of exporting partial proof.
- MCP tools now publish human titles, standard safety/idempotency/open-world annotations, and a
  shared lifecycle `outputSchema`. Successes and structured rejections are regression-tested for
  identical JSON text and `structuredContent`, including a real stdio subprocess handshake.
- Studio now adapts between split and stacked layouts, scrolls long case detail with
  Page Up/Page Down or Ctrl-U/Ctrl-D, exposes textual phase labels, and safely truncates untrusted
  Unicode terminal text by display cells.

### Changed
- After the first completed investigation, structured continuations prioritize `cortex_plan` while
  retaining `cortex_investigate` as an explicit second choice. Fresh cases still investigate first.
- Studio loads session indexes and details asynchronously, coalesces refresh bursts, and rejects
  stale responses so slow repositories cannot freeze navigation or replace the selected detail.
- Completion summaries retain exact ledger totals while rendering bounded recent, non-sensitive
  hypotheses, receipts, and evidence, so every valid maximum-size plan remains completable.
- Privacy and integration docs now describe Veclite's configurable Ollama embedding call, the
  optional-tool inventory, root documentation policy, and `cortex doctor --probe` MCP validation.

### Fixed
- fcheap artifact handling accepts the canonical stash IDs emitted by fcheap v0.29 while retaining
  the existing validation, ownership, and bounded-preview protections.
- Studio reports canonical evidence and receipt totals instead of bounded-slice lengths, surfaces
  projection warnings, keeps a matching last-good snapshot visible on refresh errors, and stays
  within narrow terminal width and height bounds.
- Completion now fails closed on corrupt hypothesis, evidence, or verification state before
  releasing ownership or changing phase; review orchestration also stops on failed lifecycle or
  projection reads, preserves degraded discovery warnings, and treats branch-restoration failure
  as an error instead of deriving a clean verdict from incomplete state.
- Shared-envelope MCP tools preserve structured JSON errors for configuration and TTL failures,
  and the real stdio regression now checks race-free capture plus bounded clean shutdown.
- Show/Studio retain only the newest 200 receipts and activity entries while deriving omitted
  receipt, evidence, and activity counts from exact totals; status leaves scope unknown with an
  explicit warning when Git cannot evaluate the declared diff.

## [0.11.0] — 2026-07-11

### Added
- **Retry-safe, coordinated agent workflow** — `open` resumes by explicit idempotency key or active
  normalized goal; `begin-change` claims an expiring actor lease; parent/child task links, durable
  human decisions, provenance notes, structured next actions, and bounded handoffs preserve work
  across response loss, delegation, and human pauses.
- **Profiled MCP surface** — the default `agent` profile exposes 17 focused lifecycle/evidence
  tools; `--profile all` exposes the complete 24-tool operator and compatibility surface.
- **Safe repository verifiers** — exact argv arrays declared in `cortex.yaml` can provide named
  build/test/lint contracts. They never invoke a shell and remain blocked until the trusted
  launcher explicitly sets `CORTEX_APPROVE_COMMANDS=1`; repository configuration cannot approve
  its own execution.
- **Bounded artifact previews** — task ownership, canonical stash IDs, safe relative paths,
  symlink rejection, 512-entry/100-file discovery caps, a 128 KiB hard content cap, and explicit
  binary opt-in protect CLI/MCP consumers from unbounded or cross-task reads.
- **Paired evaluation scorecard** — deterministic Cortex-versus-unassisted calibration covers
  evidence, disproof discipline, scope control, verifier correctness, completion honesty,
  recovery, cost, and latency. Fixtures validate the measurement model; they are not an empirical
  product-performance claim.
- **Dedicated documentation site** — the isolated `docs/` VitePress application now ships a
  responsive Cortex visual system, original favicon/social assets, local fonts, search-friendly
  metadata, and a docs-only Vercel deployment contract for `cortexai.tools`.

### Changed
- **Verification is batch- and revision-bound.** Typed claims require an explicit surface and exact
  contract. One verify call atomically commits one receipt batch only after the case revision,
  lease owner, HEAD, and dirty digest remain stable; unbound results become inconclusive and mask
  older proof. Status, Remember, Show, Studio, metrics, review, and handoff share the canonical
  `verified | partial | failed | unverified` assessment.
- **Case coordination is cross-process.** Case snapshots use optimistic revisions, plan/resolve
  companion writes are journaled transactions, and heartbeat locks retain owner identity for
  safe crash recovery. Concurrent plans, resolutions, decisions, leases, and verifier runs cannot
  silently overwrite one another.
- **Verifier output commits as one bundle.** Facts, bounded raw blobs, receipts, and the verifying
  case revision are staged until the plan/lease CAS wins, then published by one recoverable
  transaction. A discarded run leaves only its correctly attributed audit command—not evidence a
  newer owner could mistake for proof.
- **Read projections are transaction-consistent and portable.** Status streams evidence counts;
  handoff streams only its newest shareable evidence; Show/Studio/timeline read one task-locked
  snapshot (auto-refreshing Show/Studio views retain bounded recent ledgers and exact totals).
  Structured actions carry workspace, commands pin `-C` with shell-safe quoting, and
  repo-local/custom Show, Timeline, and Handoff accept an explicit workspace fallback.
- **Studio and Show use the canonical projection** for decisions, verification gaps, receipts,
  current actions, evidence, plan state, and stale-proof warnings.
- Configuration parsing and safety budgets fail closed. `cortex config` projects recall and
  verifier policy while deliberately omitting executable argv.

### Security
- Caller-supplied changed-file hints cannot invent a diff and bypass the explicit no-op gate.
- Noncanonical task IDs are rejected instead of being rewritten into colliding filesystem names.
- Redaction now covers durable write boundaries, human/decision text, receipts, handoffs, legacy
  read projections, and sensitive artifact previews.
- Handoffs omit sensitive evidence and receipts, retain only a sensitive pending decision's
  non-content identity, and enforce a 128 KiB serialized ceiling. Exported Markdown and new case
  state files are owner-only; durable text, collection, ledger-record, and snapshot sizes fail
  closed before one malformed call can inflate every later read.
- Behavioral codemap annotations are emitted only after a bound verifier bundle commits. Crash
  recovery hides and rolls back partially renamed verification/raw files before any public reader
  can observe them; orientation baseline/recall evidence uses retry-stable identities.

## [0.10.0] — 2026-07-10

This section was written as a rollup at release time: several entries below (`cortex rm`,
`cortex migrate`, the archive MCP tools, causal investigate routing, the retry budget, cross-case
disproof recall, and the `config.AgentDir` removal) first shipped in the 0.6.0–0.9.0 tags, whose
per-tag sections appear further down.

### Added
- **`cortex rm <taskId>` / `delete`** — the first (and only) destructive operation in Cortex:
  permanently deletes a session's directory and everything under it. Guarded: refuses in-flight
  sessions (terminal only — complete/abandoned/blocked), is a **dry run by default** (prints what
  would be deleted; requires explicit `--force` to actually remove anything), and works on a
  session in either the active tree or the archive. Irreversible — unlike `cortex archive`, which
  is a reversible move. Supersedes the 0.5.0 note that hard-delete was "deliberately not offered."
- **`cortex migrate`** moves a legacy `~/.cortex` (or `$CORTEX_HOME`-collapsed) tree onto the
  split XDG layout: `config.yaml` → `$XDG_CONFIG_HOME/cortex`, `sessions/`/`archive/`/anything
  else → `$XDG_STATE_HOME/cortex`, `cache/` → `$XDG_CACHE_HOME/cortex`. **Dry run by default** —
  nothing moves until you pass `--apply`. It is **all-or-nothing**: if any destination already
  exists the whole migration is blocked (rather than moving a subset and stranding the rest under
  the surviving `~/.cortex`), and the now-empty legacy directory is removed once everything moves.
- **`cortex_archive` / `cortex_unarchive` MCP tools** (17 tools total) — expose the session
  archive lifecycle to agents, mirroring the CLI `cortex archive`/`cortex unarchive` commands.
  Both are workspace-independent (the session is located by task ID across the central tree),
  reversible, and refuse in-flight sessions. Completes archive-lifecycle parity between the CLI
  and MCP surfaces.
- **Routing policy export** — `cortex --json route` returns the ordered executable routing matrix;
  `cortex --json route <question>` returns the selected first/follow-up tools and rationale.
  Repeatable `--surface` inputs are validated rather than silently ignored.
- **`cortex reindex-cases`** — idempotently backfills the recall index from active central XDG
  sessions. Strict directory enumeration counts corrupt `case.json` sessions that human list views
  omit; `sessionLoadFailed` is separate from record-level failures, warnings use each origin
  workspace's redactor, and the CLI exits non-zero after completing the scan if either counter is
  non-zero. Archives and repo-local stores remain excluded. The backfill and all live index hooks
  now fail closed when the evidence ledger cannot be read, and completion/reindex upserts preserve
  the hypothesis resolution reason.
- **Versioned codemap review consumer** — the adapter accepts legacy unversioned output and the
  canonical `schema_version: 1` producer golden. Unknown future versions degrade to
  partial/inconclusive with an explicit warning, so structural verification cannot pass on an
  unsupported contract.
- **Cross-case disproof recall** — the fourth memory layer. Resolved hypotheses
  (rejected/challenged are the gold) and definitive verification receipts are indexed into a
  veclite collection (redaction-gated; sensitive records are **excluded**, not masked). At
  orient and investigate, prior related cases surface as low-confidence `model_inference`
  evidence ("PRIOR CASE task_x (repo Y): hypothesis '…' was REJECTED — …") so a weak model
  reads prior disproofs before re-deriving a theory. Two-tier recall: repo-scoped first, then
  cross-repo. New `cortex_recall_cases` MCP tool (18 total) + `cortex recall-cases` CLI.
  Best-effort: a missing veclite or unreachable ollama degrades to a warning, never a hard
  failure. Configured via the `recall:` block / `CORTEX_RECALL_*` env (defaults: a central
  veclite DB, nomic-embed-text via ollama at localhost:11434, enabled).

### Changed
- **`cortex investigate` routing is now causal, not parallel.** Discovery (vecgrep/vidtrace)
  runs first; the top deduplicated file/symbol candidates are then fed into codemap as a second
  structural stage, instead of codemap receiving the raw question alongside discovery. Structural
  evidence records `derivedFrom` provenance linking it back to the discovery candidate that
  produced it (schema-additive — old case files stay byte-compatible). When discovery yields no
  locatable candidates, the question falls through to codemap as before. Quick depth stays
  discovery-only.
- **Read-only adapter retries now honor `budget.max_auto_retries_per_tool`.** A transient
  process failure (spawn/pipe/child-timeout — never a behavioral exit) retries up to the budget
  (default 1; 0 disables), and the attempt count + final cause are recorded on the degraded
  result and in `commands.jsonl`. Previously this was a fixed single retry. Mutating operations
  (memory writes, stashes, annotations) still never retry.
- **Codemap review v1 parsing is strict at the verification boundary.** Canonical versioned rows,
  scalar enums, risk fields, and `schema_version` placement are validated before any structural
  fact is emitted; malformed v1 remains partial/inconclusive. Explicitly unversioned legacy output
  still accepts historical `name`/`path` symbol aliases without silently dropping changed symbols.

### Removed
- **`config.AgentDir`**, the deprecated alias for `config.StateDir`, has been retired. Use
  `config.StateDir` directly.

## [0.9.0] — 2026-07-09

### Added
- **Cross-case disproof recall — the fourth memory layer** — resolved hypotheses
  (rejected/challenged are the gold) and definitive verification receipts are indexed into a
  veclite collection, redaction-gated (sensitive records are **excluded**, not masked). At orient
  and investigate, prior related cases surface as low-confidence `model_inference` evidence, so a
  weak model reads prior disproofs before re-deriving the same wrong theory. Two-tier recall:
  repo-scoped first, then cross-repo. New veclite adapter (CLI-only; embeddings via ollama),
  `cortex_recall_cases` MCP tool (18 total), and `cortex recall-cases` CLI command. Kernel hooks
  index at resolve time (immediately, with the reason) and at remember time (a completion sweep).
  Configured via the `recall:` block / `CORTEX_RECALL_*` env (defaults: a central veclite DB,
  nomic-embed-text via ollama at localhost:11434). Best-effort everywhere: a missing veclite or
  unreachable ollama degrades to a warning, never a hard failure, and indexing runs on a
  background context so a cancelled caller never drops a save.

### Fixed
- veclite adapter tests use a binary that exists on CI's PATH (`git`) so the fake-runner-driven
  cases pass without a veclite install; the deliberately-missing-binary test keeps its own bin.

## [0.8.0] — 2026-07-09

### Changed
- **`cortex investigate` routes causally, not in parallel** — bounded discovery
  (vecgrep/vidtrace) runs first; the top deduplicated file/symbol candidates feed codemap as a
  second structural stage, and each structural fact records `derivedFrom` provenance linking it
  back to the discovery candidate that produced it (schema-additive — old case files and envelope
  consumers stay byte-compatible). When discovery yields no locatable candidates the question
  falls through to codemap as before; quick depth stays discovery-only.
- **Read-only adapter retries honor `budget.max_auto_retries_per_tool`** — a transient process
  failure (spawn/pipe/child-timeout, never a behavioral exit) retries up to the budget instead of
  a fixed single retry, with the attempt count and final cause recorded on the degraded result
  and in `commands.jsonl`. Mutating operations still never retry.

## [0.7.0] — 2026-07-09

### Added
- **Every claim surface routes to a verifier** — artifact claims verify through fcheap manifest
  verification, and secret capability claims verify through value-free tvault availability. New
  CLI/MCP inputs accept artifact refs and secret projects.

### Changed
- **Verification receipts bind to the workspace revision** — new receipts record the full HEAD
  plus a digest of tracked and untracked workspace changes; status, metrics, and remember ignore
  stale proof, and stale verification is exposed with an exact rerun action. Legacy receipts
  without a `dirtyDigest` remain compatible.

## [0.6.0] — 2026-07-09

### Added
- **`cortex rm <taskId>`** — permanent session delete, the only destructive op: refuses in-flight
  sessions (terminal only), is a dry run without `--force`, and locates the session in the active
  tree or the archive. `archive`/`unarchive` remain the safe, reversible alternative.
- **`cortex migrate`** — moves a legacy `~/.cortex` onto the split XDG layout (`config.yaml` →
  XDG config, sessions/archive → XDG state, cache → XDG cache). Dry run by default; `--apply`
  performs atomic renames with a cross-device copy+rename fallback, then removes the legacy base
  if empty. No-op when `CORTEX_HOME` is set or no `~/.cortex` exists.
- **`cortex_archive` / `cortex_unarchive` MCP tools** (17 total) — the session archive lifecycle
  for agents, mirroring the CLI commands; workspace-independent, reversible, refuse in-flight
  sessions.

### Fixed
- **`cortex migrate` is all-or-nothing** — a partial migrate (one entry skipped because its XDG
  destination already existed) moved `sessions/` out but left `~/.cortex` in place, so path
  resolution kept keying off the legacy dir and the moved sessions became invisible. The whole
  migration is now blocked if any destination exists (nothing moves), and the cross-device
  fallback copies to a temp then renames so a failed copy never leaves a partial destination.

### Removed
- **`config.AgentDir`**, the deprecated alias for `config.StateDir` — nothing referenced it; use
  `config.StateDir` directly.

## [0.5.0] — 2026-07-09

### Added — session archiving (safe, reversible lifecycle)
- **`cortex archive <taskId>` / `cortex unarchive <taskId>`** complete the session lifecycle. Archiving
  **moves** a terminal (complete/abandoned/blocked) session from the active tree to
  `$XDG_STATE_HOME/cortex/archive/<repo>/` — the data is preserved and fully reversible; **nothing is
  deleted** (hard-delete is deliberately not offered). In-flight sessions are refused. This keeps
  `cortex sessions` / `overview` / `studio` focused on live work as the store accumulates history.
- **`cortex sessions --archived`** lists the archive. Completion is lifecycle-aware: `archive`
  completes active task IDs, `unarchive` completes archived ones.

### Added — dynamic task-ID shell completion
- Every `<taskId>` command (`show`, `status`, `timeline`, `metrics`, `abort`, `resolve`, `plan`,
  `verify`, `remember`, `investigate`, `read-evidence`, `read-artifact`) now **tab-completes task IDs**
  — with the goal as the shell description — reading across the whole central store. Task IDs are
  long base32 strings no one types by hand; `cortex show <TAB>` now just works. `resolve` and
  `read-evidence` also complete their **second** ID argument (hypothesis / evidence ID, with the
  statement / claim as description), so you never hand-type an ID. Install with cobra's built-in
  `cortex completion {bash|zsh|fish}`.

### Added — `cortex show <taskId>`: full session view from any repo
- **`cortex show`** (alias `view`) is a one-screen, read-only dashboard for a single session — phase
  badge, loop stepper, hypotheses, verification receipts, time-in-phase (with elapsed), and recent
  activity. It's **workspace-independent** (the session is located by ID across the central store),
  so you can inspect a task from another repository without `cd`-ing there — the gap `cortex status`
  (workspace-scoped) left open. `--json` returns the whole `SessionView`. `Timeline` was refactored
  into a store-level helper so `show` doesn't re-walk the tree.

### Added — loop stepper on `cortex status`
- **`cortex status` now draws the reasoning loop** as a "you are here" track —
  `orient─inv─[plan]─change─verify─keep`, current step highlighted, completed steps green, a `✓` on
  completion, a `■` stop marker for blocked/abandoned. Previously this visualization lived only in
  the `studio` TUI. The stage model was extracted to `domain.LoopStages` / `domain.LoopStageIndexOf`
  and is now shared by both surfaces (no duplication).

### Added — `cortex overview`: cross-repo rollup
- **`cortex overview`** (alias `dash`) aggregates every session across every repository into one
  dashboard — totals, active/stale counts, completion & verified-completion rates, mean time to
  complete, and a per-repo breakdown (sorted by session count). Fills the gap between the global
  `cortex sessions`/`timeline` and the per-workspace `cortex metrics`. `--json` for machine output.
- **`cortex_overview` MCP tool** (15 tools total) — the same cross-repo rollup for agents. Completes
  observability parity: status, list, sessions, timeline, metrics, and overview are all on both the
  CLI and MCP surfaces.

### Added — stale-session detection
- **`cortex sessions` flags forgotten work** — in-flight sessions untouched beyond `--stale-after`
  (default 24h) render their age in warning color with a `⚠`; `--stale` lists only those. Answers
  "which sessions did I start and abandon?" `SessionSummary.StaleSince(now, age)` is the predicate
  (terminal sessions are never stale).
- **`cortex doctor`** counts stale sessions in its Sessions line (and `--json`).
- **`cortex studio`** (the primary monitor) flags stale sessions too — a `⚠ N stale` count in the
  header and a `⚠` on each stale row — so all monitoring surfaces (`sessions`, `doctor`, `overview`,
  `studio`) surface forgotten work consistently.

### Added — storage transparency in `config` and `doctor`
- **`cortex config`** now prints a **Storage (XDG)** section — the resolved config, sessions,
  archive, and cache paths — so it's obvious where Cortex keeps everything (and `--json` exposes
  `configDir`/`sessionsRoot`/`archiveRoot`/`cacheDir`). Auditability without guessing.
- **`cortex doctor`** gained a **Sessions** line — total · active · distinct repos · sessions root —
  a cross-workspace monitoring glance (also in `--json` under `sessions`).

### Added — phase-latency metrics + `cortex_metrics` on MCP
- **Time-in-phase** — `cortex metrics <taskId>` now derives per-phase durations and total elapsed
  from the phase history (`phaseDurations`, `elapsedMs`), and the workspace aggregate reports
  `meanTimeToComplete`. Shows *where* time goes (long investigating vs. long changing) — the "how do
  we work" signal.
- **`cortex_metrics` MCP tool** (14 tools total) — metrics were CLI-only; agents can now read a
  task's or the workspace's observability summary for self-assessment.

### Added — phase-transition history + `cortex timeline`
- **Every phase transition is now recorded** to a per-case `phases.jsonl` ledger (stamped in the
  kernel's `transition()` and on abort), so "when did this enter verifying?" is finally answerable —
  the `CaseFile` only ever kept the *current* phase.
- **`cortex timeline <taskId>`** (alias `activity`) — merges phase transitions, evidence, audited
  tool calls, and verification receipts into one time-sorted feed. This is the first reader of
  `commands.jsonl`, the audit log that until now only fed metrics. Works from any directory (the
  session is located by ID); `--json` for agents.
- **`cortex_timeline` MCP tool** (13 tools total) — the same feed for agents.
- Fix: `StartTask` now calls `store.Create` *before* the first transition (phase recording made the
  transition create the task dir, which Create then rejected).

### Added — live cross-workspace studio board with a loop stepper
- **`cortex studio` is now a live, global monitor** — it shows every session across every repo
  (was one workspace's case files), auto-refreshes every 2s, and draws the reasoning loop as a
  stepper (`orient─inv─plan─change─verify─keep`) with a "you are here" marker on the current phase,
  green completed steps, a `✓` on completion, and a `■` stop marker for blocked/abandoned cases.
- Keys: `a` toggles active-only, plus `--repo`/`--active` flags to scope the board on launch. Detail
  loading is workspace-independent (`kernel.LoadSession`), reading the central sessions tree.

### Added — XDG-organized sessions + cross-workspace audit view
- **Sessions default to a central, XDG-organized location** —
  `$XDG_STATE_HOME/cortex/sessions/<repo-slug>/<taskId>/`, so every session across every repository
  is visible and auditable in one place and the workspace tree stays clean. Config and cache follow
  the XDG spec too (`$XDG_CONFIG_HOME/cortex`, `$XDG_CACHE_HOME/cortex`); `$CORTEX_HOME` or a
  pre-existing `~/.cortex` collapses all three into one dir. Path resolution lives in
  `internal/config/paths.go`, mirroring codemap. Repo-local `.cortex/cases` is now **opt-in** via
  `cases_dir`, and an existing repo-local store is honored automatically so upgrades never strand
  active work.
- **`cortex sessions`** (alias `sess`) — lists every session across every repo (repo · phase · age ·
  verified/required · goal), newest first; `--repo` and `--active` filters; `--json` for agents.
  The audit/monitor surface the per-workspace `list` never provided.
- **`cortex_sessions` MCP tool** (12 tools total) — the same cross-workspace view for agents, so an
  agent can see everything it has open or left unverified anywhere.

### Changed — repo-local `.cortex/cases` is now an opt-in (was the default)
- Superseded by the central XDG default above. When opted in (`cases_dir: .cortex/cases`), the store
  is `<workspace>/.cortex/cases/` (was `.agent/cases/`) and Cortex still writes `.cortex/.gitignore`
  (`*`) so git/status/scope-drift stay clean.
- **`cases_dir` / `CORTEX_CASES_DIR` unchanged** — relative paths resolve against the workspace;
  absolute or `~/…` paths store cases anywhere.
- `config.StateDir` (`.cortex`); `AgentDir` remains as a deprecated alias.

### Fixed — completion gate, CLI exit codes, memory tags, lifecycle E2E
- **Failed verification no longer completes by default** — a failed receipt means the claim did
  not hold; `cortex remember` now requires a *passing* receipt, or an explicit
  `--accept-failed` / `acceptFailed` (or `--unverified` / `verificationNotPossible`). Reviews that
  REQUEST CHANGES set `AcceptFailed` so the case can still complete with an honest failed outcome.
- **`--json` exits non-zero when `ok: false`** — agents that only check process exit codes now see
  kernel rejections (plan/verify/remember/status). JSON is still printed first; the exit code
  mirrors the envelope.
- **Durable memory tags use `repo:<name>`** — never the bare repository string. A project named
  `cortex` no longer collapses into the product tag and pollutes recall with every `tmp.*` test
  workspace's memories. Investigate recall and fcheap failed-run tags share the same helper.
- **`investigate --depth` is wired** — `quick` runs the primary route tool only (smaller candidate
  budget); `deep` raises the candidate limit; `standard` is unchanged.
- **`cortex_list_tasks` MCP tool** (11 tools total) — workspace task index for agents that only
  speak MCP.
- **`specs/lifecycle.yml`** updated for the tightened completion gate: asserts bare remember is
  rejected when verification is only inconclusive, then completes with `--unverified`.

### Added — `cortex review`: evidence-backed branch & PR review
- **`cortex review`** reviews a branch or pull request as a first-class Cortex task: it resolves the
  diff (`base…HEAD`), gathers structural + semantic context, runs the verifiers over the change
  (structural review + the behavioral specs that cover it, via this release's auto-selection), and
  completes with a **verdict — approve / request-changes / needs-verification — where every claim is
  backed by an inspectable receipt** (`cortex status <taskId> --detail full`), not a black-box LGTM.
  - **Branch review is host-agnostic** (pure git): `cortex review` diffs the current branch against
    its fork point with the default branch; `--base <ref>` / `--head <ref>` override the scope.
  - **PRs work on GitHub *and* Bitbucket** without a host CLI — fetched by git ref (`pull/N/head`,
    `pull-requests/N/from`) via a small `internal/forge` host detector; when a host can't be fetched
    by ref (e.g. Bitbucket Cloud), it degrades honestly ("check out the branch and re-run with
    `--base`") rather than reviewing the wrong thing.
  - Threaded a **diff base ref** (`Workspace.BaseRef`) through the whole diff-scoped flow
    (`git diff base…HEAD`, `codemap review --since`, spec selection `--since-codemap`/`--since`) —
    backward-compatible: a change task with no base still diffs the working tree.
  - New git helpers (`RemoteURL`/`CurrentBranch`/`DefaultBranch`/`MergeBase`/`FetchRef`/`Checkout`)
    and `internal/forge` (`Detect`/`PRHeadRefspecs`), both with tests.
  - An adversarial review of the feature (each finding independently verified) found **11 real
    bugs**, all fixed with regression tests — most notably the review's own **`APPROVE`-while-a-
    required-verifier-never-ran** verdict (cortex catching the exact `not_run`-as-`passed` failure it
    exists to prevent): the verdict now gates on the required-verifier set, `--claim` augments rather
    than replaces the per-surface safety-net claims, `--head`/`--pr` review the requested ref (checked
    out, dirty-guarded, and restored afterward), a bad base ref is a hard error instead of a false
    "no changes", `ChangedFiles` no longer conflates a git error with an empty diff or pollutes a
    committed-range diff with untracked files, `DefaultBranch` no longer dead-codes its master
    fallback, and a re-review of a force-pushed PR force-updates the local ref. Verdict honesty is
    mutation-verified.

### Added — plan-time verification selection and observability
- **Verify-time auto-selection of covering specs** — when a behavioral surface is declared and a
  diff exists but no explicit spec is supplied, `cortex verify` now asks the verifier which specs
  cover the change (`cairn run --select-only` / `glyph affected-specs`) and runs them, turning a
  `not_run` receipt into a real verification instead of requiring the agent to name the spec. Bounded
  to 3 specs/surface, opt out with `--no-auto-specs`; a surface with no covering spec is reported
  honestly. New adapter methods `Cairntrace.SelectSpecs` / `Glyphrun.AffectedSpecs`
  (`internal/kernel/verify.go`).
- **`cortex metrics` — observability** — the per-call audit log was write-only;
  it now has a reader and a metrics engine. `cortex metrics <taskId>` reports outcome + evidence
  trail (tool calls, calls before first evidence, evidence items, verification coverage by surface,
  unresolved hypotheses, scope drift, memory reuse) and each tool's **task-level contribution**
  ("codemap: 2 calls, 1 evidence → N hypotheses"). `cortex metrics` (no arg) aggregates across
  the workspace (completion rate, verified-completion rate, mean tools/completed task, drift/
  unresolved/memory-reuse rates). `--json` for both (`internal/kernel/metrics.go`, `cmd/cortex/metrics.go`).
- **Evaluation harness** — a runnable benchmark (`internal/eval`, `task eval`) that
  scores each task type on a **correct outcome AND an adequate evidence trail**. All eight lifecycle
  categories are authored: three run with no external tooling (known-symbol → verified; stale index
  → honest degradation, no fabricated pass; misleading search → candidate-not-proof); five drive a
  live specialist tool and self-skip when it's absent (vague-UI/browser → an unproven browser claim
  is never reported verified; terminal regression → same via glyph; video → an invalid bundle
  degrades honestly, no fabricated owning-code claim; secret → a secret value never enters the
  evidence ledger; broad refactor → the high-risk verification gate fires). The scenarios are **verified
  load-bearing** by mutation (disabling redaction makes the secret scenario fail; disabling the
  high-risk gate makes the refactor scenario fail). The harness caught a real gap (below).

### Fixed
- **A blocked verification no longer satisfies the completion gate** (found by the eval harness) —
  when a verifier's tool is unavailable, its receipt is `blocked`, which proves nothing (like
  `not_run`). Completion now excludes both, so a task can't complete "verified enough" when its only
  verifier never actually ran (`internal/kernel/persist.go`).

An adversarial review of this session's new code (each finding independently verified) confirmed
four more, all fixed with regression tests:
- **Browser spec auto-selection was dead** (high) — `Cairntrace.SelectSpecs` sent `cairn run
  --select-only --json` with no spec positional, which errors `missing required argument 'spec'`, so
  cortex never auto-selected any browser spec (an arg-blind fake runner hid it). Now passes `.` (the
  workspace) as cairn requires; confirmed live (`internal/adapters/cairntrace.go`).
- **`callsBeforeFirstEvidence` was pinned to 0** on every git workspace (medium) — the git
  orientation record is stamped at task creation before any tool call, so it was always "first
  evidence". The metric now measures against the first *investigation* evidence, excluding
  orientation (`internal/kernel/metrics.go`).
- **`vaultLocked` false-positive** (low) — it scanned success stdout for the substring
  `vault_locked`, so a legitimate project/key name containing it would drop the real listing. The
  scan is now gated on a non-success exit (`internal/adapters/tvault.go`).
- **vecgrep old-binary fallback** (low) — the `-f json` fallback path didn't re-check "not in a
  vecgrep project", degrading a no-index case instead of reporting it honestly
  (`internal/adapters/vecgrep.go`).

### Added
- **vidtrace adapter** (the "investigate a bug video" workflow): Cortex now composes
  [vidtrace](https://github.com/abdul-hamid-achik/vidtrace), turning a screen recording into
  timestamped evidence and linking the visible failure to the code that owns it. A new
  `--video <bundle-or-stash-id>` on `cortex investigate` (and the `video` field on
  `cortex_investigate`) runs `vidtrace investigate … --connect`; video/recording questions also
  route to vidtrace to surface prior bug-video bundles. It's an artifact-class adapter that
  degrades safely when vidtrace isn't installed, like the others.

### Changed
- The TUI command is now **`cortex studio`** (matching the ecosystem's `codemap studio`); `board`
  and `tui` remain as aliases.

### Testing
- **studio TUI BDD spec** (`specs/studio.yml`) driven by glyphrun through a real PTY: opens the
  studio, verifies the header/task-list/detail render, navigates, and quits cleanly.
- **Adapter JSON-parsing tests** (`internal/adapters/parse_test.go`): fixture-based tests, using
  the real tool output shapes via a fake runner (CI-safe, no binaries), verify every adapter maps
  output → facts/status correctly — the previously-untested, most-fragile layer. Adapter coverage
  24% → 62%.
- **MCP server end-to-end tests** (`internal/mcp/server_test.go`): drive the full lifecycle
  (start→investigate→plan→verify→remember) over the go-sdk in-memory transport, plus tools/list
  and gate-rejection cases. mcp coverage 0% → 62%.
- **CLI tests** (`cmd/cortex/cli_test.go`): real command execution with stdout capture (start/list/
  status/doctor/plan-gate) plus flag-wiring units. cmd coverage 0% → 42%.

### Fixed
- **vidtrace `investigate` parser was wrong** (found by verifying against real stashes): it assumed
  a `matches`/`results` shape and didn't handle `{ok:false, error}`, so an invalid bundle would
  have been reported as a successful "0 candidates." Rewritten against the real contract
  (`{ok, evidence:[{time_seconds, ocr, …}]}`), with the error case surfaced as partial.
  `stash_list`'s wrapped `{stashes:[…]}` and `--connect`'s real `code_matches:[{file, text, score}]`
  field (no line number) were confirmed against live runs and locked in tests.

A multi-agent correctness review (adversarially verified against source) surfaced six real bugs,
all now fixed with regression tests:
- **Redaction leaks (security)** — single-quoted secret assignments (`TOKEN='…'`, YAML
  `password: '…'`) were never masked, and a quote embedded in a value truncated the mask so the
  tail leaked in plaintext. The assigned-secret pattern now consumes the whole value (double-,
  single-, or unquoted) so nothing survives.
- **Path traversal (security)** — a caller-supplied `taskId` (from MCP/CLI input) was joined into
  the case path unsanitized, so `../` could escape the cases root. The task ID is now sanitized
  like raw IDs already were.
- **Completion invariant bypass** — a `not_run` receipt counted as "a verification exists," letting
  a task complete with zero real verification and no warning. Completion now requires a receipt
  whose status is not `not_run` (or an explicit unverified acknowledgment).
- **Resolve on a terminal task** — `cortex resolve` had no phase guard, so it could mutate a
  completed task's hypotheses/evidence and diverge from the immutable summary + memory. It now
  rejects terminal phases, like `abort`.
- **Garbled evidence text** — callers/callees claims rendered "3 callerers"/"3 calleeers".

A second adversarial review of the newer code (vidtrace + config + verify paths) surfaced five more
real bugs, all now fixed with regression tests:
- **Double-scheme vidtrace URI** — investigating a bug video by stash id (`vidtrace://vt_123`)
  matched the bundle-path heuristic (its `//`), so the evidence fact was tagged
  `vidtrace://vidtrace://vt_123`. A `vidtrace://` reference is now always treated as a stash, not a
  bundle (`internal/kernel/investigate.go`).
- **Claim-surface misrouting** — the verify surface classifier matched bare substrings, so a claim
  about "the **tui**…" or "the **cli**…" was routed to the browser surface and "the **build**…"
  could be too. It now checks the terminal surface first using space-delimited tokens
  (`internal/kernel/verify.go`).
- **`CORTEX_MAX_AUTO_RETRIES` ignored** — the field was parsed from `cortex.yaml` but had no env
  override, so the documented variable silently did nothing. Added to `applyEnv`
  (`internal/config/file.go`).
- **`ExpandPath` corrupted paths** — it ran `os.ExpandEnv`, so a legitimate path containing `$`
  was mangled, and a real file named `~foo` was wrongly expanded. It now expands only a leading
  `~`/`~/…` and never touches env vars (`internal/config/config.go`).
- **Un-normalized risk band** — `--risk HIGH` (or stray whitespace) didn't match the lowercase
  `high` the risk escalation compares against, so a high-risk change skipped its extra-verification
  gate. The risk band is now canonicalized on task start (`internal/kernel/orient.go`).

A third adversarial review (kernel/store/adapters/MCP, each finding independently verified) surfaced
four more real bugs, all now fixed with regression tests:
- **Redaction leaked secret-named JSON fields (security)** — the assigned-secret pattern required
  `<key>\s*[:=]`, but in JSON (`{"api_key":"…"}`) the key's closing quote sits between the name and
  the `:`, so the match failed and the value passed through unmasked — the single most common
  serialization the tool ecosystem emits. The pattern now allows an optional closing quote after the
  key, so JSON, env, and YAML forms all mask (`internal/store/redact/redact.go`).
- **Raw-output cap corrupted large valid JSON (correctness)** — `max_raw_output_bytes_per_tool`
  (default 32 KiB) was applied to the stdout the adapter *parses*, not just the raw *retained* on
  disk, so a rich tool response (e.g. `vidtrace investigate --connect` on a large bundle) was
  truncated mid-JSON and misreported as unparseable/degraded. The cap now applies only at the
  storage boundary (`kernel.storeRaw`); adapters parse the full output, bounded solely by the 4 MiB
  memory backstop (`internal/adapters/exec.go`, `internal/kernel/kernel.go`).
- **`cortex resolve` confirmed on assertion alone** — `Resolve` never read its `Evidence` input, so
  a hypothesis could be promoted to high confidence with zero supporting evidence, and any cited
  evidence IDs were silently dropped from the provenance chain. Confirmation now *requires* cited
  evidence, the IDs are validated against the ledger and linked into the hypothesis and the
  resolution record (`internal/kernel/resolve.go`).
- **vidtrace `stash_list` reported success on failure** — unlike the `investigate` path, it ignored
  the in-band `{ok:false, error}` shape, so a failed stash listing became a fabricated "0 archived
  bundles." It now surfaces the failure as partial with the error, matching `investigate`
  (`internal/adapters/vidtrace.go`). *(vidtrace's real `stash list --json` emits `"ok": true` on
  success — confirmed against v0.14.0.)*

### Documentation
- **Hands-on tutorial** (`docs/tutorial.md`): a pedagogical end-to-end walkthrough that plants a
  real bug and takes it through the full loop (start → investigate → plan → verify → remember),
  explaining *why* each gate exists. Every command and every block of output is copied from an
  actual run, including high-risk escalation and `not_run`-vs-`passed` receipts.
- **FAQ** (`docs/faq.md`): ~30 quick answers grouped into getting-started, day-to-day, state &
  secrets, configuration & harnesses, and troubleshooting. Both pages are wired into the VitePress
  nav, sidebar, and homepage hero.

A survey of every tool-integration seam (each verified against the **live** sibling binary) found the
cortex adapters silently mis-parsing real output; fixed with real-contract regression tests:
- **fcheap adapter matched none of fcheap's real shapes** — `search` read `stash`/`snippet` (tool
  emits `stash_id`/`text`) → blank facts + empty stash URIs; `connect` decoded a bare array (tool
  emits `{…,matches:[…]}`) → **always** degraded; `list` read `files` (tool emits `file_count`) →
  every stash showed "0 files". All three now match fcheap v0.27.0, and the tests that encoded the
  wrong shapes were corrected (`internal/adapters/fcheap.go`).
- **cairntrace failure detail was dropped** — the shared behavioral decoder read `outcomes[].message`,
  which does not exist in cairn's `RunResult` v1 (`OutcomeResult` is `{id,status}`, `.strict()`); the
  failure reason lives in `steps[].error`. The decoder now surfaces failed-step errors, so a browser
  failure explains itself instead of reducing to a bare "FAILED" (`internal/adapters/glyphrun.go`,
  shared with cairn).
- **`mcphub add … --enabled` errored** — the onboarding command in the README, AGENTS, `serve.go`,
  and the docs used a flag mcphub 0.6.0 rejects (`unknown flag: --enabled`; enabled is the default).
  Corrected everywhere to `mcphub add cortex cortex serve`.

The same survey found kernel-internal contract gaps, fixed with regression tests:
- **Completion could hide unmet required verification** — `remember` completed as long as *any* one
  receipt was non-`not_run`, so a task could pass a code review yet leave a required browser verifier
  never run and complete as if fully verified, with no warning. Completion now reports exactly which
  required verifiers were not passed — visible, not silent (`internal/kernel/persist.go`).
- **Durable memory leaked unredacted, at overstated confidence** — the memory line (built from
  model-supplied goal/outcome) was written to vecgrep's global cross-project store with no redaction
  and a hardcoded `confidence=high`, even for unverified outcomes. It is now redacted at the write
  boundary and records confidence from actual verification.

### Documentation
- **`features.md` in every sibling repo** (codemap, vecgrep, cairntrace, glyphrun, file.cheap,
  tinyvault, vidtrace, mcphub): concrete, contract-level capability requests Cortex needs from each
  tool — each with the exact CLI/JSON shape and the cortex consumer path — for parallel implementation.

### Added — adopting the sibling tools' new contracts
Seven of those requests shipped in the sibling tools; Cortex now consumes them (every change is
backward-compatible — it degrades to prior behavior against an older binary — and each has a
real-contract regression test):
- **Durable memory recall is wired** — `cortex investigate` now recalls prior durable conclusions for
  the repo up front (`vecgrep memory recall`, scoped by the `cortex`+repo tags the persist phase
  writes), so a related past case becomes orientation instead of being re-derived. The memory tier
  was write-only until now. Provider-down recall is classified `unavailable`, never a fabricated
  empty (`internal/kernel/investigate.go`, `internal/adapters/vecgrep.go`).
- **vecgrep search uses `-f json-envelope`** — an unindexed workspace now returns an honest
  "semantic discovery unavailable — run `vecgrep index`" instead of a silent empty result the model
  would read as "no such code." The matched `content` snippet enriches each hit. Falls back to the
  bare-array `-f json` shape on older vecgrep (`internal/adapters/vecgrep.go`).
- **codemap: error envelope, call-graph enum, diff risk band** — a `{ok:false,code,hint}` failure
  (corrupt/missing index) is now `unavailable` with the remediation hint, not a confidently-wrong "no
  such symbol"; confidence keys off the stable `call_graph` enum (resolved/name/unresolved); and the
  diff-scoped `risk` band surfaces to ground the risk gate in the actual change
  (`internal/adapters/codemap.go`).
- **glyphrun: `errorKind`/`diagnostic`** — a contract-hash mismatch now tells the agent to re-stamp
  (`glyph spec verify --stamp`) and a spec-parse error says to fix the spec, instead of a bare
  "errored" (still inconclusive by policy) (`internal/adapters/glyphrun.go`).
- **fcheap: index-on-save + honest unindexed connect** — `Save` passes `--index` so cortex's own
  archived evidence is searchable (the archive→search loop was silently dead); `connect` reports an
  unindexed codebase honestly (`internal/adapters/fcheap.go`).
- **tvault: lock-free enumeration** — availability/list-keys use `--names-only`, which enumerates
  project/key **names** without unlocking the vault, so the boundary works non-interactively; a
  locked vault is an honest `unavailable` (`internal/adapters/tvault.go`).
- **vidtrace: precise line anchors + structured usage errors** — connect facts anchor at `file:line`
  (0.15 adds `line`), and a usage error surfaces its reason instead of degrading; `--connect` is
  guarded on a present codebase (`internal/adapters/vidtrace.go`).
- **`cortex doctor` is gateway-aware** — a new mcphub helper (`GatewaySelfCheck`, outside the adapter
  registry) reports whether cortex is registered on the gateway and whether it has ever been routed
  to (`registered but never routed → check mcphub sync`), degrading to an advisory when mcphub is
  old/absent — never a false "not registered" (`internal/adapters/mcphub.go`,
  `internal/kernel/gateway.go`, `cmd/cortex/doctor.go`; `cortex doctor --gateway-server --probe`).
- **cairntrace: canonical failure reason** (cairn ≥1.30, the 8th and final repo) — a failed/errored
  browser run now carries the authoritative `failure.message` and `summary` in one place, so the
  receipt states *why* the flow failed ("expected exactly 3 element(s) … observed 0") instead of a
  bare "FAILED"; it supersedes the per-step/outcome scan. The always-present `spec.contractHash` is
  recorded on the run bundle as a stable "verified against contract sha256:…" identity. cairn's
  Health probe also drops from the heavy `doctor` (which spawned codemap/vecgrep/vidtrace/tvault
  sub-processes) to a plain `--version` (`internal/adapters/cairntrace.go`, shared decoder in
  `internal/adapters/glyphrun.go`).

### Infrastructure
- **CI/release workflows** (`.github/workflows/ci.yml`, `release.yml`) matching the ecosystem:
  test + race + build + coverage and golangci-lint on push/PR, and a tag-triggered GoReleaser
  release. Sibling tools aren't installed in CI — adapters degrade and tool-dependent tests skip;
  glyphrun E2E specs stay local-only.
- Verified the **VitePress docs site builds cleanly** (all pages render, no dead links) and the
  GoReleaser build config produces a binary.

### Added
- **Configuration files** (`cortex.yaml`) and `CORTEX_*` env overrides:
  the budget, redaction literals, and case-file directory are now user-configurable — activating
  the override machinery that was wired but previously only settable to defaults. Precedence is
  defaults → global → project `.config` → project root → env. A new `cortex config` command shows
  the resolved values and which files were applied; a malformed file is ignored, not fatal.

### Security
- **Action classing + approval integration point**: every tool
  operation is now classified — `read_only` / `local_mutation` / `external_mutation` /
  `secreted_execution` — and the class is recorded in the command audit trail. The
  kernel gates mutation-class actions through a policy: read-only and local-mutation run freely,
  while **external mutation is refused by default** and secret-backed execution requires the
  tvault capability. A harness can install an `Approver` to grant them — the explicit approval
  integration point. This completes all seven action controls.
- **Artifact sensitivity labels**: a verification receipt is flagged `sensitive`
  when any of its linked evidence is sensitive, so a receipt (and its archived artifact) isn't
  shared or stashed carelessly.
- **Secret redaction at the evidence-record boundary**:
  every evidence claim and source URI is now redacted before it is persisted — not just adapter
  tool output. Human/model-supplied facts (e.g. `cortex resolve` reasons) that previously bypassed
  redaction are now masked, and a record whose text matched a secret shape is flagged
  `sensitivity: sensitive`. `config.RedactLiterals` is wired into the kernel redactor so known
  secret strings are masked too.

### Added
- **Budget enforcement**: the defined budget fields now actually bound behavior —
  `max_parallel_calls` bounds the health-probe fan-out with a semaphore (no more unbounded
  subprocess bursts), `max_candidate_files_returned` caps how many discovery hits a single search
  contributes to the ledger, and `max_raw_output_bytes_per_tool` is a per-tool, config-overridable
  output cap (with a 4 MiB memory backstop) instead of a hardcoded constant. Wired from
  `config.Budget` through the registry to every adapter.
- **Verifier version on receipts**: a verification receipt now records the verifier
  tool's version when known (e.g. `codemap version 0.35.10 …`), captured best-effort at verify
  time — completing the receipt metadata. `writeReceipt` was refactored to a struct form for
  readability.
- **Grounded durable memory**: the memory line written to vecgrep follows the structured format
  `repo / area / symbol / behavior / finding / evidence / confidence / commit` —
  including the owning symbols and any linked fcheap artifact, so cross-session recalls are
  grounded and reusable instead of a free-text blob.
- **Raw output persistence & retrieval**: every tool call's redacted raw
  output is now stored once under `raw/<id>.txt` in the case dir, and each evidence record's
  `rawRef` points at it — so the compact envelope stays small while the underlying detail is
  retrievable on demand. New `cortex read-artifact <taskId> <ref>` CLI command and
  `cortex_read_artifact` MCP tool (10 tools now) resolve a `case://…/raw/…` reference to its
  content (redacted before storage) or an `fcheap://` reference to retrieval guidance. Raw IDs are
  sanitized so a reference can't escape the case directory.
- **codemap annotation sink**: after a definitive browser
  or terminal verification, Cortex attaches the proven/failed behavior (with its evidence
  reference) to the code symbols the task declared it would change — the **structural memory**
  layer. It only annotates a declared boundary symbol (reasonable-confidence identification, never
  a guess) and only for pass/fail outcomes (an errored run teaches nothing). Best-effort: a
  codemap failure is a warning, not a hard error.
- **Risk-based review escalation**: a medium/high-risk change task warns when its
  mandatory structural diff review did not pass (e.g. codemap unindexed or unavailable).
- **Change-record check**: a change task with no detected diff warns before verifying,
  so "nothing changed" can't be silently verified.
- **fcheap stash-on-failure**: a failed browser or terminal
  verification run bundle is now archived to fcheap and the verification receipt links the durable
  `fcheap://stash/<id>` — closing the "ephemeral runs become memory" loop. Passing runs are not
  archived (low value). Fixed `Fcheap.Save`'s parsing of the flat `fcheap save --json` manifest.
- **Read-only retry**: read-only idempotent tool queries retry once on a transient
  process/transport failure; mutating ops (fcheap save, vecgrep remember) never retry.
- **Verification receipt limitations**: receipts now carry a "notes on limitations"
  line explaining why a claim is not a clean pass (not_run / inconclusive / failed).

### Fixed
- **Pass-count correctness**: the verified-claim count is computed from structured
  statuses, not by substring-matching "passed" against a string embedding the free-text claim — a
  claim whose text merely contains "passed" is no longer miscounted as verified.
- **Behavioral-verifier honesty**: an ambiguous *errored* run (infrastructure/spec
  error, cold-start gate, contract-hash mismatch) is now classified `inconclusive` at medium
  confidence, not collapsed into a high-confidence FAILED behavioral verdict.
- **Timeout budgets**: codemap → 20s (structural_query), vecgrep → 15s (code_search).
- **Routing negative rule**: a known-symbol question no longer schedules a vecgrep
  follow-up — it resolves directly via codemap.

### Added
- **Hypothesis resolution** (`cortex resolve` / `cortex_resolve`): mark a hypothesis
  confirmed, challenged, or rejected as evidence accumulates. History is retained and the
  resolution is appended to the evidence ledger with its reason — contradicting evidence never
  silently overwrites a prior explanation.
- **Investigation budget guard**: each `cortex investigate` round is counted against
  the budget (default 3). Exceeding it is allowed but warns — nudging the agent to form a
  hypothesis and plan rather than search indiscriminately — and the reason is recorded on the
  case. `cortex status` now reports the round count (`rounds N/budget`).

## [0.1.0] — 2026-07-06

First MVP. An evidence-guided agent kernel with three surfaces over one kernel.

### Added
- **Kernel** — the six cognitive actions (`start`, `investigate`, `plan`, `verify`, `remember`,
  `status`) enforced by a phase machine with hard invariants: the disproof-path gate, the
  change-boundary gate, claim→verifier receipts (`not_run` never renders as `passed`), and the
  completion invariant (no complete without a verification receipt).
- **CLI** (Cobra + Charm v2 lipgloss) with `--json` on every read command and TTY-gated color;
  plus `doctor`, `list`, `abort`, `read-evidence`, and `board`.
- **MCP server** (`cortex serve`) — eight tools over newline-delimited JSON-RPC.
- **board TUI** (`cortex board`) — a read-only Charm v2 (bubbletea) case-file browser.
- **Seven adapters** (git, codemap, vecgrep, cairntrace, glyphrun, fcheap, tvault) that speak each
  tool's real flag dialect and degrade safely when a binary is absent — never fabricating output.
- **Case-file store** — JSON/JSONL under `.agent/cases/<taskId>/`, with atomic snapshot writes.
- **Secret redaction** — masks secret shapes before any text reaches model-visible output; tvault
  stays a capability boundary (no secret values).
- **Scope-drift detection** — compares the real git diff to the declared change boundary.
- Docs (VitePress), glyphrun E2E specs, Taskfile, goreleaser, and golangci config.
