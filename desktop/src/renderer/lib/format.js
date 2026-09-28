/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Formatting and semantic color mapping. Cortex has a small, fixed vocabulary
// (phases, assessments, receipt statuses, confidence bands) and the UI should
// color it consistently everywhere.

export const PHASES = ['new', 'orienting', 'investigating', 'planned', 'changing', 'verifying', 'persisting', 'complete'];
export const TERMINAL_STATES = ['complete', 'abandoned', 'blocked'];

const TONE = {
  ok: 'green',
  good: 'green',
  passed: 'green',
  verified: 'green',
  complete: 'green',
  confirmed: 'green',
  fresh: 'green',
  ready: 'green',
  authoritative: 'green',
  available: 'green',
  healthy: 'green',
  true: 'green',

  info: 'info',
  investigating: 'info',
  orienting: 'info',
  new: 'slate',
  running: 'info',
  triaged: 'info',
  converted: 'info',
  partial: 'amber',

  warn: 'amber',
  changing: 'amber',
  stale: 'amber',
  challenged: 'amber',
  not_run: 'slate',
  inconclusive: 'amber',
  blocked: 'amber',
  degraded: 'amber',
  pending: 'amber',
  unverified: 'amber',
  needs_index: 'amber',
  open: 'amber',

  error: 'red',
  failed: 'red',
  rejected: 'red',
  abandoned: 'red',
  unavailable: 'red',
  missing: 'red',
  false: 'red',
  drift: 'red',

  planned: 'violet',
  persisting: 'violet',
  verifying: 'accent',
};

export function toneFor(value, fallback = 'slate') {
  if (value === null || value === undefined || value === '') return fallback;
  return TONE[String(value).toLowerCase()] ?? fallback;
}

export function isTerminal(state) {
  return TERMINAL_STATES.includes(String(state ?? '').toLowerCase());
}

export function relativeTime(input, now = Date.now()) {
  const time = toMillis(input);
  if (time === null) return '';
  const diff = now - time;
  const abs = Math.abs(diff);
  const units = [
    ['y', 31_536_000_000],
    ['mo', 2_592_000_000],
    ['d', 86_400_000],
    ['h', 3_600_000],
    ['m', 60_000],
    ['s', 1000],
  ];
  if (abs < 1000) return 'just now';
  for (const [suffix, ms] of units) {
    if (abs >= ms) {
      const value = Math.round(abs / ms);
      return diff >= 0 ? `${value}${suffix} ago` : `in ${value}${suffix}`;
    }
  }
  return '';
}

export function toMillis(input) {
  if (input === null || input === undefined || input === '') return null;
  if (typeof input === 'number') return input > 1e12 ? input : input * 1000;
  const parsed = Date.parse(input);
  return Number.isNaN(parsed) ? null : parsed;
}

export function absoluteTime(input) {
  const ms = toMillis(input);
  if (ms === null) return '';
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export function shortTime(input) {
  const ms = toMillis(input);
  if (ms === null) return '';
  const d = new Date(ms);
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function pad(n) {
  return String(n).padStart(2, '0');
}

export function duration(ms) {
  if (ms === null || ms === undefined || Number.isNaN(Number(ms))) return '';
  const value = Number(ms);
  if (value < 1000) return `${Math.round(value)}ms`;
  if (value < 60_000) return `${(value / 1000).toFixed(value < 10_000 ? 2 : 1)}s`;
  const mins = Math.floor(value / 60_000);
  const secs = Math.round((value % 60_000) / 1000);
  if (mins < 60) return `${mins}m ${secs}s`;
  const hours = Math.floor(mins / 60);
  return `${hours}h ${mins % 60}m`;
}

export function humanDuration(text) {
  // Go durations arrive as "5m0s" / "1.2s" / "24h0m0s".
  if (!text) return '';
  const str = String(text);
  const h = Number(str.match(/(\d+)h/)?.[1] ?? 0);
  const m = Number(str.match(/(\d+)m(?!s)/)?.[1] ?? 0);
  const s = Number(str.match(/(\d+(?:\.\d+)?)s/)?.[1] ?? 0);
  if (!h && !m && !s) return str;
  if (h) return `${h}h${m ? ` ${m}m` : ''}`;
  if (m) return `${m}m${s ? ` ${Math.round(s)}s` : ''}`;
  return `${s < 1 ? s.toFixed(2) : Math.round(s)}s`;
}

export function bytes(n) {
  const value = Number(n);
  if (!Number.isFinite(value)) return '';
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`;
}

export function percent(value, digits = 0) {
  const n = Number(value);
  if (!Number.isFinite(n)) return '';
  const scaled = n <= 1 ? n * 100 : n;
  return `${scaled.toFixed(digits)}%`;
}

export function truncate(text, max = 140) {
  const str = String(text ?? '');
  return str.length > max ? `${str.slice(0, max - 1)}…` : str;
}

export function shortId(id, head = 12, tail = 4) {
  const str = String(id ?? '');
  if (str.length <= head + tail + 1) return str;
  return `${str.slice(0, head)}…${str.slice(-tail)}`;
}

export function basename(p) {
  const str = String(p ?? '');
  const index = str.lastIndexOf('/');
  return index >= 0 ? str.slice(index + 1) : str;
}

export function compactHome(p) {
  const str = String(p ?? '');
  const home = str.match(/^\/Users\/[^/]+|^\/home\/[^/]+/)?.[0];
  return home ? str.replace(home, '~') : str;
}

export function titleCase(text) {
  return String(text ?? '')
    .replace(/[_-]+/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

export function countLabel(n, singular, plural) {
  return `${n} ${n === 1 ? singular : plural ?? `${singular}s`}`;
}

/** Fuzzy-ish substring match used by the palette and every filter box. */
export function matches(query, ...fields) {
  const q = String(query ?? '').trim().toLowerCase();
  if (!q) return true;
  const terms = q.split(/\s+/);
  const haystack = fields.filter(Boolean).join(' ').toLowerCase();
  return terms.every((term) => haystack.includes(term));
}

/** Rank for palette ordering: exact prefix wins, then word start, then substring. */
export function relevance(query, text) {
  const q = String(query ?? '').trim().toLowerCase();
  const t = String(text ?? '').toLowerCase();
  if (!q) return 0;
  if (t === q) return 100;
  if (t.startsWith(q)) return 80;
  const wordStart = q.split(/\s+/).every((term) => t.split(/[\s_./-]+/).some((w) => w.startsWith(term)));
  if (wordStart) return 60;
  if (t.includes(q)) return 40;
  return q.split(/\s+/).every((term) => t.includes(term)) ? 20 : -1;
}

export function deepGet(obj, pathSpec) {
  return String(pathSpec)
    .split('.')
    .reduce((acc, key) => (acc === null || acc === undefined ? undefined : acc[key]), obj);
}

export function sortBy(list, keyFn, direction = 'desc') {
  return [...list].sort((a, b) => {
    const av = keyFn(a);
    const bv = keyFn(b);
    if (av === bv) return 0;
    if (av === null || av === undefined) return 1;
    if (bv === null || bv === undefined) return -1;
    const cmp = typeof av === 'number' && typeof bv === 'number' ? av - bv : String(av).localeCompare(String(bv));
    return direction === 'desc' ? -cmp : cmp;
  });
}

export function groupBy(list, keyFn) {
  const out = new Map();
  for (const item of list) {
    const key = keyFn(item);
    if (!out.has(key)) out.set(key, []);
    out.get(key).push(item);
  }
  return out;
}
