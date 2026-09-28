/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { h } from './dom.js';
import { icon } from './icons.js';
import { badge, empty, kv, progress, stateBadge, table, tag } from './ui.js';
import { absoluteTime, compactHome, duration, humanDuration, matches, percent, relativeTime, shortId, titleCase, truncate } from './format.js';

/** Tolerant field access: Cortex projections differ slightly per command. */
export function pick(obj, ...keys) {
  if (!obj || typeof obj !== 'object') return undefined;
  for (const key of keys) {
    const value = obj[key];
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

export function pickPath(obj, path) {
  return String(path).split('.').reduce((acc, key) => (acc === null || acc === undefined ? undefined : acc[key]), obj);
}

export function arrayOf(value) {
  if (Array.isArray(value)) return value;
  if (value === null || value === undefined) return [];
  return [value];
}

/** Find the first array-valued field among candidates; arrays pass through. */
export function firstArray(obj, ...keys) {
  if (Array.isArray(obj)) return obj;
  for (const key of keys) {
    const value = key.includes('.') ? pickPath(obj, key) : obj?.[key];
    if (Array.isArray(value)) return value;
  }
  return [];
}

/** Cortex nests some strings one level deep ({note}, {tool}); flatten them. */
export function flat(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) return value.map(flat).filter(Boolean).join(', ');
  return String(pick(value, 'note', 'tool', 'name', 'text', 'label', 'path', 'id') ?? JSON.stringify(value));
}

/* ── The reasoning loop ────────────────────────────────────────────────── */

const LOOP_STEPS = [
  { phase: 'orienting', name: 'Orient', note: 'git identity, tool health, seeds' },
  { phase: 'investigating', name: 'Investigate', note: 'discovery → structure → evidence' },
  { phase: 'planned', name: 'Plan', note: 'hypothesis + disproof + boundary' },
  { phase: 'changing', name: 'Change', note: 'bounded lease ownership' },
  { phase: 'verifying', name: 'Verify', note: 'typed claims → receipts' },
  { phase: 'persisting', name: 'Remember', note: 'durable outcome + completion' },
];

export function loopDiagram(current, opts = {}) {
  const order = LOOP_STEPS.map((s) => s.phase);
  const index = order.indexOf(String(current ?? '').toLowerCase());
  const terminal = ['complete', 'abandoned', 'blocked'].includes(String(current ?? '').toLowerCase());
  return h('div', { class: 'loop', dataset: { phase: current ?? '' } },
    LOOP_STEPS.map((step, i) => {
      const state = terminal ? 'done' : index < 0 ? '' : i < index ? 'done' : i === index ? 'current' : '';
      return h('div', { class: 'loop-step', dataset: { state } },
        h('div', { class: 'loop-index', text: `${i + 1}` }),
        h('div', { class: 'loop-name', text: step.name }),
        h('div', { class: 'loop-note', text: state === 'current' ? 'you are here' : step.note }),
      );
    }),
  );
}

/* ── Sessions & tasks ──────────────────────────────────────────────────── */

export function sessionRow(s) {
  return {
    taskId: pick(s, 'taskId', 'id', 'task'),
    goal: pick(s, 'goal', 'title', 'summary') ?? '',
    repo: pick(s, 'repository', 'repo', 'repoSlug', 'slug') ?? '',
    phase: pick(s, 'phase', 'state', 'status') ?? '',
    mode: pick(s, 'mode') ?? '',
    assessment: pick(s, 'verificationOutcome', 'assessment', 'outcome', 'result') ?? '',
    verified: pick(s, 'verified'),
    required: pick(s, 'required'),
    active: pick(s, 'active'),
    updated: pick(s, 'updatedAt', 'updated', 'mtime', 'lastActivity') ?? '',
    created: pick(s, 'createdAt', 'created') ?? '',
    stale: pick(s, 'stale') === true,
    actor: pick(s, 'actor') ?? '',
    workspace: pick(s, 'workspace', 'workspacePath') ?? '',
    raw: s,
  };
}

export const SESSION_COLUMNS = [
  {
    key: 'phase',
    label: 'Phase',
    width: '118px',
    render: (row) => {
      const s = sessionRow(row);
      return h('span', { class: 'row', style: { gap: '6px' } }, stateBadge(s.phase), s.stale ? badge('stale', 'amber') : null);
    },
  },
  {
    key: 'goal',
    label: 'Goal',
    strong: true,
    render: (row) => {
      const s = sessionRow(row);
      return h('div', { class: 'col', style: { gap: '2px' } },
        h('div', { class: 'truncate', style: { color: 'var(--text)', maxWidth: '52ch' }, text: truncate(s.goal, 110) || '(no goal)' }),
        h('div', { class: 'mono-sm faint', text: `${shortId(s.taskId, 16, 4)}${s.mode ? ` · ${s.mode}` : ''}${s.actor ? ` · ${s.actor}` : ''}` }),
      );
    },
  },
  { key: 'repo', label: 'Repo', width: '150px', render: (row) => h('span', { class: 'mono-sm', text: truncate(sessionRow(row).repo, 26) }) },
  {
    key: 'proof',
    label: 'Proof',
    width: '76px',
    align: 'right',
    render: (row) => {
      const s = sessionRow(row);
      if (s.required === undefined && s.verified === undefined) return h('span', { class: 'faint', text: '—' });
      const done = Number(s.verified ?? 0) >= Number(s.required ?? 0) && Number(s.required ?? 0) > 0;
      return badge(`${s.verified ?? 0}/${s.required ?? 0}`, done ? 'green' : 'amber');
    },
  },
  {
    key: 'assessment',
    label: 'Assessment',
    width: '112px',
    render: (row) => {
      const value = sessionRow(row).assessment;
      return value ? stateBadge(value) : h('span', { class: 'faint', text: '—' });
    },
  },
  {
    key: 'updated',
    label: 'Updated',
    width: '104px',
    align: 'right',
    render: (row) => {
      const value = sessionRow(row).updated;
      return h('span', { class: 'mono-sm faint', title: absoluteTime(value), text: relativeTime(value) || '—' });
    },
  },
];

export function sessionsTable(rows, opts = {}) {
  if (!rows?.length) {
    return empty({
      iconName: 'sessions',
      title: opts.title ?? 'No sessions match',
      note: opts.note ?? 'Cortex stores every session in the central XDG state tree. Open a case to see it here.',
    });
  }
  return table({
    columns: opts.columns ?? SESSION_COLUMNS,
    rows,
    rowKey: (row) => sessionRow(row).taskId,
    selectedKey: opts.selectedKey,
    onRowClick: opts.onRowClick,
    rowAttrs: (row) => ({ phase: String(sessionRow(row).phase ?? '') }),
    maxHeight: opts.maxHeight,
  });
}

/* ── Evidence ──────────────────────────────────────────────────────────── */

export function evidenceCard(e, opts = {}) {
  const id = pick(e, 'id', 'evidenceId') ?? '';
  const claim = flat(pick(e, 'claim', 'statement', 'summary')) || '';
  const confidence = pick(e, 'confidence') ?? '';
  const kind = pick(e, 'kind', 'type') ?? '';
  const source = flat(pick(e, 'source', 'tool', 'origin')) || '';
  const at = pick(e, 'timestamp', 'at', 'createdAt') ?? '';
  const sensitive = pick(e, 'sensitive') === true || pick(e, 'sensitivity') === 'sensitive';
  const uri = flat(pick(e, 'rawRef', 'uri', 'ref', 'path')) || '';
  return h('div', { class: 'evidence-item', dataset: { phase: confidence === 'high' ? 'complete' : confidence === 'medium' ? 'investigating' : 'paused' } },
    h('div', { class: 'evidence-claim', text: claim || '(empty claim)' }),
    h('div', { class: 'evidence-meta' },
      id ? h('span', { class: 'mono-sm', text: id, title: id }) : null,
      kind ? badge(kind, 'slate') : null,
      confidence ? badge(confidence, confidence === 'high' ? 'green' : confidence === 'medium' ? 'info' : 'slate') : null,
      source ? h('span', { text: source }) : null,
      at ? h('span', { title: absoluteTime(at), text: relativeTime(at) }) : null,
      sensitive ? badge('sensitive', 'red') : null,
      uri ? h('span', { class: 'mono-sm', text: truncate(String(uri), 70), title: String(uri) }) : null,
      opts.onPreview && uri
        ? h('button', {
            class: 'btn btn-ghost btn-sm',
            style: opts.onOpen && id ? null : { marginLeft: 'auto' },
            title: 'Preview the raw output with read-artifact',
            onclick: () => opts.onPreview(e),
          }, icon('file', 12), 'Raw')
        : null,
      opts.onOpen && id
        ? h('button', { class: 'btn btn-ghost btn-sm', style: { marginLeft: 'auto' }, onclick: () => opts.onOpen(e) }, icon('eye', 12), 'Read')
        : null,
    ),
  );
}

export function evidenceList(items, opts = {}) {
  const list = arrayOf(items);
  if (!list.length) return empty({ iconName: 'evidence', title: opts.emptyTitle ?? 'No evidence yet', note: opts.emptyNote ?? 'Investigate records candidates; verify records receipts. Both land in evidence.jsonl.' });
  return h('div', { class: 'col', style: { gap: '7px' } }, list.map((item) => evidenceCard(item, opts)));
}

/* ── Verification receipts ─────────────────────────────────────────────── */

export function receiptRow(r) {
  const claim = pick(r, 'claim', 'statement', 'claimStatement') ?? '';
  const status = String(pick(r, 'status', 'state', 'result') ?? '').toLowerCase();
  const surface = pick(r, 'surface') ?? '';
  const verifier = pick(r, 'verifier', 'tool') ?? '';
  const contract = pick(r, 'contract', 'contractPath', 'spec') ?? '';
  const claimId = pick(r, 'claimId', 'id', 'criterionId') ?? '';
  return h('div', { class: 'receipt', dataset: { status: status || 'unknown' } },
    h('div', {}, stateBadge(status || 'unknown')),
    h('div', { class: 'col', style: { gap: '3px', minWidth: '0' } },
      h('div', { style: { fontSize: 'var(--fs-12)', color: 'var(--text)' }, text: truncate(claim, 180) || '(no statement)' }),
      h('div', { class: 'evidence-meta' },
        claimId ? h('span', { class: 'mono-sm', text: String(claimId) }) : null,
        surface ? badge(surface, 'info') : null,
        verifier ? badge(verifier, 'violet') : null,
        contract ? h('span', { class: 'mono-sm faint', text: truncate(String(contract), 70), title: String(contract) }) : null,
        pick(r, 'bound') === true ? badge('bound', 'green') : null,
        pick(r, 'stale') === true ? badge('stale', 'amber') : null,
      ),
    ),
    h('div', { class: 'mono-sm faint', text: relativeTime(pick(r, 'at', 'createdAt') ?? '') }),
  );
}

export function receiptsList(items) {
  const list = arrayOf(items);
  if (!list.length) return empty({ iconName: 'shield', title: 'No receipts', note: 'A claim with no relevant verifier is recorded not_run — never passed.' });
  return h('div', { class: 'col', style: { gap: '7px' } }, list.map(receiptRow));
}

/* ── Hypotheses ────────────────────────────────────────────────────────── */

export function hypothesisCard(hyp, opts = {}) {
  const id = pick(hyp, 'id', 'hypothesisId') ?? '';
  const statement = flat(pick(hyp, 'statement', 'hypothesis', 'claim')) || '';
  const disproof = flat(pick(hyp, 'disproveBy', 'disproofPath', 'disproof')) || '';
  const status = String(pick(hyp, 'status', 'state') ?? 'open').toLowerCase();
  const support = firstArray(hyp, 'supports', 'support', 'supportingEvidence', 'evidence');
  return h('div', { class: 'hypothesis' },
    h('div', { class: 'row-wrap', style: { gap: '8px' } },
      stateBadge(status),
      id ? h('span', { class: 'mono-sm faint', text: id }) : null,
      pick(hyp, 'confidence') ? badge(pick(hyp, 'confidence'), 'slate') : null,
      h('span', { class: 'grow' }),
      opts.onResolve ? buttonSmall('Resolve', 'target', () => opts.onResolve(hyp)) : null,
    ),
    h('div', { class: 'hypothesis-statement', text: statement || '(no statement)' }),
    disproof
      ? h('div', { class: 'hypothesis-disproof' }, h('span', { class: 'micro', text: 'disproof path' }), h('div', { text: String(disproof) }))
      : h('div', { class: 'hypothesis-disproof', style: { borderColor: 'var(--red)' } }, h('span', { class: 'micro danger-text', text: 'no disproof path — the planning gate rejects this' })),
    support.length ? h('div', { class: 'pill-row' }, support.map((s) => tag(typeof s === 'string' ? s : pick(s, 'id', 'evidenceId') ?? JSON.stringify(s)))) : null,
  );
}

function buttonSmall(label, iconName, onClick) {
  return h('button', { class: 'btn btn-sm', onclick: onClick }, icon(iconName, 12), h('span', { text: label }));
}

export function hypothesesList(items, opts = {}) {
  const list = arrayOf(items);
  if (!list.length) return empty({ iconName: 'target', title: 'No hypotheses', note: 'Plans are rejected unless every hypothesis carries a disproof path.' });
  return h('div', { class: 'col', style: { gap: '9px' } }, list.map((item) => hypothesisCard(item, opts)));
}

/* ── Decisions ─────────────────────────────────────────────────────────── */

export function decisionCard(decision, opts = {}) {
  const id = pick(decision, 'id', 'decisionId') ?? '';
  const question = pick(decision, 'question', 'title') ?? '';
  const status = String(pick(decision, 'status', 'state') ?? '').toLowerCase();
  const options = firstArray(decision, 'options', 'choices');
  const answer = pick(decision, 'answer', 'selected', 'selectedOption');
  const pending = status === 'pending' || status === 'open' || (!status && !answer);

  return h('div', { class: pending ? 'decision-card' : 'card' , style: pending ? null : { padding: '12px 14px' } },
    h('div', { class: 'row-wrap', style: { gap: '8px' } },
      stateBadge(status || (pending ? 'pending' : 'answered')),
      id ? h('span', { class: 'mono-sm faint', text: id }) : null,
      pick(decision, 'requester') ? badge(`requested by ${pick(decision, 'requester')}`, 'slate') : null,
      h('span', { class: 'grow' }),
      relativeTime(pick(decision, 'at', 'createdAt', 'requestedAt') ?? ''),
    ),
    h('div', { style: { fontSize: 'var(--fs-13)', color: 'var(--text)', fontWeight: '550' }, text: question || '(no question)' }),
    options.length
      ? h('div', { class: 'col', style: { gap: '6px' } }, options.map((option) => {
          const optionId = pick(option, 'id', 'value', 'key') ?? '';
          const chosen = answer && (answer === optionId || pick(answer, 'id') === optionId);
          return h('button', {
            class: 'option-row',
            dataset: { chosen: String(Boolean(chosen)) },
            disabled: !pending || !opts.onAnswer,
            onclick: () => opts.onAnswer?.(decision, optionId),
          },
            h('span', {}, chosen ? icon('check', 14) : icon('question', 14)),
            h('span', {},
              h('div', { class: 'option-label', text: String(pick(option, 'label', 'text') ?? optionId) }),
              pick(option, 'consequence', 'consequences')
                ? h('div', { class: 'option-consequence', text: String(pick(option, 'consequence', 'consequences')) })
                : null,
            ),
          );
        }))
      : null,
    answer && !options.length ? h('div', { class: 'muted tiny', text: `answered: ${typeof answer === 'string' ? answer : JSON.stringify(answer)}` }) : null,
  );
}

/* ── Long-running work ─────────────────────────────────────────────────── */

export function findingsTable(items, opts = {}) {
  const rows = arrayOf(items);
  if (!rows.length) return empty({ iconName: 'note', title: 'No findings', note: 'Findings are a durable backlog of bugs, improvements, and ideas backed by case evidence.' });
  const showActions = Boolean(opts.onAction);
  const columns = [
    { key: 'status', label: 'Status', width: '96px', render: (f) => stateBadge(pick(f, 'status', 'state')) },
    { key: 'kind', label: 'Kind', width: '92px', render: (f) => badge(pick(f, 'kind', 'type') ?? '', 'slate') },
    { key: 'severity', label: 'Severity', width: '86px', render: (f) => stateBadge(pick(f, 'severity') ?? '') },
    {
      key: 'title',
      label: 'Finding',
      strong: true,
      render: (f) => h('div', { class: 'col', style: { gap: '2px' } },
        h('div', { class: 'truncate', style: { color: 'var(--text)' }, text: truncate(pick(f, 'title') ?? '', 120) }),
        pick(f, 'detail') ? h('div', { class: 'tiny faint clamp-2', text: truncate(pick(f, 'detail'), 180) }) : null,
        h('div', { class: 'mono-sm faint', text: [pick(f, 'id', 'findingId'), relativeTime(pick(f, 'at', 'createdAt') ?? '')].filter(Boolean).join(' · ') }),
      ),
    },
    { key: 'files', label: 'Files', width: '180px', render: (f) => h('div', { class: 'pill-row' }, firstArray(f, 'files', 'file').slice(0, 3).map((x) => tag(truncate(String(x), 28)))) },
  ];
  if (showActions) {
    columns.push({
      key: 'actions',
      label: '',
      width: '1%',
      render: (f) => h('div', { class: 'row', style: { gap: '4px' } },
        h('button', { class: 'btn btn-sm', onclick: (event) => { event.stopPropagation(); opts.onAction('triage', f); } }, 'Triage'),
        h('button', { class: 'btn btn-sm', onclick: (event) => { event.stopPropagation(); opts.onAction('convert', f); } }, 'Convert'),
        h('button', { class: 'btn btn-sm', onclick: (event) => { event.stopPropagation(); opts.onAction('dismiss', f); } }, 'Dismiss'),
      ),
    });
  }
  return table({ columns, rows, rowKey: (f) => pick(f, 'id', 'findingId') ?? '', maxHeight: opts.maxHeight });
}

export function dossierTable(items, opts = {}) {
  const rows = arrayOf(items);
  if (!rows.length) return empty({ iconName: 'database', title: 'Dossier is empty', note: 'Dossier entries are per-repository memory that outlive cases and go stale when their files change.' });
  return table({
    columns: [
      { key: 'stale', label: 'Freshness', width: '92px', render: (e) => stateBadge(pick(e, 'stale') === true ? 'stale' : 'fresh') },
      { key: 'kind', label: 'Kind', width: '104px', render: (e) => badge(pick(e, 'kind') ?? '', 'violet') },
      { key: 'module', label: 'Module', width: '180px', mono: true, render: (e) => truncate(String(pick(e, 'module') ?? ''), 34) },
      {
        key: 'title',
        label: 'Entry',
        strong: true,
        render: (e) => h('div', { class: 'col', style: { gap: '2px' } },
          h('div', { class: 'truncate', style: { color: 'var(--text)' }, text: truncate(pick(e, 'title') ?? '', 120) }),
          pick(e, 'summary') ? h('div', { class: 'tiny faint clamp-2', text: truncate(pick(e, 'summary'), 220) }) : null,
        ),
      },
      { key: 'files', label: 'Depends on', width: '200px', render: (e) => h('div', { class: 'pill-row' }, firstArray(e, 'files', 'file').slice(0, 3).map((x) => tag(truncate(String(x), 30)))) },
      { key: 'updated', label: 'Updated', width: '96px', align: 'right', render: (e) => h('span', { class: 'mono-sm faint', title: absoluteTime(pick(e, 'updatedAt', 'at') ?? ''), text: relativeTime(pick(e, 'updatedAt', 'at') ?? '') }) },
    ],
    rows,
    maxHeight: opts.maxHeight,
    onRowClick: opts.onRowClick,
  });
}

export function coverageView(payload) {
  const rows = firstArray(payload, 'modules', 'rows', 'coverage', 'ledger', 'items');
  const totals = pick(payload, 'totals', 'summary') ?? null;
  const stats = rows.length
    ? {
        total: rows.length,
        explored: rows.filter((r) => ['explored', 'summarized', 'covered', 'done'].includes(String(pick(r, 'state', 'status') ?? '').toLowerCase())).length,
        unseen: rows.filter((r) => ['unseen', 'new', 'pending', ''].includes(String(pick(r, 'state', 'status') ?? '').toLowerCase())).length,
      }
    : null;

  return h('div', { class: 'col', style: { gap: '12px' } },
    stats
      ? h('div', { class: 'grid grid-3' },
          h('div', { class: 'card stat' }, h('div', { class: 'stat-label', text: 'modules' }), h('div', { class: 'stat-value', text: String(stats.total) })),
          h('div', { class: 'card stat', dataset: { tone: 'green' } }, h('div', { class: 'stat-label', text: 'explored' }), h('div', { class: 'stat-value', text: String(stats.explored) }), h('div', { class: 'stat-foot', text: percent(stats.total ? stats.explored / stats.total : 0) })),
          h('div', { class: 'card stat', dataset: { tone: 'amber' } }, h('div', { class: 'stat-label', text: 'unseen' }), h('div', { class: 'stat-value', text: String(stats.unseen) })),
        )
      : null,
    totals && typeof totals === 'object' && !Array.isArray(totals) ? kv(Object.entries(totals).map(([k, v]) => [titleCase(k), typeof v === 'object' ? JSON.stringify(v) : String(v)])) : null,
    rows.length
      ? table({
          columns: [
            { key: 'module', label: 'Module', strong: true, mono: true, render: (r) => truncate(String(pick(r, 'module', 'path', 'name') ?? ''), 60) },
            { key: 'state', label: 'State', width: '120px', render: (r) => stateBadge(pick(r, 'state', 'status') ?? 'unseen') },
            { key: 'rounds', label: 'Rounds', width: '80px', align: 'right', render: (r) => String(pick(r, 'rounds', 'roundCount', 'investigations') ?? 0) },
            { key: 'evidence', label: 'Evidence', width: '90px', align: 'right', render: (r) => String(pick(r, 'evidence', 'evidenceCount', 'facts') ?? 0) },
            { key: 'summary', label: 'Summary', render: (r) => h('span', { class: 'tiny faint', text: truncate(String(pick(r, 'summary', 'note') ?? ''), 120) }) },
            { key: 'updated', label: 'Updated', width: '96px', align: 'right', render: (r) => h('span', { class: 'mono-sm faint', text: relativeTime(pick(r, 'updatedAt', 'at') ?? '') }) },
          ],
          rows,
          maxHeight: 460,
        })
      : empty({ iconName: 'layers', title: 'No coverage ledger', note: 'Open a case in survey mode (or investigate --module) to build one.' }),
  );
}

export function workplanView(payload, opts = {}) {
  const items = firstArray(payload, 'items', 'workItems', 'plan');
  if (!items.length) return empty({ iconName: 'jobs', title: 'No campaign items', note: 'Workplans hold dependency-aware child work handed to actors as linked, retry-keyed cases.' });
  return table({
    columns: [
      { key: 'state', label: 'State', width: '110px', render: (i) => stateBadge(pick(i, 'state', 'status', 'derived') ?? '') },
      { key: 'id', label: 'Item', width: '110px', mono: true, render: (i) => truncate(String(pick(i, 'id', 'itemId') ?? ''), 18) },
      { key: 'goal', label: 'Goal', strong: true, render: (i) => truncate(String(pick(i, 'goal', 'title') ?? ''), 140) },
      { key: 'after', label: 'Depends on', width: '150px', render: (i) => h('div', { class: 'pill-row' }, firstArray(i, 'after', 'dependsOn').map((x) => tag(String(x)))) },
      { key: 'child', label: 'Child case', width: '150px', render: (i) => (pick(i, 'childTaskId', 'taskId') ? h('button', { class: 'btn btn-sm', onclick: () => opts.onOpen?.(pick(i, 'childTaskId', 'taskId')) }, icon('case', 12), shortId(pick(i, 'childTaskId', 'taskId'), 10, 3)) : h('span', { class: 'faint', text: '—' })) },
      { key: 'actor', label: 'Actor', width: '120px', render: (i) => h('span', { class: 'mono-sm', text: String(pick(i, 'actor', 'claimedBy') ?? '—') }) },
    ],
    rows: items,
    maxHeight: 460,
  });
}

export function jobsTable(items, opts = {}) {
  const rows = arrayOf(items);
  if (!rows.length) return empty({ iconName: 'bolt', title: 'No background jobs', note: 'investigate --async and survey --fanout detach work that outlives a single call.' });
  return table({
    columns: [
      { key: 'state', label: 'State', width: '104px', render: (j) => stateBadge(pick(j, 'state', 'status') ?? '') },
      { key: 'id', label: 'Job', width: '150px', mono: true, render: (j) => truncate(String(pick(j, 'id', 'jobId') ?? ''), 24) },
      { key: 'question', label: 'Question / module', strong: true, render: (j) => truncate(String(pick(j, 'question', 'module', 'goal') ?? ''), 120) },
      { key: 'pid', label: 'PID', width: '76px', align: 'right', render: (j) => String(pick(j, 'pid') ?? '—') },
      { key: 'started', label: 'Started', width: '100px', align: 'right', render: (j) => h('span', { class: 'mono-sm faint', text: relativeTime(pick(j, 'startedAt', 'createdAt', 'at') ?? '') }) },
      { key: 'actions', label: '', width: '1%', render: (j) => (opts.onCancel ? h('button', { class: 'btn btn-sm', onclick: () => opts.onCancel(j) }, icon('stop', 12), 'Cancel') : null) },
    ],
    rows,
    maxHeight: 380,
  });
}

/* ── Environment ───────────────────────────────────────────────────────── */

export function toolHealthTable(tools) {
  const rows = arrayOf(tools);
  if (!rows.length) return empty({ iconName: 'cpu', title: 'No adapter health reported', note: 'Run doctor to probe every specialist tool.' });
  return table({
    columns: [
      {
        key: 'status',
        label: 'Health',
        width: '130px',
        render: (t) => {
          const available = t.available;
          const status = available === undefined
            ? String(pick(t, 'status', 'state', 'health') ?? 'unknown').toLowerCase()
            : available ? 'available' : 'missing';
          return h('span', { class: 'row', style: { gap: '5px' } },
            stateBadge(status),
            t.index ? badge(`index ${t.index}`, t.index === 'ready' ? 'green' : 'amber') : null,
          );
        },
      },
      { key: 'tool', label: 'Tool', width: '120px', strong: true, mono: true, render: (t) => String(pick(t, 'tool', 'name', 'adapter') ?? '') },
      { key: 'version', label: 'Version', width: '110px', mono: true, render: (t) => truncate(String(pick(t, 'version', 'toolVersion') ?? '—'), 22) },
      {
        key: 'detail',
        label: 'Detail / fix',
        render: (t) => h('span', { class: 'tiny faint' },
          flat(pick(t, 'detail', 'message', 'error', 'note') ?? ''),
          t.fixCommand ? h('span', { class: 'mono-sm', style: { marginLeft: '6px', color: 'var(--amber)' }, text: `→ ${t.fixCommand}` }) : null,
        ),
      },
      { key: 'ms', label: 'Probe', width: '78px', align: 'right', render: (t) => (pick(t, 'durationMs', 'elapsedMs') ? h('span', { class: 'mono-sm faint', text: duration(Number(pick(t, 'durationMs', 'elapsedMs'))) }) : '—') },
    ],
    rows,
    maxHeight: 420,
  });
}

export function readinessList(items) {
  const rows = arrayOf(items);
  if (!rows.length) return null;
  return h('div', { class: 'col', style: { gap: '6px' } }, rows.map((item) => {
    const ok = pick(item, 'ok', 'ready', 'satisfied');
    const label = pick(item, 'name', 'check', 'label', 'item') ?? '';
    const detail = pick(item, 'detail', 'message', 'fix', 'fixCommand', 'hint') ?? '';
    return h('div', { class: 'list-item' },
      h('span', { style: { color: ok ? 'var(--green)' : 'var(--amber)' } }, icon(ok ? 'check' : 'alert', 14)),
      h('div', { class: 'grow col', style: { gap: '2px' } },
        h('div', { style: { fontSize: 'var(--fs-12)', color: 'var(--text)' }, text: String(label) }),
        detail ? h('div', { class: 'mono-sm faint', text: truncate(String(detail), 180) }) : null,
      ),
      pick(item, 'fixCommand') ? h('button', { class: 'btn btn-sm', onclick: () => navigator.clipboard?.writeText(String(pick(item, 'fixCommand'))) }, icon('copy', 11), 'Copy fix') : null,
    );
  }));
}

export function routeMatrix(payload) {
  const rows = firstArray(payload, 'matrix', 'routes', 'rows', 'entries');
  if (!rows.length) return empty({ iconName: 'route', title: 'No routing matrix', note: 'cortex route prints the ordered matrix; cortex route <question> resolves one question.' });
  return table({
    columns: [
      { key: 'surface', label: 'Surface', width: '110px', render: (r) => badge(pick(r, 'surface') ?? '', 'info') },
      { key: 'question', label: 'Question kind', width: '170px', strong: true, render: (r) => truncate(String(pick(r, 'question', 'kind', 'intent', 'when') ?? ''), 60) },
      {
        key: 'tools',
        label: 'Route',
        render: (r) => {
          const tools = firstArray(r, 'tools', 'route', 'chain', 'steps');
          return h('div', { class: 'tool-chain' }, tools.flatMap((tool, index) => {
            const name = typeof tool === 'string' ? tool : pick(tool, 'tool', 'name', 'adapter') ?? '';
            return [index > 0 ? h('span', { class: 'route-arrow', text: '→' }) : null, badge(name, 'violet')];
          }));
        },
      },
      { key: 'fallback', label: 'Fallback', width: '160px', render: (r) => h('span', { class: 'tiny faint', text: truncate(String(pick(r, 'fallback', 'degraded', 'onMissing') ?? '—'), 60) }) },
    ],
    rows,
    maxHeight: 480,
    className: 'route-table',
  });
}

/* ── Timeline ──────────────────────────────────────────────────────────── */

export function timelineFeed(entries, opts = {}) {
  const rows = arrayOf(entries);
  if (!rows.length) return empty({ iconName: 'history', title: 'No activity yet', note: 'Phases, evidence, tool calls, and receipts merge into one time-sorted audit trail.' });
  const filter = opts.filter ? String(opts.filter).toLowerCase() : '';
  const visible = filter ? rows.filter((row) => matches(filter, pick(row, 'kind', 'type'), JSON.stringify(row))) : rows;
  return h('div', { class: 'timeline' },
    visible.slice(0, opts.limit ?? 400).map((entry) => {
      const kind = String(pick(entry, 'kind', 'type', 'category') ?? 'event').toLowerCase();
      const at = pick(entry, 'timestamp', 'at', 'time', 'createdAt') ?? '';
      const title = flat(pick(entry, 'summary', 'title', 'claim', 'phase', 'to', 'name')) || '';
      const detail = flat(pick(entry, 'detail', 'message', 'note', 'argv', 'command')) || '';
      const ref = flat(pick(entry, 'ref', 'evidenceId', 'id')) || '';
      return h('div', { class: 'tl-item', dataset: { kind } },
        h('div', { class: 'tl-head' },
          badge(kind, kind === 'phase' ? 'violet' : kind === 'evidence' ? 'accent' : kind === 'verification' ? 'green' : kind === 'command' ? 'info' : kind === 'decision' ? 'red' : 'slate'),
          h('span', { class: 'tl-time', title: absoluteTime(at), text: absoluteTime(at) || '—' }),
          pick(entry, 'tool') ? tag(flat(pick(entry, 'tool'))) : null,
          ref ? h('span', { class: 'mono-sm faint', text: truncate(ref, 26), title: ref }) : null,
          pick(entry, 'confidence') ? badge(String(pick(entry, 'confidence')), 'slate') : null,
        ),
        title ? h('div', { class: 'tl-body', text: truncate(title, 400) }) : null,
        detail && detail !== title ? h('div', { class: 'tl-body mono-sm faint', text: truncate(detail, 300) }) : null,
      );
    }),
  );
}

/* ── Misc ──────────────────────────────────────────────────────────────── */

export function notesList(items, opts = {}) {
  const rows = arrayOf(items);
  if (!rows.length) return empty({ iconName: 'note', title: 'No notes', note: 'Notes carry provenance — human, agent, or reviewer — without pretending to be verification.' });
  return h('div', { class: 'col', style: { gap: '7px' } }, rows.map((note) =>
    h('div', { class: 'evidence-item', dataset: { phase: 'paused' } },
      h('div', { class: 'evidence-claim', text: String(pick(note, 'observation', 'text', 'body', 'note') ?? '') }),
      h('div', { class: 'evidence-meta' },
        badge(pick(note, 'kind', 'type') ?? 'observation', 'slate'),
        pick(note, 'origin') ? badge(String(pick(note, 'origin')), 'info') : null,
        pick(note, 'actor') ? h('span', { text: String(pick(note, 'actor')) }) : null,
        pick(note, 'confidence') ? badge(String(pick(note, 'confidence')), 'slate') : null,
        pick(note, 'file') ? h('span', { class: 'mono-sm', text: `${pick(note, 'file')}${pick(note, 'line') ? `:${pick(note, 'line')}` : ''}` }) : null,
        note.sensitive ? badge('sensitive', 'red') : null,
        h('span', { class: 'grow' }),
        h('span', { class: 'mono-sm faint', title: absoluteTime(pick(note, 'at', 'createdAt') ?? ''), text: relativeTime(pick(note, 'at', 'createdAt') ?? '') }),
        opts.onAddSimilar ? buttonSmall('Reply', 'plus', () => opts.onAddSimilar(note)) : null,
      ),
    ),
  ));
}

export function scopePanel(scope) {
  if (!scope) return null;
  const drifted = pick(scope, 'drifted', 'drift') === true;
  const unexpected = firstArray(scope, 'unexpected', 'unexpectedFiles', 'outside', 'driftFiles');
  const boundarySource = pick(scope, 'boundary') ?? scope;
  const boundary = firstArray(boundarySource, 'files', 'symbols', 'declared', 'paths');
  return h('div', { class: 'col', style: { gap: '10px' } },
    h('div', { class: 'row-wrap' },
      stateBadge(drifted ? 'drift' : 'ok'),
      pick(scope, 'status') ? badge(String(pick(scope, 'status')), 'slate') : null,
      pick(scope, 'reason') ? h('span', { class: 'tiny faint', text: truncate(String(pick(scope, 'reason')), 160) }) : null,
    ),
    boundary.length ? h('div', {}, h('div', { class: 'micro', text: 'declared boundary' }), h('div', { class: 'pill-row', style: { marginTop: '5px' } }, boundary.slice(0, 40).map((f) => tag(truncate(typeof f === 'string' ? f : pick(f, 'path', 'file') ?? JSON.stringify(f), 44))))) : null,
    unexpected.length ? h('div', {}, h('div', { class: 'micro danger-text', text: 'outside the boundary' }), h('div', { class: 'pill-row', style: { marginTop: '5px' } }, unexpected.slice(0, 40).map((f) => tag(truncate(typeof f === 'string' ? f : pick(f, 'path', 'file') ?? JSON.stringify(f), 44))))) : null,
  );
}

export function leasePanel(lease) {
  if (!lease) return alertNote('No active lease', 'begin-change claims bounded, expiring ownership before any edit.');
  const expiresAt = pick(lease, 'expiresAt', 'expires', 'until') ?? '';
  return kv([
    ['Actor', pick(lease, 'actor', 'owner') ?? '—'],
    ['State', stateBadge(pick(lease, 'state', 'status') ?? 'active')],
    ['TTL', humanDuration(pick(lease, 'ttl') ?? '') || '—'],
    ['Expires', expiresAt ? `${absoluteTime(expiresAt)} (${relativeTime(expiresAt)})` : '—'],
    ['Acquired', relativeTime(pick(lease, 'acquiredAt', 'createdAt') ?? '') || '—'],
  ]);
}

export function alertNote(title, body, tone = 'info') {
  return h('div', { class: 'alert', dataset: { tone } },
    h('div', { class: 'alert-icon' }, icon('info', 15)),
    h('div', { class: 'alert-body' }, h('div', { class: 'alert-title', text: title }), h('div', { text: body })),
  );
}

export function workspaceLine(workspace) {
  return h('span', { class: 'mono-sm faint', title: String(workspace ?? ''), text: compactHome(workspace ?? '') });
}

export function ratioBar(done, total, tone = 'accent') {
  const pct = total ? (done / total) * 100 : 0;
  return h('div', { class: 'col', style: { gap: '4px' } },
    progress(pct, tone, `${done}/${total}`),
  );
}
