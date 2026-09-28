/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from './api.js';
import { clear, debounce, h, render } from './dom.js';
import { icon } from './icons.js';
import { confirmDialog, modal } from './modal.js';
import { state } from './state.js';
import { alert, badge, button, copyButton, empty, loading, toast } from './ui.js';
import { jsonView } from './jsonview.js';
import { duration, truncate } from './format.js';

/* ── Field rendering ───────────────────────────────────────────────────── */

function tokenField(spec, values, onChange) {
  const key = spec.name.replace(/^-+/, '');
  const list = Array.isArray(values[key]) ? [...values[key]] : [];
  const input = h('input', {
    type: 'text',
    placeholder: spec.placeholder ?? 'add and press enter',
    onkeydown: (event) => {
      if (event.key === 'Enter' || event.key === ',' || event.key === 'Tab') {
        const value = input.value.trim().replace(/,$/, '');
        if (!value) return;
        event.preventDefault();
        list.push(value);
        input.value = '';
        onChange(key, [...list]);
        repaint();
      } else if (event.key === 'Backspace' && !input.value && list.length) {
        list.pop();
        onChange(key, [...list]);
        repaint();
      }
    },
  });

  const chips = h('div', { class: 'row-wrap', style: { gap: '5px' } });
  const wrap = h('div', { class: 'tokens' }, chips, input);

  function repaint() {
    render(chips, list.map((value, index) =>
      h('span', { class: 'token' },
        h('span', { text: value, title: value }),
        h('button', {
          title: 'Remove',
          onclick: () => { list.splice(index, 1); onChange(key, [...list]); repaint(); },
        }, '×'),
      ),
    ));
  }
  repaint();
  return wrap;
}

function labeled(spec, control, extra) {
  const required = spec.required ? h('span', { class: 'req', text: '*' }) : null;
  return h('div', { class: ['field', spec.type === 'textarea' || spec.repeatable ? 'span-2' : ''].filter(Boolean).join(' ') },
    h('label', { class: 'field-label' },
      h('span', { text: spec.label ?? spec.name }),
      required,
      spec.repeatable ? h('span', { class: 'tag', text: 'repeatable' }) : null,
      extra ?? null,
    ),
    control,
    spec.help ? h('div', { class: 'field-help', text: spec.help }) : null,
  );
}

function field(spec, values, onChange) {
  const key = spec.name.replace(/^-+/, '');
  const value = values[key];

  if (spec.repeatable && spec.type !== 'boolean') return labeled(spec, tokenField(spec, values, onChange));

  if (spec.type === 'boolean') {
    const id = `f-${key}-${Math.random().toString(36).slice(2, 7)}`;
    const input = h('input', {
      type: 'checkbox',
      id,
      checked: Boolean(value),
      onchange: (event) => onChange(key, event.target.checked),
    });
    return h('div', { class: 'field' },
      h('label', { class: 'switch', for: id },
        input,
        h('span', { class: 'switch-track' }),
        h('span', { class: 'switch-text' }, spec.label ?? spec.name),
      ),
      spec.help ? h('div', { class: 'field-help', text: spec.help }) : null,
    );
  }

  if (spec.type === 'enum') {
    const select = h('select', {
      class: 'select',
      onchange: (event) => onChange(key, event.target.value),
    },
      h('option', { value: '', text: spec.required ? '— required —' : '— none —', selected: !value }),
      (spec.options ?? []).map((option) => h('option', { value: option, text: option, selected: option === value })),
    );
    return labeled(spec, select);
  }

  if (spec.type === 'textarea') {
    const area = h('textarea', {
      class: 'textarea',
      placeholder: spec.placeholder ?? '',
      rows: 3,
      oninput: (event) => onChange(key, event.target.value),
    });
    area.value = value ?? '';
    return labeled(spec, area);
  }

  const isPath = spec.type === 'path';
  const input = h('input', {
    class: ['input', spec.type === 'text' || isPath || spec.type === 'task' ? 'mono' : ''].filter(Boolean).join(' '),
    type: spec.type === 'int' || spec.type === 'number' ? 'number' : 'text',
    placeholder: spec.placeholder ?? (spec.default !== undefined ? `default: ${spec.default}` : ''),
    step: spec.type === 'number' ? '0.1' : undefined,
    oninput: (event) => onChange(key, event.target.value),
  });
  input.value = value ?? '';

  const suffix = isPath
    ? button('', {
        iconName: 'folder',
        size: 'sm',
        title: 'Choose a file in the workspace',
        onClick: async () => {
          const files = await api.repo.tree('', 3).catch(() => []);
          pickFile(files, (chosen) => { input.value = chosen; onChange(key, chosen); });
        },
      })
    : null;

  return labeled(spec,
    suffix ? h('div', { class: 'row', style: { gap: '6px' } }, input, suffix) : input,
    spec.type === 'duration' ? h('span', { class: 'tag', text: 'go duration' }) : null,
  );
}

