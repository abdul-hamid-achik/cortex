/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { toast } from './ui.js';
import { truncate } from './format.js';

async function call(channel, payload) {
  const bridge = globalThis.deck;
  if (!bridge) throw new Error('the preload bridge is unavailable — reload the window');
  const response = await bridge.invoke(channel, payload ?? {});
  if (!response) throw new Error(`${channel}: empty response from the main process`);
  if (response.ok === false) throw new Error(response.error || `${channel} failed`);
  return response.data;
}

/** Toast-and-swallow wrapper for background refreshes that must not throw. */
async function soft(label, promise) {
  try {
    return await promise;
  } catch (err) {
    toast('error', label, String(err?.message ?? err));
    return null;
  }
}

export const api = {
  call,
  soft,

  appInfo: () => call('app:info'),

  settings: {
    get: () => call('settings:get'),
    update: (patch) => call('settings:update', patch),
    reset: () => call('settings:reset'),
  },

  workspace: {
    choose: () => call('workspace:choose'),
    repos: () => call('workspace:repos'),
  },

  registry: {
    all: () => call('registry:all'),
    catalog: () => call('registry:catalog'),
    preview: (payload) => call('registry:preview', payload),
  },

  /** Run a registered cortex command. Returns the normalized result payload. */
  run: (commandId, values = {}, opts = {}) => call('cortex:run', { commandId, values, ...opts }),
  runMany: (calls) => call('cortex:runMany', { calls }),
  probe: (workspace) => call('cortex:probe', { workspace }),
  config: (opts = {}) => call('cortex:config', opts),

  store: {
    repos: (payload = {}) => call('store:repos', payload),
    sessions: (repo) => call('store:sessions', { repo }),
    files: (taskId) => call('store:files', { taskId }),
    file: (taskId, name, maxBytes) => call('store:file', { taskId, name, maxBytes }),
    locate: (taskId) => call('store:locate', { taskId }),
  },

  repo: {
    git: () => call('repo:git'),
    docs: () => call('repo:docs'),
    specs: () => call('repo:specs'),
    contracts: () => call('repo:contracts'),
    evaluations: () => call('repo:evaluations'),
    read: (path, maxBytes) => call('repo:read', { path, maxBytes }),
    facts: () => call('repo:facts'),
    stats: () => call('repo:stats'),
    tree: (path, depth) => call('repo:tree', { path, depth }),
  },

  mcp: {
    start: (payload = {}) => call('mcp:start', payload),
    stop: () => call('mcp:stop'),
    status: () => call('mcp:status'),
    refreshTools: () => call('mcp:refreshTools'),
    call: (name, args, timeoutMs) => call('mcp:call', { name, arguments: args, timeoutMs }),
  },

  runs: {
    start: (taskId) => call('run:start', { taskId }),
    kill: (runId) => call('run:kill', { runId }),
    list: () => call('run:list'),
    output: (runId, tail) => call('run:output', { runId, tail }),
    clear: () => call('run:clear'),
  },

  shell: {
    reveal: (path) => call('shell:reveal', { path }),
    openExternal: (url) => call('shell:openExternal', { url }),
  },

  onEvent: (handler) => globalThis.deck.on('deck:event', handler),
  onNavigate: (handler) => globalThis.deck.on('deck:navigate', handler),
  onPalette: (handler) => globalThis.deck.on('deck:palette', handler),
  onRefresh: (handler) => globalThis.deck.on('deck:refresh', handler),
  smokeReady: (payload) => globalThis.deck.smokeReady(payload),
};

/**
 * Run a command and surface kernel rejections the way the CLI does: the run
 * "succeeded" as a process but the gate refused it, which the user must see.
 */
export async function runChecked(commandId, values = {}, opts = {}) {
  const result = await api.run(commandId, values, opts);
  if (!result.ok) {
    const detail = result.error || result.envelope?.error || `exit ${result.exitCode}`;
    toast(opts.quiet ? 'info' : 'error', `${result.title ?? commandId} refused`, truncate(detail, 320), 7000);
  } else if (!opts.quiet) {
    toast('ok', result.title ?? commandId, truncate(result.summary || result.envelope?.summary || 'done', 220), 3600);
  }
  return result;
}
