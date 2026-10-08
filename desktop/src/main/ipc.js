/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import fs from 'node:fs';
import path from 'node:path';
import { app, dialog, ipcMain, shell } from 'electron';
import { COMMANDS, DEV_TASKS, GROUPS, buildArgv, featureCatalog, getCommand, renderArgv } from './registry.js';
import { createCortex, envelopeView } from './cortex.js';
import { createCaseStore } from './casestore.js';
import { createRepo } from './repo.js';
import { createMcpClient, toolResultEnvelope, toolResultText } from './mcp.js';
import { createRunner } from './runner.js';
import { allowedExternalUrl } from './trust.js';

const CONFIG_TTL_MS = 30_000;

function isExistingDir(value) {
  if (typeof value !== 'string' || !value) return false;
  try {
    return fs.statSync(value).isDirectory();
  } catch {
    return false;
  }
}

/** All Electron wiring lives here; every other main-process module is plain node. */
export function registerIpc({ settings, mainWindow, repoRoot = '' }) {
  const send = (payload) => {
    const win = mainWindow();
    if (win && !win.isDestroyed()) win.webContents.send('deck:event', payload);
  };

  const workspaceOf = (override) => {
    if (override && isExistingDir(override)) return override;
    const configured = settings.all().workspace;
    if (configured && isExistingDir(configured)) return configured;
    return process.cwd();
  };

  const cortex = createCortex({ settings });
  const configCache = new Map();

  async function resolvedConfig(workspace, { force = false } = {}) {
    const cached = configCache.get(workspace);
    if (!force && cached && Date.now() - cached.at < CONFIG_TTL_MS) return cached.value;
    const result = await cortex.exec(['config'], { workspace, json: true, timeoutMs: 20_000 });
    const value = result.ok && result.json ? result.json : null;
    if (value) configCache.set(workspace, { at: Date.now(), value });
    return value;
  }

  const store = createCaseStore({
    getRoots: () => {
      const workspace = workspaceOf();
      const cached = configCache.get(workspace);
      return cached?.value ?? {};
    },
  });

  const repo = createRepo({ getWorkspace: () => workspaceOf() });

  const mcp = createMcpClient({
    resolveCommand: ({ profile, workspace }) => {
      const binary = cortex.resolveBinary(workspace);
      return {
        binary: binary.path,
        argv: [binary.path, 'serve', '--profile', profile],
        // Same gating as cortex.exec: only the Settings toggles may grant CORTEX_APPROVE_*.
        env: { ...cortex.approvalEnv(), NO_COLOR: '1', TERM: 'dumb' },
      };
    },
  });
  mcp.events.on('event', (event) => send({ type: 'mcp', event }));

  const runner = createRunner({ getWorkspace: () => workspaceOf(), emit: (payload) => send(payload), repoRoot });

  /** Warm the config cache so the case store has roots before first use. */
  async function ensureRoots(workspace) {
    const config = await resolvedConfig(workspace);
    if (config?.sessionsRoot && !configCache.get(workspace)) {
      configCache.set(workspace, { at: Date.now(), value: config });
    }
    return config;
  }

  const handle = (channel, fn) => ipcMain.handle(channel, async (_event, payload) => {
    try {
      return { ok: true, data: await fn(payload ?? {}) };
    } catch (err) {
      return { ok: false, error: String(err?.message ?? err) };
    }
  });

  // ── app / settings ──────────────────────────────────────────────────────
  handle('app:info', async () => ({
    name: app.getName(),
    version: app.getVersion(),
    electron: process.versions.electron,
    node: process.versions.node,
    chrome: process.versions.chrome,
    platform: process.platform,
    arch: process.arch,
    userData: app.getPath('userData'),
    settingsFile: settings.file,
    cwd: process.cwd(),
  }));

  handle('settings:get', async () => ({ ...settings.all(), resolvedWorkspace: workspaceOf() }));

  handle('settings:update', async (payload) => {
    const patch = { ...payload };
    if (patch.workspace !== undefined) {
      if (patch.workspace && !isExistingDir(patch.workspace)) throw new Error(`not a directory: ${patch.workspace}`);
      if (patch.workspace) {
        settings.pushRecentWorkspace(patch.workspace);
        configCache.delete(patch.workspace);
      }
    }
    if (patch.trustWorkspaceBinary !== undefined) patch.trustWorkspaceBinary = patch.trustWorkspaceBinary === true;
    if (patch.binaryPath !== undefined && patch.binaryPath && !isExistingDir(path.dirname(patch.binaryPath))) {
      throw new Error(`binary directory does not exist: ${patch.binaryPath}`);
    }
    configCache.clear();
    return settings.update(patch);
  });

  handle('settings:reset', async () => settings.reset());

  handle('workspace:choose', async () => {
    const win = mainWindow();
    const result = await dialog.showOpenDialog(win, {
      title: 'Choose a workspace repository',
      defaultPath: workspaceOf(),
      properties: ['openDirectory', 'createDirectory'],
    });
    if (result.canceled || !result.filePaths.length) return { canceled: true, workspace: settings.all().workspace };
    const chosen = result.filePaths[0];
    settings.update({ workspace: chosen });
    settings.pushRecentWorkspace(chosen);
    configCache.clear();
    return { canceled: false, workspace: chosen };
  });

  handle('workspace:repos', async () => {
    const config = await ensureRoots(workspaceOf());
    const sessionsRoot = config?.sessionsRoot ?? '';
    if (!sessionsRoot) return { sessionsRoot, repos: [] };
    let entries = [];
    try {
      entries = fs.readdirSync(sessionsRoot, { withFileTypes: true });
    } catch {
      entries = [];
    }
    const repos = [];
    for (const entry of entries) {
      if (!entry.isDirectory()) continue;
      const dir = path.join(sessionsRoot, entry.name);
      let sessions = 0;
      let workspacePath = '';
      try {
        sessions = fs.readdirSync(dir, { withFileTypes: true }).filter((x) => x.isDirectory()).length;
      } catch { /* unreadable repo dir */ }
      try {
        const newest = fs.readdirSync(dir, { withFileTypes: true }).filter((x) => x.isDirectory())[0];
        if (newest) {
          const caseJson = JSON.parse(fs.readFileSync(path.join(dir, newest.name, 'case.json'), 'utf8'));
          workspacePath = caseJson?.workspace ?? '';
        }
      } catch { /* no case.json yet */ }
      repos.push({ slug: entry.name, sessions, workspacePath, path: dir });
    }
    return { sessionsRoot, repos: repos.sort((a, b) => a.slug.localeCompare(b.slug)) };
  });

  // ── registry ────────────────────────────────────────────────────────────
  handle('registry:all', async () => ({ groups: GROUPS, commands: COMMANDS, devTasks: DEV_TASKS }));
  handle('registry:catalog', async () => featureCatalog());
  handle('registry:preview', async ({ commandId, values, workspace }) => {
    const command = getCommand(commandId);
    if (!command) throw new Error(`unknown command: ${commandId}`);
    return { commandId, argv: renderArgv(command, values ?? {}, { workspace: workspaceOf(workspace) }) };
  });

  // ── cortex runs ─────────────────────────────────────────────────────────
  async function runCommand({ commandId, values, workspace, timeoutMs, stream }) {
    const command = getCommand(commandId);
    if (!command) throw new Error(`unknown command: ${commandId}`);
    const ws = workspaceOf(workspace);
    const argv = buildArgv(command, values ?? {});
    const runId = `cli_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
    if (command.kind === 'server') throw new Error(`${commandId} is a long-lived server; use the MCP view`);
    send({ type: 'cli-start', runId, commandId, argv, workspace: ws });
    const result = await cortex.exec(argv, {
      workspace: ws,
      json: command.json !== false,
      timeoutMs: timeoutMs ?? (command.kind === 'read' ? 60_000 : settings.all().timeouts.longMs),
      onChunk: stream
        ? (which, text) => send({ type: 'cli-chunk', runId, commandId, stream: which, text })
        : undefined,
    });
    const payload = {
      runId,
      commandId,
      title: command.title,
      kind: command.kind,
      argv: result.fullArgv,
      commandLine: renderArgv(command, values ?? {}, { workspace: ws, json: command.json !== false }),
      ok: result.ok,
      stage: result.stage,
      exitCode: result.exitCode,
      durationMs: result.durationMs,
      json: result.json,
      parseError: result.parseError,
      stdout: result.stdout?.slice(0, 512 * 1024) ?? '',
      stderr: result.stderr?.slice(0, 128 * 1024) ?? '',
      error: result.error,
      envelope: envelopeView(result),
      binary: result.binary,
    };
    send({ type: 'cli-end', runId, commandId, ok: payload.ok, durationMs: payload.durationMs, error: payload.error });
    return payload;
  }

  handle('cortex:run', runCommand);

  handle('cortex:runMany', async ({ calls }) => {
    const list = Array.isArray(calls) ? calls.slice(0, 12) : [];
    return Promise.all(list.map((call) => runCommand(call).catch((err) => ({ ok: false, commandId: call.commandId, error: String(err?.message ?? err) }))));
  });

  handle('cortex:probe', async ({ workspace }) => cortex.probe(workspaceOf(workspace)));

  handle('cortex:config', async ({ workspace, force }) => {
    const ws = workspaceOf(workspace);
    return { workspace: ws, config: await resolvedConfig(ws, { force: force === true }) };
  });

  // ── case store (direct file access) ─────────────────────────────────────
  handle('store:repos', async (payload) => {
    await ensureRoots(workspaceOf(payload.workspace));
    return store.listRepos({ includeArchive: payload.includeArchive !== false });
  });
  handle('store:sessions', async (payload) => {
    await ensureRoots(workspaceOf(payload.workspace));
    return store.listSessions(payload.repo);
  });
  handle('store:files', async (payload) => store.listFiles(payload.taskId));
  handle('store:file', async (payload) => store.readFileBounded(payload.taskId, payload.name, payload.maxBytes));
  handle('store:locate', async (payload) => store.locate(payload.taskId));

  // ── workspace / repository ──────────────────────────────────────────────
  handle('repo:git', async () => repo.gitInfo());
  handle('repo:docs', async () => repo.docs());
  handle('repo:specs', async () => repo.specs());
  handle('repo:contracts', async () => ({ manifest: repo.readText('contracts/v1/manifest.json'), schema: repo.readText('contracts/v1/schema.json'), fixtures: repo.contracts() }));
  handle('repo:evaluations', async () => repo.evaluations());
  handle('repo:read', async (payload) => repo.readText(payload.path, payload.maxBytes));
  handle('repo:facts', async () => repo.projectFacts());
  handle('repo:stats', async () => repo.codeStats());
  handle('repo:tree', async (payload) => repo.listDir(payload.path ?? '', { depth: payload.depth ?? 1 }));

  // ── MCP ─────────────────────────────────────────────────────────────────
  handle('mcp:start', async (payload) => {
    const ws = workspaceOf(payload.workspace);
    await ensureRoots(ws);
    return mcp.start({ profile: payload.profile === 'all' ? 'all' : 'agent', workspace: ws });
  });
  handle('mcp:stop', async () => { mcp.stop(); return mcp.status(); });
  handle('mcp:status', async () => mcp.status());
  handle('mcp:refreshTools', async () => { await mcp.refreshTools(); return mcp.status(); });
  handle('mcp:call', async (payload) => {
    const result = await mcp.callTool(payload.name, payload.arguments ?? {}, payload.timeoutMs ?? 120_000);
    const text = toolResultText(result);
    return {
      name: payload.name,
      isError: result?.isError === true,
      text: text.slice(0, 512 * 1024),
      envelope: toolResultEnvelope(result),
      raw: result,
    };
  });

  // ── developer task runner ───────────────────────────────────────────────
  handle('run:start', async (payload) => runner.start(payload.taskId));
  handle('run:kill', async (payload) => runner.kill(payload.runId));
  handle('run:list', async () => runner.list());
  handle('run:output', async (payload) => runner.output(payload.runId, payload.tail));
  handle('run:clear', async () => runner.clear());

  // ── OS integration ──────────────────────────────────────────────────────
  handle('shell:reveal', async (payload) => {
    if (typeof payload.path !== 'string' || !payload.path) throw new Error('missing path');
    shell.showItemInFolder(payload.path);
    return { revealed: payload.path };
  });
  handle('shell:openExternal', async (payload) => {
    const url = allowedExternalUrl(payload.url);
    if (!url) throw new Error('only https links to github.com and cortexai.tools may open');
    await shell.openExternal(url);
    return { opened: url };
  });

  return { cortex, store, repo, mcp, runner, resolvedConfig, ensureRoots, workspaceOf };
}
