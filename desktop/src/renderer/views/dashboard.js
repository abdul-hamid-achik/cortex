/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { navigate } from '../lib/router.js';
import { state } from '../lib/state.js';
import { alert, badge, button, card, empty, kv, loading, stat } from '../lib/ui.js';
import { firstArray, pick, sessionsTable } from '../lib/renderers.js';
import { absoluteTime, basename, compactHome, duration, percent, relativeTime, sortBy, truncate } from '../lib/format.js';
import { openCommandRunner } from '../lib/forms.js';

const QUICK_ACTIONS = [
  { commandId: 'open', label: 'Open a case', icon: 'plus', kind: 'primary' },
  { commandId: 'investigate', label: 'Investigate', icon: 'search' },
  { commandId: 'review', label: 'Review a branch', icon: 'branch' },
  { commandId: 'recall-cases', label: 'Recall prior cases', icon: 'history' },
  { commandId: 'doctor', label: 'Run doctor', icon: 'pulse' },
  { commandId: 'setup', label: 'Check readiness', icon: 'shield' },
];

export const dashboardRoute = {
  id: 'dashboard',
  label: 'Dashboard',
  icon: 'dashboard',
  group: 'Operate',
  hint: 'Cross-repo rollup plus this workspace’s health',
  async render() {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('reading the session store…'));

    const [overview, git, doctor, tasks] = await Promise.all([
      api.run('overview', {}, { stream: false }).catch(() => null),
      api.repo.git().catch(() => null),
      api.run('doctor', {}, { stream: false, timeoutMs: 90_000 }).catch(() => null),
      api.run('list', {}, { stream: false }).catch(() => null),
    ]);

    render(root,
      hero({ git, overview }),
      statsRow(overview?.json),
      h('div', { class: 'grid grid-sidebar' },
        h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } },
          repoBreakdown(overview?.json),
          workspaceCard(git),
          workspaceTasks(tasks?.json),
        ),
        h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } },
          quickActions(),
          environmentCard(doctor),
          storeCard(),
        ),
      ),
    );
    return root;
  },
};

function hero({ git, overview }) {
  const totals = overview?.json ?? {};
  const sessions = firstArray(totals, 'sessions', 'items');
  return h('div', { class: 'dash-hero' },
    h('div', {},
      h('div', { class: 'dash-hero-title', text: git?.ok ? basename(git.root) : 'Cortex Deck' }),
      h('div', { class: 'dash-hero-sub' },
        git?.ok
          ? h('span', {}, `${compactHome(git.root)} · ${git.branch} @ ${git.head} · ${git.dirty ? `${git.dirty} uncommitted change${git.dirty === 1 ? '' : 's'}` : 'clean tree'}`)
          : h('span', { text: 'No workspace selected — pick a repository to start operating on it.' }),
      ),
      h('div', { class: 'row-wrap', style: { marginTop: '10px', gap: '6px' } },
        state.config?.casesDir ? badge(`store: ${compactHome(state.config.sessionsRoot ?? '')}`, 'slate') : null,
        sessions.length ? badge(`${sessions.length} sessions indexed`, 'info') : null,
        state.settings?.approvals?.commands ? badge('commands approved', 'amber') : null,
      ),
    ),
    h('div', { class: 'dash-hero-actions' },
      button('Open a case', { kind: 'primary', iconName: 'plus', onClick: () => openCommandRunner('open', { onDone: () => navigate('workspace') }) }),
      button('Sessions', { iconName: 'sessions', onClick: () => navigate('sessions') }),
      button('Console', { iconName: 'terminal', onClick: () => navigate('console') }),
    ),
  );
}

function statsRow(overview) {
  if (!overview) {
    return alert({ tone: 'warn', title: 'No overview data', body: 'cortex overview did not return a payload. Check the binary path in Settings.' });
  }
  const total = Number(pick(overview, 'sessions', 'total') ?? 0);
  const active = Number(pick(overview, 'active') ?? 0);
  const stale = Number(pick(overview, 'stale') ?? 0);
  const completed = Number(pick(overview, 'completed') ?? 0);
  const verified = Number(pick(overview, 'verified') ?? 0);
  const completionRate = pick(overview, 'completionRate');
  const verifiedRate = pick(overview, 'verifiedRate');
  const meanMs = pick(overview, 'meanTimeToCompleteMs');

  return h('div', { class: 'grid grid-4' },
    stat({ label: 'Sessions', value: total || '—', foot: `${active} in flight · ${stale} stale`, tone: 'info' }),
    stat({
      label: 'Complete',
      value: completed || '—',
      foot: completionRate !== undefined ? `${percent(completionRate)} completion rate` : 'no completions recorded',
      tone: 'green',
      bar: completionRate !== undefined ? (completionRate <= 1 ? completionRate * 100 : completionRate) : null,
    }),
    stat({
      label: 'Verified rate',
      value: verifiedRate !== undefined ? percent(verifiedRate) : '—',
      foot: `${verified} of ${completed} completions backed by adequate proof`,
      tone: 'accent',
      bar: verifiedRate !== undefined ? (verifiedRate <= 1 ? verifiedRate * 100 : verifiedRate) : null,
    }),
    stat({ label: 'Mean time to complete', value: meanMs !== undefined ? duration(Number(meanMs)) : '—', foot: 'across every repository', tone: 'slate' }),
  );
}

