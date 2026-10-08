/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createSettings, DEFAULT_SETTINGS, normalize } from '../src/main/settings.js';

const tempDir = () => fs.mkdtempSync(path.join(os.tmpdir(), 'deck-settings-'));

test('missing or corrupt settings fall back to the documented defaults', () => {
  const dir = tempDir();
  const settings = createSettings(dir);
  assert.deepEqual(settings.all(), DEFAULT_SETTINGS);

  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, 'settings.json'), '{ this is not json');
  const corrupt = createSettings(dir);
  assert.deepEqual(corrupt.all(), DEFAULT_SETTINGS);
});

test('update merges shallowly and merges nested objects one level deep', () => {
  const settings = createSettings(tempDir());
  settings.update({ theme: 'light' });
  settings.update({ approvals: { commands: true } });
  settings.update({ timeouts: { longMs: 900_000 } });

  const all = settings.all();
  assert.equal(all.theme, 'light');
  assert.deepEqual(all.approvals, { commands: true, remoteRecall: false, trajectory: false });
  assert.equal(all.timeouts.longMs, 900_000);
  assert.equal(all.timeouts.defaultMs, DEFAULT_SETTINGS.timeouts.defaultMs);
});

test('unknown keys are ignored so a stale file cannot inject settings', () => {
  const dir = tempDir();
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, 'settings.json'), JSON.stringify({ theme: 'light', evil: 'rm -rf', approvals: { commands: 'yes please' } }));
  const all = createSettings(dir).all();
  assert.equal(all.theme, 'light');
  assert.equal('evil' in all, false);
  assert.equal(all.approvals.commands, false, 'non-boolean approvals must not be granted');
});

test('normalize coerces bad values back into range', () => {
  const normalized = normalize({
    theme: 'neon',
    density: 'huge',
    approvals: { commands: 'true', remoteRecall: 1, trajectory: null },
    timeouts: { defaultMs: -5, longMs: 99_999_999 },
    autoRefreshMs: 'soon',
    recentWorkspaces: ['/a', 42, null, '/b'],
  });
  assert.equal(normalized.theme, 'dark');
  assert.equal(normalized.density, 'comfortable');
  assert.deepEqual(normalized.approvals, { commands: false, remoteRecall: false, trajectory: false });
  assert.equal(normalized.timeouts.defaultMs, DEFAULT_SETTINGS.timeouts.defaultMs);
  assert.equal(normalized.timeouts.longMs, 3_600_000);
  assert.equal(normalized.autoRefreshMs, 0);
  assert.deepEqual(normalized.recentWorkspaces, ['/a', '/b']);
});

test('recent workspaces are deduplicated, newest first, and capped', () => {
  const settings = createSettings(tempDir());
  for (let i = 0; i < 20; i += 1) settings.pushRecentWorkspace(`/tmp/repo-${i}`);
  settings.pushRecentWorkspace('/tmp/repo-19');
  const recents = settings.all().recentWorkspaces;
  assert.equal(recents.length, 12);
  assert.equal(recents[0], '/tmp/repo-19');
  assert.equal(new Set(recents).size, recents.length);
});

test('settings are written atomically with owner-only permissions', () => {
  const dir = tempDir();
  const settings = createSettings(dir);
  settings.update({ defaultActor: 'agent-deck' });

  const file = settings.file;
  assert.ok(fs.existsSync(file));
  assert.equal(JSON.parse(fs.readFileSync(file, 'utf8')).defaultActor, 'agent-deck');
  assert.equal(fs.readdirSync(dir).some((name) => name.startsWith('settings.json.tmp-')), false, 'temp file should be renamed away');
  if (process.platform !== 'win32') {
    const mode = fs.statSync(file).mode & 0o777;
    assert.equal(mode, 0o600, `settings file should be owner-only, got 0o${mode.toString(8)}`);
  }
});

test('reset restores defaults and persists them', () => {
  const settings = createSettings(tempDir());
  settings.update({ theme: 'light', approvals: { commands: true } });
  const reset = settings.reset();
  assert.deepEqual(reset, DEFAULT_SETTINGS);
  assert.deepEqual(settings.all(), DEFAULT_SETTINGS);
});

test('get returns a copy so callers cannot mutate the cache', () => {
  const settings = createSettings(tempDir());
  const approvals = settings.get('approvals');
  approvals.commands = true;
  assert.equal(settings.get('approvals').commands, false);
});

test('trustWorkspaceBinary defaults off and only a literal true turns it on', () => {
  assert.equal(DEFAULT_SETTINGS.trustWorkspaceBinary, false);
  assert.equal(normalize({}).trustWorkspaceBinary, false);
  assert.equal(normalize({ trustWorkspaceBinary: 'true' }).trustWorkspaceBinary, false);
  assert.equal(normalize({ trustWorkspaceBinary: 1 }).trustWorkspaceBinary, false);
  assert.equal(normalize({ trustWorkspaceBinary: true }).trustWorkspaceBinary, true);

  const settings = createSettings(tempDir());
  assert.equal(settings.update({ trustWorkspaceBinary: true }).trustWorkspaceBinary, true);
  assert.equal(settings.reset().trustWorkspaceBinary, false);
});
