/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { openCommandRunner } from '../lib/forms.js';
import { jsonView } from '../lib/jsonview.js';
import { navigate } from '../lib/router.js';
import { state } from '../lib/state.js';
import { alert, badge, button, card, copyButton, empty, loading, pageHead, stateBadge, table, toast } from '../lib/ui.js';
import { evidenceList, firstArray, flat, pick } from '../lib/renderers.js';
import { absoluteTime, bytes, relativeTime, shortId, sortBy, truncate } from '../lib/format.js';

export const evidenceRoute = {
  id: 'evidence',
  label: 'Evidence',
  icon: 'evidence',
  group: 'Work',
  hint: 'ledgers, artifacts, and cross-case recall',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('reading the recall index and store…'));

    const [repos, recallConfig] = await Promise.all([
      api.store.repos({ includeArchive: false }).catch(() => []),
      Promise.resolve(state.config?.recall ?? {}),
    ]);

    render(root,
      pageHead({
        title: 'Evidence & memory',
        sub: 'Evidence is stamped at a write boundary and redacted before it is persisted. Cross-case recall (veclite) surfaces prior disproofs so a theory is not re-derived from scratch.',
        actions: [
          recallConfig.enabled ? badge('recall enabled', 'green') : badge('recall disabled', 'slate'),
          button('Rebuild index', { iconName: 'refresh', onClick: () => openCommandRunner('reindex-cases', { onDone: () => navigate('evidence', {}, { force: true }) }) }),
          button('Docs', { iconName: 'book', onClick: () => navigate('repo', { tab: 'docs', path: 'docs/case-file.md' }) }),
        ],
      }),
      recallCard(),
      readerCard(params),
      storeBrowser(repos),
    );
    return root;
  },
};

