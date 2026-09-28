/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { jsonView } from '../lib/jsonview.js';
import { markdown } from '../lib/markdown.js';
import { navigate } from '../lib/router.js';
import { state } from '../lib/state.js';
import { alert, badge, button, card, copyButton, empty, loading, pageHead, table, tabs } from '../lib/ui.js';
import { basename, bytes, compactHome, matches, truncate } from '../lib/format.js';

const TABS = [
  { id: 'docs', label: 'Docs', icon: 'book' },
  { id: 'specs', label: 'Specs', icon: 'terminal' },
  { id: 'contracts', label: 'Contracts', icon: 'shield' },
  { id: 'evaluations', label: 'Evaluations', icon: 'pulse' },
  { id: 'code', label: 'Codebase', icon: 'code' },
  { id: 'taskfile', label: 'Taskfile', icon: 'wrench' },
];

const ROOT_DOCS = ['README.md', 'AGENTS.md', 'CHANGELOG.md'];

export const repoRoute = {
  id: 'repo',
  label: 'Repository',
  icon: 'book',
  group: 'System',
  hint: 'docs, specs, contracts, evaluations, code shape, tasks',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    let active = params?.tab ?? 'docs';
    const tabHost = h('div');
    const body = h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } });

    const paintTabs = () => render(tabHost, tabs(TABS, active, (id) => { active = id; paintTabs(); paint(id); }));
    const paint = async (id = active) => {
      render(body, loading(`reading the repository…`));
      try {
        render(body, await RENDERERS[id](params));
      } catch (err) {
        render(body, alert({ tone: 'error', title: 'Failed to read the repository', body: String(err?.message ?? err) }));
      }
    };

    paintTabs();
    render(root,
      pageHead({
        title: basename(state.workspace) || 'Repository',
        sub: `${compactHome(state.workspace)} — the project’s own artifacts, read directly from disk: product docs, glyphrun specs, the public conformance corpus, evaluation manifests, code shape, and the Taskfile.`,
        actions: [
          button('Reveal', { iconName: 'folder', onClick: () => api.shell.reveal(state.workspace) }),
          button('Dev tasks', { iconName: 'wrench', onClick: () => navigate('dev') }),
        ],
      }),
      tabHost,
      body,
    );
    await paint(active);
    return root;
  },
};

/* ── shared file browser ───────────────────────────────────────────────── */

function browser({ files, initialPath, title, sub, renderFile, searchPlaceholder = 'filter…' }) {
  const list = h('div', { class: 'file-list' });
  const viewer = h('div', { class: 'col', style: { gap: '12px', minWidth: '0' } });
  let query = '';
  let selected = initialPath ?? files[0]?.path ?? '';

  const paintList = () => {
    const visible = files.filter((file) => matches(query, file.path, file.name));
    render(list, visible.length
      ? visible.map((file) =>
          h('button', {
            class: 'file-item',
            dataset: { active: String(file.path === selected) },
            onclick: () => { selected = file.path; paintList(); open(file); },
          },
            icon(file.path.endsWith('.md') ? 'note' : file.path.endsWith('.json') ? 'ledger' : 'file', 12),
            h('span', { class: 'grow', text: file.path }),
            h('span', { class: 'file-size', text: bytes(file.size ?? 0) }),
          ))
      : empty({ iconName: 'search', title: 'No matching files' }));
  };

  const open = async (file) => {
    render(viewer, loading(`reading ${file.path}…`));
    const node = await renderFile(file);
    render(viewer, node);
  };

  const first = files.find((file) => file.path === selected) ?? files[0];
  paintList();
  if (first) open(first);

  return h('div', { class: 'browser-grid' },
    card({
      title,
      sub: `${files.length} files${sub ? ` · ${sub}` : ''}`,
      iconName: 'folder',
      flush: true,
    },
      h('div', { class: 'card-body', style: { paddingBottom: '8px' } },
        h('input', { class: 'input', placeholder: searchPlaceholder, oninput: (event) => { query = event.target.value; paintList(); } }),
      ),
      list,
    ),
    viewer,
  );
}

