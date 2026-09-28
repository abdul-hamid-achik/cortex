/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom } from './helpers/mini-dom.js';

const dom = installDom();
globalThis.document = dom.document;

const { clear, debounce, fragment, h, on, render } = await import('../src/renderer/lib/dom.js');
const { icon, ICON_NAMES } = await import('../src/renderer/lib/icons.js');
const { badge, card, empty, kv, stat, table, tabs } = await import('../src/renderer/lib/ui.js');
const { jsonView } = await import('../src/renderer/lib/jsonview.js');

const text = (node) => node.textContent;
const count = (node, selector) => node.querySelectorAll(selector).length;

test('h builds plain elements and applies props', () => {
  const node = h('section', { class: ['card', null, 'x'], dataset: { tone: 'accent' }, title: 'tip' },
    h('span', { text: 'hello' }),
    'plain',
  );
  assert.equal(node.localName, 'section');
  assert.equal(node.getAttribute('class'), 'card x');
  assert.equal(node.dataset.tone, 'accent');
  assert.equal(node.getAttribute('title'), 'tip');
  assert.equal(text(node), 'helloplain');
});

test('h creates real SVG elements for svg and svg:child tags', () => {
  const svg = h('svg', { width: 16, viewBox: '0 0 24 24' }, h('svg:circle', { cx: 12 }), h('svg:g', {}));
  assert.equal(svg.localName, 'svg');
  assert.equal(svg.namespaceURI, 'http://www.w3.org/2000/svg');
  assert.deepEqual(svg.children.map((child) => child.localName), ['circle', 'g']);
  assert.equal(svg.children[0].namespaceURI, 'http://www.w3.org/2000/svg');
});

test('every icon in the set builds without an empty qualified name', () => {
  for (const name of ICON_NAMES) {
    const node = icon(name, 16);
    assert.equal(node.localName, 'svg', `${name} did not build an <svg>`);
    assert.equal(node.getAttribute('width'), '16');
    assert.ok(node.children.length >= 1, `${name} has no drawing group`);
  }
  assert.ok(ICON_NAMES.length >= 40, `expected a full icon set, got ${ICON_NAMES.length}`);
  assert.equal(icon('not-a-real-icon').localName, 'svg', 'unknown icons fall back instead of throwing');
});

test('h ignores null, undefined, and boolean children', () => {
  const node = h('div', {}, null, undefined, false, true, h('b', { text: 'kept' }));
  assert.equal(node.childNodes.length, 1);
  assert.equal(text(node), 'kept');
});

test('h supports value/checked/disabled and event props', () => {
  let clicks = 0;
  const input = h('input', { value: 'abc', checked: true, disabled: false, onclick: () => { clicks += 1; } });
  assert.equal(input.value, 'abc');
  assert.equal(input.checked, true);
  assert.equal(input.disabled, false);
  input.dispatchEvent({ type: 'click' });
  assert.equal(clicks, 1);
});

test('style props go through the CSSOM, including custom properties', () => {
  const node = h('div', { style: { width: '40%', '--phase': 'var(--accent)', gap: 8 } });
  assert.equal(node.style.width, '40%');
  assert.equal(node.style.getPropertyValue('--phase'), 'var(--accent)');
});

test('render replaces children and clear empties them', () => {
  const node = h('div', {}, h('span', { text: 'old' }));
  render(node, h('span', { text: 'new' }));
  assert.equal(text(node), 'new');
  clear(node);
  assert.equal(node.childNodes.length, 0);
});

test('fragment groups children without a wrapper element', () => {
  const frag = fragment(h('i', { text: 'a' }), h('i', { text: 'b' }));
  const host = h('div', {}, frag);
  assert.equal(count(host, 'i'), 2);
});

test('on() returns an unsubscribe function', () => {
  const node = h('div');
  let hits = 0;
  const off = on(node, 'ping', () => { hits += 1; });
  node.dispatchEvent({ type: 'ping' });
  off();
  node.dispatchEvent({ type: 'ping' });
  assert.equal(hits, 1);
});

