/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from '../lib/api.js';
import { h, render } from '../lib/dom.js';
import { busOn } from '../lib/bus.js';
import { jsonView } from '../lib/jsonview.js';
import { navigate } from '../lib/router.js';
import { state } from '../lib/state.js';
import { alert, badge, button, card, copyButton, empty, kv, loading, pageHead, toast } from '../lib/ui.js';
import { toolToCommandId } from '../lib/forms.js';
import { truncate } from '../lib/format.js';

const MAX_TRAFFIC = 220;

export const mcpRoute = {
  id: 'mcp',
  label: 'MCP server',
  icon: 'plug',
  group: 'System',
  hint: 'The agent surface: run it, list its tools, call them',
  async render({ params }) {
    const root = h('div', { class: 'col', style: { gap: '14px' } });
    let profile = params?.profile ?? 'all';
    let status = await api.mcp.status().catch(() => null);
    let selectedTool = params?.tool ?? null;
    const traffic = [];

    const statusHost = h('div');
    const toolsHost = h('div', { class: 'col', style: { gap: '1px' } });
    const detailHost = h('div', { class: 'col', style: { gap: '12px', minWidth: '0' } });
    const trafficHost = h('div', { class: 'traffic' });

    const off = busOn('mcp', ({ event }) => {
      if (!event) return;
      if (event.type === 'message') {
        traffic.unshift({ dir: event.direction, at: Date.now(), text: summarizeMessage(event.message) });
        if (traffic.length > MAX_TRAFFIC) traffic.length = MAX_TRAFFIC;
        paintTraffic();
      } else if (event.type === 'lifecycle') {
        traffic.unshift({ dir: 'event', at: Date.now(), text: `lifecycle: ${event.state ?? ''}${event.error ? ` — ${event.error}` : ''}` });
        api.mcp.status().then((next) => { status = next; paintStatus(); paintTools(); }).catch(() => {});
        paintTraffic();
      } else if (event.type === 'stderr') {
        traffic.unshift({ dir: 'stderr', at: Date.now(), text: truncate(String(event.text ?? '').trim(), 300) });
        paintTraffic();
      } else if (event.type === 'tools') {
        api.mcp.status().then((next) => { status = next; paintStatus(); paintTools(); }).catch(() => {});
      }
    });

    function paintTraffic() {
      render(trafficHost, traffic.length
        ? traffic.map((line) => h('div', { class: 'traffic-line' },
            h('span', { class: line.dir === 'send' ? 'traffic-dir-send' : line.dir === 'recv' ? 'traffic-dir-recv' : 't-dim', text: line.dir }),
            h('span', { class: 'traffic-body', text: line.text }),
          ))
        : h('div', { class: 't-dim', text: 'no JSON-RPC traffic yet' }));
    }

    function paintStatus() {
      const running = status?.state === 'ready';
      render(statusHost, card({
        title: 'Server',
        sub: running ? `cortex serve --profile ${status.profile} · pid ${status.pid ?? '?'}` : 'not running',
        iconName: 'plug',
        actions: [
          h('div', { class: 'btn-group' }, ['agent', 'all'].map((value) =>
            h('button', {
              class: 'btn btn-sm',
              dataset: { active: String(profile === value) },
              title: value === 'agent' ? 'compact agent profile' : 'adds the cross-repository operator tools',
              onclick: async () => {
                profile = value;
                if (running) { await stop(); await start(); } else paintStatus();
              },
            }, value)),
          ),
          running
            ? button('Stop', { size: 'sm', iconName: 'stop', onClick: () => stop() })
            : button('Start server', { size: 'sm', kind: 'primary', iconName: 'play', onClick: () => start() }),
        ],
      },
        kv([
          ['State', badge(status?.state ?? 'stopped', running ? 'green' : status?.state === 'error' ? 'red' : 'slate')],
          ['Profile', status?.profile ?? profile],
          ['Tools', String(status?.toolCount ?? 0)],
          ['Server info', status?.serverInfo ? `${status.serverInfo.name ?? ''} ${status.serverInfo.version ?? ''}`.trim() || '—' : '—'],
          ['Capabilities', status?.capabilities ? Object.keys(status.capabilities).join(', ') : '—'],
          ['Workspace', truncate(state.workspace, 60)],
          ['Last error', status?.lastError ? h('span', { class: 'danger-text', text: truncate(status.lastError, 160) }) : '—'],
        ]),
        h('div', { class: 'tiny faint', style: { marginTop: '10px' }, text: 'Newline-delimited JSON-RPC over stdio; all server logging goes to stderr so stdout stays pure. The compact agent profile is what mcphub registers by default.' }),
      ));
    }

    function paintTools() {
      const tools = status?.tools ?? [];
      if (!tools.length) {
        render(toolsHost, empty({ iconName: 'plug', title: 'No tools listed', note: 'Start the server to enumerate its tools.' }));
        return;
      }
      render(toolsHost, tools.map((tool) =>
        h('button', {
          class: 'tool-item',
          dataset: { active: String(tool.name === selectedTool) },
          onclick: () => { selectedTool = tool.name; paintTools(); paintTool(tool); },
        },
          h('div', { class: 'tool-name', text: tool.name }),
          h('div', { class: 'tool-desc', text: truncate(tool.description || tool.title || '', 90) }),
        ),
      ));
      if (selectedTool) {
        const tool = tools.find((t) => t.name === selectedTool);
        if (tool) paintTool(tool);
      } else {
        paintTool(tools[0]);
        selectedTool = tools[0].name;
      }
    }

    function paintTool(tool) {
      const schema = tool.inputSchema ?? {};
      const properties = schema.properties ?? {};
      const required = new Set(schema.required ?? []);
      const values = {};
      const argsHost = h('div', { class: 'form-grid' });
      const resultHost = h('div', { class: 'col', style: { gap: '10px' } });

      for (const [name, spec] of Object.entries(properties)) {
        argsHost.appendChild(schemaField(name, spec, required.has(name), values));
      }

      const runButton = button('Call tool', { kind: 'primary', iconName: 'bolt' });
      runButton.onclick = async () => {
        runButton.disabled = true;
        render(resultHost, loading(`calling ${tool.name}…`));
        try {
          const args = {};
          for (const [key, value] of Object.entries(values)) {
            if (value === '' || value === undefined || value === null) continue;
            if (Array.isArray(value) && !value.length) continue;
            args[key] = value;
          }
          const result = await api.mcp.call(tool.name, args, 180_000);
          render(resultHost,
            result.isError ? alert({ tone: 'error', title: 'Tool returned an error', body: truncate(result.text, 600) }) : null,
            result.envelope ? jsonView(result.envelope, { label: `${tool.name} envelope`, depth: 2 }) : null,
            card({ title: 'Raw tool result', sub: 'MCP content blocks', iconName: 'ledger', flush: true, actions: [copyButton(() => result.text, 'Copy result')] },
              h('div', { class: 'card-body' }, h('pre', { class: 'code', text: truncate(result.text, 200_000) || '(empty)' }))),
          );
          toast(result.isError ? 'error' : 'ok', tool.name, truncate(result.text, 160), 3200);
        } catch (err) {
          render(resultHost, alert({ tone: 'error', title: 'Call failed', body: String(err?.message ?? err) }));
        } finally {
          runButton.disabled = false;
        }
      };

      const cliEquivalent = toolToCommandId(tool.name);
      render(detailHost,
        card({
          title: tool.name,
          sub: tool.description || tool.title || '',
          iconName: 'plug',
          actions: [
            cliEquivalent ? button('Open CLI form', { size: 'sm', iconName: 'terminal', title: `cortex ${cliEquivalent}`, onClick: () => navigate('console', { commandId: cliEquivalent }) }) : null,
            runButton,
          ],
        },
          Object.keys(properties).length ? argsHost : empty({ iconName: 'sliders', title: 'This tool takes no arguments' }),
          schema ? h('details', { style: { marginTop: '10px' } },
            h('summary', { class: 'micro', text: 'input schema' }),
            h('div', { style: { marginTop: '8px' } }, jsonView(schema, { label: 'inputSchema', depth: 3, toolbar: false })),
          ) : null,
        ),
        resultHost,
      );
    }

    async function start() {
      render(statusHost, card({ title: 'Server', iconName: 'plug' }, loading(`starting cortex serve --profile ${profile}…`)));
      try {
        status = await api.mcp.start({ profile, workspace: state.workspace });
        toast('ok', 'MCP server started', `${status.toolCount} tools on the ${status.profile} profile`, 3600);
      } catch (err) {
        toast('error', 'MCP server failed to start', String(err?.message ?? err), 7000);
        status = await api.mcp.status().catch(() => null);
      }
      paintStatus();
      paintTools();
    }

    async function stop() {
      try { await api.mcp.stop(); } catch { /* already stopped */ }
      status = await api.mcp.status().catch(() => null);
      paintStatus();
      paintTools();
    }

    paintStatus();
    paintTools();
    paintTraffic();

    render(root,
      pageHead({
        title: 'MCP server',
        sub: 'Cortex speaks the Model Context Protocol over stdio. The compact agent profile is what mcphub registers; --profile all adds the seven cross-repository operator tools. Start it here, inspect every tool’s schema, and call them directly.',
        actions: [
          button('Doctor probe', { iconName: 'pulse', onClick: () => navigate('environment', { tab: 'doctor' }) }),
          button('Registration guide', { iconName: 'book', onClick: () => navigate('repo', { tab: 'docs', path: 'docs/mcp.md' }) }),
        ],
      }),
      statusHost,
      h('div', { class: 'mcp-grid' },
        card({ title: 'Tools', sub: 'click to inspect and call', iconName: 'list', flush: true }, toolsHost),
        h('div', { class: 'col', style: { gap: '14px', minWidth: '0' } }, detailHost),
      ),
      card({ title: 'JSON-RPC traffic', sub: 'stdout is pure protocol; stderr is diagnostics', iconName: 'terminal', flush: true, actions: [button('Clear', { size: 'sm', onClick: () => { traffic.length = 0; paintTraffic(); } })] },
        h('div', { class: 'card-body' }, trafficHost)),
    );

    // Tear the subscription down when the view is replaced.
    const observer = new MutationObserver(() => {
      if (!root.isConnected) { off(); observer.disconnect(); }
    });
    observer.observe(document.getElementById('view'), { childList: true });

    return root;
  },
};

