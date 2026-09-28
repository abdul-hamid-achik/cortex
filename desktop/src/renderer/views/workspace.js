/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { openCommandRunner } from '../lib/forms.js';
import { navigate } from '../lib/router.js';
import { refreshEnvironment, setSettings, state } from '../lib/state.js';
import { alert, badge, button, card, empty, kv, loading, pageHead, stateBadge, table, toast } from '../lib/ui.js';
import { firstArray, pick, sessionRow } from '../lib/renderers.js';
import { absoluteTime, basename, compactHome, relativeTime, shortId, sortBy, truncate } from '../lib/format.js';

export const workspaceRoute = {
  id: 'workspace',
  label: 'Workspace',
  icon: 'workspace',
  group: 'Operate',
  hint: 'This repository: cases, git state, and readiness',
  async render() {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('reading the workspace…'));

    const [listResult, git, setupResult, repos] = await Promise.all([
      api.run('list', {}, { stream: false }).catch(() => null),
      api.repo.git().catch(() => null),
      api.run('setup', {}, { stream: false, timeoutMs: 90_000 }).catch(() => null),
      api.workspace.repos().catch(() => null),
    ]);

    const rows = firstArray(listResult?.json, 'tasks', 'items', 'sessions').map(sessionRow);
    const sorted = sortBy(rows, (row) => row.updated || '');

    render(root,
      pageHead({
        title: basename(state.workspace) || 'Workspace',
        sub: `${compactHome(state.workspace)} — cases opened here, plus what this repository still needs for full discovery and verification.`,
        actions: [
          button('Switch…', { iconName: 'folder', onClick: () => switchWorkspace(repos) }),
          button('New case', { kind: 'primary', iconName: 'plus', onClick: () => openCommandRunner('open', { onDone: () => navigate('workspace', {}, { force: true }) }) }),
          button('Fresh case', { iconName: 'plus', onClick: () => openCommandRunner('start', { onDone: () => navigate('workspace', {}, { force: true }) }) }),
          button('Refresh', { iconName: 'refresh', onClick: () => navigate('workspace', {}, { force: true }) }),
        ],
      }),
      h('div', { class: 'grid grid-sidebar' },
        h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } },
          card({
            title: `Cases in this workspace`,
            sub: 'cortex list — newest first. Click a row to open the full case view.',
            iconName: 'case',
            flush: true,
          },
            sorted.length
              ? table({
                  rows: sorted,
                  rowKey: (row) => row.taskId,
                  maxHeight: 520,
                  onRowClick: (row) => navigate('case', { taskId: row.taskId, repo: row.repo, workspace: row.workspace || state.workspace }),
                  rowAttrs: (row) => ({ phase: row.phase }),
                  columns: [
                    { key: 'phase', label: 'Phase', width: '126px', render: (row) => stateBadge(row.phase) },
                    {
                      key: 'goal',
                      label: 'Goal',
                      strong: true,
                      render: (row) => h('div', { class: 'col', style: { gap: '2px', minWidth: '0' } },
                        h('div', { class: 'truncate', style: { color: 'var(--text)', maxWidth: '50ch' }, text: truncate(row.goal, 110) || '(no goal)' }),
                        h('div', { class: 'mono-sm faint', text: `${shortId(row.taskId, 18, 4)}${row.mode ? ` · ${row.mode}` : ''}` }),
                      ),
                    },
                    { key: 'assessment', label: 'Assessment', width: '112px', render: (row) => (row.assessment ? stateBadge(row.assessment) : h('span', { class: 'faint', text: '—' })) },
                    { key: 'updated', label: 'Updated', width: '106px', align: 'right', render: (row) => h('span', { class: 'mono-sm faint', title: absoluteTime(row.updated), text: relativeTime(row.updated) || '—' }) },
                    {
                      key: 'actions',
                      label: '',
                      width: '1%',
                      render: (row) => h('div', { class: 'row', style: { gap: '4px' } },
                        iconAction('Status', 'pulse', () => openCommandRunner('status', { values: { taskId: row.taskId, detail: 'full' } })),
                        iconAction('Handoff', 'download', () => openCommandRunner('handoff', { values: { taskId: row.taskId } })),
                        iconAction('Abort', 'stop', () => openCommandRunner('abort', { values: { taskId: row.taskId } })),
                      ),
                    },
                  ],
                })
              : empty({
                  iconName: 'case',
                  title: 'No cases in this workspace yet',
                  note: 'Open one with the button above. Cortex resumes matching active work instead of duplicating it when you use open with an idempotency key.',
                })),
          gitCard(git),
        ),
        h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } },
          readinessCard(setupResult),
          loopShortcuts(),
          recentWorkspacesCard(repos),
        ),
      ),
    );
    return root;
  },
};

function iconAction(title, iconName, onClick) {
  return h('button', {
    class: 'btn btn-ghost btn-sm btn-icon',
    title,
    onclick: (event) => { event.stopPropagation(); onClick(); },
  }, icon(iconName, 12));
}

function gitCard(git) {
  if (!git?.ok) return card({ title: 'Git', iconName: 'git' }, alert({ tone: 'warn', title: 'Not a git repository', body: git?.error ?? 'Git is a hard runtime dependency for identity, diffs, scope drift, and revision-bound verification.' }));
  return card({ title: 'Working tree', sub: `${git.branch} @ ${git.head}`, iconName: 'git', flush: true },
    h('div', { class: 'card-body' },
      kv([
        ['Uncommitted', git.dirty ? badge(`${git.dirty}`, 'amber') : badge('clean', 'green')],
        ['Remotes', git.remotes.length ? h('span', { class: 'mono-sm', text: truncate(git.remotes[0], 50) }) : '—'],
      ]),
    ),
    git.changes?.length
      ? table({
          rows: git.changes.slice(0, 200),
          maxHeight: 240,
          columns: [
            { key: 'code', label: 'St', width: '54px', render: (c) => badge(c.code, c.code.includes('D') ? 'red' : c.code.includes('?') ? 'slate' : 'amber') },
            { key: 'path', label: 'Path', mono: true, render: (c) => truncate(c.path, 90) },
          ],
        })
      : null,
  );
}

