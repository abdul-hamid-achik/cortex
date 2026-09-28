/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// A minimal DOM good enough to exercise the renderer's pure builders
// (h, markdown, renderers) under `node --test` without a browser.
// It implements exactly the surface those modules touch — nothing more.

class ClassList {
  constructor(node) { this.node = node; }
  get value() { return (this.node.attributes.class ?? '').split(/\s+/).filter(Boolean); }
  add(name) { const set = new Set([...this.value, name]); this.node.attributes.class = [...set].join(' '); }
  remove(name) { this.node.attributes.class = this.value.filter((x) => x !== name).join(' '); }
  contains(name) { return this.value.includes(name); }
}

class StyleProxy {
  constructor(node) {
    this.node = node;
    this.props = {};
    return new Proxy(this, {
      set(target, key, value) {
        if (key === 'node' || key === 'props') { target[key] = value; return true; }
        if (typeof key === 'string' && key.startsWith('--')) target.props[key] = String(value);
        else target.props[key] = typeof value === 'number' ? `${value}px` : value;
        return true;
      },
      get(target, key) {
        if (key === 'setProperty') return (name, value) => { target.props[name] = String(value); };
        if (key === 'getPropertyValue') return (name) => target.props[name] ?? '';
        if (key === 'node' || key === 'props') return target[key];
        return target.props[key];
      },
    });
  }
}

class Node {
  constructor(tagName, namespace) {
    this.nodeType = 1;
    this.tagName = String(tagName).toUpperCase();
    this.localName = String(tagName);
    this.namespaceURI = namespace ?? null;
    this.attributes = {};
    this.childNodes = [];
    this.dataset = {};
    this.listeners = {};
    this.style = new StyleProxy(this);
    this.classList = new ClassList(this);
    this._text = null;
    // Form/IDL properties a real element exposes so `'value' in node` works.
    this.value = undefined;
    this.checked = false;
    this.disabled = false;
    this.hidden = false;
    this.selected = false;
  }

  get children() { return this.childNodes.filter((n) => n.nodeType === 1); }
  get firstChild() { return this.childNodes[0] ?? null; }
  get lastChild() { return this.childNodes.at(-1) ?? null; }

  setAttribute(name, value) { this.attributes[name] = String(value); }
  getAttribute(name) { return this.attributes[name] ?? null; }
  removeAttribute(name) { delete this.attributes[name]; }

  set textContent(value) {
    this._text = value === null || value === undefined ? '' : String(value);
    this.childNodes = [];
  }

  get textContent() {
    if (this._text !== null) return this._text;
    return this.childNodes.map((child) => child.textContent ?? '').join('');
  }

  /** Structural outline for assertions: tag.class per element, indented. */
  outline() {
    const lines = [];
    const walk = (node, level) => {
      for (const child of node.childNodes) {
        if (child.nodeType !== 1) continue;
        const cls = child.attributes.class ? `.${String(child.attributes.class).trim().split(/\s+/).join('.')}` : '';
        const own = child._text ? ` "${child._text.slice(0, 48)}"` : '';
        lines.push(`${'  '.repeat(level)}${child.localName}${cls}${own}`);
        walk(child, level + 1);
      }
    };
    walk(this, 0);
    return lines.join('\n');
  }

  set innerHTML(value) { this._html = String(value); this.childNodes = []; }
  get innerHTML() { return this._html ?? ''; }

  appendChild(child) { this.childNodes.push(child); child.parentNode = this; return child; }
  append(...children) { for (const child of children) this.appendChild(child); }
  removeChild(child) {
    const index = this.childNodes.indexOf(child);
    if (index >= 0) this.childNodes.splice(index, 1);
    return child;
  }
  replaceChildren(...children) { this.childNodes = []; for (const child of children) this.appendChild(child); }
  remove() { this.parentNode?.removeChild(this); }

  addEventListener(type, handler) { (this.listeners[type] ??= []).push(handler); }
  removeEventListener(type, handler) {
    this.listeners[type] = (this.listeners[type] ?? []).filter((h) => h !== handler);
  }
  dispatchEvent(event) {
    for (const handler of this.listeners[event.type] ?? []) handler.call(this, event);
    return true;
  }

  querySelector(selector) { return this.querySelectorAll(selector)[0] ?? null; }

  querySelectorAll(selector) {
    // Supports the shapes the renderer tests need: `tag`, `.class`, `tag.class`.
    const [tagPart, ...classParts] = String(selector).split('.');
    const tag = tagPart || null;
    const wanted = classParts;
    const out = [];
    const matchesNode = (child) => {
      if (tag && child.localName !== tag) return false;
      if (!wanted.length) return true;
      const have = (child.attributes.class ?? '').split(/\s+/).filter(Boolean);
      return wanted.every((name) => have.includes(name));
    };
    const walk = (node) => {
      for (const child of node.children) {
        if (matchesNode(child)) out.push(child);
        walk(child);
      }
    };
    walk(this);
    return out;
  }

  /** Test helper: depth-first text of every element, tag-qualified. */
  describe(maxDepth = 4) {
    const lines = [];
    const walk = (node, level) => {
      if (level > maxDepth) return;
      for (const child of node.children) {
        const text = child._text ? ` "${child._text.slice(0, 60)}"` : '';
        lines.push(`${'  '.repeat(level)}${child.localName}${child.attributes.class ? `.${String(child.attributes.class).split(' ').join('.')}` : ''}${text}`);
        walk(child, level + 1);
      }
    };
    walk(this, 0);
    return lines.join('\n');
  }
}

class TextNode extends Node {
  constructor(data) {
    super('#text');
    this.nodeType = 3;
    this.data = String(data);
    // A text node never carries element IDL state.
    delete this.value;
    delete this.checked;
    delete this.disabled;
    delete this.hidden;
    delete this.selected;
  }

  get textContent() { return this.data; }
  set textContent(value) { this.data = String(value); }
}

export function installDom() {
  const assertName = (name, what) => {
    if (typeof name !== 'string' || !name.trim()) {
      // Real Chromium throws InvalidCharacterError here; the stub must too, or
      // bugs like `h('svg')` slicing to '' stay invisible until launch.
      throw new DOMException(`The qualified name provided is empty (${what})`, 'InvalidCharacterError');
    }
  };
  const document = {
    createElement: (tag) => { assertName(tag, 'createElement'); return new Node(tag); },
    createElementNS: (ns, tag) => { assertName(tag, 'createElementNS'); return new Node(tag, ns); },
    createTextNode: (data) => new TextNode(data),
    createDocumentFragment: () => new Node('#fragment'),
    body: new Node('body'),
    documentElement: new Node('html'),
  };
  document.documentElement.dataset = {};
  globalThis.document = document;
  globalThis.window = globalThis;
  globalThis.Node = Node;
  globalThis.localStorage = {
    store: new Map(),
    getItem(key) { return this.store.has(key) ? this.store.get(key) : null; },
    setItem(key, value) { this.store.set(key, String(value)); },
    removeItem(key) { this.store.delete(key); },
  };
  // Node ≥ 21 exposes a read-only `navigator`; replace it defensively.
  const navigatorStub = { clipboard: { writeText: async () => {} } };
  try {
    Object.defineProperty(globalThis, 'navigator', { value: navigatorStub, configurable: true, writable: true });
  } catch {
    /* leave the platform navigator in place — nothing under test calls it */
  }
  globalThis.location = { hash: '', search: '' };
  globalThis.history = { replaceState: () => {} };
  globalThis.MutationObserver = class { observe() {} disconnect() {} };
  return { document, Node, TextNode };
}

export { Node, TextNode };