function recallCard() {
  const results = h('div', { class: 'col', style: { gap: '10px' } });
  const query = h('input', { class: 'input', placeholder: 'search prior resolved hypotheses and definitive receipts…', style: { minWidth: '320px' } });
  const repo = h('input', { class: 'input', placeholder: 'repo (empty = cross-repo)', style: { minWidth: '160px' } });
  const limit = h('select', { class: 'select' }, [5, 10, 20, 50].map((n) => h('option', { value: String(n), text: `${n} results`, selected: n === 5 })));

  const search = async () => {
    const value = query.value.trim();
    if (!value) return;
    render(results, loading('recalling…'));
    const values = { query: value, limit: Number(limit.value) };
    if (repo.value.trim()) values.repo = repo.value.trim();
    const result = await api.run('recall-cases', values, { stream: false }).catch((err) => ({ ok: false, error: String(err?.message ?? err) }));
    if (!result.ok && !result.json) {
      render(results, alert({ tone: 'warn', title: 'Recall unavailable', body: `${result.error ?? ''} — recall is best-effort: no veclite configured means empty, never an error.` }));
      return;
    }
    const items = firstArray(result.json, 'results', 'cases', 'items', 'matches');
    render(results,
      badge(`${items.length} prior cases`, 'info'),
      items.length
        ? h('div', { class: 'col', style: { gap: '8px' } }, items.map((item) =>
            h('div', { class: 'evidence-item', dataset: { phase: 'investigating' } },
              h('div', { class: 'evidence-claim', text: truncate(String(pick(item, 'statement', 'hypothesis', 'claim', 'summary', 'text') ?? JSON.stringify(item)), 400) }),
              h('div', { class: 'evidence-meta' },
                pick(item, 'status') ? stateBadge(String(pick(item, 'status'))) : null,
                pick(item, 'taskId', 'task', 'id') ? h('button', {
                  class: 'btn btn-ghost btn-sm',
                  onclick: () => navigate('case', { taskId: String(pick(item, 'taskId', 'task', 'id')) }),
                }, icon('case', 11), shortId(String(pick(item, 'taskId', 'task', 'id')), 12, 4)) : null,
                pick(item, 'repo', 'repository') ? badge(String(pick(item, 'repo', 'repository')), 'slate') : null,
                pick(item, 'score', 'similarity') ? h('span', { class: 'mono-sm', text: `score ${Number(pick(item, 'score', 'similarity')).toFixed(3)}` }) : null,
                pick(item, 'at', 'timestamp') ? h('span', { class: 'mono-sm faint', text: relativeTime(pick(item, 'at', 'timestamp')) }) : null,
              ),
            )))
        : empty({ iconName: 'history', title: 'No prior cases matched', note: 'Rejected and challenged hypotheses are the gold here — an empty result usually means the index has not been built for this corpus yet.' }),
      card({ title: 'recall-cases --json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(result.json ?? {}, { label: 'recall', depth: 2, collapsed: true }))),
    );
  };

  query.onkeydown = (event) => { if (event.key === 'Enter') search(); };

  return card({
    title: 'Cross-case recall',
    sub: 'cortex recall-cases — prior disproofs to read before re-deriving a theory',
    iconName: 'history',
    actions: [button('Search', { kind: 'primary', size: 'sm', iconName: 'search', onClick: () => search() })],
  },
    h('div', { class: 'filter-bar', style: { marginBottom: '12px' } }, query, repo, limit),
    results,
  );
}

function readerCard(params) {
  const taskInput = h('input', { class: 'input mono', placeholder: 'task_…', value: params?.taskId ?? state.selectedTask?.taskId ?? '', style: { minWidth: '260px' } });
  const idInput = h('input', { class: 'input mono', placeholder: 'ev_… (evidence) · case://…/raw/… (artifact)', style: { minWidth: '300px' } });
  const out = h('div', { class: 'col', style: { gap: '10px' } });

  const read = async (kind) => {
    const taskId = taskInput.value.trim();
    const ref = idInput.value.trim();
    if (!taskId || !ref) { toast('warn', 'Both fields are required', 'task id and evidence/artifact reference'); return; }
    render(out, loading(`reading ${kind}…`));
    const result = await api.run(kind, kind === 'read-evidence' ? { taskId, evidenceId: ref } : { taskId, ref }, { stream: false }).catch((err) => ({ ok: false, error: String(err?.message ?? err) }));
    const json = result?.json;
    render(out,
      result?.ok ? null : alert({ tone: 'error', title: `${kind} failed`, body: result?.error ?? '' }),
      json ? jsonView(json, { label: kind, depth: 3 }) : null,
      json?.text || json?.content
        ? h('pre', { class: 'code', text: truncate(String(json.text ?? json.content), 200_000) })
        : null,
    );
  };

  return card({
    title: 'Read one record',
    sub: 'read-evidence prints a full record; read-artifact previews a task-owned raw ref or a task-referenced fcheap artifact (bounded to 128 KiB)',
    iconName: 'eye',
  },
    h('div', { class: 'filter-bar', style: { marginBottom: '12px' } },
      taskInput,
      idInput,
      button('Read evidence', { size: 'sm', iconName: 'evidence', onClick: () => read('read-evidence') }),
      button('Preview artifact', { size: 'sm', iconName: 'file', onClick: () => read('read-artifact') }),
      h('span', { class: 'grow' }),
      button('Full runner…', { size: 'sm', onClick: () => openCommandRunner('read-artifact') }),
    ),
    out,
  );
}

function storeBrowser(repos) {
  const list = Array.isArray(repos) ? repos : [];
  const repoSelect = h('select', { class: 'select', style: { minWidth: '220px' } },
    h('option', { value: '', text: `— ${list.length} repositories —` }),
    ...list.map((repo) => h('option', { value: repo.slug, text: `${repo.slug} (${repo.sessions})` })),
  );
  const sessionsHost = h('div', { class: 'col', style: { gap: '10px' } });
  const evidenceHost = h('div', { class: 'col', style: { gap: '10px' } });

  repoSelect.onchange = async () => {
    const slug = repoSelect.value;
    if (!slug) { render(sessionsHost); return; }
    render(sessionsHost, loading('reading the case store…'));
    const sessions = await api.store.sessions(slug).catch(() => []);
    const sorted = sortBy(sessions ?? [], (s) => s.mtime ?? '');
    render(sessionsHost,
      badge(`${sorted.length} sessions in ${slug}`, 'info'),
      sorted.length
        ? table({
            rows: sorted,
            maxHeight: 320,
            rowKey: (row) => row.taskId,
            onRowClick: async (row) => {
              render(evidenceHost, loading(`reading ${row.taskId}…`));
              const file = await api.store.file(row.taskId, 'evidence.jsonl').catch(() => null);
              const records = (file?.records?.records ?? []).map((r) => r.value);
              render(evidenceHost,
                card({
                  title: `${row.taskId} · evidence.jsonl`,
                  sub: `${records.length} records · ${bytes(file?.size ?? 0)}${file?.truncated ? ' · truncated' : ''}`,
                  iconName: 'ledger',
                  actions: [
                    button('Open case', { size: 'sm', iconName: 'case', onClick: () => navigate('case', { taskId: row.taskId }) }),
                    copyButton(() => records.map((r) => JSON.stringify(r)).join('\n'), 'Copy ledger'),
                  ],
                  flush: true,
                }, h('div', { class: 'card-body' }, evidenceList(sortBy(records, (r) => pick(r, 'timestamp', 'createdAt') ?? ''), {
                  onPreview: (e) => openCommandRunner('read-artifact', {
                    values: { taskId: row.taskId, ref: flat(pick(e, 'rawRef', 'uri', 'ref')) },
                    title: 'Raw preview',
                  }),
                }))),
              );
            },
            columns: [
              { key: 'taskId', label: 'Task', mono: true, width: '220px', render: (row) => shortId(row.taskId, 22, 4) },
              { key: 'mtime', label: 'Modified', width: '120px', align: 'right', render: (row) => h('span', { class: 'mono-sm faint', title: absoluteTime(row.mtime), text: relativeTime(row.mtime) }) },
              { key: 'files', label: 'Files', width: '80px', align: 'right', render: () => '—' },
              { key: 'open', label: '', width: '1%', render: (row) => h('button', { class: 'btn btn-ghost btn-sm', onclick: (event) => { event.stopPropagation(); navigate('case', { taskId: row.taskId }); } }, icon('arrowRight', 12)) },
            ],
          })
        : empty({ title: 'No sessions in this repository' }),
    );
  };

  return card({
    title: 'Browse the central store',
    sub: `${state.config?.sessionsRoot ?? '$XDG_STATE_HOME/cortex/sessions'} — ledgers read directly from disk, no projection`,
    iconName: 'database',
    actions: [state.config?.sessionsRoot ? button('Reveal', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(state.config.sessionsRoot) }) : null],
  },
    h('div', { class: 'filter-bar', style: { marginBottom: '12px' } }, repoSelect),
    h('div', { class: 'grid grid-2', style: { alignItems: 'start' } }, sessionsHost, evidenceHost),
  );
}