const RENDERERS = {
  async docs(params) {
    const [docs, roots] = await Promise.all([
      api.repo.docs().catch(() => []),
      Promise.all(ROOT_DOCS.map(async (name) => {
        const result = await api.repo.read(name).catch(() => null);
        return result?.text ? { path: name, name, size: result.size } : null;
      })),
    ]);
    const files = [...roots.filter(Boolean), ...docs];
    return browser({
      files,
      initialPath: params?.path ?? 'README.md',
      title: 'Documentation',
      sub: 'product docs live in docs/ and deploy to Vercel from main',
      renderFile: async (file) => {
        const result = await api.repo.read(file.path);
        if (result?.error) return alert({ tone: 'error', title: 'Cannot read file', body: result.error });
        return card({
          title: file.path,
          sub: `${bytes(result.size)}${result.truncated ? ' · truncated' : ''}`,
          iconName: 'book',
          actions: [
            copyButton(() => result.text, 'Copy markdown'),
            button('Reveal', { size: 'sm', iconName: 'folder', onClick: () => api.shell.reveal(result.absPath) }),
          ],
        }, markdown(result.text));
      },
    });
  },

  async specs() {
    const files = await api.repo.specs().catch(() => []);
    if (!files.length) return empty({ iconName: 'terminal', title: 'No glyphrun specs', note: 'specs/*.yml are the E2E contract; task flows runs them locally (not in CI).' });
    return browser({
      files,
      title: 'glyphrun specs',
      sub: 'the E2E contract — task flows runs them',
      renderFile: async (file) => {
        const result = await api.repo.read(file.path);
        return card({
          title: file.path,
          sub: `${bytes(result.size ?? 0)}`,
          iconName: 'terminal',
          actions: [
            copyButton(() => result.text ?? '', 'Copy spec'),
            button('Run flows', { size: 'sm', iconName: 'play', onClick: () => navigate('dev', { task: 'flows' }) }),
          ],
        }, h('pre', { class: 'code', text: result.text ?? '' }));
      },
    });
  },

  async contracts() {
    const data = await api.repo.contracts().catch(() => null);
    const manifest = data?.manifest?.json ?? safeJson(data?.manifest?.text);
    const groups = manifest?.groups ?? {};
    const files = (data?.fixtures ?? []).map((file) => ({ ...file, group: groupOf(groups, file.name) }));

    return h('div', { class: 'col', style: { gap: '14px' } },
      card({ title: 'Public conformance corpus', sub: manifest?.classification ?? 'contracts/v1', iconName: 'shield' },
        h('div', { class: 'col', style: { gap: '10px' } },
          alert({ tone: 'info', title: 'Sensitive data policy', body: manifest?.sensitiveDataPolicy ?? 'synthetic data only' }),
          Object.entries(groups).length
            ? h('div', { class: 'grid grid-2' }, Object.entries(groups).map(([group, ids]) =>
                card({ title: group, sub: `${(ids ?? []).length} fixtures`, iconName: 'ledger' },
                  h('div', { class: 'pill-row' }, (ids ?? []).map((id) => h('span', { class: 'tag', text: id }))),
                )))
            : null,
        )),
      browser({
        files,
        title: 'Fixtures',
        sub: 'generated from kernel, handoff, and MCP test paths',
        renderFile: async (file) => {
          const result = await api.repo.read(file.path);
          const json = result?.json ?? safeJson(result?.text);
          return card({
            title: file.path,
            sub: json?.generatedBy ? `generated by ${json.generatedBy}` : '',
            iconName: 'ledger',
            actions: [copyButton(() => JSON.stringify(json ?? result?.text ?? '', null, 2), 'Copy fixture')],
            flush: true,
          }, h('div', { class: 'card-body' }, json
            ? h('div', { class: 'col', style: { gap: '10px' } },
                h('div', { class: 'row-wrap' },
                  json.classification ? badge(json.classification, 'violet') : null,
                  json.contractVersion ? badge(`contract v${json.contractVersion}`, 'accent') : null,
                  json.sizeBehavior ? h('span', { class: 'tiny faint', text: truncate(String(json.sizeBehavior), 140) }) : null,
                ),
                jsonView(json.payload ?? json, { label: 'payload', depth: 3 }),
              )
            : h('pre', { class: 'code', text: result?.text ?? '' })));
        },
      }),
    );
  },

  async evaluations() {
    const files = await api.repo.evaluations().catch(() => []);
    if (!files.length) return empty({ iconName: 'pulse', title: 'No evaluation manifests', note: 'evaluations/ holds trusted empirical manifests, repository fixtures, and independent oracles.' });
    return browser({
      files,
      title: 'Evaluations',
      sub: 'trusted empirical manifests and fixtures',
      renderFile: async (file) => {
        const result = await api.repo.read(file.path);
        const json = result?.json ?? safeJson(result?.text);
        return card({
          title: file.path,
          sub: bytes(result?.size ?? 0),
          iconName: 'pulse',
          actions: [
            copyButton(() => result?.text ?? '', 'Copy'),
            file.path.endsWith('.yml') || file.path.endsWith('.yaml')
              ? button('Validate manifest…', { size: 'sm', iconName: 'shield', onClick: () => navigate('dev') })
              : null,
          ],
          flush: true,
        }, h('div', { class: 'card-body' }, json ? jsonView(json, { label: file.path, depth: 3 }) : h('pre', { class: 'code', text: result?.text ?? '' })));
      },
    });
  },

  async code() {
    const [stats, facts] = await Promise.all([api.repo.stats().catch(() => []), api.repo.facts().catch(() => ({}))]);
    const totalLines = stats.reduce((sum, row) => sum + (row.lines ?? 0), 0);
    const totalGo = stats.reduce((sum, row) => sum + (row.goFiles ?? 0), 0);
    const totalTest = stats.reduce((sum, row) => sum + (row.testFiles ?? 0), 0);
    return h('div', { class: 'col', style: { gap: '14px' } },
      h('div', { class: 'grid grid-4' },
        statTile('Packages', stats.length, 'with Go source'),
        statTile('Go files', totalGo, `${totalTest} test files alongside`),
        statTile('Lines', totalLines.toLocaleString(), 'source + tests'),
        statTile('Module', facts.module ? basename(facts.module) : '—', facts.goVersion ? `go ${facts.goVersion}` : ''),
      ),
      card({ title: 'Packages by size', sub: 'cmd → kernel → {adapters, store, config, domain, ids}; domain depends on nothing internal', iconName: 'code', flush: true },
        table({
          rows: stats,
          maxHeight: 460,
          rowKey: (row) => row.package,
          columns: [
            { key: 'package', label: 'Package', strong: true, mono: true, render: (row) => row.package },
            { key: 'goFiles', label: 'Files', width: '80px', align: 'right', render: (row) => String(row.goFiles ?? 0) },
            { key: 'testFiles', label: 'Tests', width: '80px', align: 'right', render: (row) => String(row.testFiles ?? 0) },
            { key: 'lines', label: 'Lines', width: '100px', align: 'right', render: (row) => (row.lines ?? 0).toLocaleString() },
            {
              key: 'share',
              label: 'Share',
              width: '180px',
              render: (row) => h('div', { class: 'bar', dataset: { tone: row.testFiles ? 'green' : 'info' } },
                h('span', { style: { width: `${totalLines ? ((row.lines ?? 0) / totalLines) * 100 : 0}%` } })),
            },
          ],
        })),
      facts.toolVersions
        ? card({ title: '.tool-versions', sub: 'asdf pins used by CI and the docs tasks', iconName: 'wrench' }, h('pre', { class: 'code', text: facts.toolVersions }))
        : null,
    );
  },

  async taskfile() {
    const facts = await api.repo.facts().catch(() => ({}));
    const tasks = facts.tasks ?? [];
    const text = await api.repo.read('Taskfile.yml').catch(() => null);
    return h('div', { class: 'col', style: { gap: '14px' } },
      card({
        title: 'Taskfile',
        sub: `${tasks.length} tasks · version 3`,
        iconName: 'wrench',
        actions: [button('Open runner', { size: 'sm', kind: 'primary', iconName: 'play', onClick: () => navigate('dev') })],
        flush: true,
      },
        tasks.length
          ? table({
              rows: tasks,
              maxHeight: 420,
              rowKey: (row) => row.name,
              columns: [
                { key: 'name', label: 'Task', strong: true, mono: true, width: '180px', render: (row) => `task ${row.name}` },
                { key: 'desc', label: 'Description', render: (row) => h('span', { class: 'tiny', text: row.desc || '—' }) },
                { key: 'cmds', label: 'Steps', width: '76px', align: 'right', render: (row) => String(row.cmds ?? 0) },
                { key: 'run', label: '', width: '1%', render: (row) => h('button', { class: 'btn btn-sm', onclick: () => navigate('dev', { task: row.name }) }, icon('play', 11), 'Run') },
              ],
            })
          : empty({ title: 'No tasks parsed' }),
      ),
      text?.text ? card({ title: 'Taskfile.yml', iconName: 'file', flush: true }, h('div', { class: 'card-body' }, h('pre', { class: 'code', text: text.text }))) : null,
    );
  },
};

function statTile(label, value, foot) {
  return h('div', { class: 'card stat' },
    h('div', { class: 'stat-label', text: label }),
    h('div', { class: 'stat-value', style: { fontSize: 'var(--fs-22)' }, text: String(value) }),
    foot ? h('div', { class: 'stat-foot', text: foot }) : null,
  );
}

function safeJson(text) {
  try { return JSON.parse(text); } catch { return null; }
}

function groupOf(groups, name) {
  const key = basename(name).replace(/\.json$/, '');
  for (const [group, ids] of Object.entries(groups ?? {})) if ((ids ?? []).includes(key)) return group;
  return '';
}
