/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

import { clear, h, render } from './dom.js';
import { icon } from './icons.js';
import { state, saveLocalView, loadLocalView } from './state.js';

const routes = new Map();
let currentId = '';
let currentParams = {};
let host = null;
let onNavigateHook = null;

export function registerRoute(def) {
  routes.set(def.id, def);
}

export function registerRoutes(defs) {
  for (const def of defs) registerRoute(def);
}

export function routeIds() {
  return [...routes.keys()];
}

export function routeList() {
  return [...routes.values()].map(({ id, label, icon: iconName, group, hint }) => ({ id, label, icon: iconName, group, hint }));
}

export function setHost(node) {
  host = node;
}

export function onNavigate(fn) {
  onNavigateHook = fn;
}

export function currentRoute() {
  return routes.get(currentId) ?? null;
}

export function current() {
  return { id: currentId, params: currentParams };
}

/** Navigate to a route. Params survive a re-render of the same route. */
export async function navigate(id, params = {}, opts = {}) {
  const route = routes.get(id);
  if (!route) {
    console.warn(`unknown route: ${id}`);
    return false;
  }
  if (!opts.force && id === currentId && shallowEqual(params, currentParams)) {
    await refresh();
    return true;
  }
  currentId = id;
  currentParams = { ...params };
  saveLocalView({ view: id });
  if (location.hash !== `#${id}`) history.replaceState(null, '', `#${id}`);
  onNavigateHook?.(route);
  await mount();
  return true;
}

export async function refresh() {
  await mount();
}

async function mount() {
  if (!host) return;
  const route = routes.get(currentId);
  if (!route) return;
  render(host, h('div', { class: 'loading-row' }, h('div', { class: 'spinner' }), `loading ${route.label}…`));
  try {
    const node = await route.render({ params: currentParams, route, host });
    clear(host);
    if (node) host.appendChild(node);
    host.scrollTop = 0;
    globalThis.deck?.notify?.({ type: 'route-settled', route: currentId });
  } catch (err) {
    clear(host);
    host.appendChild(h('div', { class: 'alert', dataset: { tone: 'error' } },
      icon('alert', 15),
      h('div', { class: 'alert-body' },
        h('div', { class: 'alert-title', text: `${route.label} failed to render` }),
        h('div', { text: String(err?.message ?? err) }),
        h('pre', { class: 'code', style: { marginTop: '8px', maxHeight: '220px' }, text: String(err?.stack ?? '').slice(0, 4000) }),
      ),
    ));
  }
}

export function restoreRoute(fallback = 'dashboard') {
  const fromHash = location.hash.replace(/^#/, '');
  const saved = loadLocalView().view;
  return routes.has(fromHash) ? fromHash : routes.has(saved) ? saved : fallback;
}

function shallowEqual(a, b) {
  const ka = Object.keys(a ?? {});
  const kb = Object.keys(b ?? {});
  if (ka.length !== kb.length) return false;
  return ka.every((key) => a[key] === b[key]);
}

export { state };
