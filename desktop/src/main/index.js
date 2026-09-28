/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { app, BrowserWindow, ipcMain, Menu, protocol, session } from 'electron';
import { createSettings } from './settings.js';
import { registerIpc } from './ipc.js';

const here = path.dirname(fileURLToPath(import.meta.url));
const rendererRoot = path.resolve(here, '..', 'renderer');
const appRoot = path.resolve(here, '..', '..');
const appIcon = path.join(appRoot, 'build', 'icon.png');

// ESM in the renderer needs a real origin: file:// modules are blocked by CORS.
protocol.registerSchemesAsPrivileged([
  { scheme: 'deck', privileges: { standard: true, secure: true, supportFetchAPI: true, stream: true, codeCache: true } },
]);

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.woff2': 'font/woff2',
  '.ico': 'image/x-icon',
};

const isSmoke = process.argv.includes('--deck-smoke');

let win = null;
let services = null;

function repoRootGuess() {
  // Packaged builds have no repository around them; unpacked runs sit in desktop/.
  const candidate = path.resolve(appRoot, '..');
  try {
    if (fs.statSync(path.join(candidate, 'go.mod')).isFile()) return candidate;
  } catch { /* not the repo */ }
  return '';
}

function createMainWindow() {
  win = new BrowserWindow({
    width: 1480,
    height: 940,
    minWidth: 1080,
    minHeight: 680,
    show: false,
    title: 'Cortex Deck',
    icon: fs.existsSync(appIcon) ? appIcon : undefined,
    backgroundColor: '#0b0e13',
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    trafficLightPosition: { x: 16, y: 18 },
    webPreferences: {
      preload: path.resolve(here, '..', 'preload', 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      spellcheck: false,
      webSecurity: true,
    },
  });

  win.webContents.on('will-navigate', (event, url) => {
    if (!url.startsWith('deck://ui/')) event.preventDefault();
  });
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));

  win.once('ready-to-show', () => {
    // showInactive keeps the smoke run from stealing focus while still painting.
    if (isSmoke) win.showInactive();
    else win.show();
  });

  if (isSmoke) {
    win.webContents.on('console-message', (_event, level, message, line, sourceId) => {
      if (level >= 2) console.error(`[renderer] ${message} (${sourceId}:${line})`);
    });
  }

  win.loadURL(`deck://ui/index.html${isSmoke ? '?smoke=1' : ''}`);
  return win;
}