function schemaField(name, spec, required, values) {
  const type = spec.type ?? 'string';
  const label = h('label', { class: 'field-label' }, h('span', { text: name }), required ? h('span', { class: 'req', text: '*' }) : null);
  const help = spec.description ? h('div', { class: 'field-help', text: truncate(spec.description, 240) }) : null;

  if (Array.isArray(spec.enum)) {
    const select = h('select', { class: 'select', onchange: (event) => { values[name] = event.target.value; } },
      h('option', { value: '', text: required ? '— required —' : '— none —' }),
      spec.enum.map((option) => h('option', { value: option, text: String(option) })),
    );
    return h('div', { class: 'field' }, label, select, help);
  }

  if (type === 'boolean') {
    const id = `mcp-${name}-${Math.random().toString(36).slice(2, 7)}`;
    return h('div', { class: 'field' },
      h('label', { class: 'switch', for: id },
        h('input', { type: 'checkbox', id, onchange: (event) => { values[name] = event.target.checked; } }),
        h('span', { class: 'switch-track' }),
        h('span', { class: 'switch-text', text: name }),
      ),
      help,
    );
  }

  if (type === 'array') {
    const list = [];
    values[name] = list;
    const chips = h('div', { class: 'row-wrap', style: { gap: '5px' } });
    const input = h('input', {
      type: 'text',
      placeholder: spec.items?.type === 'object' ? 'JSON item + enter' : 'add and press enter',
      onkeydown: (event) => {
        if (event.key !== 'Enter') return;
        const raw = input.value.trim();
        if (!raw) return;
        event.preventDefault();
        let value = raw;
        if (spec.items?.type === 'object' || spec.items?.type === 'number' || spec.items?.type === 'integer') {
          try { value = JSON.parse(raw); } catch { /* keep the string */ }
        }
        list.push(value);
        input.value = '';
        paintChips();
      },
    });
    const paintChips = () => render(chips, list.map((value, index) =>
      h('span', { class: 'token' },
        h('span', { text: typeof value === 'string' ? value : JSON.stringify(value) }),
        h('button', { onclick: () => { list.splice(index, 1); paintChips(); } }, '×'),
      ),
    ));
    return h('div', { class: 'field span-2' }, label, h('div', { class: 'tokens' }, chips, input), help);
  }

  if (type === 'object') {
    const area = h('textarea', {
      class: 'textarea',
      placeholder: '{ }',
      oninput: (event) => {
        try { values[name] = JSON.parse(event.target.value); } catch { values[name] = undefined; }
      },
    });
    return h('div', { class: 'field span-2' }, label, area, help);
  }

  const isText = type === 'string';
  const long = /summary|outcome|goal|question|observation|detail|reason|hypothes|statement/i.test(name) || /long|multi|paragraph/i.test(spec.description ?? '');
  if (isText && long) {
    const area = h('textarea', { class: 'textarea', placeholder: spec.default ?? '', oninput: (event) => { values[name] = event.target.value; } });
    return h('div', { class: 'field span-2' }, label, area, help);
  }

  const input = h('input', {
    class: 'input mono',
    type: type === 'integer' || type === 'number' ? 'number' : 'text',
    placeholder: spec.default !== undefined ? String(spec.default) : '',
    oninput: (event) => { values[name] = type === 'integer' ? Number(event.target.value) : type === 'number' ? Number(event.target.value) : event.target.value; },
  });
  return h('div', { class: 'field' }, label, input, help);
}

function summarizeMessage(message) {
  if (!message) return '';
  if (message.method) {
    const args = message.params?.arguments ? ` ${JSON.stringify(message.params.arguments)}` : '';
    return `${message.id !== undefined ? `#${message.id} ` : ''}${message.method}${args ? truncate(args, 180) : ''}`;
  }
  if (message.error) return `#${message.id} error ${message.error.code ?? ''}: ${truncate(message.error.message ?? '', 180)}`;
  if (message.result !== undefined) {
    const result = message.result;
    if (Array.isArray(result?.tools)) return `#${message.id} tools/list → ${result.tools.length} tools`;
    if (result?.content) return `#${message.id} result → ${truncate(JSON.stringify(result.content), 180)}`;
    return `#${message.id} result → ${truncate(JSON.stringify(result), 180)}`;
  }
  return truncate(JSON.stringify(message), 180);
}
