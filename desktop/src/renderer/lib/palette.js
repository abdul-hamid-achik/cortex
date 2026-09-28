/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from './api.js';
import { debounce, h, render } from './dom.js';
import { icon } from './icons.js';
import { openCommandRunner } from './forms.js';
import { navigate, routeList } from './router.js';
import { state } from './state.js';
import { badge } from './ui.js';
import { relevance, truncate } from './format.js';

let open = false;

/** ⌘K: one box for views, commands, sessions, and deck actions. */
export async function openPalette(preloadQuery = '') {
  if (open) return;
  open = true;
  const root = document.getElementById('palette-root');
  root.hidden = false;

  const sessions = await recentSessions();
  const items = buildItems(sessions);

  let query = preloadQuery;
  let activeIndex = 0;
  let visible = items;

  const listHost = h('div', { class: 'palette-list' });
  const input = h('input', {
    class: 'palette-input',
    placeholder: 'Search commands, views, sessions… (enter to run, ⇧enter to open the full form)',
    value: query,
    oninput: debounce((event) => { query = event.target.value; activeIndex = 0; paint(); }, 90),
    onkeydown: (event) => {
      if (event.key === 'ArrowDown') { event.preventDefault(); activeIndex = Math.min(activeIndex + 1, visible.length - 1); paint(); scrollActive(); }
      else if (event.key === 'ArrowUp') { event.preventDefault(); activeIndex = Math.max(activeIndex - 1, 0); paint(); scrollActive(); }
      else if (event.key === 'Enter') { event.preventDefault(); run(visible[activeIndex], event.shiftKey); }
      else if (event.key === 'Escape') { event.preventDefault(); close(); }
    },
  });

  const panel = h('div', { class: 'palette', role: 'dialog', 'aria-label': 'Command palette' },
    h('div', { class: 'palette-input-row' }, icon('search', 16), input, h('kbd', { text: 'esc' })),
    listHost,
    h('div', { class: 'palette-foot' },
      h('span', {}, h('kbd', { text: '↑↓' }), ' navigate'),
      h('span', {}, h('kbd', { text: '↵' }), ' run'),
      h('span', {}, h('kbd', { text: '⇧↵' }), ' full form'),
      h('span', { class: 'grow' }),
      h('span', { id: 'palette-count' }),
    ),
  );

  function scrollActive() {
    const node = listHost.querySelector('.palette-item[data-active="true"]');
    node?.scrollIntoView({ block: 'nearest' });
  }

  function paint() {
    const q = query.trim();
    visible = q
      ? items
          .map((item) => ({ item, score: Math.max(relevance(q, item.title), relevance(q, item.subtitle ?? ''), relevance(q, item.keywords ?? ''), q.split(/\s+/).every((term) => `${item.title} ${item.subtitle ?? ''} ${item.keywords ?? ''}`.toLowerCase().includes(term)) ? 12 : -1) }))
          .filter((entry) => entry.score >= 0)
          .sort((a, b) => b.score - a.score)
          .slice(0, 60)
          .map((entry) => entry.item)
      : items.slice(0, 60);

    activeIndex = Math.min(activeIndex, Math.max(0, visible.length - 1));
    const nodes = [];
    let lastGroup = null;
    visible.forEach((item, index) => {
      if (item.group !== lastGroup) {
        lastGroup = item.group;
        nodes.push(h('div', { class: 'palette-group', text: item.group }));
      }
      nodes.push(h('button', {
        class: 'palette-item',
        dataset: { active: String(index === activeIndex) },
        onmousemove: () => { activeIndex = index; paintActiveOnly(); },
        onclick: (event) => run(item, event.shiftKey),
      },
        icon(item.icon ?? 'bolt', 14),
        h('div', { class: 'grow', style: { minWidth: '0' } },
          h('div', { class: 'palette-item-title', text: item.title }),
          item.subtitle ? h('div', { class: 'palette-item-sub', text: truncate(item.subtitle, 120) }) : null,
        ),
        item.kind ? h('span', { class: 'palette-item-kind' }, badge(item.kind, item.kind === 'read' ? 'green' : item.kind === 'destructive' ? 'red' : 'slate')) : null,
      ));
    });
    render(listHost, nodes.length ? nodes : h('div', { class: 'empty' }, h('div', { class: 'empty-title', text: 'No matches' })));
    const counter = panel.querySelector('#palette-count');
    if (counter) counter.textContent = `${visible.length} of ${items.length}`;
  }

  function paintActiveOnly() {
    const nodes = [...listHost.querySelectorAll('.palette-item')];
    nodes.forEach((node, index) => { node.dataset.active = String(index === activeIndex); });
  }

  function run(item, shift) {
    if (!item) return;
    close();
    item.action?.(shift);
  }

  function close() {
    open = false;
    root.hidden = true;
    render(root);
    document.removeEventListener('keydown', onDocKey, true);
  }

  const onDocKey = (event) => { if (event.key === 'Escape') close(); };
  document.addEventListener('keydown', onDocKey, true);
  root.onclick = (event) => { if (event.target === root) close(); };

  render(root, panel);
  paint();
  input.focus();
  input.select();
  return close;
}

