/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { api } from './api.js';

const LOCAL_KEY = 'cortex-deck:view';

export const state = {
  booted: false,
  settings: null,
  workspace: '',
  groups: [],
  commands: new Map(),
  devTasks: [],
  config: null,
  probe: null,
  selectedTask: null,
  lastResults: new Map(),
  events: [],
};

const listeners = new Set();

export function subscribe(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function emit(reason = 'state') {
  for (const fn of listeners) {
    try {
      fn(state, reason);
    } catch (err) {
      console.error('state listener failed', err);
    }
  }
}

export function commandById(id) {
  return state.commands.get(id) ?? null;
}

export function commandsList() {
  return [...state.commands.values()];
}

export function loadLocalView() {
  try {
    return JSON.parse(localStorage.getItem(LOCAL_KEY) || '{}');
  } catch {
    return {};
  }
}

export function saveLocalView(patch) {
  try {
    localStorage.setItem(LOCAL_KEY, JSON.stringify({ ...loadLocalView(), ...patch }));
  } catch {
    /* storage may be unavailable; view state is a convenience */
  }
}

/** Load settings, the command registry, the binary probe, and resolved config. */
export async function boot() {
  const [settings, registry, appInfo] = await Promise.all([api.settings.get(), api.registry.all(), api.appInfo()]);
  state.settings = settings;
  state.appInfo = appInfo;
  state.workspace = settings.resolvedWorkspace || settings.workspace || '';
  state.groups = registry.groups;
  state.devTasks = registry.devTasks;
  state.commands = new Map(registry.commands.map((command) => [command.id, command]));

  const local = loadLocalView();
  if (local.selectedTask?.taskId) state.selectedTask = local.selectedTask;

  await refreshEnvironment();
  state.booted = true;
  emit('boot');
  return state;
}

/** Re-probe the binary and re-read resolved config (workspace or approvals changed). */
export async function refreshEnvironment() {
  const [probe, config] = await Promise.all([
    api.probe(state.workspace).catch(() => null),
    api.config({ workspace: state.workspace, force: true }).catch(() => null),
  ]);
  state.probe = probe;
  state.config = config?.config ?? null;
  if (config?.workspace) state.workspace = config.workspace;
  emit('environment');
}

export async function setSettings(patch) {
  const previousWorkspace = state.workspace;
  state.settings = await api.settings.update(patch);
  if (patch.workspace && patch.workspace !== previousWorkspace) {
    state.workspace = patch.workspace;
    state.selectedTask = null;
    saveLocalView({ selectedTask: null });
    await refreshEnvironment();
  }
  emit('settings');
  return state.settings;
}

export function applyTheme() {
  document.documentElement.dataset.theme = state.settings?.theme === 'light' ? 'light' : 'dark';
  document.documentElement.dataset.density = state.settings?.density === 'compact' ? 'compact' : 'comfortable';
}

export function selectTask(task) {
  if (!task?.taskId) return;
  state.selectedTask = {
    taskId: task.taskId,
    repo: task.repo ?? task.repository ?? '',
    workspace: task.workspace ?? state.workspace,
    goal: task.goal ?? '',
    phase: task.phase ?? task.state ?? '',
  };
  saveLocalView({ selectedTask: state.selectedTask });
  emit('task');
}

export function clearTask() {
  state.selectedTask = null;
  saveLocalView({ selectedTask: null });
  emit('task');
}

/** Cache the last result per command id so views can re-render instantly. */
export function rememberResult(commandId, key, result) {
  const bucket = state.lastResults.get(commandId) ?? new Map();
  bucket.set(key ?? 'default', { at: Date.now(), result });
  state.lastResults.set(commandId, bucket);
}

export function lastResult(commandId, key) {
  return state.lastResults.get(commandId)?.get(key ?? 'default')?.result ?? null;
}

export function pushEvent(event) {
  state.events.unshift(event);
  if (state.events.length > 400) state.events.length = 400;
}

export const approvals = () => state.settings?.approvals ?? { commands: false, remoteRecall: false, trajectory: false };
