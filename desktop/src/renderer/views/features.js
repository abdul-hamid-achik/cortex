/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { openCommandRunner } from '../lib/forms.js';
import { navigate } from '../lib/router.js';
import { state } from '../lib/state.js';
import { badge, button, card, empty, loading, pageHead, table } from '../lib/ui.js';
import { matches, truncate } from '../lib/format.js';

// Where each feature area surfaces in the deck. Kept explicit so the catalog is
// an honest map, not a guess: every row names a real view and tab.
const INTEGRATION = [
  { area: 'Task loop (open → remember)', where: 'Case ▸ Loop / Actions · Workspace ▸ The loop · Console', ids: ['open', 'start', 'investigate', 'plan', 'begin-change', 'verify', 'remember', 'resolve', 'abort', 'status', 'show'] },
  { area: 'Change ownership (leases)', where: 'Case ▸ Loop (lease panel) · Case ▸ Actions', ids: ['begin-change', 'lease.renew', 'lease.release'] },
  { area: 'Session observability', where: 'Dashboard · Sessions · Case ▸ Overview / Timeline / Metrics / Resume', ids: ['list', 'sessions', 'overview', 'timeline', 'metrics', 'resume'] },
  { area: 'Session lifecycle admin', where: 'Sessions (row actions) · Case ▸ Actions', ids: ['archive', 'unarchive', 'prune', 'rm'] },
  { area: 'Human/agent collaboration', where: 'Case ▸ Decisions / Notes / Handoff', ids: ['note', 'decision.request', 'decision.answer', 'decision.resume', 'handoff'] },
  { area: 'Long-running work', where: 'Long-running · Case ▸ Survey & jobs / Findings', ids: ['coverage', 'finding.add', 'finding.list', 'finding.triage', 'finding.dismiss', 'finding.convert', 'dossier.list', 'dossier.add', 'dossier.refresh', 'workplan.list', 'workplan.add', 'workplan.next', 'job.list', 'job.cancel'] },
  { area: 'Evidence & cross-case memory', where: 'Evidence · Case ▸ Evidence / Files', ids: ['read-evidence', 'read-artifact', 'recall-cases', 'reindex-cases'] },
  { area: 'Diff-scoped review', where: 'Review', ids: ['review'] },
  { area: 'Environment & configuration', where: 'Environment ▸ Doctor / Readiness / Configuration / Routing / Migration', ids: ['doctor', 'setup', 'config', 'init', 'migrate', 'route', 'completion'] },
  { area: 'MCP server (agent surface)', where: 'MCP server (start, list tools, call them, JSON-RPC traffic)', ids: ['serve'] },
  { area: 'Case-file inspection', where: 'Case ▸ Case files — ledgers, snapshots, summary.md, raw/', ids: [] },
  { area: 'Repository artifacts', where: 'Repository ▸ Docs / Specs / Contracts / Evaluations / Codebase / Taskfile', ids: [] },
  { area: 'Developer tasks', where: 'Dev tasks — curated Taskfile commands with live output', ids: [] },
  { area: 'Launcher approvals', where: 'Settings ▸ Trusted-launcher approvals (commands, remote recall, trajectory)', ids: [] },
];

