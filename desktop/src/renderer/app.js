/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from './lib/api.js';
import { busEmit, busOn } from './lib/bus.js';
import { h, render } from './lib/dom.js';
import { icon } from './lib/icons.js';
import { openPalette } from './lib/palette.js';
import { navigate, onNavigate, refresh, registerRoutes, restoreRoute, routeList, setHost } from './lib/router.js';
import { applyTheme, approvals, boot, pushEvent, state, subscribe } from './lib/state.js';
import { badge, button, chip, toast } from './lib/ui.js';
import { basename, compactHome, duration, relativeTime, truncate } from './lib/format.js';

import { dashboardRoute } from './views/dashboard.js';
import { sessionsRoute } from './views/sessions.js';
import { workspaceRoute } from './views/workspace.js';
import { caseRoute } from './views/case.js';
import { consoleRoute } from './views/console.js';
import { longRunRoute } from './views/longrun.js';
import { evidenceRoute } from './views/evidence.js';
import { reviewRoute } from './views/review.js';
import { environmentRoute } from './views/environment.js';
import { mcpRoute } from './views/mcp.js';
import { repoRoute } from './views/repo.js';
import { devRoute } from './views/dev.js';
import { featuresRoute } from './views/features.js';
import { settingsRoute } from './views/settings.js';

const NAV_GROUPS = [
  { label: 'Operate', ids: ['dashboard', 'sessions', 'workspace', 'case', 'console'] },
  { label: 'Work', ids: ['longrun', 'evidence', 'review'] },
  { label: 'System', ids: ['environment', 'mcp', 'repo', 'dev'] },
  { label: 'Reference', ids: ['features', 'settings'] },
];

const SMOKE_VIEWS = ['dashboard', 'sessions', 'workspace', 'console', 'features', 'environment', 'repo', 'evidence', 'longrun', 'review', 'mcp', 'dev', 'settings'];

let activityOpen = false;
const activity = [];

async function main() {
  registerRoutes([
    dashboardRoute, sessionsRoute, workspaceRoute, caseRoute, consoleRoute,
    longRunRoute, evidenceRoute, reviewRoute, environmentRoute, mcpRoute,
    repoRoute, devRoute, featuresRoute, settingsRoute,
  ]);

  try {
    await boot();
  } catch (err) {
    render(document.getElementById('view'), h('div', { class: 'alert', dataset: { tone: 'error' } },
      icon('alert', 15),
      h('div', { class: 'alert-body' },
        h('div', { class: 'alert-title', text: 'Cortex Deck could not start' }),
        h('div', { text: String(err?.message ?? err) }),
      ),
    ));
    hideBoot();
    return;
  }

  applyTheme();
  setHost(document.getElementById('view'));
  renderRail();
  renderTopbar();
  wireEvents();
  wireKeys();

  onNavigate((route) => {
    document.title = `${route.label} · Cortex Deck`;
    for (const node of document.querySelectorAll('.rail-item')) {
      node.dataset.active = String(node.dataset.route === route.id);
    }
    const title = document.getElementById('topbar-title');
    if (title) title.textContent = route.label;
    const hint = document.getElementById('topbar-hint');
    if (hint) hint.textContent = route.hint ?? '';
  });

  await navigate(restoreRoute('dashboard'));
  hideBoot();

  const smokeParam = new URLSearchParams(location.search).get('smoke');
  const isSmoke = smokeParam === '1' || smokeParam === '2';
  if (isSmoke) {
    const smokeTask = new URLSearchParams(location.search).get('task');
    let views = SMOKE_VIEWS;
    if (smokeParam === '2') {
      views = ['case'];
      if (smokeTask) await navigate('case', { taskId: smokeTask }, { force: true });
    }
    api.smokeReady({
      views: views.map((id) => ({ id })),
      probe: state.probe ?? null,
      routes: routeList().map((r) => r.id),
      commands: state.commands.size,
      workspace: state.workspace,
      smokeTask: smokeTask ?? null,
    });

    // Prove the renderer → main → cortex bridge end to end from inside the app,
    // including the MCP console: start the server, list tools, call one, stop.
    const runs = [];
    try {
      const list = await api.run('list', {}, { stream: false });
      runs.push({ id: 'list', ok: list.ok, rows: Array.isArray(list.json) ? list.json.length : 0 });
      const sessions = await api.run('sessions', { active: true }, { stream: false });
      runs.push({ id: 'sessions', ok: sessions.ok, rows: Array.isArray(sessions.json) ? sessions.json.length : 0 });
      if (smokeTask) {
        const status = await api.run('status', { taskId: smokeTask, detail: 'full' }, { stream: false });
        runs.push({ id: 'status', ok: status.ok, phase: status.json?.phase ?? '' });
      }
      const started = await api.mcp.start({ profile: 'all', workspace: state.workspace });
      runs.push({ id: 'mcp-start', ok: started.state === 'ready', tools: started.toolCount });
      const call = await api.mcp.call('cortex_sessions', { active: true }, 90_000);
      runs.push({ id: 'mcp-call', ok: call.isError !== true && Boolean(call.envelope) });
      await api.mcp.stop();
      runs.push({ id: 'mcp-stop', ok: true });
    } catch (err) {
      runs.push({ id: 'bridge', ok: false, error: String(err?.message ?? err) });
    }
    globalThis.deck?.notify?.({ type: 'smoke-runs', runs });
  }
}