function repoBreakdown(overview) {
  const repos = firstArray(overview, 'repos', 'repositories', 'perRepo', 'byRepo');
  const rows = repos.length ? sortBy(repos, (r) => Number(pick(r, 'sessions', 'total', 'count') ?? 0)).slice(0, 14) : [];
  const max = rows.reduce((acc, r) => Math.max(acc, Number(pick(r, 'sessions', 'total', 'count') ?? 0)), 0) || 1;

  return card({ title: 'Where the work sits', sub: 'per-repository rollup from the central XDG store', iconName: 'database', flush: true },
    rows.length
      ? h('div', { class: 'card-body' },
          h('div', { class: 'repo-bars' }, rows.map((row) => {
            const name = String(pick(row, 'repo', 'name', 'slug') ?? '');
            const sessions = Number(pick(row, 'sessions', 'total', 'count') ?? 0);
            const completed = Number(pick(row, 'completed') ?? 0);
            const active = Number(pick(row, 'active') ?? 0);
            return h('div', { class: 'repo-bar' },
              h('button', { class: 'repo-bar-name', title: `Filter sessions by ${name}`, onclick: () => navigate('sessions', { repo: name }) }, name),
              h('div', { class: 'bar', dataset: { tone: completed ? 'green' : 'info' } }, h('span', { style: { width: `${Math.max(3, (sessions / max) * 100)}%` } })),
              h('div', { class: 'repo-bar-num', title: `${sessions} sessions · ${completed} complete · ${active} active` }, `${sessions}`),
            );
          })),
        )
      : empty({ iconName: 'database', title: 'No per-repo breakdown', note: 'cortex overview returned no repository rows.' }),
  );
}

function workspaceCard(git) {
  if (!git?.ok) return card({ title: 'Workspace', iconName: 'git' }, alert({ tone: 'warn', title: 'Not a git repository', body: git?.error ?? 'Cortex needs git for identity, diffs, and revision-bound verification.' }));
  return card({
    title: 'Workspace state',
    sub: compactHome(git.root),
    iconName: 'git',
    actions: [button('Reveal', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(git.root) })],
  },
    kv([
      ['Branch', h('span', { class: 'mono-sm', text: git.branch })],
      ['HEAD', h('span', { class: 'mono-sm', text: git.head })],
      ['Uncommitted', git.dirty ? badge(`${git.dirty} file${git.dirty === 1 ? '' : 's'}`, 'amber') : badge('clean', 'green')],
      ['Remotes', git.remotes.length ? h('span', { class: 'mono-sm', text: truncate(git.remotes[0], 60) }) : '—'],
    ]),
    git.changes?.length
      ? h('div', { style: { marginTop: '12px' } },
          h('div', { class: 'micro', text: 'changed files' }),
          h('div', { class: 'pill-row', style: { marginTop: '6px' } }, git.changes.slice(0, 24).map((c) => h('span', { class: 'tag', title: `${c.code} ${c.path}`, text: truncate(c.path, 34) }))),
        )
      : null,
    git.commits?.length
      ? h('div', { style: { marginTop: '14px' } },
          h('div', { class: 'micro', text: 'recent commits' }),
          h('div', { class: 'list', style: { marginTop: '6px' } }, git.commits.map((c) =>
            h('div', { class: 'list-item' },
              h('span', { class: 'mono-sm accent-text', text: c.hash }),
              h('span', { class: 'grow truncate', text: c.subject }),
              h('span', { class: 'mono-sm faint', title: absoluteTime(c.date), text: relativeTime(c.date) }),
            ),
          )),
        )
      : null,
  );
}

