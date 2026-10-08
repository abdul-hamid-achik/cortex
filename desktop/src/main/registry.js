/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Declarative registry of every Cortex feature reachable from the CLI.
//
// This file is the single source of truth the desktop shell renders from: the
// console forms, the command palette, inline row actions, and the feature
// catalog are all projections of it. Adding a Cortex command means adding an
// entry here — nothing in the renderer hardcodes argv.

export const GROUPS = [
  { id: 'loop', label: 'Task loop', hint: 'orient → investigate → plan → change → verify → remember' },
  { id: 'change', label: 'Change ownership', hint: 'bounded, expiring leases' },
  { id: 'cases', label: 'Sessions & cases', hint: 'inspect, monitor, retire' },
  { id: 'collab', label: 'Collaboration', hint: 'notes, decisions, handoffs' },
  { id: 'longrun', label: 'Long-running work', hint: 'survey, findings, dossier, campaigns, jobs' },
  { id: 'evidence', label: 'Evidence & memory', hint: 'ledgers, artifacts, cross-case recall' },
  { id: 'review', label: 'Review', hint: 'diff-scoped branch and PR review' },
  { id: 'env', label: 'Environment', hint: 'doctor, setup, config, routing' },
  { id: 'mcp', label: 'MCP server', hint: 'the agent surface over stdio' },
  { id: 'repo', label: 'Repository tooling', hint: 'Taskfile, specs, contracts, docs' },
];

// kind drives confirmation UX and the badge color:
//   read       — safe, side-effect free
//   mutate     — writes case state (still bounded and audited by the kernel)
//   dryrun     — reports by default, mutates only with an explicit flag
//   destructive— irreversible; always confirmed twice in the UI
//   server     — long-lived process
const KIND = {
  read: 'read',
  mutate: 'mutate',
  dryrun: 'dryrun',
  destructive: 'destructive',
  server: 'server',
};

const SURFACES = ['code', 'browser', 'terminal', 'artifact', 'secret'];
const MODES = ['change', 'investigate', 'review', 'survey'];
const RISKS = ['low', 'medium', 'high'];

const taskArg = (label = 'Task ID', required = true) => ({
  name: 'taskId',
  label,
  type: 'task',
  required,
  help: 'A cortex task id (task_…). Filled from the selected case when you launch this from a case view.',
});

const surfaceFlag = (extra = {}) => ({
  name: '--surface',
  label: 'Surface',
  type: 'enum',
  options: SURFACES,
  repeatable: true,
  help: 'User-visible surface this work touches. Repeatable.',
  ...extra,
});

const actorFlag = (extra = {}) => ({
  name: '--actor',
  label: 'Actor',
  type: 'text',
  placeholder: 'agent-auth',
  help: 'Stable person or agent identifier recorded as provenance.',
  ...extra,
});

const criterionFlag = () => ({
  name: '--criterion',
  label: 'Acceptance criterion',
  type: 'text',
  repeatable: true,
  placeholder: 'checkout_return=Login started at checkout returns to checkout',
  help: 'Immutable id=statement pair. Once registered it cannot be edited or removed.',
});

const processCriterionFlag = () => ({
  name: '--process-criterion',
  label: 'Process criterion',
  type: 'text',
  repeatable: true,
  placeholder: 'no_commit=No commit is made',
  help: 'Immutable id=statement process rule. Satisfiable by an evidence-backed verify --attest instead of a verifier.',
});

const stringArray = (name, label, help, placeholder) => ({
  name,
  label,
  type: 'text',
  repeatable: true,
  placeholder,
  help,
});

