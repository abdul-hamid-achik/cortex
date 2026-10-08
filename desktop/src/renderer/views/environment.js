/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { openCommandRunner } from '../lib/forms.js';
import { jsonView } from '../lib/jsonview.js';
import { navigate } from '../lib/router.js';
import { refreshEnvironment, state } from '../lib/state.js';
import { alert, badge, button, card, empty, kv, loading, pageHead, table, tabs } from '../lib/ui.js';
import { firstArray, pick, readinessList, routeMatrix, toolHealthTable } from '../lib/renderers.js';
import { compactHome, humanDuration, truncate } from '../lib/format.js';

const TABS = [
  { id: 'doctor', label: 'Doctor', icon: 'pulse' },
  { id: 'setup', label: 'Readiness', icon: 'shield' },
  { id: 'config', label: 'Configuration', icon: 'sliders' },
  { id: 'route', label: 'Routing', icon: 'route' },
  { id: 'migrate', label: 'Migration', icon: 'history' },
];

export const environmentRoute = {
  id: 'environment',
  label: 'Environment',
  icon: 'pulse',
  group: 'System',
  hint: 'doctor, setup, config, routing, migration',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    let active = params?.tab ?? 'doctor';
    const tabHost = h('div');
    const body = h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } });

    const paintTabs = () => render(tabHost, tabs(TABS, active, async (id) => { active = id; paintTabs(); await paint(); }));
    const paint = async () => {
      render(body, loading(`running cortex ${active}…`));
      try {
        const node = await RENDERERS[active]();
        render(body, node);
      } catch (err) {
        render(body, alert({ tone: 'error', title: 'Failed', body: String(err?.message ?? err) }));
      }
    };

    paintTabs();
    render(root,
      pageHead({
        title: 'Environment',
        sub: 'Cortex degrades safely: a missing specialist tool blocks verification on that surface instead of fabricating output. Everything here is read-only unless you explicitly ask for a write.',
        actions: [
          state.probe?.ok ? badge(state.probe.version ?? 'cortex', 'accent') : badge('binary missing', 'red'),
          button('Re-probe', { iconName: 'refresh', onClick: async () => { await refreshEnvironment(); navigate('environment', {}, { force: true }); } }),
        ],
      }),
      binaryCard(),
      tabHost,
      body,
    );
    await paint();
    return root;
  },
};

function binaryCard() {
  const probe = state.probe;
  return card({ title: 'Cortex binary', sub: 'resolved by settings → PATH (workspace bin/ only when trusted in Settings)', iconName: 'cpu' },
    kv([
      ['Path', h('span', { class: 'mono-sm', text: probe?.path ?? '—' })],
      ['Found via', probe?.source ?? '—'],
      ['Version', probe?.ok ? badge(probe.version ?? 'ok', 'green') : badge(probe?.error ?? 'not found', 'red')],
      ['Store root', h('span', { class: 'mono-sm', text: compactHome(state.config?.sessionsRoot ?? '—') })],
    ]),
    h('div', { class: 'row', style: { gap: '8px', marginTop: '10px' } },
      button('Change binary…', { size: 'sm', iconName: 'wrench', onClick: () => navigate('settings') }),
      probe?.path ? button('Reveal', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(probe.path) }) : null,
    ),
  );
}