function hideBoot() {
  const boot = document.getElementById('boot');
  if (!boot) return;
  boot.dataset.hidden = 'true';
  setTimeout(() => boot.remove(), 260);
}

/* ── Rail ──────────────────────────────────────────────────────────────── */

function renderRail() {
  const rail = document.getElementById('rail');
  const routes = new Map(routeList().map((route) => [route.id, route]));

  const collapseButton = h('button', {
    class: 'rail-item',
    title: 'Collapse sidebar',
    onclick: () => {
      const app = document.getElementById('app');
      app.dataset.rail = app.dataset.rail === 'collapsed' ? 'expanded' : 'collapsed';
    },
  }, icon('chevronRight', 15), h('span', { class: 'rail-label', text: 'Collapse' }));

  const scroll = h('div', { class: 'rail-scroll' },
    NAV_GROUPS.map((group) => [
      h('div', { class: 'rail-section' }, h('div', { class: 'rail-section-label', text: group.label })),
      group.ids
        .map((id) => routes.get(id))
        .filter(Boolean)
        .map((route) => h('button', {
          class: 'rail-item',
          dataset: { route: route.id, active: 'false' },
          title: `${route.label} — ${route.hint ?? ''}`,
          onclick: () => navigate(route.id),
        },
          icon(route.icon ?? 'grid', 15, 'rail-icon'),
          h('span', { class: 'rail-label', text: route.label }),
          route.id === 'case' && state.selectedTask?.taskId
            ? h('span', { class: 'rail-count', text: '●' })
            : null,
        )),
    ]),
  );

  render(rail,
    h('div', { class: 'rail-brand' },
      h('img', { class: 'rail-logo', src: './assets/icon.png', alt: 'Cortex Deck' }),
      h('div', {},
        h('div', { class: 'rail-title', text: 'Cortex Deck' }),
        h('div', { class: 'rail-sub', text: 'operator console' }),
      ),
    ),
    scroll,
    h('div', { class: 'rail-foot' },
      h('button', {
        class: 'rail-item',
        title: 'Activity log',
        onclick: () => toggleActivity(),
      }, icon('history', 15, 'rail-icon'), h('span', { class: 'rail-label', text: 'Activity' }), h('span', { class: 'rail-count', id: 'activity-count', text: String(activity.length) })),
      collapseButton,
    ),
  );
}

/* ── Topbar ────────────────────────────────────────────────────────────── */

function renderTopbar() {
  const bar = document.getElementById('topbar');
  const route = routeList()[0];

  const workspaceChip = chip(basename(state.workspace) || 'no workspace', {
    iconName: 'folder',
    title: `${state.workspace} — click to switch`,
    onClick: async () => {
      const result = await api.workspace.choose();
      if (result?.canceled) return;
      navigate('workspace', {}, { force: true });
      renderTopbar();
    },
  });

  const binaryChip = chip(
    state.probe?.ok ? truncate((state.probe.version ?? 'cortex').replace(/^cortex version /, ''), 28) : 'cortex not found',
    { dot: true, state: state.probe?.ok ? 'ok' : 'error', title: state.probe?.path ?? 'binary not resolved', onClick: () => navigate('environment') },
  );

  const approval = approvals();
  const approvalChip = chip(
    [approval.commands && 'cmds', approval.remoteRecall && 'recall', approval.trajectory && 'traj'].filter(Boolean).join('+') || 'no approvals',
    { state: approval.commands || approval.remoteRecall || approval.trajectory ? 'warn' : 'ok', title: 'CORTEX_APPROVE_* grants for processes this app launches', onClick: () => navigate('settings') },
  );

  render(bar,
    h('div', { class: 'col', style: { gap: '1px', minWidth: '0' } },
      h('div', { class: 'topbar-title', id: 'topbar-title', text: route?.label ?? 'Cortex Deck' }),
      h('div', { class: 'topbar-hint truncate', id: 'topbar-hint', text: compactHome(state.workspace) }),
    ),
    h('span', { class: 'grow' }),
    workspaceChip,
    binaryChip,
    approvalChip,
    h('button', { class: 'search-trigger', onclick: () => openPalette() }, icon('search', 13), h('span', { text: 'Search or run a command…' }), h('kbd', { text: '⌘K' })),
    button('', { iconName: 'refresh', size: 'sm', title: 'Refresh this view (⌘R)', onClick: () => refresh() }),
    button('', { iconName: state.settings?.theme === 'light' ? 'sparkles' : 'sparkles', size: 'sm', title: 'Toggle theme', onClick: async () => { const { setSettings } = await import('./lib/state.js'); await setSettings({ theme: state.settings.theme === 'dark' ? 'light' : 'dark' }); applyTheme(); renderTopbar(); } }),
  );
}

