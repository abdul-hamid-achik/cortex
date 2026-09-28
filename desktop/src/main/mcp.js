/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { spawn as nodeSpawn } from 'node:child_process';
import { EventEmitter } from 'node:events';

const PROTOCOL_VERSION = '2025-06-18';
const CLIENT_INFO = { name: 'cortex-deck', version: '0.1.0' };

/**
 * A minimal MCP client over stdio for `cortex serve`.
 *
 * Cortex writes newline-delimited JSON-RPC (never Content-Length framing) and
 * keeps stdout pure by logging to stderr, so the client is a line splitter plus
 * an id → pending-request map. `spawnFn` is injectable so the framing can be
 * unit-tested without a real binary.
 */
export function createMcpClient({ resolveCommand, spawnFn = nodeSpawn, requestTimeoutMs = 60_000 } = {}) {
  const events = new EventEmitter();
  events.setMaxListeners(64);

  let child = null;
  let buffer = '';
  let nextId = 1;
  const pending = new Map();
  let state = 'stopped';
  let lastError = '';
  let serverInfo = null;
  let capabilities = null;
  let tools = [];
  let profile = 'agent';
  let workspace = '';

  const emit = (type, payload) => events.emit('event', { type, at: new Date().toISOString(), ...payload });

  function failAllPending(reason) {
    for (const [, entry] of pending) entry.reject(new Error(reason));
    pending.clear();
  }

  function handleLine(line) {
    const text = line.trim();
    if (!text) return;
    let message;
    try {
      message = JSON.parse(text);
    } catch {
      emit('protocol', { direction: 'recv', raw: text.slice(0, 2000), error: 'non-JSON line on stdout' });
      return;
    }
    emit('message', { direction: 'recv', message });
    if (message.id !== undefined && (message.result !== undefined || message.error !== undefined)) {
      const entry = pending.get(message.id);
      if (!entry) return;
      pending.delete(message.id);
      clearTimeout(entry.timer);
      if (message.error) entry.reject(Object.assign(new Error(message.error.message || 'mcp error'), { code: message.error.code, data: message.error.data }));
      else entry.resolve(message.result);
      return;
    }
    if (message.method) emit('notification', { method: message.method, params: message.params });
  }

  function sendRaw(message) {
    if (!child?.stdin?.writable) throw new Error('mcp server is not running');
    const line = `${JSON.stringify(message)}\n`;
    child.stdin.write(line);
    emit('message', { direction: 'send', message });
  }

  function request(method, params, timeoutMs = requestTimeoutMs) {
    if (!child) return Promise.reject(new Error('mcp server is not running'));
    const id = nextId++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`${method} timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      pending.set(id, { resolve, reject, timer });
      try {
        sendRaw({ jsonrpc: '2.0', id, method, params });
      } catch (err) {
        pending.delete(id);
        clearTimeout(timer);
        reject(err);
      }
    });
  }

  function notify(method, params) {
    sendRaw({ jsonrpc: '2.0', method, params });
  }

  async function start(opts = {}) {
    if (child) throw new Error('mcp server is already running');
    profile = opts.profile === 'all' ? 'all' : 'agent';
    workspace = opts.workspace || '';
    const { binary, argv, env } = resolveCommand({ profile, workspace });
    const args = ['serve', '--profile', profile];
    if (workspace) args.push('-C', workspace);

    emit('lifecycle', { state: 'starting', binary, argv: [binary, ...args] });
    child = spawnFn(binary, args, { cwd: workspace || process.cwd(), env, windowsHide: true });
    state = 'starting';
    buffer = '';

    child.stdout.setEncoding?.('utf8');
    child.stderr.setEncoding?.('utf8');
    child.stdout.on('data', (chunk) => {
      buffer += chunk.toString('utf8');
      let index = buffer.indexOf('\n');
      while (index >= 0) {
        const line = buffer.slice(0, index);
        buffer = buffer.slice(index + 1);
        handleLine(line);
        index = buffer.indexOf('\n');
      }
    });
    child.stderr.on('data', (chunk) => emit('stderr', { text: chunk.toString('utf8') }));
    child.on('error', (err) => {
      lastError = String(err?.message ?? err);
      state = 'error';
      emit('lifecycle', { state, error: lastError });
      failAllPending(lastError);
    });
    child.on('close', (code, signal) => {
      state = 'stopped';
      child = null;
      emit('lifecycle', { state, code, signal });
      failAllPending(`mcp server exited (${code ?? signal ?? 'unknown'})`);
    });

    try {
      const init = await request('initialize', {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: CLIENT_INFO,
      });
      serverInfo = init?.serverInfo ?? null;
      capabilities = init?.capabilities ?? null;
      notify('notifications/initialized', {});
      state = 'ready';
      emit('lifecycle', { state, serverInfo, capabilities, profile });
      await refreshTools();
      return status();
    } catch (err) {
      lastError = String(err?.message ?? err);
      state = 'error';
      emit('lifecycle', { state, error: lastError });
      stop();
      throw err;
    }
  }

  async function refreshTools() {
    const result = await request('tools/list', {});
    tools = Array.isArray(result?.tools) ? result.tools : [];
    emit('tools', { count: tools.length, profile });
    return tools;
  }

  async function callTool(name, args = {}, timeoutMs = requestTimeoutMs) {
    const result = await request('tools/call', { name, arguments: args ?? {} }, timeoutMs);
    return result;
  }

  function stop() {
    if (!child) return;
    const victim = child;
    child = null;
    failAllPending('mcp server stopped');
    try {
      victim.stdin?.end();
    } catch {
      /* ignore */
    }
    try {
      victim.kill('SIGTERM');
    } catch {
      /* ignore */
    }
    // Keep a failed handshake visible as an error; only a healthy server
    // transitions to stopped, otherwise the UI loses the diagnostic.
    if (state !== 'error') state = 'stopped';
    emit('lifecycle', { state });
  }

  function status() {
    return {
      state,
      profile,
      workspace,
      serverInfo,
      capabilities,
      toolCount: tools.length,
      tools: tools.map((t) => ({ name: t.name, title: t.title ?? '', description: t.description ?? '', inputSchema: t.inputSchema ?? null })),
      lastError,
      pid: child?.pid ?? null,
    };
  }

  return { start, stop, status, callTool, refreshTools, request, events, get tools() { return tools; } };
}

/** Pull plain text out of an MCP tool result's content blocks. */
export function toolResultText(result) {
  const parts = [];
  for (const block of result?.content ?? []) {
    if (block?.type === 'text' && typeof block.text === 'string') parts.push(block.text);
    else if (block?.type === 'resource' && typeof block.resource?.text === 'string') parts.push(block.resource.text);
    else parts.push(JSON.stringify(block));
  }
  return parts.join('\n');
}

/** MCP tool results carry the cortex envelope as JSON text; parse it when possible. */
export function toolResultEnvelope(result) {
  const text = toolResultText(result).trim();
  if (!text.startsWith('{') && !text.startsWith('[')) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}
