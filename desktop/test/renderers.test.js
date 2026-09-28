/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom } from './helpers/mini-dom.js';

installDom();
globalThis.document.getElementById = () => null;

const {
  arrayOf, coverageView, evidenceCard, evidenceList, findingsTable, firstArray, flat, hypothesesList,
  loopDiagram, pick, receiptsList, scopePanel, sessionRow, timelineFeed, toolHealthTable, workplanView,
} = await import('../src/renderer/lib/renderers.js');

const count = (node, selector) => node.querySelectorAll(selector).length;
const text = (node) => node.textContent;

test('pick returns the first present key and skips empties', () => {
  assert.equal(pick({ a: '', b: 'x' }, 'a', 'b'), 'x');
  assert.equal(pick({ a: null, b: 0 }, 'a', 'b'), 0, 'zero is a value');
  assert.equal(pick({ a: false }, 'a'), false);
  assert.equal(pick(null, 'a'), undefined);
  assert.equal(pick({ b: 1 }, 'a'), undefined);
});

test('firstArray tolerates nested paths and non-arrays', () => {
  assert.deepEqual(firstArray({ a: [1, 2] }, 'missing', 'a'), [1, 2]);
  assert.deepEqual(firstArray({ a: { b: [3] } }, 'a.b'), [3]);
  assert.deepEqual(firstArray({ a: 'nope' }, 'a'), []);
  assert.deepEqual(firstArray(null, 'a'), []);
});

test('arrayOf wraps single objects and passes arrays through', () => {
  assert.deepEqual(arrayOf([1, 2]), [1, 2]);
  assert.deepEqual(arrayOf({ a: 1 }), [{ a: 1 }]);
  assert.deepEqual(arrayOf(null), []);
});

test('sessionRow normalizes the several spellings cortex uses', () => {
  const row = sessionRow({ id: 'task_1', title: 'fix it', repository: 'cortex', state: 'verifying', mode: 'change', outcome: 'verified', updatedAt: '2026-09-01T00:00:00Z', stale: true });
  assert.equal(row.taskId, 'task_1');
  assert.equal(row.goal, 'fix it');
  assert.equal(row.repo, 'cortex');
  assert.equal(row.phase, 'verifying');
  assert.equal(row.assessment, 'verified');
  assert.equal(row.stale, true);

  const alt = sessionRow({ taskId: 'task_2', goal: 'g', repo: 'r', phase: 'planned', assessment: 'partial' });
  assert.equal(alt.taskId, 'task_2');
  assert.equal(alt.phase, 'planned');
  assert.equal(alt.assessment, 'partial');
});

test('loopDiagram marks the current step and completes everything after a terminal phase', () => {
  const changing = loopDiagram('changing');
  const steps = changing.children;
  assert.equal(steps.length, 6);
  assert.equal(steps[3].dataset.state, 'current');
  assert.equal(steps[0].dataset.state, 'done');
  assert.equal(steps[5].dataset.state, '');

  const done = loopDiagram('complete');
  assert.ok(done.children.every((step) => step.dataset.state === 'done'));

  const unknown = loopDiagram('nonsense');
  assert.ok(unknown.children.every((step) => step.dataset.state === ''));
});

test('evidenceList renders claims with confidence, source, and sensitivity', () => {
  const list = evidenceList([
    { id: 'ev_1', claim: 'the callback drops returnTo', confidence: 'high', kind: 'code_location', source: 'codemap' },
    { id: 'ev_2', claim: 'secret shape', sensitive: true, confidence: 'low' },
  ]);
  assert.equal(count(list, 'div.evidence-item'), 2);
  assert.match(text(list), /the callback drops returnTo/);
  assert.match(text(list), /codemap/);
  assert.match(text(list), /sensitive/);
});

test('evidenceList explains why an empty ledger is not an error', () => {
  const list = evidenceList([]);
  assert.match(text(list), /No evidence yet/);
});

test('receiptsList colors each receipt by its status', () => {
  const list = receiptsList([
    { claim: 'browser returns to checkout', status: 'passed', surface: 'browser', verifier: 'cairntrace', contract: 'specs/x.yml', bound: true },
    { claim: 'unit tests pass', status: 'failed', surface: 'code' },
    { claim: 'no verifier for this', status: 'not_run' },
  ]);
  const receipts = list.querySelectorAll('div.receipt');
  assert.deepEqual(receipts.map((r) => r.dataset.status), ['passed', 'failed', 'not_run']);
  assert.match(text(list), /specs\/x\.yml/);
  assert.match(text(list), /bound/);
});