const RENDERERS = {
  async doctor() {
    const result = await api.run('doctor', {}, { stream: false, timeoutMs: 120_000 });
    const json = result?.json ?? {};
    const tools = firstArray(json, 'tools', 'adapters', 'toolHealth', 'health');
    const workspaceInfo = pick(json, 'workspace', 'environment') ?? null;
    const sessions = pick(json, 'sessions', 'sessionSnapshot', 'store') ?? null;

    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Specialist tool health',
        sub: 'every adapter probes concurrently; missing binaries are reported, never invented',
        iconName: 'cpu',
        actions: [
          button('Re-run', { size: 'sm', iconName: 'refresh', onClick: () => navigate('environment', { tab: 'doctor' }, { force: true }) }),
          button('Gateway probe', { size: 'sm', iconName: 'plug', onClick: () => openCommandRunner('doctor', { values: { probe: true } }) }),
        ],
        flush: true,
      }, h('div', { class: 'card-body' }, toolHealthTable(tools))),
      h('div', { class: 'grid grid-2' },
        card({ title: 'Workspace', iconName: 'workspace' }, workspaceInfo && typeof workspaceInfo === 'object'
          ? kv(Object.entries(workspaceInfo).map(([key, value]) => [key, typeof value === 'object' ? JSON.stringify(value) : String(value)]))
          : empty({ title: 'No workspace block' })),
        card({ title: 'Session snapshot', iconName: 'sessions' }, sessions && typeof sessions === 'object'
          ? kv(Object.entries(sessions).map(([key, value]) => [key, typeof value === 'object' ? JSON.stringify(value) : String(value)]))
          : empty({ title: 'No session snapshot' })),
      ),
      card({ title: 'doctor --json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(json, { label: 'doctor', depth: 2 }))),
      result && !result.ok ? alert({ tone: 'warn', title: 'doctor reported problems', body: result.error ?? '' }) : null,
    );
  },

  async setup() {
    const result = await api.run('setup', {}, { stream: false, timeoutMs: 120_000 });
    const json = result?.json ?? {};
    const checks = firstArray(json, 'checks', 'items', 'requirements', 'gaps');
    const rows = checks.length ? checks : Object.entries(json)
      .filter(([, value]) => value && typeof value === 'object')
      .map(([key, value]) => ({ name: key, ...value }));

    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Workspace readiness',
        sub: 'git, cortex.yaml, and whether codemap/vecgrep are installed and indexed — with the exact fix for each gap',
        iconName: 'shield',
        actions: [button('Trust command verifiers…', { size: 'sm', iconName: 'shield', onClick: () => openCommandRunner('setup', { values: { 'trust-commands': true, yes: true } }) })],
      }, readinessList(rows) ?? empty({ title: 'Nothing reported' })),
      card({ title: 'setup --json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(json, { label: 'setup', depth: 2 }))),
    );
  },

  async config() {
    const { config } = await api.config({ force: true });
    const yaml = await api.repo.read('cortex.yaml').catch(() => null);
    const budget = config?.budget ?? {};
    const verifiers = config?.verifiers ?? [];

    return h('div', { class: 'col', style: { gap: '14px' } },
      h('div', { class: 'grid grid-2' },
        card({ title: 'Budget', sub: 'bounded by default — the kernel refuses to fan out forever', iconName: 'sliders' },
          kv(Object.entries(budget).map(([key, value]) => [key.replace(/_/g, ' '), String(value)]))),
        card({ title: 'Command verifiers', sub: 'only exact argv declared in cortex.yaml may run, and only with CORTEX_APPROVE_COMMANDS=1', iconName: 'shield' },
          verifiers.length
            ? table({
                rows: verifiers,
                columns: [
                  { key: 'name', label: 'Name', strong: true, mono: true, render: (v) => String(pick(v, 'name') ?? '') },
                  { key: 'kind', label: 'Kind', width: '110px', render: (v) => badge(String(pick(v, 'kind') ?? ''), 'violet') },
                  { key: 'surface', label: 'Surface', width: '90px', render: (v) => badge(String(pick(v, 'surface') ?? ''), 'info') },
                  { key: 'timeout', label: 'Timeout', width: '90px', align: 'right', render: (v) => h('span', { class: 'mono-sm', text: humanDuration(pick(v, 'timeout') ?? '') }) },
                ],
              })
            : alert({ tone: 'warn', title: 'No verifiers configured', body: 'Run cortex init to detect your test runner and write one.' })),
      ),
      card({ title: 'Resolved paths & recall', sub: (config?.sources ?? []).map(compactHome).join('  →  ') || 'no cortex.yaml applied', iconName: 'database' },
        kv([
          ['Config dir', h('span', { class: 'mono-sm', text: compactHome(config?.configDir ?? '—') })],
          ['Sessions root', h('span', { class: 'mono-sm', text: compactHome(config?.sessionsRoot ?? '—') })],
          ['This workspace', h('span', { class: 'mono-sm', text: compactHome(config?.casesDir ?? '—') })],
          ['Archive', h('span', { class: 'mono-sm', text: compactHome(config?.archiveRoot ?? '—') })],
          ['Cache', h('span', { class: 'mono-sm', text: compactHome(config?.cacheDir ?? '—') })],
          ['Recall db', h('span', { class: 'mono-sm', text: compactHome(config?.recall?.dbPath ?? '—') })],
          ['Recall enabled', config?.recall?.enabled ? badge('enabled', 'green') : badge('disabled', 'slate')],
          ['Embed model', config?.recall?.embedModel ?? '—'],
          ['Embed url', h('span', { class: 'mono-sm', text: config?.recall?.embedUrl ?? '—' })],
          ['Remote approved', config?.recall?.remoteApproved ? badge('approved', 'amber') : badge('loopback only', 'slate')],
          ['Redact literals', String(config?.redactLiteralCount ?? 0)],
        ])),
      card({
        title: 'cortex.yaml',
        sub: 'project configuration in the workspace',
        iconName: 'file',
        actions: [
          button('Init / rewrite…', { size: 'sm', iconName: 'wrench', onClick: () => openCommandRunner('init', { values: { force: true } }) }),
          yaml?.absPath ? button('Reveal', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(yaml.absPath) }) : null,
        ],
      }, yaml?.text ? h('pre', { class: 'code', text: yaml.text }) : alert({ tone: 'warn', title: 'No cortex.yaml', body: 'Run cortex init to create one with a detected test-runner verifier.' })),
      card({ title: 'config --json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(config ?? {}, { label: 'config', depth: 2 }))),
    );
  },

  async route() {
    const matrix = await api.run('route', {}, { stream: false });
    const resolvedHost = h('div');
    const input = h('input', {
      class: 'input',
      placeholder: 'ask a question to see which tools cortex routes it to…',
      onkeydown: async (event) => {
        if (event.key !== 'Enter') return;
        const value = event.target.value.trim();
        if (!value) return;
        render(resolvedHost, loading('resolving…'));
        const result = await api.run('route', { question: value }, { stream: false });
        render(resolvedHost, card({ title: `Routing for “${truncate(value, 80)}”`, iconName: 'route', flush: true },
          h('div', { class: 'card-body' }, result?.json ? jsonView(result.json, { label: 'route', depth: 3 }) : alert({ tone: 'error', body: result?.error ?? 'no result' }))));
      },
    });

    return h('div', { class: 'col', style: { gap: '14px' } },
      card({ title: 'Resolve a question', sub: 'cortex route <question> — press enter', iconName: 'search' }, input, resolvedHost),
      card({ title: 'Routing matrix', sub: 'ordered discovery → structure → verification per surface', iconName: 'route', flush: true },
        h('div', { class: 'card-body' }, matrix?.json ? routeMatrix(matrix.json) : alert({ tone: 'error', body: matrix?.error ?? 'no matrix' }))),
      card({ title: 'route --json', iconName: 'ledger', flush: true }, h('div', { class: 'card-body' }, jsonView(matrix?.json ?? {}, { label: 'route', depth: 2 }))),
    );
  },

  async migrate() {
    const dry = await api.run('migrate', {}, { stream: false });
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Legacy ~/.cortex migration',
        sub: 'dry run by default; all-or-nothing so it can never leave a half-migrated layout',
        iconName: 'history',
        actions: [
          button('Dry run again', { size: 'sm', iconName: 'refresh', onClick: () => navigate('environment', { tab: 'migrate' }, { force: true }) }),
          button('Apply…', { size: 'sm', kind: 'danger', iconName: 'bolt', onClick: () => openCommandRunner('migrate', { values: { apply: true } }) }),
        ],
      },
        dry?.json ? jsonView(dry.json, { label: 'migrate (dry run)', depth: 2 }) : alert({ tone: dry?.ok ? 'ok' : 'warn', title: dry?.ok ? 'Nothing to migrate' : 'migrate failed', body: dry?.error ?? 'no legacy layout detected' })),
    );
  },
};