/** Every registry entry, grouped. */
export const COMMANDS = [
  // ── Task loop ────────────────────────────────────────────────────────────
  {
    id: 'open',
    group: 'loop',
    path: ['open'],
    title: 'Open or resume a task',
    summary:
      'Retry-safely resumes matching active work or starts one durable case. An idempotency key wins over heuristic matching; a new case can register an immutable acceptance contract.',
    usage: 'cortex open <goal> [flags]',
    kind: KIND.mutate,
    json: true,
    loopStep: 1,
    args: [{ name: 'goal', label: 'Goal', type: 'textarea', required: true, placeholder: 'Fix post-login checkout redirect', help: 'What "done" means in user-visible terms.' }],
    flags: [
      { name: '--mode', label: 'Mode', type: 'enum', options: MODES, default: 'change', help: 'change edits code, investigate only learns, review audits a diff, survey maps a repository.' },
      surfaceFlag(),
      actorFlag(),
      { name: '--risk', label: 'Risk', type: 'enum', options: RISKS, default: 'medium', help: 'Risk band; high-risk changes require drift acknowledgment at verify time.' },
      { name: '--idempotency-key', label: 'Idempotency key', type: 'text', placeholder: 'checkout-redirect', help: 'Stable retry key. An exact match returns the existing task, even after completion.' },
      criterionFlag(),
      processCriterionFlag(),
      stringArray('--allow-path', 'Allowed path', 'Immutable owner path contract (path.Match pattern). Plans and changes outside it are refused.', 'internal/auth/*.go'),
      { name: '--parent', label: 'Parent task', type: 'task', help: 'Parent task id for delegated work (workplan items and converted findings set this).' },
      stringArray('--seed', 'Orientation seed', 'Note or packet file stamped into orientation evidence (max 8).', 'handoff.md'),
    ],
    examples: [
      'cortex open "Fix post-login checkout redirect" --surface code --surface browser --actor agent-auth --idempotency-key checkout-redirect',
    ],
    docs: 'docs/cli.md',
    related: ['start', 'investigate', 'status'],
  },
  {
    id: 'start',
    group: 'loop',
    path: ['start'],
    title: 'Start a deliberately fresh case',
    summary: 'Opens a new case and orients it (git identity + specialist tool health). Use only when a fresh case is genuinely required — `open` is the retry-safe default.',
    usage: 'cortex start <goal> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [{ name: 'goal', label: 'Goal', type: 'textarea', required: true }],
    flags: [
      { name: '--mode', label: 'Mode', type: 'enum', options: MODES, default: 'change' },
      surfaceFlag(),
      { name: '--risk', label: 'Risk', type: 'enum', options: RISKS, default: 'medium' },
      criterionFlag(),
      processCriterionFlag(),
      stringArray('--allow-path', 'Allowed path', 'Immutable owner path contract (path.Match pattern). Plans and changes outside it are refused.', 'internal/auth/*.go'),
    ],
    docs: 'docs/cli.md',
    related: ['open'],
  },
  {
    id: 'investigate',
    group: 'loop',
    path: ['investigate'],
    title: 'Investigate a question',
    summary:
      'Routes a question through discovery (vecgrep) then structure (codemap) and records evidence with provenance. Search output is a candidate, not proof; weak rounds record zero facts. Deep depth decomposes compound questions into sub-queries.',
    usage: 'cortex investigate <taskId> [question] [flags]',
    kind: KIND.mutate,
    json: true,
    loopStep: 2,
    aliases: ['inv'],
    args: [
      taskArg(),
      { name: 'question', label: 'Question', type: 'textarea', placeholder: 'where is the OAuth return URL handled', help: 'Optional in survey fan-out mode.' },
    ],
    flags: [
      { name: '--depth', label: 'Depth', type: 'enum', options: ['quick', 'standard', 'deep'], default: 'standard', help: 'deep decomposes compound questions into ≤5 sub-queries.' },
      surfaceFlag({ name: '--surface', label: 'Override surfaces', help: 'Override routing surfaces for this round.' }),
      { name: '--module', label: 'Module scope', type: 'text', placeholder: 'internal/kernel', help: 'Survey: scope discovery to one directory and record coverage for it.' },
      { name: '--fanout', label: 'Fan out unseen modules', type: 'boolean', help: 'Survey: queue one background round per unseen module.' },
      { name: '--max', label: 'Fan-out size', type: 'int', default: 5, help: 'Max 32.' },
      { name: '--async', label: 'Detached background job', type: 'boolean', help: 'Run the round in a background job; poll with job list.' },
      { name: '--video', label: 'Bug video', type: 'text', placeholder: 'vidtrace stash id or bundle path', help: 'Runs vidtrace → code investigation.' },
    ],
    examples: ['cortex investigate task_06FK… "where is the OAuth return URL handled" --depth deep'],
    docs: 'docs/cli.md',
    related: ['plan', 'coverage', 'job.list', 'route'],
  },
  {
    id: 'plan',
    group: 'loop',
    path: ['plan'],
    title: 'Declare the planning gate',
    summary:
      'Every hypothesis must carry a disproof path and a change task must declare a boundary — plans without one are rejected. Uncertainty must be stated explicitly.',
    usage: 'cortex plan <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    loopStep: 3,
    args: [taskArg()],
    flags: [
      stringArray('--hypothesis', 'Hypothesis', 'A falsifiable statement. Supports the inline "statement :: disproof" form.', 'returnTo is dropped before callback :: run login-from-checkout browser flow'),
      stringArray('--disprove', 'Disproof path', 'Matched positionally with --hypothesis. Required.', 'run login-from-checkout browser flow'),
      stringArray('--support', 'Evidence support', 'hypothesis-index=evidence-id[,evidence-id…]', '0=ev_06FK…'),
      stringArray('--file', 'Boundary file', 'A file inside the declared change boundary.', 'src/auth/callback.ts'),
      stringArray('--symbol', 'Boundary symbol', 'A symbol inside the declared change boundary.', 'HandleCallback'),
      { name: '--boundary-reason', label: 'Boundary reason', type: 'textarea', help: 'Why these files/symbols are the expected change set.' },
      { name: '--uncertainty', label: 'Uncertainty', type: 'textarea', required: true, help: 'Explicit statement of what remains uncertain (required).' },
      { name: '--confidence', label: 'Confidence', type: 'enum', options: ['high', 'medium', 'low', 'unknown'], default: 'low' },
      stringArray('--verify', 'Required verifier', 'A verifier the plan requires: codemap_review, cairntrace_flow, glyphrun_flow, …', 'codemap_review'),
      stringArray('--timeout', 'Tool timeout', 'Per-tool timeout override as tool=duration.', 'codemap=45s'),
    ],
    examples: [
      'cortex plan task_06FK… --hypothesis "returnTo is dropped :: run login-from-checkout flow" --file src/auth/callback.ts --uncertainty "unsure whether state signing strips it"',
    ],
    docs: 'docs/cli.md',
    related: ['begin-change', 'resolve', 'verify'],
  },
  {
    id: 'begin-change',
    group: 'loop',
    path: ['begin-change'],
    title: 'Claim bounded change ownership',
    summary:
      'Atomically acquires the bounded, expiring change lease for an actor and moves planned → changing. Competing actors lose the compare-and-swap race.',
    usage: 'cortex begin-change <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    loopStep: 4,
    args: [taskArg()],
    flags: [
      actorFlag({ required: true, help: 'The actor taking ownership. Verify must run as this actor.' }),
      { name: '--ttl', label: 'Lease TTL', type: 'duration', default: '15m', help: 'Bounded ownership duration (1s to 1h).' },
    ],
    docs: 'docs/cli.md',
    related: ['lease.renew', 'lease.release', 'verify'],
  },
  {
    id: 'verify',
    group: 'loop',
    path: ['verify'],
    title: 'Verify claims and write receipts',
    summary:
      'Runs a structural diff review, any behavioral specs, and scope-drift detection. Each typed claim gets a receipt; a claim with no relevant verifier is recorded not_run — never passed. Receipts publish as one revision-bound batch.',
    usage: 'cortex verify <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    loopStep: 5,
    args: [taskArg()],
    flags: [
      stringArray('--claim', 'Claim', 'A user-facing claim to prove.', 'the OAuth callback preserves the return URL'),
      stringArray('--claim-id', 'Claim ID', 'Stable id per --claim; required to prove a registered acceptance criterion.', 'checkout_return'),
      stringArray('--claim-surface', 'Claim surface', 'Explicit surface per --claim.', 'browser'),
      stringArray('--claim-verifier', 'Claim verifier', 'Optional exact verifier per --claim.', 'cairntrace'),
      stringArray('--claim-contract', 'Claim contract', 'Required exact spec/check per --claim.', 'specs/cairntrace/checkout_return.yml'),
      stringArray('--claim-spec', 'Typed claim (self-contained)', 'One self-contained claim: id=|surface=|verifier=|contract=|<statement>. Replaces the coupled --claim-* flags.', 'id=checkout_return|surface=browser|contract=specs/cairntrace/checkout_return.yml|the callback preserves the return URL'),
      { name: '--from-plan', label: 'Materialize from plan', type: 'boolean', help: 'Build typed claims from acceptance criteria and the plan’s verification requirements.' },
      { name: '--browser-spec', label: 'Browser spec', type: 'path', help: 'cairntrace spec path proving browser claims.' },
      { name: '--terminal-spec', label: 'Terminal spec', type: 'path', help: 'glyphrun spec path proving terminal claims.' },
      { name: '--artifact-ref', label: 'Artifact ref', type: 'text', help: 'fcheap stash id or fcheap:// URI proving an artifact claim.' },
      { name: '--secret-project', label: 'Secret project', type: 'text', help: 'tvault project whose value-free availability proves secret capability.' },
      actorFlag({ help: 'Defaults to the active lease owner. Pass explicitly only to assert a specific actor — it must match the owner.' }),
      stringArray('--changed-file', 'Changed file override', 'Override the changed-file set (derived from git when omitted).', 'src/auth/callback.ts'),
      { name: '--no-auto-specs', label: 'Disable spec auto-selection', type: 'boolean', help: 'Do not auto-select and run the specs covering the change.' },
      { name: '--no-op', label: 'Acknowledge no diff', type: 'boolean', help: 'Explicitly acknowledge that this change task intentionally produced no diff.' },
      { name: '--ack-drift', label: 'Acknowledge scope drift', type: 'boolean', help: 'Acknowledge unexpected files on a high-risk change so verification may proceed.' },
      { name: '--attest', label: 'Attest process criterion', type: 'text', repeatable: true, placeholder: 'no_commit=ev_123|checked git log', help: 'id=evidence-id[,evidence-id...][|note] for a process criterion. Never verifier proof.' },
    ],
    examples: [
      'cortex verify task_06FK… --claim-spec "id=checkout_return|surface=browser|contract=specs/cairntrace/checkout_return.yml|Login started at checkout returns to checkout" --browser-spec specs/cairntrace/checkout_return.yml',
    ],
    docs: 'docs/cli.md',
    related: ['status', 'remember', 'begin-change'],
  },
  {
    id: 'remember',
    group: 'loop',
    path: ['remember'],
    title: 'Persist the outcome and complete',
    summary:
      'Normal completion requires the canonical assessment to be verified. Explicit acknowledgments preserve partial, unverified, or failed outcomes honestly instead of faking green.',
    usage: 'cortex remember <taskId> <outcome> [flags]',
    kind: KIND.mutate,
    json: true,
    loopStep: 6,
    aliases: ['complete'],
    args: [
      taskArg(),
      { name: 'outcome', label: 'Outcome', type: 'textarea', required: true, placeholder: 'returnTo was dropped from signed state; fixed and browser-verified' },
    ],
    flags: [
      stringArray('--tag', 'Tag', 'Tag for durable-memory recall.', 'auth'),
      { name: '--importance', label: 'Importance', type: 'number', default: 0.5, help: '0..1 importance for durable memory.' },
      { name: '--unverified', label: 'Acknowledge partial/unverified', type: 'boolean', help: 'Complete with an explicit partial or unverified assessment.' },
      { name: '--accept-failed', label: 'Acknowledge failed verification', type: 'boolean' },
      { name: '--accept-open-children', label: 'Accept open children', type: 'boolean', help: 'Complete a parent while child tasks are still in flight.' },
      { name: '--accept-partial-coverage', label: 'Accept partial coverage', type: 'boolean', help: 'Complete a survey while ledger modules remain unseen.' },
      { name: '--accept-missing-criteria', label: 'Accept missing criteria', type: 'text', placeholder: 'retry_ok,no_commit', help: 'Record exactly the unproven acceptance criteria (comma-separated ids) as unmet. The outcome can only be partial.' },
    ],
    docs: 'docs/cli.md',
    related: ['status', 'handoff', 'dossier.add'],
  },
  {
    id: 'resolve',
    group: 'loop',
    path: ['resolve'],
    title: 'Resolve a hypothesis',
    summary: 'Marks a hypothesis confirmed, challenged, or rejected without erasing history; the resolution is appended to the evidence ledger with your reason.',
    usage: 'cortex resolve <taskId> <hypothesisId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [
      taskArg(),
      { name: 'hypothesisId', label: 'Hypothesis ID', type: 'text', required: true, placeholder: 'hyp_06FK…', help: 'Pick one from the case’s Hypotheses tab.' },
    ],
    flags: [
      { name: '--status', label: 'Status', type: 'enum', options: ['confirmed', 'challenged', 'rejected'], required: true },
      { name: '--reason', label: 'Reason', type: 'textarea', required: true, help: 'What evidence changed the status.' },
      stringArray('--evidence', 'Evidence IDs', 'Supporting or contradicting evidence ids.', 'ev_06FK…'),
    ],
    docs: 'docs/cli.md',
    related: ['plan', 'status'],
  },
  {
    id: 'status',
    group: 'loop',
    path: ['status'],
    title: 'Agent checkpoint status',
    summary: 'Phase, unresolved hypotheses, scope drift, missing verification, the canonical assessment, and structured next actions. `--detail full` adds tool health and discovery index readiness.',
    usage: 'cortex status <taskId> [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg()],
    flags: [{ name: '--detail', label: 'Detail', type: 'enum', options: ['standard', 'full'], default: 'standard' }],
    docs: 'docs/cli.md',
    related: ['show', 'timeline', 'metrics'],
  },
  {
    id: 'show',
    group: 'loop',
    path: ['show'],
    title: 'One-screen session view',
    summary: 'Loop position, hypotheses, verification receipts, time-in-phase, and recent activity with exact ledger totals. Locates the session by id from any directory.',
    usage: 'cortex show <taskId> [flags]',
    kind: KIND.read,
    json: true,
    aliases: ['view'],
    args: [taskArg()],
    flags: [],
    docs: 'docs/cli.md',
    related: ['status', 'timeline'],
  },
  {
    id: 'abort',
    group: 'loop',
    path: ['abort'],
    title: 'Abort a task',
    summary: 'Stops a task without deleting its evidence. The reason is recorded in the case.',
    usage: 'cortex abort <taskId> <reason> [flags]',
    kind: KIND.mutate,
    json: true,
    confirm: 'Abort this task? Evidence is preserved, but the case leaves the active loop.',
    args: [taskArg(), { name: 'reason', label: 'Reason', type: 'textarea', required: true }],
    flags: [],
    docs: 'docs/cli.md',
    related: ['archive', 'prune'],
  },

  // ── Change ownership ─────────────────────────────────────────────────────
  {
    id: 'lease.renew',
    group: 'change',
    path: ['lease', 'renew'],
    title: 'Renew a change lease',
    summary: 'Extends an active lease owned by the same actor. A released or expired lease may be replaced by another actor.',
    usage: 'cortex lease renew <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg()],
    flags: [actorFlag({ required: true }), { name: '--ttl', label: 'TTL', type: 'duration', default: '15m' }],
    docs: 'docs/cli.md',
    related: ['begin-change', 'lease.release'],
  },
  {
    id: 'lease.release',
    group: 'change',
    path: ['lease', 'release'],
    title: 'Release a change lease',
    summary: 'Gives up bounded change ownership so another actor can claim it.',
    usage: 'cortex lease release <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg()],
    flags: [actorFlag({ required: true })],
    docs: 'docs/cli.md',
    related: ['begin-change', 'lease.renew'],
  },

  // ── Sessions & cases ─────────────────────────────────────────────────────
  {
    id: 'list',
    group: 'cases',
    path: ['list'],
    title: 'List workspace tasks',
    summary: 'Every task in the current workspace, newest first.',
    usage: 'cortex list [flags]',
    kind: KIND.read,
    json: true,
    aliases: ['ls'],
    args: [],
    flags: [],
    docs: 'docs/cli.md',
    related: ['sessions', 'overview'],
  },
  {
    id: 'sessions',
    group: 'cases',
    path: ['sessions'],
    title: 'All sessions, every repository',
    summary: 'The central XDG audit view across every repo, with repo/active/stale/archived/query filters. Repo-local `cases_dir` sessions are not listed here.',
    usage: 'cortex sessions [flags]',
    kind: KIND.read,
    json: true,
    aliases: ['sess'],
    args: [],
    flags: [
      { name: '--repo', label: 'Repository contains', type: 'text', placeholder: 'cortex' },
      { name: '--query', label: 'Query', type: 'text', placeholder: 'billing partial', help: 'Case-insensitive AND terms across id, goal, state, mode, repo, workspace, outcome.' },
      { name: '--active', label: 'In-flight only', type: 'boolean' },
      { name: '--stale', label: 'Stale only', type: 'boolean' },
      { name: '--archived', label: 'Archived', type: 'boolean' },
      { name: '--stale-after', label: 'Stale after', type: 'duration', default: '24h' },
    ],
    docs: 'docs/cli.md',
    related: ['overview', 'show', 'prune'],
  },
  {
    id: 'overview',
    group: 'cases',
    path: ['overview'],
    title: 'Cross-repo dashboard',
    summary: 'Totals, active/stale counts, completion and verified-completion rates, mean time to complete, and a per-repo breakdown. Workspace-independent.',
    usage: 'cortex overview [flags]',
    kind: KIND.read,
    json: true,
    aliases: ['dash'],
    args: [],
    flags: [{ name: '--stale-after', label: 'Stale after', type: 'duration', default: '24h' }],
    docs: 'docs/cli.md',
    related: ['sessions', 'metrics'],
  },
  {
    id: 'timeline',
    group: 'cases',
    path: ['timeline'],
    title: 'Chronological session feed',
    summary: 'Phase transitions, evidence, audited tool calls, and verification receipts merged into one time-sorted audit trail.',
    usage: 'cortex timeline <taskId> [flags]',
    kind: KIND.read,
    json: true,
    aliases: ['activity'],
    args: [taskArg()],
    flags: [],
    docs: 'docs/cli.md',
    related: ['show', 'metrics'],
  },
  {
    id: 'metrics',
    group: 'cases',
    path: ['metrics'],
    title: 'Outcome & evidence metrics',
    summary: 'With a task id: tool calls, calls before first evidence, verification coverage by surface, scope drift, memory reuse, and per-tool contribution. Without: workspace aggregates.',
    usage: 'cortex metrics [taskId] [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg('Task ID (empty = workspace aggregate)', false)],
    flags: [],
    docs: 'docs/cli.md',
    related: ['overview', 'timeline'],
  },
  {
    id: 'resume',
    group: 'cases',
    path: ['resume'],
    title: 'Checkpoint + deltas',
    summary: 'The recovery packet for a case plus everything that changed since a cursor — what an agent reads after context loss.',
    usage: 'cortex resume <taskId> [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg()],
    flags: [
      { name: '--since', label: 'Since', type: 'text', placeholder: '2026-09-01T12:00:00Z', help: 'RFC3339 cursor; return only records after it.' },
      { name: '--limit', label: 'Limit', type: 'int', default: 50 },
    ],
    docs: 'docs/long-running.md',
    related: ['handoff', 'show'],
  },
  {
    id: 'archive',
    group: 'cases',
    path: ['archive'],
    title: 'Archive a finished session',
    summary: 'Moves a terminal session out of the active tree. Reversible with unarchive; nothing is deleted. In-flight sessions are refused.',
    usage: 'cortex archive <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    confirm: 'Archive this session? It leaves the active view but stays recoverable with unarchive.',
    args: [taskArg()],
    flags: [],
    docs: 'docs/cli.md',
    related: ['unarchive', 'rm', 'prune'],
  },
  {
    id: 'unarchive',
    group: 'cases',
    path: ['unarchive'],
    title: 'Restore an archived session',
    summary: 'Brings an archived session back into the active view.',
    usage: 'cortex unarchive <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg()],
    flags: [],
    docs: 'docs/cli.md',
    related: ['archive'],
  },
  {
    id: 'prune',
    group: 'cases',
    path: ['prune'],
    title: 'Prune forgotten in-flight sessions',
    summary: 'Dry run by default: reports in-flight sessions idle beyond --older-than. With --apply each is aborted (reason recorded) and archived.',
    usage: 'cortex prune [flags]',
    kind: KIND.dryrun,
    json: true,
    args: [],
    flags: [
      { name: '--older-than', label: 'Older than', type: 'text', default: '7d', placeholder: '7d | 24h' },
      { name: '--repo', label: 'Repository contains', type: 'text' },
      { name: '--apply', label: 'Apply (abort + archive)', type: 'boolean', danger: true, help: 'Without this flag nothing changes.' },
    ],
    docs: 'docs/cli.md',
    related: ['sessions', 'archive'],
  },
  {
    id: 'rm',
    group: 'cases',
    path: ['rm'],
    title: 'Permanently delete a session',
    summary: 'Dry run without --force. Refuses in-flight sessions. Irreversible — prefer archive when you only want it out of the way.',
    usage: 'cortex rm <taskId> [flags]',
    kind: KIND.destructive,
    json: true,
    aliases: ['delete'],
    confirm: 'Permanently delete this session directory and everything under it? This cannot be undone.',
    args: [taskArg()],
    flags: [{ name: '--force', label: 'Force delete', type: 'boolean', danger: true, help: 'Without this flag rm is a dry run.' }],
    docs: 'docs/cli.md',
    related: ['archive'],
  },

  // ── Collaboration ────────────────────────────────────────────────────────
  {
    id: 'note',
    group: 'collab',
    path: ['note'],
    title: 'Attach a note',
    summary: 'Records a human or agent observation, decision, constraint, or handoff note with provenance — without treating prose as verification.',
    usage: 'cortex note <taskId> <observation> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg(), { name: 'observation', label: 'Observation', type: 'textarea', required: true }],
    flags: [
      { name: '--kind', label: 'Kind', type: 'enum', options: ['observation', 'decision', 'constraint', 'handoff'], default: 'observation' },
      { name: '--origin', label: 'Origin', type: 'enum', options: ['human', 'agent', 'reviewer'], default: 'human' },
      actorFlag(),
      { name: '--confidence', label: 'Confidence', type: 'enum', options: ['low', 'medium'], default: 'medium' },
      { name: '--file', label: 'File', type: 'path' },
      { name: '--line', label: 'Line', type: 'int' },
      { name: '--ref', label: 'Reference', type: 'text', placeholder: 'https://… or fcheap://…' },
      { name: '--sensitive', label: 'Sensitive', type: 'boolean', help: 'Keeps the note out of exported handoff packets.' },
    ],
    docs: 'docs/cli.md',
    related: ['decision.request', 'handoff'],
  },
  {
    id: 'decision.request',
    group: 'collab',
    path: ['decision', 'request'],
    title: 'Request a bounded human decision',
    summary: 'Pauses a task on one bounded question with explicit options and consequences. At least two options are required.',
    usage: 'cortex decision request <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg()],
    flags: [
      { name: '--question', label: 'Question', type: 'textarea', required: true },
      stringArray('--option', 'Option', 'id=label|consequence (at least two).', 'roll_forward=Ship the fix now|Rollback=Revert the release'),
      { name: '--requester', label: 'Requester', type: 'text', default: 'agent' },
    ],
    docs: 'docs/cli.md',
    related: ['decision.answer', 'decision.resume'],
  },
  {
    id: 'decision.answer',
    group: 'collab',
    path: ['decision', 'answer'],
    title: 'Answer a pending decision',
    summary: 'Records the selected option and resumes the exact paused phase.',
    usage: 'cortex decision answer <taskId> <decisionId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg(), { name: 'decisionId', label: 'Decision ID', type: 'text', required: true, placeholder: 'dec_06FK…' }],
    flags: [
      { name: '--answer', label: 'Option id', type: 'text', required: true },
      { name: '--responder', label: 'Responder', type: 'text', default: 'human' },
    ],
    docs: 'docs/cli.md',
    related: ['decision.request', 'decision.resume'],
  },
  {
    id: 'decision.resume',
    group: 'collab',
    path: ['decision', 'resume'],
    title: 'Recover an unresumed decision',
    summary: 'Repairs a task whose answered decision was never resumed after a crash.',
    usage: 'cortex decision resume <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg()],
    flags: [],
    docs: 'docs/cli.md',
    related: ['decision.answer'],
  },
  {
    id: 'handoff',
    group: 'collab',
    path: ['handoff'],
    title: 'Export a handoff packet',
    summary: 'A bounded packet for another person or agent. General handoffs cap at 128 KiB; complete verified handoffs budget 90 KiB and keep the entire non-sensitive proof closure or omit it atomically.',
    usage: 'cortex handoff <taskId> [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg()],
    flags: [
      { name: '--compact', label: 'Compact', type: 'boolean', help: 'Short LLM-readable packet: tip, top claims, open items, next command.' },
      { name: '-o', label: 'Output file', type: 'path', help: 'Write Markdown to a file instead of stdout (- for stdout).' },
    ],
    docs: 'docs/cli.md',
    related: ['resume', 'note'],
  },

  // ── Long-running work ────────────────────────────────────────────────────
  {
    id: 'coverage',
    group: 'longrun',
    path: ['coverage'],
    title: 'Survey coverage ledger',
    summary: 'Which modules were explored, summarized, or never seen. Survey rounds are budgeted per module row, not per case.',
    usage: 'cortex coverage <taskId> [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg()],
    flags: [{ name: '--all', label: 'Every module', type: 'boolean', help: 'Not just the first 50.' }],
    docs: 'docs/long-running.md',
    related: ['investigate', 'dossier.list'],
  },
  {
    id: 'finding.add',
    group: 'longrun',
    path: ['finding', 'add'],
    title: 'Record a finding',
    summary: 'A durable bug, improvement, feature, or question backed by evidence ids from the case.',
    usage: 'cortex finding add <taskId> <title> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg(), { name: 'title', label: 'Title', type: 'text', required: true }],
    flags: [
      { name: '--kind', label: 'Kind', type: 'enum', options: ['bug', 'improvement', 'feature', 'question'], default: 'bug' },
      { name: '--severity', label: 'Severity', type: 'enum', options: ['low', 'medium', 'high'], default: 'medium' },
      { name: '--detail', label: 'Detail', type: 'textarea' },
      stringArray('--evidence', 'Evidence IDs', 'Evidence ids from this case.', 'ev_06FK…'),
      stringArray('--file', 'File', 'Files involved.', 'internal/kernel/verify.go'),
      stringArray('--symbol', 'Symbol', 'Symbols involved.', 'Verify'),
      actorFlag(),
      { name: '--sensitive', label: 'Sensitive', type: 'boolean' },
    ],
    docs: 'docs/long-running.md',
    related: ['finding.list', 'finding.convert'],
  },
  {
    id: 'finding.list',
    group: 'longrun',
    path: ['finding', 'list'],
    title: 'List findings',
    summary: 'A case’s durable findings, optionally filtered by status.',
    usage: 'cortex finding list <taskId> [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg()],
    flags: [{ name: '--status', label: 'Status', type: 'enum', options: ['open', 'triaged', 'converted', 'dismissed'] }],
    docs: 'docs/long-running.md',
    related: ['finding.add', 'finding.triage'],
  },
  {
    id: 'finding.triage',
    group: 'longrun',
    path: ['finding', 'triage'],
    title: 'Triage a finding',
    summary: 'Marks a finding acknowledged but not yet acted on.',
    usage: 'cortex finding triage <taskId> <findingId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg(), { name: 'findingId', label: 'Finding ID', type: 'text', required: true }],
    flags: [actorFlag(), { name: '--reason', label: 'Reason', type: 'textarea' }],
    docs: 'docs/long-running.md',
    related: ['finding.list'],
  },
  {
    id: 'finding.dismiss',
    group: 'longrun',
    path: ['finding', 'dismiss'],
    title: 'Dismiss a finding',
    summary: 'Dismissals require a reason and are kept for cross-case recall.',
    usage: 'cortex finding dismiss <taskId> <findingId> [flags]',
    kind: KIND.mutate,
    json: true,
    confirm: 'Dismiss this finding? The reason is kept for recall.',
    args: [taskArg(), { name: 'findingId', label: 'Finding ID', type: 'text', required: true }],
    flags: [actorFlag(), { name: '--reason', label: 'Reason', type: 'textarea', required: true }],
    docs: 'docs/long-running.md',
    related: ['finding.list'],
  },
  {
    id: 'finding.convert',
    group: 'longrun',
    path: ['finding', 'convert'],
    title: 'Convert a finding to a child case',
    summary: 'Opens a linked child case whose acceptance criterion is the finding.',
    usage: 'cortex finding convert <taskId> <findingId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg(), { name: 'findingId', label: 'Finding ID', type: 'text', required: true }],
    flags: [
      { name: '--mode', label: 'Child mode', type: 'enum', options: ['change', 'investigate', 'review'], default: 'change' },
      { name: '--risk', label: 'Child risk', type: 'enum', options: RISKS },
      surfaceFlag(),
      actorFlag(),
    ],
    docs: 'docs/long-running.md',
    related: ['workplan.add', 'open'],
  },
  {
    id: 'dossier.list',
    group: 'longrun',
    path: ['dossier', 'list'],
    title: 'Repository dossier',
    summary: 'Per-repository memory that outlives cases and goes stale when its files change. Orients every later case.',
    usage: 'cortex dossier list [flags]',
    kind: KIND.read,
    json: true,
    args: [],
    flags: [
      { name: '--module', label: 'Module prefix', type: 'text', placeholder: 'internal/kernel' },
      { name: '--kind', label: 'Kind', type: 'enum', options: ['architecture', 'invariant', 'hotspot', 'convention', 'question'] },
      { name: '--stale', label: 'Stale only', type: 'boolean' },
      { name: '--limit', label: 'Limit', type: 'int', default: 50 },
    ],
    docs: 'docs/long-running.md',
    related: ['dossier.add', 'dossier.refresh'],
  },
  {
    id: 'dossier.add',
    group: 'longrun',
    path: ['dossier', 'add'],
    title: 'Write a dossier entry',
    summary: 'Writes or updates an evidence-backed entry describing a module, invariant, hotspot, convention, or open question.',
    usage: 'cortex dossier add <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg()],
    flags: [
      { name: '--title', label: 'Title', type: 'text', required: true },
      { name: '--module', label: 'Module', type: 'text', required: true, placeholder: 'internal/kernel' },
      { name: '--summary', label: 'Summary', type: 'textarea', required: true, help: 'What the module does / which invariant it keeps.' },
      { name: '--kind', label: 'Kind', type: 'enum', options: ['architecture', 'invariant', 'hotspot', 'convention', 'question'], default: 'architecture' },
      stringArray('--file', 'Depends on file', 'Defaults to the module.', 'internal/kernel/verify.go'),
      stringArray('--evidence', 'Evidence IDs', 'Evidence ids from this case.', 'ev_06FK…'),
      { name: '--entry', label: 'Entry id to update', type: 'text' },
      actorFlag(),
    ],
    docs: 'docs/long-running.md',
    related: ['dossier.list'],
  },
  {
    id: 'dossier.refresh',
    group: 'longrun',
    path: ['dossier', 'refresh'],
    title: 'Refresh dossier freshness',
    summary: 'Recomputes and persists stale marks for every dossier entry against HEAD.',
    usage: 'cortex dossier refresh [flags]',
    kind: KIND.mutate,
    json: true,
    args: [],
    flags: [],
    docs: 'docs/long-running.md',
    related: ['dossier.list'],
  },
  {
    id: 'workplan.list',
    group: 'longrun',
    path: ['workplan', 'list'],
    title: 'Campaign workplan',
    summary: 'The campaign with each item’s derived state: ready, blocked, claimed, done.',
    usage: 'cortex workplan list <taskId> [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg('Parent task ID')],
    flags: [],
    docs: 'docs/long-running.md',
    related: ['workplan.add', 'workplan.next'],
  },
  {
    id: 'workplan.add',
    group: 'longrun',
    path: ['workplan', 'add'],
    title: 'Add a campaign item',
    summary: 'Adds a dependency-aware, delegable work item under a parent case.',
    usage: 'cortex workplan add <taskId> <goal> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg('Parent task ID'), { name: 'goal', label: 'Item goal', type: 'textarea', required: true }],
    flags: [
      { name: '--id', label: 'Item id', type: 'text', help: 'Generated when empty.' },
      stringArray('--after', 'Depends on', 'Item ids this one depends on.', 'wp_1'),
      stringArray('--file', 'Expected file', 'Files the item is expected to touch.', 'internal/kernel/verify.go'),
      criterionFlag(),
      { name: '--mode', label: 'Child mode', type: 'enum', options: ['change', 'investigate', 'review'], default: 'change' },
      { name: '--risk', label: 'Child risk', type: 'enum', options: RISKS },
      surfaceFlag(),
    ],
    docs: 'docs/long-running.md',
    related: ['workplan.next'],
  },
  {
    id: 'workplan.next',
    group: 'longrun',
    path: ['workplan', 'next'],
    title: 'Claim the next ready item',
    summary: 'Hands the next ready item to an actor as a linked, retry-keyed child case.',
    usage: 'cortex workplan next <taskId> [flags]',
    kind: KIND.mutate,
    json: true,
    args: [taskArg('Parent task ID')],
    flags: [actorFlag({ required: true }), { name: '--item', label: 'Specific item id', type: 'text' }],
    docs: 'docs/long-running.md',
    related: ['workplan.list'],
  },
  {
    id: 'job.list',
    group: 'longrun',
    path: ['job', 'list'],
    title: 'Background jobs',
    summary: 'Detached investigation jobs that outlive an MCP call. Dead workers are reported as failed rather than silently dropped.',
    usage: 'cortex job list <taskId> [flags]',
    kind: KIND.read,
    json: true,
    args: [taskArg()],
    flags: [],
    docs: 'docs/long-running.md',
    related: ['job.cancel', 'investigate'],
  },
  {
    id: 'job.cancel',
    group: 'longrun',
    path: ['job', 'cancel'],
    title: 'Cancel a background job',
    summary: 'Cancels a detached job; evidence already recorded is kept.',
    usage: 'cortex job cancel <taskId> <jobId> [flags]',
    kind: KIND.mutate,
    json: true,
    confirm: 'Cancel this job? Evidence already recorded is kept.',
    args: [taskArg(), { name: 'jobId', label: 'Job ID', type: 'text', required: true }],
    flags: [],
    docs: 'docs/long-running.md',
    related: ['job.list'],
  },

  // ── Evidence & memory ────────────────────────────────────────────────────
  {
    id: 'read-evidence',
    group: 'evidence',
    path: ['read-evidence'],
    title: 'Read one evidence record',
    summary: 'Prints a full evidence record by id. `/raw/` references support a bounded preview.',
    usage: 'cortex read-evidence <taskId> <evidenceId> [flags]',
    kind: KIND.read,
    json: true,
    aliases: ['evidence'],
    args: [
      taskArg(),
      {
        name: 'evidenceId',
        label: 'Evidence ID',
        type: 'text',
        required: true,
        placeholder: 'ev_06FK…',
        help: 'An evidence id (ev_…). Raw output is previewed with read-artifact using the record’s rawRef, not here.',
      },
    ],
    flags: [],
    docs: 'docs/case-file.md',
    related: ['read-artifact', 'status'],
  },
  {
    id: 'read-artifact',
    group: 'evidence',
    path: ['read-artifact'],
    title: 'Preview an artifact',
    summary: 'Previews a task-owned raw ref or a task-referenced fcheap artifact. Reads are task-scoped, bounded to 128 KiB, and binary content is refused unless explicitly allowed.',
    usage: 'cortex read-artifact <taskId> <ref> [flags]',
    kind: KIND.read,
    json: true,
    aliases: ['artifact', 'raw'],
    args: [
      taskArg(),
      {
        name: 'ref',
        label: 'Reference',
        type: 'text',
        required: true,
        placeholder: 'case://task_…/raw/raw_… or fcheap://…',
        help: 'Copy the full URI verbatim from an evidence record’s rawRef — bare raw_ ids are not accepted.',
      },
    ],
    flags: [
      { name: '--path', label: 'Path in stash', type: 'text', help: 'Safe relative path inside an fcheap stash; empty uses bounded discovery.' },
      { name: '--max-bytes', label: 'Max bytes', type: 'int', default: 32768, help: 'Hard-capped at 131072.' },
      { name: '--allow-binary', label: 'Allow binary', type: 'boolean', help: 'Return bounded binary content as base64.' },
    ],
    docs: 'docs/case-file.md',
    related: ['read-evidence'],
  },
  {
    id: 'recall-cases',
    group: 'evidence',
    path: ['recall-cases'],
    title: 'Recall prior cases',
    summary: 'Searches the cross-case recall index (veclite) for prior resolved hypotheses — rejected and challenged are the gold — and definitive receipts. Best-effort: no veclite means empty, never an error.',
    usage: 'cortex recall-cases <query> [flags]',
    kind: KIND.read,
    json: true,
    args: [{ name: 'query', label: 'Query', type: 'text', required: true }],
    flags: [
      { name: '--repo', label: 'Repository', type: 'text', help: 'Empty = cross-repo.' },
      { name: '--limit', label: 'Limit', type: 'int', default: 5 },
    ],
    docs: 'docs/cli.md',
    related: ['reindex-cases'],
  },
  {
    id: 'reindex-cases',
    group: 'evidence',
    path: ['reindex-cases'],
    title: 'Rebuild the recall index',
    summary: 'Reindexes from the central sessions tree. Archives and repo-local cases_dir overrides are deliberately excluded; individual failures are reported while the rest continue.',
    usage: 'cortex reindex-cases [flags]',
    kind: KIND.mutate,
    json: true,
    args: [],
    flags: [],
    docs: 'docs/cli.md',
    related: ['recall-cases'],
  },

  // ── Review ───────────────────────────────────────────────────────────────
  {
    id: 'review',
    group: 'review',
    path: ['review'],
    title: 'Review a branch or pull request',
    summary: 'Resolves the diff (base…HEAD), gathers structural and semantic context, runs the verifiers over the change, and produces approve / request-changes / needs-verification with a receipt behind every claim.',
    usage: 'cortex review [flags]',
    kind: KIND.mutate,
    json: true,
    args: [],
    flags: [
      { name: '--base', label: 'Base ref', type: 'text', placeholder: 'release/2.1', help: 'Default: merge-base with the default branch.' },
      { name: '--head', label: 'Head ref', type: 'text', help: 'Default: current branch.' },
      { name: '--pr', label: 'PR number', type: 'int', help: 'Fetch and review a pull/merge request (GitHub or Bitbucket).' },
      { name: '--risk', label: 'Risk', type: 'enum', options: RISKS, default: 'medium' },
      surfaceFlag({ options: ['code', 'browser', 'terminal'] }),
      stringArray('--claim', 'Additional claim', 'An extra claim to prove.', 'the diff keeps the phase machine invariants'),
    ],
    examples: ['cortex review --pr 42 --surface code', 'cortex review --base release/2.1 --surface browser'],
    docs: 'docs/cli.md',
    related: ['verify', 'status'],
  },

  // ── Environment ──────────────────────────────────────────────────────────
  {
    id: 'doctor',
    group: 'env',
    path: ['doctor'],
    title: 'Environment doctor',
    summary: 'Resolved workspace and case store plus every specialist tool’s health. Missing tools are not an error — adapters degrade safely and verification on that surface is blocked rather than fabricated.',
    usage: 'cortex doctor [flags]',
    kind: KIND.read,
    json: true,
    args: [],
    flags: [
      { name: '--probe', label: 'Live gateway handshake', type: 'boolean', help: 'Completes a real MCP handshake when checking gateway registration.' },
      { name: '--gateway-server', label: 'Gateway server name', type: 'text', default: 'cortex' },
    ],
    docs: 'docs/cli.md',
    related: ['setup', 'config'],
  },
  {
    id: 'setup',
    group: 'env',
    path: ['setup'],
    title: 'Workspace readiness',
    summary: 'Reports git, cortex.yaml, and whether codemap/vecgrep are installed and indexed — with the exact command to fix each gap. Read-only unless --trust-commands is passed.',
    usage: 'cortex setup [flags]',
    kind: KIND.read,
    json: true,
    args: [],
    flags: [
      { name: '--trust-commands', label: 'Trust configured command verifiers', type: 'boolean', danger: true, help: 'Grants configured argv for this workspace; digests are stored outside the repo.' },
      { name: '--yes', label: 'Skip confirmation', type: 'boolean', help: 'Trusted launcher only.' },
    ],
    docs: 'docs/cli.md',
    related: ['init', 'doctor'],
  },
  {
    id: 'config',
    group: 'env',
    path: ['config'],
    title: 'Resolved configuration',
    summary: 'Precedence, lowest to highest: built-in defaults → global config → project .config/cortex.yaml → project cortex.yml/.yaml → CORTEX_* environment variables. Verifier argv is deliberately omitted.',
    usage: 'cortex config [flags]',
    kind: KIND.read,
    json: true,
    args: [],
    flags: [],
    docs: 'docs/configuration.md',
    related: ['init', 'doctor'],
  },
  {
    id: 'init',
    group: 'env',
    path: ['init'],
    title: 'Create a starter cortex.yaml',
    summary: 'Detects the project test runner (Go, Rust, Node, Python) and writes a command verifier for it. Refuses to overwrite an existing config without --force.',
    usage: 'cortex init [flags]',
    kind: KIND.mutate,
    json: true,
    confirm: 'Write cortex.yaml into this workspace?',
    args: [],
    flags: [{ name: '--force', label: 'Overwrite existing', type: 'boolean', danger: true }],
    docs: 'docs/configuration.md',
    related: ['config', 'setup'],
  },
  {
    id: 'migrate',
    group: 'env',
    path: ['migrate'],
    title: 'Migrate a legacy ~/.cortex layout',
    summary: 'Dry run by default. All-or-nothing: if any XDG destination exists the whole migration is blocked so it cannot leave a half-migrated state.',
    usage: 'cortex migrate [flags]',
    kind: KIND.dryrun,
    json: true,
    args: [],
    flags: [{ name: '--apply', label: 'Apply the migration', type: 'boolean', danger: true }],
    docs: 'docs/configuration.md',
    related: ['config'],
  },
  {
    id: 'route',
    group: 'env',
    path: ['route'],
    title: 'Routing matrix',
    summary: 'Prints the ordered routing matrix, or resolves which tools cortex routes one question to.',
    usage: 'cortex route [question] [flags]',
    kind: KIND.read,
    json: true,
    args: [{ name: 'question', label: 'Question', type: 'textarea', required: false, help: 'Empty prints the whole matrix.' }],
    flags: [surfaceFlag({ help: 'Override detected surfaces for the resolved question.' })],
    examples: ['cortex route', 'cortex route "why does the checkout redirect drop returnTo" --surface code --surface browser'],
    docs: 'docs/adapters.md',
    related: ['investigate', 'doctor'],
  },
  {
    id: 'completion',
    group: 'env',
    path: ['completion'],
    title: 'Shell completion script',
    summary: 'Generates the autocompletion script for a shell — the one feature whose output is meant to be sourced, not read.',
    usage: 'cortex completion <bash|zsh|fish|powershell>',
    kind: KIND.read,
    json: false,
    args: [{ name: 'shell', label: 'Shell', type: 'enum', options: ['bash', 'zsh', 'fish', 'powershell'], required: true }],
    flags: [],
    docs: 'docs/cli.md',
    related: [],
  },

  // ── MCP server ───────────────────────────────────────────────────────────
  {
    id: 'serve',
    group: 'mcp',
    path: ['serve'],
    title: 'Run the MCP server',
    summary: 'Newline-delimited JSON-RPC over stdio; all logging goes to stderr. The compact agent profile is the default, `--profile all` adds the cross-repository operator tools.',
    usage: 'cortex serve [flags]',
    kind: KIND.server,
    json: false,
    aliases: ['mcp'],
    args: [],
    flags: [{ name: '--profile', label: 'Profile', type: 'enum', options: ['agent', 'all'], default: 'agent' }],
    docs: 'docs/mcp.md',
    related: ['doctor'],
  },
];

