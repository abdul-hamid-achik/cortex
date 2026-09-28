/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { debounce, h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { commandForm, followAction, resultView } from '../lib/forms.js';
import { state } from '../lib/state.js';
import { alert, badge, button, card, empty, loading, pageHead, toast } from '../lib/ui.js';
import { absoluteTime, duration, relevance, truncate } from '../lib/format.js';

const HISTORY_KEY = 'cortex-deck:console-history';

function loadHistory() {
  try { return JSON.parse(localStorage.getItem(HISTORY_KEY) ?? '[]'); } catch { return []; }
}

function saveHistory(list) {
  try { localStorage.setItem(HISTORY_KEY, JSON.stringify(list.slice(0, 40))); } catch { /* best effort */ }
}

export const consoleRoute = {
  id: 'console',
  label: 'Console',
  icon: 'terminal',
  group: 'Operate',
  hint: 'Every cortex command, form-driven, with its receipt',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    const commands = [...state.commands.values()];
    let query = '';
    let selectedId = params?.commandId ?? 'status';
    if (!state.commands.has(selectedId)) selectedId = 'open';

    const listHost = h('div', { class: 'cmd-list' });
    const formHost = h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } });
    const history = loadHistory();

    function paintList() {
      const scored = commands
        .map((command) => ({ command, score: query ? Math.max(relevance(query, command.title), relevance(query, command.id), relevance(query, command.summary), relevance(query, `cortex ${command.path.join(' ')}`)) : 0 }))
        .filter((entry) => !query || entry.score >= 0)
        .sort((a, b) => b.score - a.score);

      if (!scored.length) { render(listHost, empty({ iconName: 'search', title: 'No command matches', note: query })); return; }

      const nodes = [];
      let lastGroup = null;
      for (const { command } of scored) {
        const group = state.groups.find((g) => g.id === command.group);
        if (!query && group && group.id !== lastGroup) {
          lastGroup = group.id;
          nodes.push(h('div', { class: 'palette-group', text: group.label }));
        }
        nodes.push(h('button', {
          class: 'cmd-item',
          dataset: { active: String(command.id === selectedId) },
          onclick: () => { selectedId = command.id; paintList(); paintForm(); },
        },
          h('div', { class: 'row', style: { gap: '6px' } },
            h('span', { class: 'cmd-item-title grow', text: command.title }),
            command.kind === 'destructive' ? badge('destructive', 'red') : command.kind === 'read' ? null : badge(command.kind, 'slate'),
          ),
          h('div', { class: 'cmd-item-usage', text: command.usage }),
        ));
      }
      render(listHost, nodes);
    }

    function paintForm() {
      const command = state.commands.get(selectedId);
      if (!command) { render(formHost, empty({ title: 'Pick a command' })); return; }
      const preset = {};
      if ((command.args ?? []).some((a) => a.type === 'task') && state.selectedTask?.taskId) preset.taskId = state.selectedTask.taskId;

      const values = { ...preset };
      const resultHost = h('div', { class: 'col', style: { gap: '12px' } });
      const form = commandForm(command, { values, onChange: (next) => Object.assign(values, next) });
      const runButton = button(command.kind === 'server' ? 'Open in MCP view' : `Run cortex ${command.path.join(' ')}`, {
        kind: 'primary',
        iconName: command.kind === 'server' ? 'plug' : 'play',
      });

      runButton.onclick = async () => {
        if (command.kind === 'server') {
          const { navigate } = await import('../lib/router.js');
          navigate('mcp');
          return;
        }
        runButton.disabled = true;
        render(resultHost, loading(`running cortex ${command.path.join(' ')}…`));
        try {
          const result = await api.run(command.id, values, { stream: true });
          render(resultHost, resultView(result, { onAction: (action) => followAction(action) }));
          history.unshift({ commandId: command.id, values: { ...values }, at: Date.now(), ok: result.ok, durationMs: result.durationMs });
          saveHistory(history);
          paintHistory();
          if (result.ok) toast('ok', command.title, truncate(result.envelope?.summary ?? 'done', 200), 3600);
          else toast('error', `${command.title} refused`, truncate(result.error ?? '', 320), 7200);
        } catch (err) {
          render(resultHost, alert({ tone: 'error', title: 'Run failed', body: String(err?.message ?? err) }));
        } finally {
          runButton.disabled = false;
        }
      };

      render(formHost,
        card({
          title: command.title,
          sub: command.usage,
          iconName: command.kind === 'read' ? 'eye' : command.kind === 'destructive' ? 'trash' : 'bolt',
          actions: [
            command.docs ? button(command.docs, { size: 'sm', iconName: 'book', onClick: () => openDocs(command.docs) }) : null,
            runButton,
          ],
        },
          form.node,
          command.kind === 'destructive' || command.confirm
            ? alert({ tone: 'warn', title: 'This command can destroy data', body: command.confirm ?? 'It runs a dry run unless you pass the force flag.' })
            : null,
        ),
        resultHost,
      );
    }

    const historyHost = h('div', { class: 'col', style: { gap: '6px' } });
    function paintHistory() {
      const items = loadHistory().slice(0, 12);
      if (!items.length) { render(historyHost, h('div', { class: 'tiny faint', text: 'Runs you make here are remembered on this machine only.' })); return; }
      render(historyHost, items.map((entry) => {
        const command = state.commands.get(entry.commandId);
        return h('button', {
          class: 'action-item',
          title: 'Reopen with the same values',
          onclick: () => { selectedId = entry.commandId; paintList(); paintForm(); },
        },
          h('span', { class: 'action-tool', text: entry.ok ? '✓' : '✕' }),
          h('span', { class: 'action-reason', text: command?.title ?? entry.commandId }),
          h('span', { class: 'action-cmd', text: `${absoluteTime(entry.at)} · ${duration(entry.durationMs ?? 0)}` }),
        );
      }));
    }

    const search = h('input', {
      class: 'input',
      placeholder: 'filter commands…  (⌘K opens the palette anywhere)',
      oninput: debounce((event) => { query = event.target.value; paintList(); }, 130),
    });

    paintList();
    paintForm();
    paintHistory();

    render(root,
      pageHead({
        title: 'Console',
        sub: `Every one of the ${commands.length} cortex commands as a form: the argv preview is live, writes are confirmed, and the receipt shows facts, next actions, and raw output.`,
        actions: [
          badge(`${commands.length} commands`, 'accent'),
          badge(`${state.groups.length} groups`, 'info'),
          button('Features', { size: 'sm', iconName: 'sparkles', onClick: async () => (await import('../lib/router.js')).navigate('features') }),
        ],
      }),
      h('div', { class: 'console-grid' },
        card({ title: 'Commands', iconName: 'list', flush: true },
          h('div', { class: 'card-body', style: { paddingBottom: '10px' } }, search),
          listHost,
        ),
        h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } }, formHost, card({ title: 'Recent runs', iconName: 'history' }, historyHost)),
      ),
    );
    if (params?.focus !== false) search.focus();
    return root;
  },
};

async function openDocs(docPath) {
  const { navigate } = await import('../lib/router.js');
  navigate('repo', { tab: 'docs', path: docPath });
}
