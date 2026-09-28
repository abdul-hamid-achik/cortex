/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom } from './helpers/mini-dom.js';

installDom();
globalThis.document.getElementById = () => null;

const { markdown, markdownToText } = await import('../src/renderer/lib/markdown.js');
const { h } = await import('../src/renderer/lib/dom.js');

const tags = (node) => node.children.map((child) => child.localName);
const find = (node, tag) => node.querySelectorAll(tag);

test('headings map to h1..h4 with inline formatting preserved as text', () => {
  const out = markdown('# Title\n\n## Sub **bold**\n\n#### Deep\n');
  assert.deepEqual(tags(out), ['h1', 'h2', 'h4']);
  assert.equal(out.children[0].textContent, 'Title');
  assert.equal(out.children[1].textContent, 'Sub bold');
  assert.equal(find(out.children[1], 'strong').length, 1);
});

test('fenced code blocks keep their content verbatim and record the language', () => {
  const out = markdown('```bash\ncortex open "a b" --surface code\n```\n');
  const pre = find(out, 'pre')[0];
  assert.ok(pre, 'expected a <pre>');
  const code = find(pre, 'code')[0];
  assert.equal(code.textContent, 'cortex open "a b" --surface code');
  assert.equal(code.dataset.lang, 'bash');
});

test('unordered and ordered lists nest by indentation', () => {
  const out = markdown('- one\n- two\n  - nested\n- three\n\n1. first\n2. second\n');
  const lists = out.children.filter((child) => child.localName === 'ul' || child.localName === 'ol');
  assert.equal(lists.length, 2);
  assert.equal(lists[0].localName, 'ul');
  assert.equal(lists[1].localName, 'ol');
  assert.equal(find(lists[0], 'li').length >= 3, true);
  assert.ok(lists[0].querySelector('ul'), 'nested list should exist');
});

test('tables render thead/tbody with every cell', () => {
  const out = markdown('| Action | Gate |\n|---|---|\n| plan | disproof path |\n| verify | receipts |\n');
  const table = find(out, 'table')[0];
  assert.ok(table);
  assert.equal(find(table, 'th').length, 2);
  assert.equal(find(table, 'td').length, 4);
  assert.equal(find(table, 'th')[0].textContent, 'Action');
  assert.equal(find(table, 'td')[3].textContent, 'receipts');
});

test('blockquotes and rules are recognized', () => {
  const out = markdown('> More tools without structure = more ways to get lost.\n\n---\n\nafter\n');
  assert.ok(find(out, 'blockquote').length === 1);
  assert.ok(find(out, 'hr').length === 1);
  assert.match(out.textContent, /More tools without structure/);
});

test('inline code, links, and bare urls are rendered as nodes, not markup', () => {
  const out = markdown('run `cortex status` and see [the docs](https://cortexai.tools) or https://github.com/x\n');
  assert.equal(find(out, 'code')[0].textContent, 'cortex status');
  const links = find(out, 'a');
  assert.equal(links.length, 2);
  assert.equal(links[0].getAttribute('href'), 'https://cortexai.tools');
  assert.equal(links[0].textContent, 'the docs');
  assert.equal(links[1].textContent, 'https://github.com/x');
});

test('raw HTML in markdown is never interpreted — it stays text', () => {
  const out = markdown('<script>alert("xss")</script>\n\n<img src=x onerror=alert(1)>\n');
  assert.equal(find(out, 'script').length, 0);
  assert.equal(find(out, 'img').length, 0);
  assert.match(out.textContent, /<script>alert\("xss"\)<\/script>/);
  assert.match(out.textContent, /<img src=x onerror=alert\(1\)>/);
});

test('a real Cortex README section renders completely', () => {
  const source = [
    '## The reasoning loop',
    '',
    'Cortex enforces:',
    '',
    '```',
    'orient → investigate → plan → change → verify → remember',
    '```',
    '',
    '| Action | Gate |',
    '|---|---|',
    '| `plan` | disproof path required |',
    '| `verify` | typed claims → receipts |',
    '',
    '- plans without a **disproof path** are rejected',
    '- completion requires `verified`',
    '',
  ].join('\n');
  const out = markdown(source);
  assert.deepEqual(tags(out), ['h2', 'p', 'pre', 'table', 'ul']);
  assert.equal(find(out, 'td').length, 4);
  assert.equal(find(out, 'code').length >= 2, true);
  assert.match(out.textContent, /disproof path required/);
});

test('markdownToText flattens a document for search indexes', () => {
  const text = markdownToText('# Title\n\n- **bold** item\n\n```js\ncode()\n```\n\n[link](https://x.dev)\n');
  assert.equal(text.includes('```'), false);
  assert.equal(text.includes('**'), false);
  assert.match(text, /Title bold item link/);
  assert.equal(text.includes('code()'), false, 'fenced code should not pollute the index');
});

test('empty and whitespace-only input produce no nodes', () => {
  assert.equal(markdown('').children.length, 0);
  assert.equal(markdown('\n\n   \n').children.length, 0);
  assert.equal(markdownToText(''), '');
});

test('h() builds elements, applies classes, and never interpolates HTML from text', () => {
  const node = h('div', { class: ['a', 'b'], dataset: { phase: 'changing' }, title: 'x' },
    h('span', { text: '<img src=x onerror=alert(1)>' }),
    null,
    false,
    'tail',
  );
  assert.equal(node.getAttribute('class'), 'a b');
  assert.equal(node.dataset.phase, 'changing');
  assert.equal(node.children.length, 1);
  assert.equal(node.textContent, '<img src=x onerror=alert(1)>tail');
  assert.equal(find(node, 'img').length, 0);
});
