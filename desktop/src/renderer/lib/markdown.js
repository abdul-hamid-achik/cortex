/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { h } from './dom.js';

// A dependency-free Markdown renderer that builds DOM nodes instead of HTML
// strings, so document content can never inject markup. It covers the subset
// the Cortex docs, README, and summary.md files actually use: ATX headings,
// fenced code, blockquotes, nested lists, tables, rules, links, emphasis, and
// inline code.

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\n]+\*)|(\[[^\]]+\]\([^)\s]+\))|(https?:\/\/[^\s)<]+)/g;

function inlineNodes(text) {
  const out = [];
  let last = 0;
  for (const match of String(text ?? '').matchAll(INLINE)) {
    if (match.index > last) out.push(document.createTextNode(String(text).slice(last, match.index)));
    const token = match[0];
    if (token.startsWith('`')) {
      out.push(h('code', { text: token.slice(1, -1) }));
    } else if (token.startsWith('**')) {
      out.push(h('strong', { text: token.slice(2, -2) }));
    } else if (token.startsWith('*')) {
      out.push(h('em', { text: token.slice(1, -1) }));
    } else if (token.startsWith('[')) {
      const parsed = token.match(/^\[([^\]]+)\]\(([^)\s]+)\)$/);
      const label = parsed?.[1] ?? token;
      const href = parsed?.[2] ?? '#';
      if (/^https?:\/\//.test(href)) {
        out.push(h('a', { href, text: label, onclick: (event) => { event.preventDefault(); window.deck?.invoke('shell:openExternal', { url: href }); } }));
      } else {
        out.push(h('span', { class: 'code-inline', text: label }));
      }
    } else {
      out.push(h('a', { href: token, text: token, onclick: (event) => { event.preventDefault(); window.deck?.invoke('shell:openExternal', { url: token }); } }));
    }
    last = match.index + token.length;
  }
  if (last < String(text ?? '').length) out.push(document.createTextNode(String(text).slice(last)));
  return out;
}

function codeBlock(lines, lang) {
  return h('pre', {}, h('code', { dataset: { lang: lang ?? '' }, text: lines.join('\n') }));
}

function splitRow(line) {
  return line
    .replace(/^\s*\|/, '')
    .replace(/\|\s*$/, '')
    .split('|')
    .map((cell) => cell.trim());
}

function isTableDivider(line) {
  return /^\s*\|?[\s:-]*-[\s:|-]*\|?\s*$/.test(line) && line.includes('-');
}

const BULLET = /^(\s*)([-*+]|\d+\.)\s+(.*)$/;

/**
 * Build a list from lines that all belong to it. Items at the shallowest
 * indent become siblings; deeper bullets recurse into a nested list, and any
 * other indented line is treated as continuation text for the current item.
 */
function buildList(lines) {
  const items = [];
  let baseIndent = null;
  let current = null;

  for (const line of lines) {
    if (!line.trim()) continue;
    const match = line.match(BULLET);
    if (match) {
      const indent = match[1].length;
      if (baseIndent === null) baseIndent = indent;
      if (indent <= baseIndent) {
        current = { ordered: /\d/.test(match[2]), text: match[3], nested: [] };
        items.push(current);
        continue;
      }
      if (current) { current.nested.push(line); continue; }
    }
    if (current) {
      if (BULLET.test(line)) current.nested.push(line);
      else current.text = `${current.text} ${line.trim()}`;
    }
  }

  if (!items.length) return h('span');
  const parent = items[0].ordered ? h('ol') : h('ul');
  for (const item of items) {
    const li = h('li', {}, ...inlineNodes(item.text));
    if (item.nested.length) {
      const nested = buildList(item.nested);
      if (nested.localName === 'ul' || nested.localName === 'ol') li.appendChild(nested);
    }
    parent.appendChild(li);
  }
  return parent;
}