test('hypothesesList flags a missing disproof path as a gate failure', () => {
  const list = hypothesesList([
    { id: 'hyp_1', statement: 'returnTo is dropped', disproofPath: 'run the browser flow', status: 'open', support: ['ev_1'] },
    { id: 'hyp_2', statement: 'no disproof here', status: 'open' },
  ]);
  assert.match(text(list), /run the browser flow/);
  assert.match(text(list), /no disproof path — the planning gate rejects this/);
  assert.match(text(list), /ev_1/);
  assert.equal(count(list, 'div.hypothesis'), 2);
});

test('findingsTable renders status, severity, and the triage actions', () => {
  const tableNode = findingsTable([
    { id: 'fnd_1', title: 'Lease can expire mid-edit', kind: 'bug', severity: 'high', status: 'open', files: ['internal/kernel/lease.go'] },
  ], { onAction: () => {} });
  assert.match(text(tableNode), /Lease can expire mid-edit/);
  assert.match(text(tableNode), /internal\/kernel\/lease\.go/);
  assert.equal(count(tableNode, 'button'), 3, 'triage / convert / dismiss');
});

test('coverageView derives explored vs unseen totals from the ledger rows', () => {
  const view = coverageView({
    totals: { modules: 4 },
    modules: [
      { module: 'internal/kernel', state: 'explored', rounds: 3, evidence: 7 },
      { module: 'internal/adapters', state: 'summarized', rounds: 1, evidence: 2 },
      { module: 'cmd/cortex', state: 'unseen' },
      { module: 'docs', state: 'unseen' },
    ],
  });
  assert.match(text(view), /50%/, 'two of four modules explored');
  assert.match(text(view), /internal\/kernel/);
});

test('coverageView explains that only survey cases build a ledger', () => {
  assert.match(text(coverageView({})), /No coverage ledger/);
});

test('workplanView shows dependencies and links the child case', () => {
  const view = workplanView({ items: [{ id: 'wp_1', goal: 'harden leases', state: 'ready', after: ['wp_0'] }, { id: 'wp_2', goal: 'child work', state: 'claimed', childTaskId: 'task_9', actor: 'agent-a' }] }, { onOpen: () => {} });
  assert.match(text(view), /harden leases/);
  assert.match(text(view), /wp_0/);
  assert.match(text(view), /task_9/);
  assert.match(text(view), /agent-a/);
  assert.equal(workplanView({}).textContent.includes('No campaign items'), true);
});

test('toolHealthTable reports degraded adapters without inventing health', () => {
  const view = toolHealthTable([
    { tool: 'codemap', status: 'ok', version: 'v1.2.3', durationMs: 42 },
    { tool: 'vecgrep', status: 'missing', detail: 'binary not on PATH' },
  ]);
  assert.match(text(view), /codemap/);
  assert.match(text(view), /missing/);
  assert.match(text(view), /binary not on PATH/);
  assert.match(text(toolHealthTable([])), /No adapter health reported/);
});

test('timelineFeed renders every kind and filters by it', () => {
  const entries = [
    { kind: 'phase', at: '2026-09-01T10:00:00Z', phase: 'planned' },
    { kind: 'evidence', at: '2026-09-01T10:01:00Z', claim: 'found the callback', confidence: 'high' },
    { kind: 'command', at: '2026-09-01T10:02:00Z', tool: 'codemap', argv: 'codemap impact x' },
    { kind: 'verification', at: '2026-09-01T10:03:00Z', status: 'passed' },
  ];
  const all = timelineFeed(entries);
  assert.equal(count(all, 'div.tl-item'), 4);
  assert.match(text(all), /found the callback/);
  assert.match(text(all), /codemap/);

  const onlyEvidence = timelineFeed(entries, { filter: 'evidence' });
  assert.equal(count(onlyEvidence, 'div.tl-item'), 1);
  assert.equal(timelineFeed([]).textContent.includes('No activity yet'), true);
});

test('scopePanel shows the declared boundary and anything outside it', () => {
  const view = scopePanel({ drifted: true, boundary: { files: ['internal/kernel/verify.go'] }, unexpected: ['docs/index.md'] });
  assert.match(text(view), /declared boundary/);
  assert.match(text(view), /internal\/kernel\/verify\.go/);
  assert.match(text(view), /outside the boundary/);
  assert.match(text(view), /docs\/index\.md/);
  assert.equal(scopePanel(null), null);
});