// ── Repository tooling (Taskfile + specs + contracts + docs) ───────────────
//
// These are not cortex subcommands; they are the project’s own developer
// surface, exposed here so the deck can run and inspect them too.
export const DEV_TASKS = [
  { id: 'doctor', cmd: 'task', args: ['doctor'], label: 'task doctor', summary: 'Check go/task/glyph/bun and which sibling tools are on PATH.', kind: 'read' },
  { id: 'build', cmd: 'task', args: ['build'], label: 'task build', summary: 'Build → ./bin/cortex with version ldflags.', kind: 'read' },
  { id: 'test', cmd: 'task', args: ['test'], label: 'task test', summary: 'go test ./…', kind: 'read' },
  { id: 'race', cmd: 'task', args: ['race'], label: 'task race', summary: 'CGO_ENABLED=1 go test -race ./…', kind: 'read' },
  { id: 'lint', cmd: 'task', args: ['lint'], label: 'task lint', summary: 'golangci-lint v2 (falls back to go vet + gofmt -l).', kind: 'read' },
  { id: 'fmt', cmd: 'task', args: ['fmt'], label: 'task fmt', summary: 'gofmt -s -w .', kind: 'mutate' },
  { id: 'check', cmd: 'task', args: ['check'], label: 'task check', summary: 'fmt + lint + test — the pre-commit gate.', kind: 'read' },
  { id: 'flows', cmd: 'task', args: ['flows'], label: 'task flows', summary: 'glyph run specs/*.yml — E2E, local only.', kind: 'read' },
  { id: 'eval', cmd: 'task', args: ['eval'], label: 'task eval', summary: 'Lifecycle scenarios + paired unassisted-baseline scorecard.', kind: 'read' },
  { id: 'docs', cmd: 'task', args: ['docs'], label: 'task docs', summary: 'VitePress dev server (Bun).', kind: 'server' },
  { id: 'docsbuild', cmd: 'task', args: ['docsbuild'], label: 'task docsbuild', summary: 'Build the documentation site.', kind: 'read' },
  { id: 'docstest', cmd: 'task', args: ['docstest'], label: 'task docstest', summary: 'Test the documentation build.', kind: 'read' },
  { id: 'ship', cmd: 'task', args: ['ship'], label: 'task ship', summary: 'check + race + build + flows + docstest + docsbuild.', kind: 'read' },
  { id: 'install', cmd: 'task', args: ['install'], label: 'task install', summary: 'go install ./cmd/cortex', kind: 'mutate' },
  { id: 'go-test-deck', cmd: 'go', args: ['test', './internal/...'], label: 'go test ./internal/…', summary: 'Kernel, adapter, store, and domain tests only.', kind: 'read' },
];

