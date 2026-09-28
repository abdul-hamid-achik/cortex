/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { CASE_FILES, createCaseStore, parseJsonl } from '../src/main/casestore.js';

function makeStore() {
  const sessionsRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'deck-store-'));
  const archiveRoot = path.join(path.dirname(sessionsRoot), 'archive');
  fs.mkdirSync(archiveRoot, { recursive: true });

  const taskDir = path.join(sessionsRoot, 'cortex', 'task_01');
  fs.mkdirSync(path.join(taskDir, 'raw'), { recursive: true });
  fs.writeFileSync(path.join(taskDir, 'case.json'), `${JSON.stringify({ id: 'task_01', phase: 'verifying', goal: 'fix it', workspace: sessionsRoot })}\n`);
  fs.writeFileSync(path.join(taskDir, 'evidence.jsonl'), [
    JSON.stringify({ id: 'ev_1', claim: 'first', confidence: 'high' }),
    JSON.stringify({ id: 'ev_2', claim: 'second', confidence: 'low' }),
    '{ this line is corrupt',
    '',
  ].join('\n'));
  fs.writeFileSync(path.join(taskDir, 'summary.md'), '# Outcome\n\nIt worked.\n');
  fs.writeFileSync(path.join(taskDir, 'raw', 'tool-output.json'), '{"raw":true}');

  const archivedDir = path.join(archiveRoot, 'cortex', 'task_99');
  fs.mkdirSync(archivedDir, { recursive: true });
  fs.writeFileSync(path.join(archivedDir, 'case.json'), JSON.stringify({ id: 'task_99', phase: 'complete' }));

  return { sessionsRoot, archiveRoot, taskDir, store: createCaseStore({ getRoots: () => ({ sessionsRoot, archiveRoot }) }) };
}

test('listRepos reports every repository slug with its session count', () => {
  const { store } = makeStore();
  const repos = store.listRepos();
  assert.equal(repos.length, 1);
  assert.equal(repos[0].slug, 'cortex');
  assert.equal(repos[0].sessions, 1);
});

test('listRepos can include the archive tree', () => {
  const { store } = makeStore();
  const withArchive = store.listRepos({ includeArchive: true });
  assert.ok(withArchive.some((repo) => repo.archived === true));
});

test('listSessions returns task directories newest first', () => {
  const { store } = makeStore();
  const sessions = store.listSessions('cortex');
  assert.equal(sessions.length, 1);
  assert.equal(sessions[0].taskId, 'task_01');
});

test('locate finds a task by id across repositories and marks archived ones', () => {
  const { store } = makeStore();
  const active = store.locate('task_01');
  assert.equal(active.repo, 'cortex');
  assert.equal(active.archived, false);

  const archived = store.locate('task_99');
  assert.equal(archived.archived, true);

  assert.equal(store.locate('task_nope'), null);
});

test('locate rejects ids that could escape the store', () => {
  const { store } = makeStore();
  assert.equal(store.locate('../../etc/passwd'), null);
  assert.equal(store.locate('/absolute/path'), null);
  assert.equal(store.locate(''), null);
  assert.equal(store.locate(null), null);
});

test('listFiles annotates known case files and walks raw/', () => {
  const { store } = makeStore();
  const { found, files } = store.listFiles('task_01');
  assert.equal(found.taskId, 'task_01');
  const names = files.map((file) => file.name);
  assert.ok(names.includes('case.json'));
  assert.ok(names.includes('evidence.jsonl'));
  assert.ok(names.includes('summary.md'));
  assert.ok(names.some((name) => name.startsWith('raw/')));
  const caseFile = files.find((file) => file.name === 'case.json');
  assert.equal(caseFile.meta.kind, 'snapshot');
  assert.ok(caseFile.meta.summary.length > 10);
});

test('readFileBounded parses snapshots, ledgers, and markdown separately', () => {
  const { store } = makeStore();

  const snapshot = store.readFileBounded('task_01', 'case.json');
  assert.equal(snapshot.json.phase, 'verifying');
  assert.equal(snapshot.truncated, false);

  const ledger = store.readFileBounded('task_01', 'evidence.jsonl');
  assert.equal(ledger.records.records.length, 2);
  assert.equal(ledger.records.corrupt.length, 1);
  assert.equal(ledger.records.corrupt[0].line, 3);

  const text = store.readFileBounded('task_01', 'summary.md');
  assert.match(text.text, /^# Outcome/);
  assert.equal(text.json, undefined);
});

test('readFileBounded refuses traversal, directories, and unknown files', () => {
  const { store } = makeStore();
  assert.match(store.readFileBounded('task_01', '../task_02/case.json').error, /invalid file name/);
  assert.match(store.readFileBounded('task_01', '/etc/passwd').error, /invalid file name/);
  assert.match(store.readFileBounded('task_01', 'raw').error, /is a directory/);
  assert.match(store.readFileBounded('task_01', 'nope.json').error, /no such file/);
  assert.match(store.readFileBounded('task_missing', 'case.json').error, /not found/);
});

test('readFileBounded truncates at the requested bound and says so', () => {
  const { store } = makeStore();
  const result = store.readFileBounded('task_01', 'case.json', 12);
  assert.equal(result.truncated, true);
  assert.equal(result.text.length, 12);
  assert.ok(result.parseError, 'a truncated snapshot cannot parse');
});

test('safeJoin refuses paths that leave the root', () => {
  const { store, sessionsRoot } = makeStore();
  assert.equal(store.safeJoin(sessionsRoot, 'cortex', 'task_01'), path.join(sessionsRoot, 'cortex', 'task_01'));
  assert.throws(() => store.safeJoin(sessionsRoot, '..', '..', 'etc'), /escapes the case store/);
});

test('parseJsonl keeps corrupt lines visible instead of dropping them', () => {
  const parsed = parseJsonl('{"a":1}\nnot json\n\n{"b":2}\n');
  assert.equal(parsed.records.length, 2);
  assert.deepEqual(parsed.records[0].value, { a: 1 });
  assert.equal(parsed.records[0].line, 1);
  assert.equal(parsed.corrupt.length, 1);
  assert.equal(parsed.corrupt[0].preview, 'not json');
});

test('parseJsonl respects its record bound', () => {
  const lines = Array.from({ length: 20 }, (_, i) => JSON.stringify({ i })).join('\n');
  const parsed = parseJsonl(lines, 5);
  assert.equal(parsed.records.length, 5);
  assert.equal(parsed.truncated, true);
});

test('the documented case-file inventory matches docs/case-file.md', () => {
  const names = CASE_FILES.map((file) => file.name);
  for (const expected of ['case.json', 'plan.json', 'hypotheses.json', 'verification.json', 'decisions.json', 'evidence.jsonl', 'commands.jsonl', 'phases.jsonl', 'summary.md']) {
    assert.ok(names.includes(expected), `CASE_FILES is missing ${expected}`);
  }
  assert.equal(new Set(names).size, names.length, 'duplicate case file entries');
  for (const file of CASE_FILES) {
    assert.ok(['snapshot', 'ledger', 'text'].includes(file.kind), `${file.name} has an unknown kind`);
    assert.ok(file.summary.length > 8, `${file.name} needs a summary`);
  }
});

test('an unconfigured store degrades to empty instead of throwing', () => {
  const store = createCaseStore({ getRoots: () => ({}) });
  assert.deepEqual(store.listRepos(), []);
  assert.deepEqual(store.listSessions('anything'), []);
  assert.equal(store.locate('task_01'), null);
  assert.equal(store.sessionsRoot(), '');
});