test('evidenceCard exposes the full id and timestamp in tooltips', () => {
  const cardNode = evidenceCard({ id: 'ev_01JXXXXXXXXX', claim: 'c', confidence: 'medium', at: '2026-09-01T10:00:00Z', uri: 'file:///x/y.go' });
  assert.match(text(cardNode), /ev_01JXXXXXXXXX/);
  assert.match(text(cardNode), /y\.go/);
});

test('flat unwraps the one-level objects cortex nests into strings', () => {
  assert.equal(flat('plain'), 'plain');
  assert.equal(flat({ note: 'run the browser flow' }), 'run the browser flow');
  assert.equal(flat({ tool: 'git' }), 'git');
  assert.equal(flat(0), '0');
  assert.equal(flat(false), 'false');
  assert.equal(flat(['a', { tool: 'b' }]), 'a, b');
  assert.equal(flat(null), '');
  assert.equal(flat(undefined), '');
});

test('firstArray passes bare arrays through — timeline and sessions are arrays', () => {
  const rows = [{ kind: 'phase' }, { kind: 'evidence' }];
  assert.equal(firstArray(rows, 'events'), rows);
  assert.deepEqual(firstArray({ events: rows }, 'events'), rows);
  assert.deepEqual(firstArray({ nothing: 1 }, 'events'), []);
});

test('sessionRow reads the real sessions projection (id, repository, verificationOutcome)', () => {
  const row = sessionRow({
    id: 'task_06G6XMSSA4Y0YK65',
    goal: 'Auditar Teak',
    phase: 'verifying',
    mode: 'survey',
    repository: 'teak',
    workspace: '/Users/x/projects/teak',
    createdAt: '2026-09-04T23:48:51Z',
    updatedAt: '2026-09-05T00:44:13Z',
    verified: 0,
    required: 1,
    verificationOutcome: 'unverified',
    active: true,
  });
  assert.equal(row.taskId, 'task_06G6XMSSA4Y0YK65');
  assert.equal(row.repo, 'teak');
  assert.equal(row.assessment, 'unverified');
  assert.equal(row.verified, 0);
  assert.equal(row.required, 1);
  assert.equal(row.active, true);
  assert.equal(row.workspace, '/Users/x/projects/teak');
});

test('evidenceCard handles object-valued source and offers a raw preview from rawRef', () => {
  let previewed = null;
  const cardNode = evidenceCard(
    { id: 'ev_1', claim: 'workspace dirty', source: { tool: 'git' }, rawRef: 'case://task_1/raw/raw_9', confidence: 'high' },
    { onPreview: (e) => { previewed = e.rawRef; } },
  );
  assert.match(text(cardNode), /git/, 'object source must flatten to the tool name');
  assert.match(text(cardNode), /case:\/\/task_1\/raw\/raw_9/);
  const rawButton = cardNode.querySelectorAll('button').find((b) => b.textContent === 'Raw');
  assert.ok(rawButton, 'expected a Raw preview button');
  rawButton.dispatchEvent({ type: 'click' });
  assert.equal(previewed, 'case://task_1/raw/raw_9');
});

test('hypothesisCard reads disproveBy.note from the real show projection', () => {
  const list = hypothesesList([{
    id: 'hyp_1',
    statement: 'route gating is complete',
    supports: ['ev_1'],
    confidence: 'high',
    disproveBy: { note: 'producer golden fails adapter parsing' },
    status: 'active',
  }]);
  assert.match(text(list), /producer golden fails adapter parsing/);
  assert.equal(text(list).includes('[object Object]'), false);
});

test('renderers never throw on partially-populated payloads', () => {
  const payloads = [null, undefined, {}, [], { items: null }, { receipts: [{}] }, { hypotheses: [{}] }, { modules: [{}] }];
  for (const payload of payloads) {
    assert.doesNotThrow(() => evidenceList(firstArray(payload, 'items')));
    assert.doesNotThrow(() => receiptsList(firstArray(payload, 'receipts')));
    assert.doesNotThrow(() => hypothesesList(firstArray(payload, 'hypotheses')));
    assert.doesNotThrow(() => coverageView(payload ?? {}));
    assert.doesNotThrow(() => workplanView(payload ?? {}));
    assert.doesNotThrow(() => findingsTable(firstArray(payload, 'items')));
    assert.doesNotThrow(() => toolHealthTable(firstArray(payload, 'items')));
    assert.doesNotThrow(() => timelineFeed(firstArray(payload, 'items')));
  }
});