const byId = new Map(COMMANDS.map((c) => [c.id, c]));

export function getCommand(id) {
  return byId.get(id);
}

export function commandsByGroup() {
  return GROUPS.map((g) => ({ ...g, commands: COMMANDS.filter((c) => c.group === g.id) }));
}

/**
 * Build argv from a registry entry and a values map.
 * Empty/undefined values are dropped, booleans become bare flags, and
 * repeatable values expand to one flag (or positional) per item.
 */
export function buildArgv(command, values = {}) {
  const argv = [...command.path];
  for (const arg of command.args ?? []) {
    const v = values[arg.name];
    if (arg.type === 'task' || arg.type === 'text' || arg.type === 'textarea' || arg.type === 'path' || arg.type === 'enum') {
      if (v === undefined || v === null || v === '') {
        if (arg.required) throw new Error(`missing required argument: ${arg.label}`);
        continue;
      }
      argv.push(String(v));
    } else if (arg.type === 'int' || arg.type === 'number') {
      if (v === undefined || v === null || v === '') {
        if (arg.required) throw new Error(`missing required argument: ${arg.label}`);
        continue;
      }
      argv.push(String(v));
    }
  }
  for (const flag of command.flags ?? []) {
    const v = values[flagKey(flag.name)];
    if (v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0)) continue;
    if (flag.type === 'boolean') {
      if (v) argv.push(flag.name);
      continue;
    }
    const items = Array.isArray(v) ? v.filter((x) => x !== '' && x !== null && x !== undefined) : [v];
    for (const item of items) argv.push(flag.name, String(item));
  }
  return argv;
}