function buildMenu() {
  const template = [
    ...(process.platform === 'darwin' ? [{ role: 'appMenu' }] : []),
    {
      label: 'View',
      submenu: [
        { label: 'Dashboard', accelerator: 'CmdOrCtrl+1', click: () => win?.webContents.send('deck:navigate', 'dashboard') },
        { label: 'Sessions', accelerator: 'CmdOrCtrl+2', click: () => win?.webContents.send('deck:navigate', 'sessions') },
        { label: 'Workspace', accelerator: 'CmdOrCtrl+3', click: () => win?.webContents.send('deck:navigate', 'workspace') },
        { label: 'Console', accelerator: 'CmdOrCtrl+4', click: () => win?.webContents.send('deck:navigate', 'console') },
        { label: 'Environment', accelerator: 'CmdOrCtrl+5', click: () => win?.webContents.send('deck:navigate', 'environment') },
        { label: 'MCP', accelerator: 'CmdOrCtrl+6', click: () => win?.webContents.send('deck:navigate', 'mcp') },
        { label: 'Long-running', accelerator: 'CmdOrCtrl+7', click: () => win?.webContents.send('deck:navigate', 'longrun') },
        { label: 'Evidence', accelerator: 'CmdOrCtrl+8', click: () => win?.webContents.send('deck:navigate', 'evidence') },
        { label: 'Repository', accelerator: 'CmdOrCtrl+9', click: () => win?.webContents.send('deck:navigate', 'repo') },
        { type: 'separator' },
        { role: 'reload' },
        { role: 'togglefullscreen' },
      ],
    },
    {
      label: 'Tools',
      submenu: [
        { label: 'Command palette', accelerator: 'CmdOrCtrl+K', click: () => win?.webContents.send('deck:palette') },
        { label: 'Refresh current view', accelerator: 'CmdOrCtrl+R', click: () => win?.webContents.send('deck:refresh') },
        { type: 'separator' },
        { role: 'toggleDevTools' },
      ],
    },
    { role: 'editMenu' },
    { role: 'windowMenu' },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

async function runSmoke() {
  const start = Date.now();
  const errors = [];
  win.webContents.on('console-message', (_event, level, message) => {
    if (level >= 2) errors.push(message);
  });

  // The renderer reports when a route finished mounting, so screenshots never
  // catch a half-loaded view no matter how slow the underlying cortex probes are.
  const settledRoutes = [];
  let smokeRuns = null;
  ipcMain.on('deck:notify', (_event, payload) => {
    if (payload?.type === 'route-settled') settledRoutes.push(payload.route);
    if (payload?.type === 'smoke-runs') smokeRuns = payload.runs ?? null;
  });
  const waitForSettle = (routeId, timeoutMs) => {
    const seen = settledRoutes.length;
    return new Promise((resolve) => {
      const timer = setTimeout(() => resolve(false), timeoutMs);
      const check = setInterval(() => {
        if (settledRoutes.length > seen && settledRoutes.at(-1) === routeId) {
          clearInterval(check);
          clearTimeout(timer);
          resolve(true);
        }
      }, 80);
    });
  };

  const ready = new Promise((resolve) => {
    const timer = setTimeout(() => resolve({ timedOut: true }), 45_000);
    ipcMain.once('deck:smoke-ready', (_event, payload) => {
      clearTimeout(timer);
      resolve(payload ?? {});
    });
  });

  // The window already loaded index.html?smoke=1 in createMainWindow; pass 2
  // re-loads with the case view so a real session gets exercised too.
  const pass1 = await ready;
  await new Promise((r) => setTimeout(r, 2500));
  const shots = [];
  const shotDir = path.join(appRoot, 'screenshots');
  fs.mkdirSync(shotDir, { recursive: true });

  const capture = async (views, prefix) => {
    for (const view of views) {
      const settled = waitForSettle(view.id, 20_000);
      win.webContents.send('deck:navigate', view.id);
      await settled;
      await new Promise((r) => setTimeout(r, 700));
      const image = await win.webContents.capturePage();
      const png = image.toPNG();
      const file = path.join(shotDir, `${prefix}${String(view.id).replace(/[^a-z0-9-]/gi, '_')}.png`);
      fs.writeFileSync(file, png);
      shots.push({ view: view.id, file, bytes: png.length });
    }
  };

  await capture(pass1.views ?? [], '');

  let pass2 = { views: [] };
  const taskId = await findSampleTask();
  if (taskId) {
    const ready2 = new Promise((resolve) => {
      const timer = setTimeout(() => resolve({ timedOut: true }), 45_000);
      ipcMain.once('deck:smoke-ready', (_event, payload) => {
        clearTimeout(timer);
        resolve(payload ?? {});
      });
    });
    await win.webContents.loadURL(`deck://ui/index.html?smoke=2&task=${encodeURIComponent(taskId)}`);
    pass2 = await ready2;
    await new Promise((r) => setTimeout(r, 3000));
    await capture(pass2.views ?? [], 'case_');
  }

  const report = {
    ok: errors.length === 0 && !pass1.timedOut && bridgeOk(smokeRuns),
    durationMs: Date.now() - start,
    rendererReady: !pass1.timedOut,
    views: [...(pass1.views ?? []), ...(pass2.views ?? []).map((v) => ({ ...v, id: `case:${v.id}` }))],
    sampleTask: taskId ?? null,
    probe: pass1.probe ?? null,
    commands: pass1.commands ?? null,
    routes: pass1.routes ?? [],
    workspace: pass1.workspace ?? null,
    bridgeRuns: smokeRuns ?? [],
    errors,
    screenshots: shots,
  };
  fs.writeFileSync(path.join(shotDir, 'smoke-report.json'), `${JSON.stringify(report, null, 2)}\n`);
  console.log(JSON.stringify(report, null, 2));
  services?.mcp?.stop?.();
  app.exit(report.ok ? 0 : 1);
}

/** Pick a real session so the case view is smoke-tested with real data. */
async function findSampleTask() {
  try {
    const forced = process.env.CORTEX_DECK_SMOKE_TASK;
    if (forced) return forced;
    const base = process.env.XDG_STATE_HOME
      ? path.join(process.env.XDG_STATE_HOME, 'cortex', 'sessions')
      : path.join(process.env.HOME ?? '', '.local', 'state', 'cortex', 'sessions');
    if (!fs.existsSync(base)) return null;
    const repos = fs.readdirSync(base, { withFileTypes: true }).filter((entry) => entry.isDirectory());
    // Prefer this repository's own sessions so the screenshot is representative.
    const preferred = repos.filter((entry) => entry.name === 'cortex');
    for (const repo of [...preferred, ...repos.filter((entry) => entry.name !== 'cortex')]) {
      const dir = path.join(base, repo.name);
      for (const task of fs.readdirSync(dir, { withFileTypes: true })) {
        if (!task.isDirectory()) continue;
        const caseFile = path.join(dir, task.name, 'case.json');
        try {
          const parsed = JSON.parse(fs.readFileSync(caseFile, 'utf8'));
          // On-disk case snapshots carry `status`; CLI projections carry `phase`.
          if (parsed?.status || parsed?.phase) return task.name;
        } catch { /* keep looking for a readable case */ }
      }
    }
  } catch {
    return null;
  }
  return null;
}

/** The in-app bridge probe must report every run as ok for the smoke to pass. */
function bridgeOk(runs) {
  if (!Array.isArray(runs) || !runs.length) return false;
  return runs.every((run) => run.ok === true || run.skipped === true);
}

app.setName('Cortex Deck');

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (!win) return;
    if (win.isMinimized()) win.restore();
    win.focus();
  });

  app.whenReady().then(async () => {
    // In dev (`electron .`) macOS would otherwise show the stock Electron dock icon.
    if (process.platform === 'darwin' && app.dock && fs.existsSync(appIcon)) {
      app.dock.setIcon(appIcon);
    }
    const settingsDir = app.getPath('userData');
    const settings = createSettings(settingsDir);
    if (!settings.all().workspace) {
      const guess = repoRootGuess();
      if (guess) settings.update({ workspace: guess });
    }

    session.defaultSession.protocol.handle('deck', async (request) => {
      const url = new URL(request.url);
      const rel = decodeURIComponent(url.pathname).replace(/^\/+/, '') || 'index.html';
      const file = path.resolve(rendererRoot, rel);
      if (!file.startsWith(rendererRoot + path.sep)) return new Response('forbidden', { status: 403 });
      try {
        const body = await fs.promises.readFile(file);
        return new Response(body, {
          status: 200,
          headers: { 'content-type': MIME[path.extname(file)] ?? 'application/octet-stream', 'cache-control': 'no-cache' },
        });
      } catch {
        return new Response('not found', { status: 404 });
      }
    });

    if (process.env.CORTEX_DECK_WORKSPACE && fs.existsSync(process.env.CORTEX_DECK_WORKSPACE)) {
      settings.update({ workspace: process.env.CORTEX_DECK_WORKSPACE });
    }

    services = registerIpc({ settings, mainWindow: () => win });
    buildMenu();
    createMainWindow();

    win.on('closed', () => { win = null; });

    app.on('activate', () => {
      if (BrowserWindow.getAllWindows().length === 0) createMainWindow();
    });

    if (isSmoke) await runSmoke();
  });

  app.on('window-all-closed', () => {
    services?.mcp?.stop?.();
    if (process.platform !== 'darwin' || isSmoke) app.quit();
  });

  app.on('before-quit', () => services?.mcp?.stop?.());
}