function workspaceTasks(json) {
  const rows = firstArray(json, 'tasks', 'items', 'sessions');
  return card({
    title: 'This workspace',
    sub: 'cortex list — newest first',
    iconName: 'case',
    flush: true,
    actions: [button('All sessions', { size: 'sm', onClick: () => navigate('sessions') })],
  },
    rows.length
      ? sessionsTable(rows, {
          maxHeight: 320,
          onRowClick: (row) => {
            const taskId = pick(row, 'taskId', 'id');
            navigate('case', { taskId });
          },
        })
      : empty({ iconName: 'case', title: 'No cases in this workspace', note: 'Open one with the button above — or run any command from the Console.' }),
  );
}

function quickActions() {
  return card({ title: 'Quick actions', sub: 'every one of these opens the full command form', iconName: 'bolt' },
    h('div', { class: 'task-grid' }, QUICK_ACTIONS.map((action) =>
      h('button', {
        class: 'feature-card',
        onclick: () => openCommandRunner(action.commandId, { onDone: () => navigate('workspace') }),
      },
        h('div', { class: 'feature-title' }, icon(action.icon, 14), h('span', { text: action.label })),
        h('div', { class: 'feature-summary', text: state.commands.get(action.commandId)?.summary ?? '' }),
      ),
    )),
  );
}

function environmentCard(doctorResult) {
  const json = doctorResult?.json;
  if (!json) return card({ title: 'Environment', iconName: 'pulse' }, alert({ tone: 'warn', title: 'Doctor unavailable', body: doctorResult?.error ?? 'no result' }));
  const tools = firstArray(json, 'tools', 'adapters', 'toolHealth', 'health');
  const ok = tools.filter((t) => ['ok', 'healthy', 'available', 'authoritative', 'ready'].includes(String(pick(t, 'status', 'state', 'health') ?? '').toLowerCase()));
  const missing = tools.filter((t) => ['missing', 'unavailable', 'not_found'].includes(String(pick(t, 'status', 'state', 'health') ?? '').toLowerCase()));

  return card({
    title: 'Specialist tools',
    sub: 'adapters degrade safely — a missing tool is not an error',
    iconName: 'cpu',
    actions: [button('Doctor', { size: 'sm', onClick: () => navigate('environment') })],
    flush: true,
  },
    h('div', { class: 'card-body col', style: { gap: '10px' } },
      h('div', { class: 'row-wrap' },
        badge(`${ok.length} healthy`, 'green'),
        badge(`${missing.length} missing`, missing.length ? 'amber' : 'slate'),
        badge(`${tools.length} total`, 'info'),
      ),
      tools.length
        ? h('div', { class: 'pill-row' }, tools.map((t) => {
            const status = String(pick(t, 'status', 'state', 'health') ?? 'unknown').toLowerCase();
            return badge(`${pick(t, 'tool', 'name', 'adapter') ?? '?'} · ${status}`, status === 'ok' || status === 'healthy' || status === 'available' ? 'green' : status === 'missing' || status === 'unavailable' ? 'slate' : 'amber');
          }))
        : h('div', { class: 'tiny faint', text: 'doctor returned no tool health rows' }),
    ),
    doctorResult && !doctorResult.ok ? h('div', { class: 'card-foot' }, alert({ tone: 'error', title: 'doctor failed', body: doctorResult.error })) : null,
  );
}

function storeCard() {
  const config = state.config ?? {};
  return card({
    title: 'Case store',
    sub: 'central XDG layout — every session, every repo, one place',
    iconName: 'database',
    actions: [button('Open folder', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(config.sessionsRoot ?? '') })],
  },
    kv([
      ['Sessions root', h('span', { class: 'mono-sm', text: compactHome(config.sessionsRoot ?? '—') })],
      ['This workspace', h('span', { class: 'mono-sm', text: compactHome(config.casesDir ?? '—') })],
      ['Archive', h('span', { class: 'mono-sm', text: compactHome(config.archiveRoot ?? '—') })],
      ['Config', h('span', { class: 'mono-sm', text: compactHome(config.configDir ?? '—') })],
      ['Recall', config.recall?.enabled ? badge(`enabled · ${config.recall.embedModel ?? ''}`, 'accent') : badge('disabled', 'slate')],
      ['Verifiers', (config.verifiers ?? []).length ? h('div', { class: 'pill-row' }, config.verifiers.map((v) => tag(v.name ?? JSON.stringify(v)))) : badge('none configured', 'amber')],
    ]),
  );
}

function tag(text) {
  return h('span', { class: 'tag', text: String(text) });
}
