/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import { COMMANDS, DEV_TASKS, GROUPS, buildArgv, commandsByGroup, featureCatalog, flagKey, getCommand, quoteArg, renderArgv } from '../src/main/registry.js';

test('every command has a unique id, a group, and a non-empty argv path', () => {
  const ids = new Set();
  for (const command of COMMANDS) {
    assert.ok(command.id, 'command missing id');
    assert.ok(!ids.has(command.id), `duplicate command id: ${command.id}`);
    ids.add(command.id);
    assert.ok(Array.isArray(command.path) && command.path.length, `${command.id} has no argv path`);
    assert.ok(GROUPS.some((group) => group.id === command.group), `${command.id} references unknown group ${command.group}`);
    assert.ok(command.title, `${command.id} has no title`);
    assert.ok(command.summary, `${command.id} has no summary`);
    assert.ok(command.usage?.startsWith('cortex '), `${command.id} usage should start with "cortex "`);
    assert.ok(['read', 'mutate', 'dryrun', 'destructive', 'server'].includes(command.kind), `${command.id} has an unknown kind`);
  }
});

test('the registry covers every cortex subcommand in the CLI help tree', () => {
  // Derived from `cortex --help` (Available Commands) at v0.20.0. `help` is
  // cobra's own and `job run` is a hidden worker entry point, so both are out.
  const expected = [
    'abort', 'archive', 'begin-change', 'completion', 'config', 'coverage', 'decision', 'doctor',
    'dossier', 'finding', 'handoff', 'init', 'investigate', 'job', 'lease', 'list', 'metrics',
    'migrate', 'note', 'open', 'overview', 'plan', 'prune', 'read-artifact', 'read-evidence',
    'recall-cases', 'reindex-cases', 'remember', 'resolve', 'resume', 'review', 'rm', 'route',
    'serve', 'sessions', 'setup', 'show', 'start', 'status', 'timeline', 'unarchive', 'verify',
    'workplan',
  ];
  const roots = new Set(COMMANDS.map((command) => command.path[0]));
  for (const name of expected) assert.ok(roots.has(name), `registry is missing the "${name}" command`);
});

test('grouped subcommands are all present', () => {
  const paths = new Set(COMMANDS.map((command) => command.path.join(' ')));
  for (const path of [
    'decision answer', 'decision request', 'decision resume',
    'dossier add', 'dossier list', 'dossier refresh',
    'finding add', 'finding convert', 'finding dismiss', 'finding list', 'finding triage',
    'job cancel', 'job list',
    'lease release', 'lease renew',
    'workplan add', 'workplan list', 'workplan next',
  ]) {
    assert.ok(paths.has(path), `registry is missing "${path}"`);
  }
});

test('related references resolve to real commands', () => {
  for (const command of COMMANDS) {
    for (const related of command.related ?? []) {
      assert.ok(getCommand(related), `${command.id} relates to unknown command ${related}`);
    }
  }
});

test('buildArgv renders positionals, enums, booleans, and repeatable flags', () => {
  const open = getCommand('open');
  const argv = buildArgv(open, {
    goal: 'Fix the redirect',
    mode: 'investigate',
    surface: ['code', 'browser'],
    actor: 'agent-auth',
    'idempotency-key': 'checkout-redirect',
    criterion: ['a=first', 'b=second'],
  });
  assert.deepEqual(argv, [
    'open', 'Fix the redirect',
    '--mode', 'investigate',
    '--surface', 'code', '--surface', 'browser',
    '--actor', 'agent-auth',
    '--idempotency-key', 'checkout-redirect',
    '--criterion', 'a=first', '--criterion', 'b=second',
  ]);
});

test('buildArgv drops empty values and omits false booleans', () => {
  const verify = getCommand('verify');
  const argv = buildArgv(verify, {
    taskId: 'task_1',
    claim: ['a claim'],
    'claim-surface': [],
    'ack-drift': false,
    'no-op': true,
    actor: '',
  });
  assert.deepEqual(argv, ['verify', 'task_1', '--claim', 'a claim', '--no-op']);
});

test('buildArgv refuses to run a command missing a required argument', () => {
  const plan = getCommand('plan');
  assert.throws(() => buildArgv(plan, { hypothesis: ['x :: y'] }), /missing required argument/i);
});

