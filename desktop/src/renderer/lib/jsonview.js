/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { h } from './dom.js';
import { icon } from './icons.js';
import { copyText } from './ui.js';
import { truncate } from './format.js';

const DEFAULT_DEPTH = 2;

function isExpandable(value) {
  return value !== null && typeof value === 'object';
}

function primitiveNode(value) {
  if (value === null) return h('span', { class: 'json-null', text: 'null' });
  if (typeof value === 'string') {
    const long = value.length > 240;
    const node = h('span', { class: 'json-str', text: long ? `${truncate(value, 240)}` : `"${value}"`, title: long ? value : undefined });
    return node;
  }
  if (typeof value === 'number') return h('span', { class: 'json-num', text: String(value) });
  if (typeof value === 'boolean') return h('span', { class: 'json-bool', text: String(value) });
  return h('span', { text: String(value) });
}

function build(key, value, depth, opts) {
  const expandable = isExpandable(value);
  const open = expandable && (opts.expanded === true || (opts.collapsed !== true && depth < (opts.depth ?? DEFAULT_DEPTH)));

  if (!expandable) {
    return h('div', { class: 'json-row' },
      h('span', { class: 'json-toggle' }),
      key !== undefined ? h('span', { class: 'json-key', text: key }) : null,
      key !== undefined ? h('span', { class: 'json-punc', text: ':' }) : null,
      primitiveNode(value),
    );
  }

  const entries = Array.isArray(value) ? value.map((v, i) => [String(i), v]) : Object.entries(value);
  const openToken = Array.isArray(value) ? '[' : '{';
  const closeToken = Array.isArray(value) ? ']' : '}';

  const children = h('div', { class: 'json-children', dataset: { collapsed: String(!open) } },
    entries.map(([childKey, childValue]) => build(childKey, childValue, depth + 1, opts)),
    h('div', { class: 'json-row' }, h('span', { class: 'json-punc', text: closeToken })),
  );

  const toggle = h('button', {
    class: 'json-toggle',
    title: open ? 'Collapse' : 'Expand',
    onclick: (event) => {
      event.stopPropagation();
      const collapsed = children.dataset.collapsed === 'true';
      children.dataset.collapsed = String(!collapsed);
      toggle.replaceChildren(icon(collapsed ? 'chevronDown' : 'chevronRight', 10));
    },
  }, icon(open ? 'chevronDown' : 'chevronRight', 10));

  return h('div', { class: 'json-node' },
    h('div', { class: 'json-row' },
      toggle,
      key !== undefined ? h('span', { class: 'json-key', text: key }) : null,
      key !== undefined ? h('span', { class: 'json-punc', text: ':' }) : null,
      h('span', { class: 'json-punc', text: openToken }),
      open ? null : h('span', {
        class: 'json-count',
        text: `${entries.length} ${Array.isArray(value) ? 'items' : 'keys'} … ${closeToken}`,
      }),
    ),
    children,
  );
}

/** Collapsible JSON inspector with toolbar. */
export function jsonView(value, opts = {}) {
  const body = h('div', { class: 'json' }, build(undefined, value, 0, opts));

  const setAll = (collapsed) => {
    for (const node of body.querySelectorAll('.json-children')) node.dataset.collapsed = String(collapsed);
    for (const toggle of body.querySelectorAll('.json-toggle')) {
      if (toggle.firstChild) toggle.replaceChildren(icon(collapsed ? 'chevronRight' : 'chevronDown', 10));
    }
  };

  return h('div', { class: 'col', style: { gap: '6px', minWidth: '0' } },
    opts.toolbar === false
      ? null
      : h('div', { class: 'row', style: { gap: '6px' } },
          h('span', { class: 'micro', text: opts.label ?? 'json' }),
          h('span', { class: 'grow' }),
          h('button', { class: 'btn btn-ghost btn-sm', onclick: () => setAll(false) }, 'Expand all'),
          h('button', { class: 'btn btn-ghost btn-sm', onclick: () => setAll(true) }, 'Collapse all'),
          h('button', { class: 'btn btn-ghost btn-sm', onclick: () => copyText(JSON.stringify(value, null, 2), 'JSON copied') }, icon('copy', 12), 'Copy'),
        ),
    body,
  );
}

/** Pretty-printed JSON in a scrollable code block (for raw/stderr panes). */
export function jsonBlock(value, opts = {}) {
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  return h('pre', { class: 'code', style: opts.maxHeight ? { maxHeight: `${opts.maxHeight}px` } : null, text: text ?? '' });
}
