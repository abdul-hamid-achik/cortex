# Changelog

All notable changes to Cortex are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/); versions follow semver.

## [Unreleased]

### Changed — case files default to `.cortex/cases` (still fully configurable)
- **Default case store** is now `<workspace>/.cortex/cases/` (was `.agent/cases/`). The generic
  `.agent` name is shared by many tools; Cortex brands its own dir and still writes
  `.cortex/.gitignore` (`*`) so git/status/scope-drift stay clean.
- **`cases_dir` / `CORTEX_CASES_DIR` unchanged** — relative paths resolve against the workspace;
  absolute or `~/…` paths store cases outside the repo entirely (no in-repo ignore file written).
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

### Added — plan-time spec selection & observability (SPEC §14/§18)
- **Verify-time auto-selection of covering specs** — when a behavioral surface is declared and a
  diff exists but no explicit spec is supplied, `cortex verify` now asks the verifier which specs
  cover the change (`cairn run --select-only` / `glyph affected-specs`) and runs them, turning a
  `not_run` receipt into a real verification instead of requiring the agent to name the spec. Bounded
  to 3 specs/surface, opt out with `--no-auto-specs`; a surface with no covering spec is reported
  honestly. New adapter methods `Cairntrace.SelectSpecs` / `Glyphrun.AffectedSpecs`
  (`internal/kernel/verify.go`).
- **`cortex metrics` — observability (SPEC §18.1/§18.2)** — the per-call audit log was write-only;
  it now has a reader and a metrics engine. `cortex metrics <taskId>` reports outcome + evidence
  trail (tool calls, calls before first evidence, evidence items, verification coverage by surface,
  unresolved hypotheses, scope drift, memory reuse) and each tool's **task-level contribution**
  ("codemap: 2 calls, 1 evidence → N hypotheses", §18.2). `cortex metrics` (no arg) aggregates across
  the workspace (completion rate, verified-completion rate, mean tools/completed task, drift/
  unresolved/memory-reuse rates). `--json` for both (`internal/kernel/metrics.go`, `cmd/cortex/metrics.go`).
- **Evaluation harness (SPEC §18.3)** — a runnable benchmark (`internal/eval`, `task eval`) that
  scores each task type on a **correct outcome AND an adequate evidence trail**. All eight §18.3
  categories are authored: three run with no external tooling (known-symbol → verified; stale index
  → honest degradation, no fabricated pass; misleading search → candidate-not-proof); five drive a
  live specialist tool and self-skip when it's absent (vague-UI/browser → an unproven browser claim
  is never reported verified; terminal regression → same via glyph; video → an invalid bundle
  degrades honestly, no fabricated owning-code claim; secret → a secret value never enters the
  evidence ledger; broad refactor → the §13.3 high-risk gate fires). The scenarios are **verified
  load-bearing** by mutation (disabling redaction makes the secret scenario fail; disabling the
  §13.3 gate makes the refactor scenario fail). The harness caught a real gap (below).

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
- **vidtrace adapter** (SPEC §19.4 "investigate a bug video"): Cortex now composes
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
  `high` the §13.3 escalation compares against, so a high-risk change skipped its extra-verification
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
  actual run, including the §13.3 high-risk escalation and `not_run`-vs-`passed` receipts.
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

The same survey found kernel-internal gaps vs the SPEC, fixed with regression tests:
- **Completion could hide unmet required verification** — `remember` completed as long as *any* one
  receipt was non-`not_run`, so a task could pass a code review yet leave a required browser verifier
  never run and complete as if fully verified, with no warning. Completion now reports exactly which
  required verifiers were not passed (SPEC §6.2/§14.2) — visible, not silent (`internal/kernel/persist.go`).
- **Durable memory leaked unredacted, at overstated confidence** — the memory line (built from
  model-supplied goal/outcome) was written to vecgrep's global cross-project store with no redaction
  and a hardcoded `confidence=high`, even for unverified outcomes. It is now redacted at the write
  boundary and records confidence from actual verification (SPEC §8.6, §15.2, §16.2).

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
  diff-scoped `risk` band surfaces to ground the §13.3 gate in the actual change
  (`internal/adapters/codemap.go`).
- **glyphrun: `errorKind`/`diagnostic`** — a contract-hash mismatch now tells the agent to re-stamp
  (`glyph spec verify --stamp`) and a spec-parse error says to fix the spec, instead of a bare
  "errored" (still inconclusive, per §11.4) (`internal/adapters/glyphrun.go`).
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
- **Configuration files** (`cortex.yaml`) and `CORTEX_*` env overrides (SPEC §27 precedence):
  the budget, redaction literals, and case-file directory are now user-configurable — activating
  the override machinery that was wired but previously only settable to defaults. Precedence is
  defaults → global → project `.config` → project root → env. A new `cortex config` command shows
  the resolved values and which files were applied; a malformed file is ignored, not fatal.