function buildItems(sessions) {
  const items = [];

  for (const route of routeList()) {
    items.push({
      group: 'Views',
      title: route.label,
      subtitle: route.hint ?? '',
      icon: route.icon ?? 'grid',
      keywords: `view ${route.id} ${route.label}`,
      action: () => navigate(route.id),
    });
  }

  for (const command of [...state.commands.values()]) {
    items.push({
      group: 'Cortex commands',
      title: command.title,
      subtitle: command.usage,
      icon: command.kind === 'read' ? 'eye' : command.kind === 'destructive' ? 'trash' : 'bolt',
      kind: command.kind === 'read' ? 'read' : command.kind === 'destructive' ? 'destructive' : command.kind,
      keywords: `cortex ${command.path.join(' ')} ${command.id} ${(command.aliases ?? []).join(' ')} ${command.summary ?? ''}`,
      action: (shift) => (shift ? navigate('console', { commandId: command.id }) : openCommandRunner(command.id)),
    });
  }

  for (const session of sessions) {
    items.push({
      group: 'Sessions',
      title: truncate(session.goal || session.taskId, 80),
      subtitle: `${session.taskId} · ${session.phase}${session.repo ? ` · ${session.repo}` : ''}`,
      icon: 'case',
      kind: session.phase,
      keywords: `task session ${session.taskId} ${session.goal ?? ''} ${session.repo ?? ''} ${session.phase ?? ''}`,
      action: () => navigate('case', { taskId: session.taskId, repo: session.repo, workspace: session.workspace }),
    });
  }

  for (const task of state.devTasks ?? []) {
    items.push({
      group: 'Developer tasks',
      title: task.label,
      subtitle: task.summary ?? '',
      icon: 'wrench',
      keywords: `task ${task.id} ${task.label} ${task.cmd} ${task.args.join(' ')}`,
      action: () => navigate('dev', { task: task.id }),
    });
  }

  items.push(
    { group: 'Deck', title: 'Switch workspace…', subtitle: 'choose the repository cortex runs against', icon: 'folder', keywords: 'workspace repo switch', action: async () => { const result = await api.workspace.choose(); if (!result?.canceled) navigate('workspace', {}, { force: true }); } },
    { group: 'Deck', title: 'Toggle theme', subtitle: 'dark ⇄ light', icon: 'sparkles', keywords: 'theme dark light appearance', action: async () => { const { setSettings, applyTheme } = await import('./state.js'); await setSettings({ theme: state.settings.theme === 'dark' ? 'light' : 'dark' }); applyTheme(); } },
    { group: 'Deck', title: 'Toggle density', subtitle: 'comfortable ⇄ compact', icon: 'sliders', keywords: 'density compact comfortable', action: async () => { const { setSettings, applyTheme } = await import('./state.js'); await setSettings({ density: state.settings.density === 'compact' ? 'comfortable' : 'compact' }); applyTheme(); } },
    { group: 'Deck', title: 'Start MCP server', subtitle: 'cortex serve --profile all', icon: 'plug', keywords: 'mcp serve tools', action: () => navigate('mcp') },
    { group: 'Deck', title: 'Re-probe environment', subtitle: 'binary, config, store roots', icon: 'refresh', keywords: 'probe doctor config refresh', action: async () => { const { refreshEnvironment } = await import('./state.js'); await refreshEnvironment(); } },
    { group: 'Deck', title: 'Feature catalog', subtitle: 'every feature and where it lives', icon: 'sparkles', keywords: 'features catalog list', action: () => navigate('features') },
  );

  return items;
}

async function recentSessions() {
  if (state.selectedTask?.taskId) {
    return [{ taskId: state.selectedTask.taskId, goal: state.selectedTask.goal, phase: state.selectedTask.phase, repo: state.selectedTask.repo, workspace: state.selectedTask.workspace }];
  }
  try {
    const result = await api.run('sessions', { active: true }, { stream: false, timeoutMs: 30_000 });
    const rows = Array.isArray(result?.json?.sessions) ? result.json.sessions : Array.isArray(result?.json?.items) ? result.json.items : [];
    return rows.slice(0, 25).map((row) => ({
      taskId: String(row.taskId ?? row.id ?? ''),
      goal: row.goal ?? row.title ?? '',
      phase: row.phase ?? row.state ?? '',
      repo: row.repo ?? row.repository ?? '',
      workspace: row.workspace ?? row.workspacePath ?? '',
    })).filter((row) => row.taskId);
  } catch {
    return [];
  }
}
