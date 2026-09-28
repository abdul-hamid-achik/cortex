/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import fs from 'node:fs';
import path from 'node:path';

export const DEFAULT_SETTINGS = Object.freeze({
  binaryPath: '',
  workspace: '',
  theme: 'dark',
  density: 'comfortable',
  defaultActor: '',
  recentWorkspaces: [],
  approvals: { commands: false, remoteRecall: false, trajectory: false },
  timeouts: { defaultMs: 120_000, longMs: 600_000 },
  autoRefreshMs: 0,
  lastView: 'dashboard',
});

const KNOWN_KEYS = new Set(Object.keys(DEFAULT_SETTINGS));

/**
 * A tiny JSON settings store kept out of the repository (Electron userData).
 * Importable from plain node so the unit tests never need Electron.
 */
export function createSettings(dir) {
  const file = path.join(dir, 'settings.json');
  let cache = null;

  const read = () => {
    if (cache) return cache;
    let raw = null;
    try {
      raw = fs.readFileSync(file, 'utf8');
    } catch {
      raw = null;
    }
    let parsed = {};
    if (raw) {
      try {
        const decoded = JSON.parse(raw);
        if (decoded && typeof decoded === 'object' && !Array.isArray(decoded)) parsed = decoded;
      } catch {
        parsed = {};
      }
    }
    cache = normalize({ ...structuredClone(DEFAULT_SETTINGS), ...parsed });
    return cache;
  };

  const write = (next) => {
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    const tmp = `${file}.tmp-${process.pid}`;
    fs.writeFileSync(tmp, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
    fs.renameSync(tmp, file);
    cache = next;
    return next;
  };

  return {
    get file() {
      return file;
    },
    all() {
      return structuredClone(read());
    },
    get(key) {
      return structuredClone(read()[key]);
    },
    /** Shallow merge; nested objects (approvals, timeouts) merge one level deep. */
    update(patch) {
      const current = read();
      const next = { ...current };
      for (const [key, value] of Object.entries(patch ?? {})) {
        if (!KNOWN_KEYS.has(key)) continue;
        const existing = current[key];
        if (value && typeof value === 'object' && !Array.isArray(value) && existing && typeof existing === 'object' && !Array.isArray(existing)) {
          next[key] = { ...existing, ...value };
        } else {
          next[key] = value;
        }
      }
      return structuredClone(write(normalize(next)));
    },
    pushRecentWorkspace(dirPath) {
      const current = read();
      const list = [dirPath, ...(current.recentWorkspaces ?? [])].filter((x) => typeof x === 'string' && x);
      return write(normalize({ ...current, recentWorkspaces: [...new Set(list)].slice(0, 12) }));
    },
    reset() {
      return structuredClone(write(structuredClone(DEFAULT_SETTINGS)));
    },
  };
}

/** Coerce anything read from disk back into the documented shape. */
export function normalize(settings) {
  const out = structuredClone(DEFAULT_SETTINGS);
  const src = settings ?? {};

  for (const key of ['binaryPath', 'workspace', 'theme', 'density', 'defaultActor', 'lastView']) {
    if (typeof src[key] === 'string') out[key] = src[key];
  }
  if (!['dark', 'light'].includes(out.theme)) out.theme = 'dark';
  if (!['compact', 'comfortable'].includes(out.density)) out.density = 'comfortable';
  if (Array.isArray(src.recentWorkspaces)) {
    out.recentWorkspaces = src.recentWorkspaces.filter((x) => typeof x === 'string' && x).slice(0, 12);
  }
  if (src.approvals && typeof src.approvals === 'object') {
    for (const key of ['commands', 'remoteRecall', 'trajectory']) {
      out.approvals[key] = src.approvals[key] === true;
    }
  }
  if (src.timeouts && typeof src.timeouts === 'object') {
    for (const key of ['defaultMs', 'longMs']) {
      const v = Number(src.timeouts[key]);
      if (Number.isFinite(v) && v >= 1000) out.timeouts[key] = Math.min(v, 3_600_000);
    }
  }
  const auto = Number(src.autoRefreshMs);
  out.autoRefreshMs = Number.isFinite(auto) && auto >= 0 ? Math.min(auto, 600_000) : 0;
  return out;
}