### Security
- **Action classing + approval integration point** (SPEC §16.2 #3/#4, §16.3): every tool
  operation is now classified — `read_only` / `local_mutation` / `external_mutation` /
  `secreted_execution` — and the class is recorded in the command audit trail (§16.2 #7). The
  kernel gates mutation-class actions through a policy: read-only and local-mutation run freely,
  while **external mutation is refused by default** and secret-backed execution requires the
  tvault capability. A harness can install an `Approver` to grant them — the explicit approval
  integration point. This completes all seven §16.2 controls.
- **Artifact sensitivity labels** (SPEC §16.2 #5): a verification receipt is flagged `sensitive`
  when any of its linked evidence is sensitive, so a receipt (and its archived artifact) isn't
  shared or stashed carelessly.
- **Secret redaction at the evidence-record boundary** (SPEC §6.3 invariant #4, §16.2 #1):
  every evidence claim and source URI is now redacted before it is persisted — not just adapter
  tool output. Human/model-supplied facts (e.g. `cortex resolve` reasons) that previously bypassed
  redaction are now masked, and a record whose text matched a secret shape is flagged
  `sensitivity: sensitive`. `config.RedactLiterals` is wired into the kernel redactor so known
  secret strings are masked too.

### Added
- **Budget enforcement** (SPEC §7.3): the defined budget fields now actually bound behavior —
  `max_parallel_calls` bounds the health-probe fan-out with a semaphore (no more unbounded
  subprocess bursts), `max_candidate_files_returned` caps how many discovery hits a single search
  contributes to the ledger, and `max_raw_output_bytes_per_tool` is a per-tool, config-overridable
  output cap (with a 4 MiB memory backstop) instead of a hardcoded constant. Wired from
  `config.Budget` through the registry to every adapter.
- **Verifier version on receipts** (SPEC §14.3): a verification receipt now records the verifier
  tool's version when known (e.g. `codemap version 0.35.10 …`), captured best-effort at verify
  time — completing the §14.3 receipt fields. `writeReceipt` was refactored to a struct form for
  readability.
- **Grounded durable memory** (SPEC §15.3): the memory line written to vecgrep now follows the
  spec format — `repo / area / symbol / behavior / finding / evidence / confidence / commit` —
  including the owning symbols and any linked fcheap artifact, so cross-session recalls are
  grounded and reusable instead of a free-text blob.
- **Raw output persistence & retrieval** (SPEC §11.4, §10.4): every tool call's redacted raw
  output is now stored once under `raw/<id>.txt` in the case dir, and each evidence record's
  `rawRef` points at it — so the compact envelope stays small while the underlying detail is
  retrievable on demand. New `cortex read-artifact <taskId> <ref>` CLI command and
  `cortex_read_artifact` MCP tool (10 tools now) resolve a `case://…/raw/…` reference to its
  content (redacted before storage) or an `fcheap://` reference to retrieval guidance. Raw IDs are
  sanitized so a reference can't escape the case directory.
- **codemap annotation sink** (SPEC §12.2, §15.1, acceptance §25 #7): after a definitive browser
  or terminal verification, Cortex attaches the proven/failed behavior (with its evidence
  reference) to the code symbols the task declared it would change — the **structural memory**
  layer. It only annotates a declared boundary symbol (reasonable-confidence identification, never
  a guess) and only for pass/fail outcomes (an errored run teaches nothing). Best-effort: a
  codemap failure is a warning, not a hard error.
- **Risk-based review escalation** (SPEC §13.3): a medium/high-risk change task warns when its
  mandatory structural diff review did not pass (e.g. codemap unindexed or unavailable).
- **Change-record check** (SPEC §6.2): a change task with no detected diff warns before verifying,
  so "nothing changed" can't be silently verified.
- **fcheap stash-on-failure** (SPEC §12.6, acceptance §25 #6): a failed browser or terminal
  verification run bundle is now archived to fcheap and the verification receipt links the durable
  `fcheap://stash/<id>` — closing the "ephemeral runs become memory" loop. Passing runs are not
  archived (low value). Fixed `Fcheap.Save`'s parsing of the flat `fcheap save --json` manifest.
- **Read-only retry** (SPEC §17.3): read-only idempotent tool queries retry once on a transient
  process/transport failure; mutating ops (fcheap save, vecgrep remember) never retry.
- **Verification receipt limitations** (SPEC §14.3): receipts now carry a "notes on limitations"
  line explaining why a claim is not a clean pass (not_run / inconclusive / failed).

### Fixed
- **Pass-count correctness** (SPEC §14.2): the verified-claim count is computed from structured
  statuses, not by substring-matching "passed" against a string embedding the free-text claim — a
  claim whose text merely contains "passed" is no longer miscounted as verified.
- **Behavioral-verifier honesty** (SPEC §11.4): an ambiguous *errored* run (infrastructure/spec
  error, cold-start gate, contract-hash mismatch) is now classified `inconclusive` at medium
  confidence, not collapsed into a high-confidence FAILED behavioral verdict.
- **Timeout budgets** (SPEC §17.2): codemap → 20s (structural_query), vecgrep → 15s (code_search).
- **Routing negative rule** (SPEC §7.2): a known-symbol question no longer schedules a vecgrep
  follow-up — it resolves directly via codemap.

### Added
- **Hypothesis resolution** (`cortex resolve` / `cortex_resolve`): mark a hypothesis
  confirmed, challenged, or rejected as evidence accumulates. History is retained and the
  resolution is appended to the evidence ledger with its reason — contradicting evidence never
  silently overwrites a prior explanation (SPEC §9.3).
- **Investigation budget guard** (SPEC §7.3): each `cortex investigate` round is counted against
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
