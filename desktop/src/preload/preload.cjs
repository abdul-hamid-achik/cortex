/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Sandboxed preload: CommonJS, contextIsolation on, and an explicit channel
// allowlist so the renderer can only reach the IPC surface declared here.
const { contextBridge, ipcRenderer } = require('electron');

const REQUEST_CHANNELS = [
  'app:info',
  'settings:get',
  'settings:update',
  'settings:reset',
  'workspace:choose',
  'workspace:repos',
  'registry:all',
  'registry:catalog',
  'registry:preview',
  'cortex:run',
  'cortex:runMany',
  'cortex:probe',
  'cortex:config',
  'store:repos',
  'store:sessions',
  'store:files',
  'store:file',
  'store:locate',
  'repo:git',
  'repo:docs',
  'repo:specs',
  'repo:contracts',
  'repo:evaluations',
  'repo:read',
  'repo:facts',
  'repo:stats',
  'repo:tree',
  'mcp:start',
  'mcp:stop',
  'mcp:status',
  'mcp:refreshTools',
  'mcp:call',
  'run:start',
  'run:kill',
  'run:list',
  'run:output',
  'run:clear',
  'shell:reveal',
  'shell:openExternal',
];

const EVENT_CHANNELS = ['deck:event', 'deck:navigate', 'deck:palette', 'deck:refresh'];

contextBridge.exposeInMainWorld('deck', {
  platform: process.platform,
  versions: { node: process.versions.node, electron: process.versions.electron, chrome: process.versions.chrome },

  invoke(channel, payload) {
    if (!REQUEST_CHANNELS.includes(channel)) return Promise.reject(new Error(`blocked ipc channel: ${channel}`));
    return ipcRenderer.invoke(channel, payload ?? {});
  },

  on(channel, listener) {
    if (!EVENT_CHANNELS.includes(channel)) return () => {};
    const wrapped = (_event, payload) => listener(payload);
    ipcRenderer.on(channel, wrapped);
    return () => ipcRenderer.removeListener(channel, wrapped);
  },

  smokeReady(payload) {
    ipcRenderer.send('deck:smoke-ready', payload ?? {});
  },

  notify(payload) {
    ipcRenderer.send('deck:notify', payload ?? {});
  },
});