/** Consume every line that belongs to the list starting at `start`. */
function collectListLines(lines, start) {
  const block = [];
  let i = start;
  let ordered = null;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      let next = i + 1;
      while (next < lines.length && !lines[next].trim()) next += 1;
      const following = lines[next];
      // A blank line only continues the list when the next bullet is the same
      // kind — otherwise `1.` after `-` would merge two separate lists.
      if (following && BULLET.test(following) && ordered !== null && /\d/.test(following.match(BULLET)[2]) === ordered) {
        i = next;
        continue;
      }
      if (following && /^\s{2,}\S/.test(following) && block.length) {
        i = next;
        continue;
      }
      break;
    }
    const match = line.match(BULLET);
    if (match) {
      if (ordered === null) ordered = /\d/.test(match[2]);
      block.push(line);
      i += 1;
      continue;
    }
    if (/^\s{2,}\S/.test(line) && block.length) { block.push(line); i += 1; continue; }
    break;
  }
  return { block, next: i };
}

function parseBlocks(lines, level = 0) {
  const root = h('div');
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (!line.trim()) { i += 1; continue; }

    // fenced code
    const fence = line.match(/^\s*```(\w*)\s*$/);
    if (fence) {
      const body = [];
      i += 1;
      while (i < lines.length && !/^\s*```/.test(lines[i])) { body.push(lines[i]); i += 1; }
      i += 1;
      root.appendChild(codeBlock(body, fence[1]));
      continue;
    }

    // heading
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      const depth = heading[1].length;
      root.appendChild(h(`h${depth}`, {}, ...inlineNodes(heading[2])));
      i += 1;
      continue;
    }

    // horizontal rule
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      root.appendChild(h('hr'));
      i += 1;
      continue;
    }

    // blockquote
    if (/^\s*>/.test(line)) {
      const quote = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) { quote.push(lines[i].replace(/^\s*>\s?/, '')); i += 1; }
      root.appendChild(h('blockquote', {}, ...parseBlocks(quote, level + 1).childNodes));
      continue;
    }

    // table
    if (line.includes('|') && i + 1 < lines.length && isTableDivider(lines[i + 1])) {
      const head = splitRow(line);
      i += 2;
      const rows = [];
      while (i < lines.length && lines[i].includes('|') && lines[i].trim()) { rows.push(splitRow(lines[i])); i += 1; }
      root.appendChild(h('table', {},
        h('thead', {}, h('tr', {}, head.map((cell) => h('th', {}, ...inlineNodes(cell))))),
        h('tbody', {}, rows.map((row) => h('tr', {}, row.map((cell) => h('td', {}, ...inlineNodes(cell)))))),
      ));
      continue;
    }

    // lists
    if (BULLET.test(line)) {
      const { block, next } = collectListLines(lines, i);
      root.appendChild(buildList(block));
      i = next;
      continue;
    }

    // paragraph
    const paragraph = [];
    while (i < lines.length && lines[i].trim() && !/^(#{1,6}\s|\s*```|\s*>|\s*([-*+]|\d+\.)\s)/.test(lines[i])) {
      paragraph.push(lines[i]);
      i += 1;
    }
    if (!paragraph.length) { paragraph.push(lines[i]); i += 1; }
    root.appendChild(h('p', {}, ...inlineNodes(paragraph.join(' ').replace(/\s{2,}\n/g, '\n'))));
  }

  return root;
}

/** Render markdown text into a `.md` container. */
export function markdown(text) {
  const lines = String(text ?? '').replace(/\r\n/g, '\n').split('\n');
  return h('div', { class: 'md' }, ...parseBlocks(lines).childNodes);
}

/** Strip markdown to plain text — used for search indexes and previews. */
export function markdownToText(text) {
  return String(text ?? '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/^\s*#{1,6}\s+/gm, '')
    .replace(/^\s*([-*+]|\d+\.)\s+/gm, '')
    .replace(/^\s*>\s?/gm, '')
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/[*_`]/g, '')
    .replace(/^\s*([-*_])\1{2,}\s*$/gm, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}