/** Values are keyed by flag name without the leading dashes. */
export function flagKey(name) {
  return name.replace(/^-+/, '');
}

/** A shell-safe rendering used for the argv preview and "copy command". */
export function renderArgv(command, values = {}, opts = {}) {
  let argv;
  try {
    argv = buildArgv(command, values);
  } catch {
    argv = [...command.path];
  }
  // Mirrors main/cortex.js exactly: -C first, then the subcommand, then --json.
  const parts = ['cortex'];
  if (opts.workspace) parts.push('-C', quoteArg(opts.workspace));
  parts.push(...argv.map(quoteArg));
  if (opts.json !== false && command.json) parts.push('--json');
  return parts.join(' ');
}

export function quoteArg(value) {
  const s = String(value);
  if (s === '') return "''";
  return /^[A-Za-z0-9_@%+=:,./-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}

/** Flat list of every feature the deck integrates, for the catalog view. */
export function featureCatalog() {
  return GROUPS.map((group) => ({
    ...group,
    features: COMMANDS.filter((c) => c.group === group.id).map((c) => ({
      id: c.id,
      title: c.title,
      summary: c.summary,
      usage: c.usage,
      kind: c.kind,
      json: c.json,
      aliases: c.aliases ?? [],
      flags: (c.flags ?? []).length,
      args: (c.args ?? []).length,
      docs: c.docs,
    })),
  }));
}

export { KIND };