test('debounce collapses bursts into one call', async () => {
  let calls = 0;
  const fn = debounce(() => { calls += 1; }, 20);
  fn(); fn(); fn();
  assert.equal(calls, 0);
  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(calls, 1);
});

test('badge picks its tone and renders nothing for empty values', () => {
  assert.equal(text(badge('verified', 'green')), 'verified');
  assert.equal(badge(''), null);
  assert.equal(badge(null), null);
  assert.equal(badge('x', 'slate', { dot: false }).querySelectorAll('span.badge-dot').length, 0);
});

test('card composes head, body, and footer', () => {
  const node = card({ title: 'Title', sub: 'Sub', iconName: 'shield', actions: [h('button', { text: 'Go' })], foot: h('span', { text: 'foot' }) },
    h('p', { text: 'body' }));
  assert.match(text(node), /Title/);
  assert.match(text(node), /Sub/);
  assert.match(text(node), /body/);
  assert.match(text(node), /foot/);
  assert.equal(count(node, 'button'), 1);
});

test('table renders headers, rows, and the empty state', () => {
  const columns = [
    { key: 'name', label: 'Name', strong: true },
    { key: 'n', label: 'N', align: 'right', render: (row) => String(row.n) },
  ];
  const node = table({ columns, rows: [{ name: 'a', n: 1 }, { name: 'b', n: 2 }], rowKey: (row) => row.name });
  assert.equal(count(node, 'th'), 2);
  assert.equal(count(node, 'tr'), 3, 'header + two body rows');
  assert.match(text(node), /a1b2/);

  const emptyTable = table({ columns, rows: [], empty: empty({ title: 'Nothing' }) });
  assert.match(text(emptyTable), /Nothing/);
});

test('kv skips undefined values and reports an empty state', () => {
  const node = kv([['a', '1'], ['b', undefined], ['c', '3']]);
  assert.equal(count(node, 'dt'), 2);
  assert.match(text(node), /a1c3/);
  assert.match(text(kv([])), /Nothing recorded/);
});

test('stat renders label, value, and an optional bar width', () => {
  const node = stat({ label: 'Sessions', value: 12, foot: '3 stale', bar: 42, tone: 'green' });
  assert.match(text(node), /Sessions123 stale/);
  assert.equal(node.querySelector('span').style.width, '42%');
});

test('tabs mark the active tab and fire the callback', () => {
  let selected = null;
  const node = tabs([{ id: 'a', label: 'A' }, { id: 'b', label: 'B', count: 3 }], 'b', (id) => { selected = id; });
  const buttons = node.querySelectorAll('button');
  assert.equal(buttons[0].dataset.active, 'false');
  assert.equal(buttons[1].dataset.active, 'true');
  buttons[0].dispatchEvent({ type: 'click' });
  assert.equal(selected, 'a');
});

test('jsonView renders keys, primitives, and collapse toggles', () => {
  const node = jsonView({ ok: true, count: 2, note: 'hi', missing: null, nested: { a: [1, 2] } }, { label: 'result' });
  assert.match(text(node), /ok/);
  assert.match(text(node), /true/);
  assert.match(text(node), /"hi"/);
  assert.match(text(node), /null/);
  assert.ok(node.querySelectorAll('button.json-toggle').length >= 1);
  assert.equal(node.querySelectorAll('span.json-num').length >= 1, true);
});

test('jsonView expands and collapses every node through the toolbar', () => {
  const node = jsonView({ a: { b: { c: 1 } } }, { depth: 0 });
  const buttons = [...node.querySelectorAll('button')].filter((b) => /Expand all|Collapse all/.test(b.textContent));
  assert.equal(buttons.length, 2);
  buttons[0].dispatchEvent({ type: 'click' });
  assert.equal(node.querySelectorAll('div.json-children').every((child) => child.dataset.collapsed === 'false'), true);
  buttons[1].dispatchEvent({ type: 'click' });
  assert.equal(node.querySelectorAll('div.json-children').every((child) => child.dataset.collapsed === 'true'), true);
});
