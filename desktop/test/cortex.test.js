/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createCortex, envelopeView } from '../src/main/cortex.js';

function tempDir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'deck-cortex-'));
}

function writeScript(dir, name, body) {
  const file = path.join(dir, name);
  fs.writeFileSync(file, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
  return file;
}

function settingsFor(binaryPath, overrides = {}) {
  return {
    all: () => ({
      binaryPath,
      workspace: '',
      approvals: { commands: false, remoteRecall: false, trajectory: false },
      timeouts: { defaultMs: 20_000, longMs: 20_000 },
      ...overrides,
    }),
  };
}

test('a successful run parses the envelope and reports ok', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', `cat <<'JSON'
{"ok":true,"taskId":"task_1","phase":"investigating","summary":"started","facts":[{"id":"ev_1","claim":"c","confidence":"high"}],"actions":[{"tool":"cortex_investigate","command":"cortex investigate task_1"}],"nextActions":["treat search output as candidates"]}
JSON`);
  const cortex = createCortex({ settings: settingsFor(binary) });
  const result = await cortex.exec(['open', 'goal'], { workspace: dir });

  assert.equal(result.ok, true);
  assert.equal(result.exitCode, 0);
  assert.equal(result.stage, 'ok');
  assert.equal(result.json.taskId, 'task_1');
  assert.equal(result.envelope.phase, 'investigating');
  assert.deepEqual(result.argv, ['open', 'goal']);
  assert.ok(result.fullArgv.includes('--json'));
  assert.ok(result.fullArgv.includes('-C'));
  assert.ok(result.durationMs >= 0);
});

test('a kernel rejection (exit 0, ok:false) is not reported as success', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', `cat <<'JSON'
{"ok":false,"taskId":"task_1","summary":"plan rejected: hypothesis has no disproof path","error":"plan rejected: hypothesis has no disproof path"}
JSON`);
  const result = await createCortex({ settings: settingsFor(binary) }).exec(['plan', 'task_1'], { workspace: dir });
  assert.equal(result.ok, false);
  assert.equal(result.exitCode, 0);
  assert.match(result.error, /no disproof path/);
});

test('a non-zero exit surfaces stderr as the error', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'echo "boom" >&2\nexit 3');
  const result = await createCortex({ settings: settingsFor(binary) }).exec(['status', 'task_x'], { workspace: dir });
  assert.equal(result.ok, false);
  assert.equal(result.exitCode, 3);
  assert.equal(result.stage, 'exit');
  assert.match(result.error, /boom/);
});

test('invalid JSON on stdout is reported as a parse error, not swallowed', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'echo "not json at all"');
  const result = await createCortex({ settings: settingsFor(binary) }).exec(['list'], { workspace: dir });
  assert.equal(result.ok, false);
  assert.ok(result.parseError, 'expected a parseError');
  assert.equal(result.json, null);
});

test('json:false keeps raw stdout for commands that are not envelopes', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'echo "cortex version v0.20.0 (commit abc, built today)"');
  const result = await createCortex({ settings: settingsFor(binary) }).exec(['--version'], { workspace: dir, json: false });
  assert.equal(result.ok, true);
  assert.equal(result.json, null);
  assert.match(result.stdout, /v0\.20\.0/);
});

test('a missing binary fails at spawn time with a helpful message', async () => {
  const cortex = createCortex({ settings: settingsFor('/nonexistent/path/to/cortex') });
  const result = await cortex.exec(['list'], { workspace: '/tmp' });
  assert.equal(typeof result.ok, 'boolean');
  if (!result.ok) {
    // Either the spawn failed (no cortex on PATH) or cortex itself refused /tmp.
    assert.ok(result.stage === 'spawn' || result.stage === 'exit', `unexpected stage: ${result.stage}`);
    if (result.stage === 'spawn') assert.match(`${result.stderr}${result.error}`, /not found|no such file|ENOENT/i);
  }
});

test('timeouts kill the child and are reported as a timeout', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'sleep 5\necho "{}"');
  const result = await createCortex({ settings: settingsFor(binary) }).exec(['list'], { workspace: dir, timeoutMs: 300 });
  assert.equal(result.ok, false);
  assert.equal(result.stage, 'timeout');
  assert.match(result.error, /timed out/);
});

test('approval env vars are only set when the user granted them', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'echo "{\\"commands\\":\\"${CORTEX_APPROVE_COMMANDS-unset}\\",\\"recall\\":\\"${CORTEX_APPROVE_REMOTE_RECALL-unset}\\",\\"traj\\":\\"${CORTEX_APPROVE_TRAJECTORY-unset}\\",\\"color\\":\\"${NO_COLOR-unset}\\"}"');

  const denied = await createCortex({ settings: settingsFor(binary) }).exec(['config'], { workspace: dir });
  assert.deepEqual(denied.json, { commands: 'unset', recall: 'unset', traj: 'unset', color: '1' });

  const granted = await createCortex({
    settings: settingsFor(binary, { approvals: { commands: true, remoteRecall: true, trajectory: true } }),
  }).exec(['config'], { workspace: dir });
  assert.deepEqual(granted.json, { commands: '1', recall: '1', traj: '1', color: '1' });
});

test('streaming delivers stdout chunks as they arrive', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'echo "line one"; echo "line two"; echo "{}"');
  const chunks = [];
  await createCortex({ settings: settingsFor(binary) }).exec(['list'], {
    workspace: dir,
    onChunk: (which, text) => chunks.push([which, text]),
  });
  assert.ok(chunks.length >= 1);
  assert.ok(chunks.every(([which]) => which === 'stdout' || which === 'stderr'));
  assert.match(chunks.map(([, text]) => text).join(''), /line one/);
});

test('binary resolution prefers the configured path, then <workspace>/bin/cortex', () => {
  const dir = tempDir();
  fs.mkdirSync(path.join(dir, 'bin'));
  const binary = writeScript(dir, path.join('bin', 'cortex'), 'echo "{}"');

  const auto = createCortex({ settings: settingsFor('') });
  assert.equal(auto.resolveBinary(dir).path, binary);
  assert.equal(auto.resolveBinary(dir).source, 'workspace bin/');

  const explicit = createCortex({ settings: settingsFor(binary) });
  const resolved = explicit.resolveBinary(dir);
  assert.equal(resolved.source, 'settings');
  assert.equal(resolved.path, binary);
  assert.equal(explicit.candidates(dir)[0].path, binary);
});

test('exec puts -C before the subcommand — cobra rejects the other order', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'for a in "$@"; do printf "%s\\n" "$a"; done');
  const result = await createCortex({ settings: settingsFor(binary) }).exec(['--version'], { workspace: dir, json: false });
  const argv = (result.stdout ?? '').split('\n').filter(Boolean);
  assert.equal(argv[0], '-C');
  assert.equal(argv[1], dir);
  assert.equal(argv[2], '--version');
});

test('probe reports the version string from --version', async () => {
  const dir = tempDir();
  const binary = writeScript(dir, 'cortex', 'echo "cortex version v0.20.0 (commit abc123, built 2026-09-01)"');
  const probe = await createCortex({ settings: settingsFor(binary) }).probe(dir);
  assert.equal(probe.ok, true);
  assert.match(probe.version, /v0\.20\.0/);
});

test('envelopeView lifts the shared fields and tolerates non-envelope payloads', () => {
  const view = envelopeView({ envelope: { ok: true, taskId: 'task_1', summary: 's', facts: [1], actions: [], nextActions: [], rawAvailable: true } });
  assert.equal(view.taskId, 'task_1');
  assert.equal(view.rawAvailable, true);
  assert.deepEqual(view.facts, [1]);
  assert.equal(envelopeView({ json: null }), null);
  assert.equal(envelopeView(null), null);
});