/** Small workspace-file picker used by path flags (specs, contracts, seeds). */
export function pickFile(files, onPick) {
  let query = '';
  const list = h('div', { class: 'file-list' });
  const search = h('input', {
    class: 'input',
    placeholder: 'filter workspace files…',
    oninput: (event) => { query = event.target.value.toLowerCase(); paint(); },
  });
  function paint() {
    const matches = (files ?? [])
      .filter((f) => !f.isDir && f.path.toLowerCase().includes(query))
      .slice(0, 400);
    render(list, matches.map((f) =>
      h('button', {
        class: 'file-item',
        onclick: () => { onPick(f.path); dialog.close(); },
      }, icon('file', 12), h('span', { class: 'grow', text: f.path }), h('span', { class: 'file-size', text: `${Math.round((f.size ?? 0) / 1024)}k` })),
    ));
    if (!matches.length) render(list, empty({ title: 'No matching files' }));
  }
  const dialog = modal({
    title: 'Choose a file',
    sub: state.workspace,
    width: 620,
    body: h('div', { class: 'col', style: { gap: '10px' } }, search, list),
  });
  paint();
  search.focus();
}

/* ── Command form ──────────────────────────────────────────────────────── */

/**
 * Builds a form for any registry command. `values` is mutated in place and the
 * caller re-reads it, so the argv preview can stay in sync.
 */
export function commandForm(command, options = {}) {
  const values = { ...(options.values ?? {}) };
  const listeners = new Set(options.onValues ?? []);
  const preview = h('div', { class: 'argv-preview' });

  const notify = () => {
    refreshPreview();
    options.onChange?.(values);
  };
  const onChange = (key, value) => {
    if (value === '' || value === undefined || value === null || (Array.isArray(value) && !value.length)) delete values[key];
    else values[key] = value;
    notify();
  };

  const refreshPreview = debounce(async () => {
    const scope = options.workspace || state.workspace;
    try {
      const result = await api.registry.preview({ commandId: command.id, values, workspace: scope });
      render(preview, h('span', { text: result.argv }), copyButton(result.argv, 'Command copied'));
    } catch (err) {
      render(preview, h('span', { text: `cortex ${command.path.join(' ')} …` }), h('span', { class: 'faint', text: String(err?.message ?? err) }));
    }
  }, 130);

  const fields = [
    ...(command.args ?? []).map((spec) => field(spec, values, onChange)),
    ...(command.flags ?? []).map((spec) => field(spec, values, onChange)),
  ];

  refreshPreview();

  const node = h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } },
    command.summary ? h('p', { class: 'muted', style: { fontSize: 'var(--fs-12)', lineHeight: '1.6' }, text: command.summary }) : null,
    h('div', { class: 'col', style: { gap: '4px' } },
      h('div', { class: 'micro', text: 'argv preview' }),
      preview,
    ),
    fields.length ? h('div', { class: 'form-grid' }, fields) : empty({ iconName: 'play', title: 'No arguments', note: 'This command takes no input — just run it.' }),
    command.examples?.length
      ? h('div', { class: 'col', style: { gap: '5px' } },
          h('div', { class: 'micro', text: 'examples' }),
          ...command.examples.map((example) => h('div', { class: 'code', style: { maxHeight: 'none', padding: '7px 10px' }, text: example })),
        )
      : null,
  );

  return { node, values, getValues: () => ({ ...values }), refreshPreview };
}

/* ── Result rendering ──────────────────────────────────────────────────── */