export const featuresRoute = {
  id: 'features',
  label: 'Features',
  icon: 'sparkles',
  group: 'Reference',
  hint: 'every cortex feature and where the deck exposes it',
  async render() {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('building the catalog…'));

    const catalog = await api.registry.catalog().catch(() => []);
    const commands = [...state.commands.values()];
    const mcp = await api.mcp.status().catch(() => null);
    const flagCount = commands.reduce((sum, command) => sum + (command.flags?.length ?? 0), 0);
    const caseFiles = 15;

    let query = '';
    const groupsHost = h('div');

    const paint = () => {
      const groups = (catalog.length ? catalog : state.groups.map((g) => ({ ...g, features: [] })))
        .map((group) => ({
          ...group,
          features: (group.features ?? commands.filter((c) => c.group === group.id).map((c) => ({ ...c })))
            .filter((feature) => matches(query, feature.title, feature.id, feature.summary, feature.usage)),
        }))
        .filter((group) => group.features.length);

      render(groupsHost, groups.length
        ? groups.map((group) => h('div', { class: 'feature-group' },
            h('div', { class: 'feature-group-head' },
              h('span', { class: 'feature-group-title', text: group.label }),
              badge(`${group.features.length}`, 'accent'),
              group.hint ? h('span', { class: 'feature-group-hint', text: group.hint }) : null,
            ),
            h('div', { class: 'feature-grid' }, group.features.map((feature) =>
              h('button', {
                class: 'feature-card',
                title: feature.usage,
                onclick: () => (feature.id === 'serve' ? navigate('mcp') : openCommandRunner(feature.id)),
              },
                h('div', { class: 'feature-title' },
                  icon(kindIcon(feature.kind), 13),
                  h('span', { text: feature.title }),
                ),
                h('div', { class: 'feature-summary', text: truncate(feature.summary ?? '', 220) }),
                h('div', { class: 'feature-foot' },
                  badge(`cortex ${feature.id.replace(/\./g, ' ')}`, 'slate'),
                  feature.kind === 'read' ? badge('read-only', 'green') : feature.kind === 'destructive' ? badge('destructive', 'red') : feature.kind === 'dryrun' ? badge('dry run by default', 'amber') : feature.kind === 'server' ? badge('server', 'violet') : badge('writes case state', 'info'),
                  feature.json ? badge('--json', 'slate') : null,
                  feature.flags ? badge(`${feature.flags} flags`, 'slate') : null,
                ),
              ))),
          ))
        : empty({ iconName: 'search', title: 'No feature matches', note: query }));
    };

    render(root,
      pageHead({
        title: 'Feature catalog',
        sub: 'Every Cortex feature the deck integrates, grouped the way the kernel groups them. Click any card to open its form — the argv preview is live, writes are confirmed, and the receipt shows facts, next actions, and raw output.',
        actions: [
          button('Console', { iconName: 'terminal', onClick: () => navigate('console') }),
          button('Docs', { iconName: 'book', onClick: () => navigate('repo', { tab: 'docs', path: 'docs/cli.md' }) }),
        ],
      }),
      h('div', { class: 'grid grid-4' },
        tile('CLI features', commands.length, `${state.groups.length} groups · ${flagCount} flags`),
        tile('MCP tools', mcp?.toolCount ?? '—', mcp?.state === 'ready' ? `${mcp.profile} profile · live` : 'start the server to enumerate'),
        tile('Developer tasks', (state.devTasks ?? []).length, 'curated Taskfile commands'),
        tile('Case files', caseFiles, 'ledgers, snapshots, summary.md, raw/'),
      ),
      card({ title: 'Where each feature lives', sub: 'an honest map from Cortex capability to deck surface', iconName: 'grid', flush: true },
        table({
          rows: INTEGRATION,
          rowKey: (row) => row.area,
          columns: [
            { key: 'area', label: 'Feature area', strong: true, width: '30%', render: (row) => row.area },
            { key: 'where', label: 'Where in the deck', render: (row) => h('span', { class: 'tiny', text: row.where }) },
            { key: 'count', label: 'Commands', width: '92px', align: 'right', render: (row) => (row.ids.length ? badge(String(row.ids.length), 'accent') : h('span', { class: 'faint', text: 'built-in' })) },
            {
              key: 'open',
              label: '',
              width: '1%',
              render: (row) => (row.ids.length
                ? h('button', { class: 'btn btn-sm', onclick: () => openCommandRunner(row.ids[0]) }, icon('play', 11), 'First')
                : null),
            },
          ],
        })),
      card({ title: 'Filter the catalog', iconName: 'search' },
        h('input', { class: 'input', placeholder: 'search features… (title, id, summary, usage)', oninput: (event) => { query = event.target.value; paint(); } }),
      ),
      groupsHost,
      card({ title: 'Not integrated on purpose', sub: 'what the deck deliberately does not do', iconName: 'shield' },
        h('ul', { class: 'md' },
          h('li', {}, 'No free-form shell: only registered cortex commands and the curated Taskfile list can execute.'),
          h('li', {}, 'No writes outside the workspace and the case store — file reads are path-confined and bounded.'),
          h('li', {}, 'No fabrication: a missing specialist tool renders as unavailable, exactly as the adapters report it.'),
          h('li', {}, 'Approvals (CORTEX_APPROVE_COMMANDS / _REMOTE_RECALL / _TRAJECTORY) stay off until you toggle them in Settings.'),
          h('li', {}, 'job run — the detached worker entry point — is a kernel internal, not an operator command.'),
        )),
    );

    paint();
    return root;
  },
};

function tile(label, value, foot) {
  return h('div', { class: 'card stat', dataset: { tone: 'accent' } },
    h('div', { class: 'stat-label', text: label }),
    h('div', { class: 'stat-value', text: String(value) }),
    h('div', { class: 'stat-foot', text: foot }),
  );
}

function kindIcon(kind) {
  if (kind === 'read') return 'eye';
  if (kind === 'destructive') return 'trash';
  if (kind === 'server') return 'plug';
  if (kind === 'dryrun') return 'alert';
  return 'bolt';
}