test('nested subcommand argv keeps its two-word path', () => {
  const argv = buildArgv(getCommand('finding.dismiss'), { taskId: 'task_1', findingId: 'fnd_2', reason: 'not a bug', actor: 'me' });
  assert.deepEqual(argv, ['finding', 'dismiss', 'task_1', 'fnd_2', '--actor', 'me', '--reason', 'not a bug']);
});

test('renderArgv quotes shell-hostile values and mirrors the real argv order', () => {
  const line = renderArgv(getCommand('note'), { taskId: 'task_1', observation: "it's broken; rm -rf /" }, { workspace: '/tmp/ws' });
  assert.ok(line.startsWith('cortex -C /tmp/ws note task_1 '), line);
  assert.ok(line.includes(`'it'\\''s broken; rm -rf /'`), line);
  assert.ok(line.endsWith('--json'), line);
});

test('renderArgv omits --json for commands that do not emit an envelope', () => {
  const line = renderArgv(getCommand('completion'), { shell: 'zsh' }, { workspace: '/tmp/ws' });
  assert.equal(line, 'cortex -C /tmp/ws completion zsh');
});

test('quoteArg leaves safe tokens alone and single-quotes everything else', () => {
  assert.equal(quoteArg('task_06FK'), 'task_06FK');
  assert.equal(quoteArg('specs/a.yml'), 'specs/a.yml');
  assert.equal(quoteArg('a b'), `'a b'`);
  assert.equal(quoteArg(''), `''`);
  assert.equal(quoteArg(`it's`), `'it'\\''s'`);
});

test('flagKey strips leading dashes for value keys', () => {
  assert.equal(flagKey('--claim-id'), 'claim-id');
  assert.equal(flagKey('-o'), 'o');
  assert.equal(flagKey('plain'), 'plain');
});

test('featureCatalog returns every command under its documented group', () => {
  const catalog = featureCatalog();
  assert.equal(catalog.length, GROUPS.length);
  const total = catalog.reduce((sum, group) => sum + group.features.length, 0);
  assert.equal(total, COMMANDS.length);
  for (const group of catalog) {
    for (const feature of group.features) {
      assert.equal(feature.id, getCommand(feature.id).id);
      assert.ok(typeof feature.flags === 'number');
    }
  }
});

test('commandsByGroup preserves group order and drops empty groups', () => {
  const grouped = commandsByGroup();
  assert.equal(grouped.length, GROUPS.length);
  const ids = grouped.flatMap((group) => group.commands.map((command) => command.id));
  assert.equal(new Set(ids).size, ids.length);
});

test('destructive and dry-run commands are marked so the UI can confirm them', () => {
  assert.equal(getCommand('rm').kind, 'destructive');
  assert.ok(getCommand('rm').confirm);
  assert.equal(getCommand('prune').kind, 'dryrun');
  assert.equal(getCommand('migrate').kind, 'dryrun');
  assert.equal(getCommand('archive').kind, 'mutate');
  assert.equal(getCommand('status').kind, 'read');
  assert.equal(getCommand('serve').kind, 'server');
});

test('read-only commands all support --json except the completion script', () => {
  for (const command of COMMANDS.filter((c) => c.kind === 'read')) {
    if (command.id === 'completion') continue;
    assert.equal(command.json, true, `${command.id} should support --json`);
  }
});

test('the loop steps are numbered open → remember', () => {
  const steps = COMMANDS.filter((command) => command.loopStep).sort((a, b) => a.loopStep - b.loopStep);
  assert.deepEqual(steps.map((s) => s.id), ['open', 'investigate', 'plan', 'begin-change', 'verify', 'remember']);
});

test('dev tasks are curated and unique', () => {
  const ids = new Set();
  for (const task of DEV_TASKS) {
    assert.ok(!ids.has(task.id), `duplicate dev task ${task.id}`);
    ids.add(task.id);
    assert.ok(task.cmd && Array.isArray(task.args), `${task.id} is not a fixed argv`);
    assert.ok(['read', 'mutate', 'server'].includes(task.kind), `${task.id} has an unknown kind`);
  }
  assert.ok(ids.has('check') && ids.has('flows') && ids.has('eval'));
});
