/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { clear, h, render } from './dom.js';
import { icon } from './icons.js';
import { percent, toneFor, truncate } from './format.js';

/* ── Toasts ────────────────────────────────────────────────────────────── */

export function toast(kind, title, message, timeoutMs = 5200) {
  const host = document.getElementById('toasts');
  if (!host) return;
  const tone = kind === 'ok' ? 'ok' : kind === 'error' ? 'error' : kind === 'warn' ? 'warn' : 'info';
  const iconName = tone === 'ok' ? 'check' : tone === 'error' ? 'alert' : tone === 'warn' ? 'alert' : 'info';
  const node = h('div', { class: 'toast', dataset: { tone } },
    icon(iconName, 14),
    h('div', { class: 'grow' },
      title ? h('div', { class: 'toast-title', text: title }) : null,
      message ? h('div', { text: truncate(message, 420) }) : null,
    ),
    h('button', { class: 'btn btn-ghost btn-sm btn-icon', title: 'Dismiss', onclick: () => remove() }, icon('x', 12)),
  );
  const remove = () => {
    node.dataset.leaving = 'true';
    setTimeout(() => node.remove(), 220);
  };
  host.appendChild(node);
  setTimeout(remove, timeoutMs);
  return remove;
}

/* ── Buttons ───────────────────────────────────────────────────────────── */

export function button(label, opts = {}) {
  const { kind = '', size = '', iconName, onClick, disabled, title, dataset } = opts;
  return h('button', {
    class: ['btn', kind ? `btn-${kind}` : '', size ? `btn-${size}` : ''].filter(Boolean).join(' '),
    disabled: Boolean(disabled),
    title: title ?? label ?? '',
    dataset,
    onclick: onClick ? (event) => { event.preventDefault(); onClick(event); } : undefined,
  }, iconName ? icon(iconName, size === 'sm' ? 12 : 14) : null, label ? h('span', { text: label }) : null);
}

export function iconButton(iconName, opts = {}) {
  return h('button', {
    class: ['btn', 'btn-ghost', 'btn-icon', opts.size ? `btn-${opts.size}` : ''].filter(Boolean).join(' '),
    title: opts.title ?? iconName,
    disabled: Boolean(opts.disabled),
    onclick: opts.onClick ? (event) => { event.preventDefault(); opts.onClick(event); } : undefined,
  }, icon(iconName, opts.size === 'sm' ? 12 : 14));
}

export async function copyText(text, label = 'Copied') {
  try {
    await navigator.clipboard.writeText(String(text ?? ''));
    toast('ok', label, truncate(String(text ?? ''), 120), 2200);
    return true;
  } catch (err) {
    toast('error', 'Copy failed', String(err?.message ?? err));
    return false;
  }
}

export function copyButton(getText, label = 'Copy') {
  return iconButton('copy', { title: label, size: 'sm', onClick: () => copyText(typeof getText === 'function' ? getText() : getText, label) });
}

/* ── Badges ────────────────────────────────────────────────────────────── */

export function badge(text, tone = 'slate', opts = {}) {
  if (text === null || text === undefined || text === '') return null;
  return h('span', {
    class: 'badge',
    dataset: { tone },
    title: opts.title ?? String(text),
  }, opts.dot === false ? null : h('span', { class: 'badge-dot' }), h('span', { text: String(text) }));
}

export function stateBadge(value, opts = {}) {
  return badge(value ?? '—', toneFor(value), opts);
}

export function tag(text) {
  return text ? h('span', { class: 'tag', text: String(text) }) : null;
}

/* ── Cards & layout ────────────────────────────────────────────────────── */

export function card(opts = {}, ...body) {
  const { title, sub, actions, flush, foot, icon: iconName, className, dataset } = opts;
  return h('section', { class: ['card', className].filter(Boolean).join(' '), dataset },
    title || actions
      ? h('header', { class: 'card-head' },
          iconName ? icon(iconName, 15) : null,
          h('div', { class: 'grow' },
            title ? h('h3', { class: 'card-title', text: title }) : null,
            sub ? h('div', { class: 'card-sub', text: sub }) : null,
          ),
          actions ? h('div', { class: 'row', style: { gap: '6px', flexWrap: 'wrap' } }, actions) : null)
      : null,
    h('div', { class: flush ? 'card-body-flush' : 'card-body' }, body),
    foot ? h('footer', { class: 'card-foot' }, foot) : null,
  );
}

