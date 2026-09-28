/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { debounce, h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { openCommandRunner } from '../lib/forms.js';
import { navigate } from '../lib/router.js';
import { alert, badge, button, card, empty, loading, pageHead, stateBadge, table } from '../lib/ui.js';
import { firstArray, sessionRow } from '../lib/renderers.js';
import { absoluteTime, relativeTime, shortId, sortBy, truncate } from '../lib/format.js';

const FILTERS = {
  repo: '',
  query: '',
  active: false,
  stale: false,
  archived: false,
  staleAfter: '24h',
};

export const sessionsRoute = {
  id: 'sessions',
  label: 'Sessions',
  icon: 'sessions',
  group: 'Operate',
  hint: 'Every session in the central store, any repository',
  async render({ params }) {
    if (params?.repo !== undefined) FILTERS.repo = params.repo ?? '';
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    const listHost = h('div');
    const meta = h('div', { class: 'row', style: { gap: '8px' } });

    const controls = h('div', { class: 'filter-bar' },
      h('input', {
        class: 'input',
        style: { minWidth: '220px' },
        placeholder: 'search id, goal, state, mode, repo, outcome…',
        value: FILTERS.query,
        oninput: debounce((event) => { FILTERS.query = event.target.value; load(); }, 260),
      }),
      h('input', {
        class: 'input',
        style: { minWidth: '140px' },
        placeholder: 'repo contains',
        value: FILTERS.repo,
        oninput: debounce((event) => { FILTERS.repo = event.target.value; load(); }, 260),
      }),
      toggle('In-flight only', 'active'),
      toggle('Stale only', 'stale'),
      toggle('Archived', 'archived'),
      h('select', {
        class: 'select',
        title: 'Stale threshold',
        onchange: (event) => { FILTERS.staleAfter = event.target.value; load(); },
      }, ['6h', '24h', '3d', '7d', '30d'].map((value) => h('option', { value, text: `stale after ${value}`, selected: value === FILTERS.staleAfter }))),
      h('span', { class: 'grow' }),
      meta,
      button('Refresh', { iconName: 'refresh', size: 'sm', onClick: () => load() }),
      button('Prune stale…', { iconName: 'trash', size: 'sm', onClick: () => openCommandRunner('prune', { onDone: () => load() }) }),
    );

    function toggle(label, key) {
      return h('label', { class: 'switch' },
        h('input', { type: 'checkbox', checked: FILTERS[key], onchange: (event) => { FILTERS[key] = event.target.checked; load(); } }),
        h('span', { class: 'switch-track' }),
        h('span', { class: 'switch-text', text: label }),
      );
    }

    async function load() {
      render(listHost, loading('reading the central session store…'));
      const values = {};
      if (FILTERS.repo) values.repo = FILTERS.repo;
      if (FILTERS.query) values.query = FILTERS.query;
      if (FILTERS.active) values.active = true;
      if (FILTERS.stale) values.stale = true;
      if (FILTERS.archived) values.archived = true;
      values['stale-after'] = FILTERS.staleAfter;

      const result = await api.run('sessions', values, { stream: false, timeoutMs: 90_000 }).catch((err) => ({ ok: false, error: String(err?.message ?? err) }));
      if (!result.ok && !result.json) {
        render(listHost, alert({ tone: 'error', title: 'Could not list sessions', body: result.error ?? 'unknown error' }));
        render(meta);
        return;
      }
      const rows = normalize(result.json);
      render(meta, badge(`${rows.length} sessions`, 'info'), result.durationMs ? h('span', { class: 'mono-sm faint', text: `${result.durationMs}ms` }) : null);
      render(listHost, rows.length ? sessionsView(rows, result, FILTERS.archived) : empty({
        iconName: 'sessions',
        title: 'No sessions match these filters',
        note: 'Sessions live in $XDG_STATE_HOME/cortex/sessions/<repo>/<taskId>. Repo-local cases_dir sessions only show up in cortex list for that repo.',
      }));
    }

    await load();

    render(root,
      pageHead({
        title: 'Sessions',
        sub: 'The central XDG audit view: every Cortex session across every repository, newest first. Click a row to open the case.',
        actions: [
          button('Overview', { iconName: 'dashboard', onClick: () => navigate('dashboard') }),
          button('Workspace tasks', { iconName: 'workspace', onClick: () => navigate('workspace') }),
        ],
      }),
      card({ title: 'Filters', iconName: 'filter', flush: true }, h('div', { class: 'card-body' }, controls)),
      listHost,
    );
    return root;
  },
};

function normalize(json) {
  if (!json) return [];
  const rows = firstArray(json, 'sessions', 'items', 'tasks');
  if (rows.length) return rows;
  if (Array.isArray(json)) return json;
  const nested = Object.values(json).find((value) => Array.isArray(value) && value.length && typeof value[0] === 'object');
  return nested ?? [];
}

function sessionsView(rows, result, archived) {
  const sorted = sortBy(rows.map(sessionRow), (row) => row.updated || '');
  return card({
    title: 'All sessions',
    sub: result?.commandLine ? truncate(result.commandLine, 160) : '',
    iconName: 'sessions',
    flush: true,
  },
    table({
      rows: sorted,
      rowKey: (row) => row.taskId,
      maxHeight: 640,
      onRowClick: (row) => navigate('case', { taskId: row.taskId, repo: row.repo, workspace: row.workspace }),
      rowAttrs: (row) => ({ phase: row.phase }),
      columns: [
        {
          key: 'phase',
          label: 'Phase',
          width: '130px',
          render: (row) => h('span', { class: 'row', style: { gap: '5px' } }, stateBadge(row.phase), row.stale ? badge('stale', 'amber') : null),
        },
        {
          key: 'goal',
          label: 'Goal',
          strong: true,
          render: (row) => h('div', { class: 'col', style: { gap: '2px', minWidth: '0' } },
            h('div', { class: 'truncate', style: { color: 'var(--text)', maxWidth: '58ch' }, text: truncate(row.goal, 120) || '(no goal)' }),
            h('div', { class: 'mono-sm faint' }, `${shortId(row.taskId, 18, 4)}${row.mode ? ` · ${row.mode}` : ''}${row.actor ? ` · ${row.actor}` : ''}`),
          ),
        },
        { key: 'repo', label: 'Repo', width: '140px', render: (row) => h('span', { class: 'mono-sm', text: truncate(row.repo, 24) }) },
        { key: 'assessment', label: 'Assessment', width: '116px', render: (row) => (row.assessment ? stateBadge(row.assessment) : h('span', { class: 'faint', text: '—' })) },
        {
          key: 'updated',
          label: 'Updated',
          width: '112px',
          align: 'right',
          render: (row) => h('span', { class: 'mono-sm faint', title: absoluteTime(row.updated), text: relativeTime(row.updated) || '—' }),
        },
        {
          key: 'actions',
          label: '',
          width: '1%',
          render: (row) => h('div', { class: 'row', style: { gap: '4px' } },
            rowAction('show', 'eye', 'Open case', () => navigate('case', { taskId: row.taskId, repo: row.repo, workspace: row.workspace })),
            rowAction('handoff', 'download', 'Handoff', () => openCommandRunner('handoff', { values: { taskId: row.taskId } })),
            archived
              ? rowAction('unarchive', 'archive', 'Unarchive', () => openCommandRunner('unarchive', { values: { taskId: row.taskId }, onDone: () => navigate('sessions', {}, { force: true }) }))
              : rowAction('archive', 'archive', 'Archive', () => openCommandRunner('archive', { values: { taskId: row.taskId }, onDone: () => navigate('sessions', {}, { force: true }) })),
          ),
        },
      ],
    }),
  );
}

function rowAction(commandId, iconName, title, onClick) {
  return h('button', {
    class: 'btn btn-ghost btn-sm btn-icon',
    title: `${title} (${commandId})`,
    onclick: (event) => { event.stopPropagation(); onClick(); },
  }, icon(iconName, 12));
}