/* ── Activity inspector ────────────────────────────────────────────────── */

function toggleActivity(force) {
  const app = document.getElementById('app');
  const inspector = document.getElementById('inspector');
  activityOpen = force ?? !activityOpen;
  app.dataset.inspector = activityOpen ? 'open' : 'closed';
  inspector.hidden = !activityOpen;
  if (activityOpen) paintActivity();
}

function paintActivity() {
  const inspector = document.getElementById('inspector');
  render(inspector,
    h('div', { class: 'inspector-head' },
      icon('history', 15),
      h('div', { class: 'grow' },
        h('div', { class: 'card-title', text: 'Activity' }),
        h('div', { class: 'card-sub', text: `${activity.length} events this session` }),
      ),
      button('Clear', { size: 'sm', onClick: () => { activity.length = 0; paintActivity(); } }),
      button('', { size: 'sm', iconName: 'x', title: 'Close', onClick: () => toggleActivity(false) }),
    ),
    h('div', { class: 'inspector-body' },
      activity.length
        ? activity.slice(0, 120).map((entry) => h('div', { class: 'evidence-item', dataset: { phase: entry.ok === false ? 'abandoned' : entry.ok ? 'complete' : 'investigating' } },
            h('div', { class: 'evidence-claim', text: entry.title }),
            entry.detail ? h('div', { class: 'tiny faint', style: { overflowWrap: 'anywhere' }, text: truncate(entry.detail, 240) }) : null,
            h('div', { class: 'evidence-meta' },
              badge(entry.kind, entry.ok === false ? 'red' : 'slate'),
              entry.durationMs ? h('span', { text: duration(entry.durationMs) }) : null,
              h('span', { class: 'grow' }),
              h('span', { title: new Date(entry.at).toISOString(), text: relativeTime(entry.at) }),
            ),
          ))
        : h('div', { class: 'empty' }, h('div', { class: 'empty-title', text: 'Nothing yet' }), h('div', { class: 'empty-note', text: 'Every cortex run, task run, and MCP call lands here.' })),
    ),
  );
  const counter = document.getElementById('activity-count');
  if (counter) counter.textContent = String(activity.length);
}

function recordActivity(entry) {
  activity.unshift({ at: Date.now(), ...entry });
  if (activity.length > 300) activity.length = 300;
  pushEvent(entry);
  if (activityOpen) paintActivity();
  const counter = document.getElementById('activity-count');
  if (counter) counter.textContent = String(activity.length);
}

/* ── Events & keys ─────────────────────────────────────────────────────── */

function wireEvents() {
  api.onEvent((event) => {
    busEmit(event?.type ?? 'unknown', event);
    if (event?.type === 'cli-start') recordActivity({ kind: 'cli', title: `cortex ${event.commandId}`, detail: (event.argv ?? []).join(' '), ok: null });
    else if (event?.type === 'cli-end') recordActivity({ kind: event.ok ? 'cli' : 'refused', title: `cortex ${event.commandId}`, detail: event.error ?? '', ok: event.ok, durationMs: event.durationMs });
    else if (event?.type === 'run-start') recordActivity({ kind: 'task', title: event.run?.label ?? 'task', ok: null });
    else if (event?.type === 'run-end') recordActivity({ kind: 'task', title: event.run?.label ?? 'task', detail: `exit ${event.run?.exitCode ?? '—'}`, ok: event.run?.state === 'passed' });
    else if (event?.type === 'mcp' && event.event?.type === 'lifecycle') recordActivity({ kind: 'mcp', title: `mcp ${event.event.state ?? ''}`, detail: event.event.error ?? '', ok: event.event.state === 'ready' });
  });

  api.onNavigate((viewId) => navigate(String(viewId)));
  api.onPalette(() => openPalette());
  api.onRefresh(() => refresh());

  busOn('run-output', () => { /* dev view consumes this directly */ });

  subscribe((_state, reason) => {
    if (reason === 'settings' || reason === 'environment') {
      renderTopbar();
      renderRail();
    }
  });
}

function wireKeys() {
  document.addEventListener('keydown', (event) => {
    const meta = event.metaKey || event.ctrlKey;
    if (!meta) return;
    const key = event.key.toLowerCase();
    if (key === 'k') { event.preventDefault(); openPalette(); return; }
    if (key === 'r' && !event.shiftKey) { event.preventDefault(); refresh(); return; }
    if (key === 'j') { event.preventDefault(); toggleActivity(); return; }
    if (key === ',') { event.preventDefault(); navigate('settings'); return; }
    if (/^[1-9]$/.test(key)) {
      const order = NAV_GROUPS.flatMap((group) => group.ids);
      const target = order[Number(key) - 1];
      if (target) { event.preventDefault(); navigate(target); }
    }
  });
}

main().catch((err) => {
  hideBoot();
  console.error(err);
  toast('error', 'Startup failed', String(err?.message ?? err), 12_000);
});