export function stat({ label, value, foot, tone = '', bar }) {
  return h('div', { class: 'card stat', dataset: { tone } },
    h('div', { class: 'stat-label', text: label }),
    h('div', { class: 'stat-value', text: String(value ?? '—') }),
    bar !== undefined && bar !== null
      ? h('div', { class: 'bar', dataset: { tone } }, h('span', { style: { width: `${Math.max(0, Math.min(100, Number(bar) || 0))}%` } }))
      : null,
    foot ? h('div', { class: 'stat-foot', text: foot }) : null,
  );
}

export function sectionTitle(text, ...extra) {
  return h('div', { class: 'section-title' }, h('span', { text }), extra);
}

export function kv(pairs, opts = {}) {
  const dl = h('dl', { class: 'kv' });
  for (const [key, value] of pairs) {
    if (value === undefined) continue;
    dl.appendChild(h('dt', { text: key }));
    dl.appendChild(h('dd', {}, value instanceof Node ? value : h('span', { text: value === null || value === '' ? '—' : String(value) })));
  }
  if (!dl.children.length && opts.empty !== false) return empty({ title: 'Nothing recorded', note: opts.emptyNote ?? '' });
  return dl;
}

export function progress(value, tone = 'accent', label = '') {
  const pct = Math.max(0, Math.min(100, Number(value) || 0));
  return h('div', { class: 'row', style: { gap: '8px' } },
    h('div', { class: 'bar grow', dataset: { tone } }, h('span', { style: { width: `${pct}%` } })),
    label ? h('span', { class: 'mono-sm faint', text: label }) : null,
  );
}

export function rateRing(ratio, { size = 92, label = 'verified', tone = 'var(--accent)' } = {}) {
  const value = Number.isFinite(Number(ratio)) ? Math.max(0, Math.min(1, Number(ratio) <= 1 ? Number(ratio) : Number(ratio) / 100)) : 0;
  const stroke = 7;
  const radius = (size - stroke) / 2;
  const circumference = 2 * Math.PI * radius;
  return h('div', { class: 'rate-ring' },
    h('svg', { width: size, height: size, viewBox: `0 0 ${size} ${size}` },
      h('svg:circle', { cx: size / 2, cy: size / 2, r: radius, fill: 'none', stroke: 'var(--panel-3)', 'stroke-width': stroke }),
      h('svg:circle', {
        cx: size / 2, cy: size / 2, r: radius, fill: 'none', stroke: tone, 'stroke-width': stroke,
        'stroke-linecap': 'round', 'stroke-dasharray': `${circumference}`,
        'stroke-dashoffset': `${circumference * (1 - value)}`,
        transform: `rotate(-90 ${size / 2} ${size / 2})`,
      }),
      h('svg:text', {
        x: '50%', y: '50%', 'text-anchor': 'middle', 'dominant-baseline': 'central',
        fill: 'var(--text)', 'font-size': size > 80 ? 17 : 13, 'font-family': 'var(--font-mono)',
        ref: (node) => { node.textContent = percent(value); },
      }),
    ),
    label ? h('div', { class: 'rate-ring-label', text: label }) : null,
  );
}

/* ── States ────────────────────────────────────────────────────────────── */

export function empty({ iconName = 'info', title = 'Nothing here', note = '', action = null } = {}) {
  return h('div', { class: 'empty' },
    h('div', { class: 'empty-icon' }, icon(iconName, 26)),
    h('div', { class: 'empty-title', text: title }),
    note ? h('div', { class: 'empty-note', text: note }) : null,
    action,
  );
}

export function loading(label = 'Loading…') {
  return h('div', { class: 'loading-row' }, h('div', { class: 'spinner' }), h('span', { text: label }));
}

export function skeletonRows(count = 4) {
  return h('div', { class: 'col', style: { gap: '8px', padding: '14px' } },
    Array.from({ length: count }, (_, i) => h('div', { class: 'skeleton', style: { width: `${92 - i * 9}%` } })),
  );
}

export function alert({ tone = 'info', title = '', body = '', iconName } = {}) {
  const name = iconName ?? (tone === 'error' ? 'alert' : tone === 'warn' ? 'alert' : tone === 'ok' ? 'check' : 'info');
  return h('div', { class: 'alert', dataset: { tone } },
    h('div', { class: 'alert-icon' }, icon(name, 15)),
    h('div', { class: 'alert-body' },
      title ? h('div', { class: 'alert-title', text: title }) : null,
      body instanceof Node ? body : body ? h('div', { text: body }) : null,
    ),
  );
}

