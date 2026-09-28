/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import {
  absoluteTime, basename, bytes, compactHome, countLabel, deepGet, duration, groupBy,
  humanDuration, isTerminal, matches, percent, relevance, relativeTime, shortId, shortTime, sortBy,
  titleCase, toneFor, toMillis, truncate,
} from '../src/renderer/lib/format.js';

const NOW = Date.parse('2026-09-28T12:00:00Z');

test('relativeTime renders compact offsets in both directions', () => {
  assert.equal(relativeTime(NOW - 500, NOW), 'just now');
  assert.equal(relativeTime(NOW - 45_000, NOW), '45s ago');
  assert.equal(relativeTime(NOW - 5 * 60_000, NOW), '5m ago');
  assert.equal(relativeTime(NOW - 3 * 3_600_000, NOW), '3h ago');
  assert.equal(relativeTime(NOW - 2 * 86_400_000, NOW), '2d ago');
  assert.equal(relativeTime(NOW + 2 * 3_600_000, NOW), 'in 2h');
  assert.equal(relativeTime('', NOW), '');
  assert.equal(relativeTime(null, NOW), '');
});

test('toMillis accepts RFC3339, epoch seconds, and epoch millis', () => {
  assert.equal(toMillis('2026-09-28T12:00:00Z'), NOW);
  assert.equal(toMillis(NOW), NOW);
  assert.equal(toMillis(Math.floor(NOW / 1000)), NOW);
  assert.equal(toMillis('nonsense'), null);
  assert.equal(toMillis(undefined), null);
});

test('absoluteTime and shortTime are stable, locale-independent formats', () => {
  const iso = new Date(NOW).toISOString();
  assert.match(absoluteTime(iso), /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
  assert.match(shortTime(iso), /^\d{2}-\d{2} \d{2}:\d{2}$/);
  assert.equal(absoluteTime(''), '');
});

test('duration formats millis for human reading', () => {
  assert.equal(duration(42), '42ms');
  assert.equal(duration(1500), '1.50s');
  assert.equal(duration(45_000), '45.0s');
  assert.equal(duration(125_000), '2m 5s');
  assert.equal(duration(3_720_000), '1h 2m');
  assert.equal(duration(NaN), '');
});

test('humanDuration parses Go duration strings', () => {
  assert.equal(humanDuration('5m0s'), '5m');
  assert.equal(humanDuration('24h0m0s'), '24h');
  assert.equal(humanDuration('1h30m0s'), '1h 30m');
  assert.equal(humanDuration('45s'), '45s');
  assert.equal(humanDuration('0.25s'), '0.25s');
  assert.equal(humanDuration(''), '');
  assert.equal(humanDuration('weird'), 'weird');
});

test('bytes and percent stay compact', () => {
  assert.equal(bytes(512), '512 B');
  assert.equal(bytes(2048), '2.0 KiB');
  assert.equal(bytes(5 * 1024 * 1024), '5.0 MiB');
  assert.equal(bytes('nope'), '');
  assert.equal(percent(0.5), '50%');
  assert.equal(percent(0.5321, 1), '53.2%');
  assert.equal(percent(73), '73%');
  assert.equal(percent(undefined), '');
});

test('truncate and shortId keep identifiers recognizable', () => {
  assert.equal(truncate('abcdef', 4), 'abc…');
  assert.equal(truncate('abc', 4), 'abc');
  assert.equal(truncate(null), '');
  assert.equal(shortId('task_01JXXXXXXXXXXXXXXXX', 10, 4), 'task_01JXX…XXXX');
  assert.equal(shortId('short', 10, 4), 'short');
});

test('toneFor maps the kernel vocabulary consistently', () => {
  assert.equal(toneFor('verified'), 'green');
  assert.equal(toneFor('PASSED'), 'green');
  assert.equal(toneFor('failed'), 'red');
  assert.equal(toneFor('not_run'), 'slate');
  assert.equal(toneFor('inconclusive'), 'amber');
  assert.equal(toneFor('changing'), 'amber');
  assert.equal(toneFor('planned'), 'violet');
  assert.equal(toneFor('verifying'), 'accent');
  assert.equal(toneFor('something-new'), 'slate');
  assert.equal(toneFor(null, 'info'), 'info');
});

test('isTerminal knows which states end the loop', () => {
  assert.equal(isTerminal('complete'), true);
  assert.equal(isTerminal('abandoned'), true);
  assert.equal(isTerminal('blocked'), true);
  assert.equal(isTerminal('investigating'), false);
  assert.equal(isTerminal(undefined), false);
});

test('matches requires every term, relevance ranks prefix over substring', () => {
  assert.equal(matches('verify browser', 'verify the browser claim', ''), true);
  assert.equal(matches('verify terminal', 'verify the browser claim'), false);
  assert.equal(matches('', 'anything'), true);
  assert.ok(relevance('stat', 'status') > relevance('stat', 'cortex status check'));
  assert.ok(relevance('status', 'status') > relevance('status', 'statusx'));
  assert.equal(relevance('zzz', 'status'), -1);
});

test('titleCase, basename, compactHome, countLabel', () => {
  assert.equal(titleCase('verification_receipts'), 'Verification Receipts');
  assert.equal(basename('/a/b/specs/flow.yml'), 'flow.yml');
  assert.equal(basename('plain'), 'plain');
  assert.equal(compactHome('/Users/someone/projects/cortex'), '~/projects/cortex');
  assert.equal(compactHome('/opt/cortex'), '/opt/cortex');
  assert.equal(countLabel(1, 'session'), '1 session');
  assert.equal(countLabel(3, 'session'), '3 sessions');
  assert.equal(countLabel(2, 'criterion', 'criteria'), '2 criteria');
});

test('deepGet, sortBy, and groupBy behave on missing data', () => {
  assert.equal(deepGet({ a: { b: 2 } }, 'a.b'), 2);
  assert.equal(deepGet({ a: null }, 'a.b'), undefined);
  assert.deepEqual(sortBy([{ n: 1 }, { n: 3 }, { n: 2 }], (x) => x.n).map((x) => x.n), [3, 2, 1]);
  assert.deepEqual(sortBy([{ n: 1 }, { n: 3 }], (x) => x.n, 'asc').map((x) => x.n), [1, 3]);
  assert.deepEqual(sortBy([{ n: null }, { n: 1 }], (x) => x.n).map((x) => x.n), [1, null]);
  const grouped = groupBy([{ k: 'a', v: 1 }, { k: 'b' }, { k: 'a', v: 2 }], (x) => x.k);
  assert.equal(grouped.get('a').length, 2);
  assert.equal(grouped.get('b').length, 1);
});
