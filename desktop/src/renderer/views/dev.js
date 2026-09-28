/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { busOn } from '../lib/bus.js';
import { navigate } from '../lib/router.js';
import { state } from '../lib/state.js';
import { alert, badge, button, card, copyButton, loading, pageHead, toast } from '../lib/ui.js';
import { absoluteTime, truncate } from '../lib/format.js';

const MAX_RENDERED_LINES = 1200;

export const devRoute = {
  id: 'dev',
  label: 'Dev tasks',
  icon: 'wrench',
  group: 'System',
  hint: 'run the project’s own Taskfile with live output',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    render(root, loading('reading the runner…'));

    const runs = await api.runs.list().catch(() => []);
    const tasks = state.devTasks ?? [];
    let activeRunId = params?.runId ?? runs.at(-1)?.id ?? null;
    const lines = [];
    const output = h('div', { class: 'terminal', style: { maxHeight: '46vh' } });
    const runsHost = h('div', { class: 'col', style: { gap: '6px' } });
    const headerHost = h('div', { class: 'run-head' });

    const off = busOn('run-output', (event) => {
      if (event.runId !== activeRunId) return;
      lines.push(event);
      if (lines.length > MAX_RENDERED_LINES) lines.splice(0, lines.length - MAX_RENDERED_LINES);
      appendLine(event);
    });
    const offStart = busOn('run-start', (event) => {
      activeRunId = event.run.id;
      lines.length = 0;
      render(output);
      paintHeader();
      refreshRuns();
      appendLine({ stream: 'stdout', text: `$ ${event.run.cmd} ${event.run.args.join(' ')}`, at: Date.now() });
    });
    const offEnd = busOn('run-end', (event) => {
      appendLine({ stream: event.run.exitCode === 0 ? 'stdout' : 'stderr', text: `\n── ${event.run.label} ${event.run.state} (exit ${event.run.exitCode ?? '—'}) ──`, at: Date.now() });
      paintHeader();
      refreshRuns();
      toast(event.run.exitCode === 0 ? 'ok' : 'error', event.run.label, `${event.run.state} · exit ${event.run.exitCode ?? '—'}`, 4200);
    });

    function appendLine(event) {
      const node = h('div', { class: event.stream === 'stderr' ? 't-err' : undefined, text: event.text });
      output.appendChild(node);
      while (output.childNodes.length > MAX_RENDERED_LINES) output.removeChild(output.firstChild);
      output.scrollTop = output.scrollHeight;
    }

    function paintHeader() {
      const run = runs.find((r) => r.id === activeRunId);
      render(headerHost,
        run ? badge(run.state, run.state === 'passed' ? 'green' : run.state === 'failed' ? 'red' : run.state === 'running' ? 'info' : 'slate') : null,
        h('span', { class: 'run-label', text: run ? `${run.cmd} ${run.args.join(' ')}` : 'no run selected' }),
        run ? h('span', { class: 'mono-sm faint', text: `started ${absoluteTime(run.startedAt)}` }) : null,
        h('span', { class: 'grow' }),
        copyButton(() => lines.map((l) => l.text).join('\n'), 'Copy output'),
        run?.state === 'running'
          ? button('Stop', { size: 'sm', kind: 'danger', iconName: 'stop', onClick: async () => { await api.runs.kill(run.id); refreshRuns(); } })
          : null,
      );
    }

    async function refreshRuns() {
      const list = await api.runs.list().catch(() => []);
      runs.length = 0;
      runs.push(...list);
      paintHeader();
      render(runsHost, list.length
        ? list.slice().reverse().map((run) =>
            h('button', {
              class: 'action-item',
              dataset: { active: String(run.id === activeRunId) },
              onclick: async () => {
                activeRunId = run.id;
                lines.length = 0;
                render(output);
                const detail = await api.runs.output(run.id, MAX_RENDERED_LINES).catch(() => null);
                for (const line of detail?.lines ?? []) appendLine(line);
                paintHeader();
              },
            },
              h('span', { class: 'action-tool', text: run.state === 'passed' ? '✓' : run.state === 'failed' ? '✕' : run.state === 'running' ? '▶' : '·' }),
              h('span', { class: 'action-reason', text: run.label }),
              h('span', { class: 'action-cmd', text: `${run.lineCount} lines · ${absoluteTime(run.startedAt)}` }),
            ))
        : h('div', { class: 'tiny faint', text: 'No runs yet this session. Runs stay in memory only — nothing is written to the repository.' }));
    }

    const startTask = async (task) => {
      const result = await api.runs.start(task.id).catch((err) => ({ error: String(err?.message ?? err) }));
      if (result?.error) { toast('error', 'Cannot start task', result.error); return; }
      activeRunId = result.run.id;
      lines.length = 0;
      render(output);
      appendLine({ stream: 'stdout', text: `$ ${task.cmd} ${task.args.join(' ')}   (cwd: ${state.workspace})`, at: Date.now() });
      refreshRuns();
    };

    render(root,
      pageHead({
        title: 'Developer tasks',
        sub: 'The project’s own Taskfile, run with live streaming output. Only these curated commands can run — the deck never executes a free-form shell string. Nothing here writes into the repository except the tasks that say so.',
        actions: [
          button('Taskfile', { iconName: 'wrench', onClick: () => navigate('repo', { tab: 'taskfile' }) }),
          button('Clear finished', { iconName: 'trash', onClick: async () => { await api.runs.clear(); refreshRuns(); } }),
        ],
      }),
      alert({
        tone: 'info',
        title: 'Before committing',
        body: 'task check (fmt + lint + test) → task build → task flows if specs changed → task docsbuild when documentation changed. task ship runs the whole set.',
      }),
      card({ title: 'Tasks', sub: `${tasks.length} curated commands`, iconName: 'wrench', flush: true },
        h('div', { class: 'card-body task-grid' }, tasks.map((task) =>
          h('div', { class: 'task-card' },
            h('div', { class: 'task-card-head' },
              h('span', { class: 'task-name', text: task.label }),
              h('span', { class: 'grow' }),
              task.kind === 'server' ? badge('long-running', 'amber') : task.kind === 'mutate' ? badge('writes files', 'amber') : null,
              h('button', { class: 'btn btn-sm btn-primary', onclick: () => startTask(task) }, icon('play', 11), 'Run'),
            ),
            h('div', { class: 'task-summary', text: truncate(task.summary ?? '', 160) }),
          ),
        ))),
      card({ title: 'Output', iconName: 'terminal', flush: true },
        h('div', { class: 'card-body', style: { paddingBottom: '8px' } }, headerHost),
        h('div', { class: 'card-body', style: { paddingTop: '0' } }, output),
      ),
      card({ title: 'Run history', sub: 'this app session only', iconName: 'history' }, runsHost),
    );

    await refreshRuns();
    if (params?.task) {
      const task = tasks.find((t) => t.id === params.task || t.label.endsWith(params.task));
      if (task) await startTask(task);
    } else if (activeRunId) {
      const detail = await api.runs.output(activeRunId, MAX_RENDERED_LINES).catch(() => null);
      for (const line of detail?.lines ?? []) appendLine(line);
      paintHeader();
    }

    const observer = new MutationObserver(() => {
      if (!root.isConnected) { off(); offStart(); offEnd(); observer.disconnect(); }
    });
    observer.observe(document.getElementById('view'), { childList: true });

    return root;
  },
};