/* ── Tabs ──────────────────────────────────────────────────────────────── */

export function tabs(items, activeId, onSelect) {
  return h('div', { class: 'tabs', role: 'tablist' },
    items.map((item) =>
      h('button', {
        class: 'tab',
        role: 'tab',
        dataset: { active: String(item.id === activeId), id: item.id },
        'aria-selected': String(item.id === activeId),
        title: item.title ?? item.label,
        onclick: () => onSelect(item.id),
      },
        item.icon ? icon(item.icon, 13) : null,
        h('span', { text: item.label }),
        item.count !== undefined && item.count !== null ? h('span', { class: 'tab-count', text: String(item.count) }) : null,
      ),
    ),
  );
}

/* ── Table ─────────────────────────────────────────────────────────────── */

/**
 * columns: [{ key, label, render(row), width, align, sortValue(row) }]
 * rows:    array of data
 */
export function table({ columns, rows, empty: emptyState, onRowClick, rowKey, selectedKey, rowAttrs, maxHeight }) {
  if (!rows?.length) return emptyState ?? empty({ title: 'No rows' });
  const head = h('tr', {}, columns.map((col) =>
    h('th', { style: col.width ? { width: col.width } : null, class: col.align === 'right' ? 'right' : '' }, col.label ?? ''),
  ));
  const body = rows.map((row) => {
    const key = rowKey ? rowKey(row) : null;
    return h('tr', {
      dataset: {
        clickable: String(Boolean(onRowClick)),
        selected: key !== null && selectedKey !== undefined ? String(key === selectedKey) : 'false',
        ...(rowAttrs ? rowAttrs(row) ?? {} : {}),
      },
      onclick: onRowClick ? () => onRowClick(row) : undefined,
    }, columns.map((col) => {
      const content = col.render ? col.render(row) : row[col.key];
      return h('td', {
        class: [col.strong ? 'strong' : '', col.align === 'right' ? 'num' : '', col.mono ? 'mono-sm' : ''].filter(Boolean).join(' '),
      }, content instanceof Node || content === null || content === undefined || typeof content === 'boolean'
        ? (content ?? '—')
        : String(content));
    }));
  });
  return h('div', { class: 'table-wrap', style: maxHeight ? { maxHeight: `${maxHeight}px` } : null },
    h('table', { class: 'table' }, h('thead', {}, head), h('tbody', {}, body)),
  );
}

/* ── Async section helper ──────────────────────────────────────────────── */

/**
 * Renders a placeholder, awaits the loader, then swaps in the content.
 * Errors become an inline alert instead of an unhandled rejection.
 */
export async function into(container, loader, placeholder = loading()) {
  render(container, placeholder);
  try {
    const node = await loader();
    if (node) render(container, node);
    return node;
  } catch (err) {
    render(container, alert({ tone: 'error', title: 'Failed to load', body: String(err?.message ?? err) }));
    return null;
  }
}

export function actionList(actions, onPick) {
  if (!actions?.length) return empty({ iconName: 'arrowRight', title: 'No next actions', note: 'The kernel returns structured continuations after every write.' });
  return h('div', { class: 'action-list' },
    actions.map((action) =>
      h('button', { class: 'action-item', onclick: () => onPick?.(action) },
        h('span', { class: 'action-tool', text: action.tool ?? 'action' }),
        h('span', { class: 'action-reason', text: action.reason ?? action.summary ?? '' }),
        h('span', { class: 'action-cmd', text: truncate(action.command ?? '', 90) }),
      ),
    ),
  );
}

export function chip(label, opts = {}) {
  return h('button', {
    class: 'chip',
    dataset: opts.state ? { state: opts.state } : null,
    title: opts.title ?? label,
    disabled: opts.disabled,
    onclick: opts.onClick,
  }, opts.dot ? h('span', { class: 'chip-dot' }) : null, opts.iconName ? icon(opts.iconName, 12) : null, h('span', { class: 'truncate', text: label }));
}

export function pageHead({ title, sub, actions = [], children = [] }) {
  return h('div', { class: 'page-head' },
    h('div', { class: 'grow' },
      h('h1', { class: 'page-title', text: title }),
      sub ? h('p', { class: 'page-sub', text: sub }) : null,
    ),
    actions.length ? h('div', { class: 'page-actions' }, actions) : null,
    children,
  );
}

export function scrollReset(node) {
  const view = document.getElementById('view');
  if (view) view.scrollTop = 0;
  return node;
}

export { clear, render };
