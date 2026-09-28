/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

const channels = new Map();

export function busOn(type, handler) {
  if (!channels.has(type)) channels.set(type, new Set());
  channels.get(type).add(handler);
  return () => channels.get(type)?.delete(handler);
}

export function busEmit(type, payload) {
  for (const handler of channels.get(type) ?? []) {
    try {
      handler(payload);
    } catch (err) {
      console.error(`bus handler for ${type} failed`, err);
    }
  }
  for (const handler of channels.get('*') ?? []) {
    try {
      handler({ type, payload });
    } catch (err) {
      console.error('wildcard bus handler failed', err);
    }
  }
}