export function factRow(fact) {
  return h('div', { class: 'evidence-item', dataset: { phase: 'verifying' } },
    h('div', { class: 'evidence-claim', text: fact.claim ?? fact.statement ?? JSON.stringify(fact) }),
    h('div', { class: 'evidence-meta' },
      fact.id ? h('span', { class: 'mono-sm', text: fact.id }) : null,
      fact.kind ? badge(fact.kind, 'slate') : null,
      fact.confidence ? badge(fact.confidence, fact.confidence === 'high' ? 'green' : fact.confidence === 'medium' ? 'info' : 'slate') : null,
      fact.source ? h('span', { text: `source: ${fact.source}` }) : null,
      fact.uri ? h('span', { class: 'mono-sm', text: truncate(fact.uri, 90) }) : null,
    ),
  );
}

export function factsList(facts) {
  if (!facts?.length) return empty({ iconName: 'evidence', title: 'No facts recorded', note: 'Search output is a candidate, not proof — weak rounds record zero facts on purpose.' });
  return h('div', { class: 'col', style: { gap: '7px' } }, facts.map(factRow));
}

/**
 * Structured actions are the kernel's machine-readable "what to do next".
 * Every action opens the matching registry command with its arguments prefilled.
 */
export function actionsList(actions, { onAction } = {}) {
  if (!actions?.length) return empty({ iconName: 'arrowRight', title: 'No structured actions', note: 'Write commands return the next safe continuation here.' });
  return h('div', { class: 'action-list' },
    actions.map((action) =>
      h('button', { class: 'action-item', onclick: () => onAction?.(action) },
        h('span', { class: 'action-tool', text: action.tool ?? 'action' }),
        h('span', { class: 'action-reason', text: action.reason ?? action.summary ?? '' }),
        h('span', { class: 'action-cmd', text: truncate(action.command ?? '', 110) }),
      ),
    ),
  );
}

export function resultView(result, opts = {}) {
  if (!result) return empty({ iconName: 'terminal', title: 'Nothing run yet', note: 'Pick a command and run it — the receipt lands here.' });
  const envelope = result.envelope;
  const tone = result.ok ? 'ok' : 'error';

  const head = h('div', { class: 'result-head' },
    badge(result.ok ? 'ok' : (result.stage ?? 'failed'), tone),
    h('span', { class: 'card-title', text: result.title ?? result.commandId ?? '' }),
    h('span', { class: 'faint mono-sm', text: `${duration(result.durationMs ?? 0)} · exit ${result.exitCode ?? '—'}` }),
    h('span', { class: 'grow' }),
    copyButton(result.commandLine ?? '', 'Copy command'),
  );

  const summaryBlock = result.error && !result.ok
    ? alert({ tone: 'error', title: envelope?.summary || 'Refused or failed', body: result.error })
    : envelope?.summary
      ? alert({ tone: result.ok ? 'ok' : 'warn', title: result.ok ? 'Kernel summary' : 'Kernel refused', body: envelope.summary })
      : null;

  const body = h('div', { class: 'col', style: { gap: '12px' } });
  let tabsNode = null;
  const available = [
    envelope?.facts?.length ? { id: 'facts', label: 'Facts', count: envelope.facts.length } : null,
    envelope?.actions?.length ? { id: 'actions', label: 'Next actions', count: envelope.actions.length } : null,
    envelope?.nextActions?.length ? { id: 'next', label: 'Guidance', count: envelope.nextActions.length } : null,
    result.json ? { id: 'json', label: 'JSON' } : null,
    result.stdout || result.stderr ? { id: 'raw', label: 'Raw output' } : null,
  ].filter(Boolean);

  const showTab = (id) => {
    if (id === 'facts') render(body, factsList(envelope.facts));
    else if (id === 'actions') render(body, actionsList(envelope.actions, { onAction: opts.onAction }));
    else if (id === 'next') render(body, h('ul', { class: 'md' }, envelope.nextActions.map((line) => h('li', { text: line }))));
    else if (id === 'json') render(body, opts.renderJson ? opts.renderJson(result.json) : jsonView(result.json, { label: `${result.commandId} result` }));
    else if (id === 'raw') {
      render(body,
        h('div', { class: 'col', style: { gap: '8px' } },
          h('div', { class: 'micro', text: 'argv' }),
          h('div', { class: 'code', style: { maxHeight: 'none' }, text: `cortex ${(result.argv ?? []).join(' ')}` }),
          result.stdout ? h('div', { class: 'micro', text: 'stdout' }) : null,
          result.stdout ? h('pre', { class: 'terminal', text: truncate(result.stdout, 200_000) }) : null,
          result.stderr ? h('div', { class: 'micro', text: 'stderr' }) : null,
          result.stderr ? h('pre', { class: 'terminal', text: truncate(result.stderr, 60_000) }) : null,
          result.parseError ? alert({ tone: 'warn', title: 'stdout was not valid JSON', body: result.parseError }) : null,
        ),
      );
    }
    for (const node of tabsNode?.querySelectorAll('.tab') ?? []) node.dataset.active = String(node.dataset.id === id);
  };

  const defaultTab = !result.ok && result.error ? null : available[0]?.id ?? null;
  if (available.length) {
    tabsNode = h('div', { class: 'tabs' }, available.map((tab) =>
      h('button', { class: 'tab', dataset: { id: tab.id, active: 'false' }, onclick: () => showTab(tab.id) },
        h('span', { text: tab.label }),
        tab.count ? h('span', { class: 'tab-count', text: String(tab.count) }) : null,
      ),
    ));
    if (defaultTab) showTab(defaultTab);
    else render(body, empty({ title: 'Empty result' }));
  }

  return h('div', { class: 'col', style: { gap: '12px', minWidth: '0' } },
    head,
    summaryBlock,
    tabsNode,
    body,
  );
}