function readinessCard(result) {
  const json = result?.json ?? {};
  const checks = firstArray(json, 'checks', 'items', 'requirements', 'gaps');
  const rows = checks.length
    ? checks
    : Object.entries(json)
        .filter(([, value]) => value && typeof value === 'object' && ('ok' in value || 'ready' in value || 'status' in value))
        .map(([key, value]) => ({ name: key, ...value }));

  return card({
    title: 'Readiness',
    sub: 'cortex setup — git, cortex.yaml, and whether discovery tools are indexed',
    iconName: 'shield',
    actions: [
      button('Re-check', { size: 'sm', iconName: 'refresh', onClick: () => navigate('workspace', {}, { force: true }) }),
      button('Init config', { size: 'sm', iconName: 'wrench', onClick: () => openCommandRunner('init') }),
    ],
    flush: true,
  },
    h('div', { class: 'card-body col', style: { gap: '8px' } },
      rows.length
        ? rows.map((row) => {
            const ok = pick(row, 'ok', 'ready', 'satisfied');
            const status = String(pick(row, 'status', 'state') ?? (ok ? 'ok' : 'gap')).toLowerCase();
            const good = ok === true || ['ok', 'ready', 'indexed', 'installed'].includes(status);
            return h('div', { class: 'list-item' },
              h('span', { style: { color: good ? 'var(--green)' : 'var(--amber)' } }, icon(good ? 'check' : 'alert', 14)),
              h('div', { class: 'grow col', style: { gap: '2px', minWidth: '0' } },
                h('div', { style: { fontSize: 'var(--fs-12)', color: 'var(--text)' }, text: truncate(String(pick(row, 'name', 'check', 'label', 'item') ?? status), 60) }),
                pick(row, 'detail', 'message', 'fix', 'fixCommand', 'hint')
                  ? h('div', { class: 'mono-sm faint', text: truncate(String(pick(row, 'detail', 'message', 'fix', 'fixCommand', 'hint')), 160) })
                  : null,
              ),
              stateBadge(status),
            );
          })
        : alert({ tone: result?.ok ? 'ok' : 'warn', title: result?.ok ? 'No gaps reported' : 'setup did not return checks', body: result?.error ?? truncate(JSON.stringify(json), 300) }),
    ),
  );
}

function loopShortcuts() {
  const taskId = state.selectedTask?.taskId ?? '';
  const steps = [
    { id: 'open', label: 'Open / resume', icon: 'plus' },
    { id: 'investigate', label: 'Investigate', icon: 'search' },
    { id: 'plan', label: 'Plan', icon: 'note' },
    { id: 'begin-change', label: 'Begin change', icon: 'bolt' },
    { id: 'verify', label: 'Verify', icon: 'shield' },
    { id: 'remember', label: 'Remember', icon: 'check' },
  ];
  return card({ title: 'The loop', sub: taskId ? `prefilled with ${shortId(taskId, 14, 4)}` : 'select a case to prefill these', iconName: 'pulse' },
    h('div', { class: 'task-grid' }, steps.map((step, index) =>
      h('button', {
        class: 'feature-card',
        onclick: () => openCommandRunner(step.id, { values: taskId ? { taskId } : {}, onDone: () => navigate('workspace', {}, { force: true }) }),
      },
        h('div', { class: 'feature-title' }, icon(step.icon, 13), h('span', { text: `${index + 1}. ${step.label}` })),
        h('div', { class: 'feature-summary', text: truncate(state.commands.get(step.id)?.summary ?? '', 120) }),
      ),
    )),
  );
}

function recentWorkspacesCard(repos) {
  const recents = state.settings?.recentWorkspaces ?? [];
  const known = (repos?.repos ?? []).filter((r) => r.workspacePath).slice(0, 12);
  return card({ title: 'Other repositories', sub: 'from the central session store', iconName: 'database', flush: true },
    h('div', { class: 'card-body col', style: { gap: '10px' } },
      recents.length
        ? h('div', {},
            h('div', { class: 'micro', text: 'recent' }),
            h('div', { class: 'pill-row', style: { marginTop: '6px' } }, recents.map((dir) =>
              h('button', { class: 'btn btn-sm', onclick: () => choose(dir) }, icon('folder', 11), basename(dir)),
            )),
          )
        : null,
      known.length
        ? h('div', {},
            h('div', { class: 'micro', text: 'with recorded sessions' }),
            h('div', { class: 'pill-row', style: { marginTop: '6px' } }, known.map((repo) =>
              h('button', {
                class: 'btn btn-sm',
                title: `${repo.workspacePath} · ${repo.sessions} sessions`,
                onclick: () => choose(repo.workspacePath),
              }, icon('workspace', 11), `${repo.slug} (${repo.sessions})`),
            )),
          )
        : null,
    ),
  );
}

async function choose(dir) {
  if (!dir) return;
  await setSettings({ workspace: dir });
  await refreshEnvironment();
  toast('ok', 'Workspace switched', compactHome(dir), 2600);
  navigate('workspace', {}, { force: true });
}

async function switchWorkspace() {
  const result = await api.workspace.choose();
  if (result?.canceled) return;
  await setSettings({ workspace: result.workspace });
  await refreshEnvironment();
  navigate('workspace', {}, { force: true });
}
