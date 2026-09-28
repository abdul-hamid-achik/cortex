/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { h, render } from './dom.js';
import { icon } from './icons.js';
import { button } from './ui.js';

let stack = [];

function closeTop() {
  const entry = stack.pop();
  if (!entry) return;
  entry.root.remove();
  if (!stack.length) document.body.style.overflow = '';
  entry.onClose?.();
}

/** Generic modal. Returns { close, body }. */
export function modal({ title, sub, body, footer, width, onClose, iconName }) {
  const root = document.getElementById('modal-root');
  root.hidden = false;
  render(root);

  const bodyHost = h('div', { class: 'modal-body' }, body);
  const panel = h('div', { class: 'modal', style: width ? { width: `min(${width}px, 94vw)` } : null },
    h('div', { class: 'modal-head' },
      iconName ? icon(iconName, 16) : null,
      h('div', { class: 'grow' },
        h('div', { class: 'modal-title', text: title ?? '' }),
        sub ? h('div', { class: 'modal-sub', text: sub }) : null,
      ),
      button('', { iconName: 'x', size: 'sm', onClick: () => close() }),
    ),
    bodyHost,
    footer ? h('div', { class: 'modal-foot' }, footer) : null,
  );

  root.appendChild(panel);
  document.body.style.overflow = 'hidden';

  const entry = { root, onClose };
  stack.push(entry);

  const onKey = (event) => {
    if (event.key === 'Escape' && stack.at(-1) === entry) {
      event.stopPropagation();
      close();
    }
  };
  document.addEventListener('keydown', onKey, true);

  root.onclick = (event) => {
    if (event.target === root && stack.at(-1) === entry) close();
  };

  function close() {
    document.removeEventListener('keydown', onKey, true);
    if (stack.at(-1) === entry) closeTop();
    else {
      const index = stack.indexOf(entry);
      if (index >= 0) {
        stack.splice(index, 1);
        entry.root?.remove?.();
      }
    }
    if (!stack.length) {
      root.hidden = true;
      document.body.style.overflow = '';
    }
  }

  const focusable = panel.querySelector('input, textarea, select, button');
  focusable?.focus();

  return { close, body: bodyHost, panel };
}

/** Promise-based confirmation used before every mutating/destructive run. */
export function confirmDialog({ title, body, confirmLabel = 'Confirm', cancelLabel = 'Cancel', danger = false, details }) {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      resolve(value);
    };
    const dialog = modal({
      title,
      iconName: danger ? 'alert' : 'question',
      width: 560,
      body: h('div', { class: 'col', style: { gap: '12px' } },
        body ? h('div', { class: 'muted', style: { fontSize: 'var(--fs-12)', lineHeight: '1.6' }, text: body }) : null,
        details ? h('pre', { class: 'code', style: { maxHeight: '180px' }, text: details }) : null,
      ),
      footer: [
        button(cancelLabel, { onClick: () => { dialog.close(); finish(false); } }),
        button(confirmLabel, { kind: danger ? 'danger' : 'primary', onClick: () => { dialog.close(); finish(true); } }),
      ],
      onClose: () => finish(false),
    });
  });
}