/* ── Universal command runner ──────────────────────────────────────────── */

/**
 * Opens any registry command as a modal: form → confirm (for writes) → run →
 * receipt. This is what makes every feature reachable even where a bespoke
 * view does not exist, and it is what inline action buttons call.
 */
export async function openCommandRunner(commandId, options = {}) {
  const command = state.commands.get(commandId);
  if (!command) {
    toast('error', 'Unknown command', commandId);
    return null;
  }

  const workspace = options.workspace || state.workspace;
  const values = { ...(options.values ?? {}) };
  if (options.taskId && !values.taskId) values.taskId = options.taskId;
  if (!values.taskId && state.selectedTask?.taskId && needsTask(command)) values.taskId = state.selectedTask.taskId;

  const form = commandForm(command, { values, workspace, onChange: (next) => Object.assign(values, next) });
  const resultHost = h('div', { class: 'col', style: { gap: '10px' } });
  const runButton = button(options.label ?? `Run ${command.path.join(' ')}`, {
    kind: command.kind === 'destructive' ? 'danger' : 'primary',
    iconName: 'play',
    disabled: command.kind === 'server',
  });

  const dialog = modal({
    title: options.title ?? command.title,
    sub: command.usage,
    iconName: command.kind === 'read' ? 'eye' : command.kind === 'destructive' ? 'trash' : 'bolt',
    width: options.width ?? 900,
    body: h('div', { class: 'col', style: { gap: '16px' } }, form.node, resultHost),
    footer: [
      command.docs ? h('span', { class: 'faint mono-sm grow', text: `docs: ${command.docs}` }) : h('span', { class: 'grow' }),
      button('Close', { onClick: () => dialog.close() }),
      runButton,
    ],
  });

  runButton.onclick = async () => {
    const needsConfirm = command.confirm || command.kind === 'destructive' || (command.kind === 'mutate' && options.confirmWrites === true);
    if (needsConfirm) {
      const preview = await api.registry.preview({ commandId: command.id, values, workspace }).catch(() => ({ argv: '' }));
      const ok = await confirmDialog({
        title: command.confirm ? command.title : `Run ${command.title}?`,
        body: command.confirm ?? 'This writes to the case file. Cortex keeps the evidence either way.',
        details: preview.argv,
        danger: command.kind === 'destructive',
        confirmLabel: command.kind === 'destructive' ? 'Delete permanently' : 'Run it',
      });
      if (!ok) return;
    }

    runButton.disabled = true;
    render(resultHost, loading('Running cortex…'));
    try {
      const result = await api.run(command.id, values, { stream: options.stream !== false, timeoutMs: options.timeoutMs, workspace });
      render(resultHost, resultView(result, { onAction: (action) => followAction(action, dialog) }));
      if (result.ok) toast('ok', command.title, truncate(result.envelope?.summary ?? result.summary ?? 'done', 200), 3800);
      else toast('error', `${command.title} refused`, truncate(result.error ?? '', 300), 7000);
      options.onDone?.(result);
    } catch (err) {
      render(resultHost, alert({ tone: 'error', title: 'Run failed', body: String(err?.message ?? err) }));
    } finally {
      runButton.disabled = false;
    }
  };

  return dialog;
}

/** Does this command take a task id positional? Used to prefill from the selection. */
export function needsTask(command) {
  return (command?.args ?? []).some((arg) => arg.type === 'task');
}

/**
 * Turn a kernel `actions[]` entry into a prefilled runner. Cortex actions carry
 * {tool, command, reason, arguments, inputs}; we map the tool name back to the
 * registry id and pass the arguments through.
 */
export function followAction(action, parentDialog) {
  const tool = String(action?.tool ?? '');
  const commandId = toolToCommandId(tool);
  const args = action?.arguments ?? {};
  if (!commandId) {
    modal({
      title: action?.tool ?? 'Next action',
      sub: action?.reason ?? '',
      body: h('div', { class: 'col', style: { gap: '10px' } },
        action?.command ? h('div', { class: 'argv-preview' }, h('span', { text: action.command }), copyButton(action.command)) : null,
        jsonView(args, { label: 'arguments' }),
      ),
      footer: [button('Close', { kind: 'primary', onClick: () => {} })],
    });
    return;
  }
  const values = {};
  let workspace = '';
  for (const [key, value] of Object.entries(args)) {
    if (value === null || value === undefined) continue;
    if (key === 'workspace') { workspace = String(value); continue; }
    values[normalizeKey(key)] = value;
  }
  parentDialog?.close?.();
  openCommandRunner(commandId, {
    values,
    workspace: workspace || undefined,
    title: `Next: ${state.commands.get(commandId)?.title ?? commandId}`,
  });
}

const TOOL_TO_COMMAND = {
  cortex_open_task: 'open',
  cortex_start_task: 'start',
  cortex_investigate: 'investigate',
  cortex_plan: 'plan',
  cortex_begin_change: 'begin-change',
  cortex_verify: 'verify',
  cortex_remember: 'remember',
  cortex_status: 'status',
  cortex_show: 'show',
  cortex_resolve: 'resolve',
  cortex_note: 'note',
  cortex_request_decision: 'decision.request',
  cortex_answer_decision: 'decision.answer',
  cortex_handoff: 'handoff',
  cortex_abort_task: 'abort',
  cortex_read_evidence: 'read-evidence',
  cortex_read_artifact: 'read-artifact',
  cortex_recall_cases: 'recall-cases',
  cortex_timeline: 'timeline',
  cortex_metrics: 'metrics',
  cortex_sessions: 'sessions',
  cortex_list_tasks: 'list',
  cortex_overview: 'overview',
  cortex_archive: 'archive',
  cortex_unarchive: 'unarchive',
  cortex_resume: 'resume',
  cortex_finding: 'finding.list',
  cortex_dossier: 'dossier.list',
  cortex_coverage: 'coverage',
  cortex_workplan: 'workplan.list',
  cortex_job: 'job.list',
};

export function toolToCommandId(tool) {
  if (TOOL_TO_COMMAND[tool]) return TOOL_TO_COMMAND[tool];
  const guess = String(tool).replace(/^cortex_/, '').replace(/_/g, '-');
  return state.commands.has(guess) ? guess : null;
}

/** camelCase / snake_case → the flag key the registry uses (--claim-id → claim-id). */
export function normalizeKey(key) {
  return String(key)
    .replace(/([a-z0-9])([A-Z])/g, '$1-$2')
    .toLowerCase();
}

/** Run a read command inline and hand the payload to a renderer. */
export async function loadCommand(commandId, values = {}, opts = {}) {
  const result = await api.run(commandId, values, { stream: false, ...opts });
  if (!result.ok && !opts.allowFailure) throw new Error(result.error || `${commandId} failed (exit ${result.exitCode})`);
  return result;
}

export { clear };
