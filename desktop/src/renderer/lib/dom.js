/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */

// Tiny hyperscript. Everything in the renderer is built with `h`, so there is
// no innerHTML interpolation of untrusted data anywhere in the app.

const SVG_NS = 'http://www.w3.org/2000/svg';

export function h(tag, props, ...children) {
  const isSvg = typeof tag === 'string' && (tag === 'svg' || tag.startsWith('svg:'));
  const localTag = typeof tag === 'string' && tag.startsWith('svg:') ? tag.slice(4) : tag;
  const node = isSvg
    ? document.createElementNS(SVG_NS, localTag)
    : typeof tag === 'string'
      ? document.createElement(tag)
      : tag;

  if (props) {
    for (const [key, value] of Object.entries(props)) {
      if (value === null || value === undefined || value === false) continue;
      if (key === 'class') node.setAttribute('class', Array.isArray(value) ? value.filter(Boolean).join(' ') : value);
      else if (key === 'text') node.textContent = String(value);
      else if (key === 'style' && typeof value === 'object') applyStyle(node, value);
      else if (key === 'dataset') { for (const [dk, dv] of Object.entries(value)) if (dv !== undefined && dv !== null) node.dataset[dk] = dv; }
      else if (key.startsWith('on') && typeof value === 'function') node.addEventListener(key.slice(2).toLowerCase(), value);
      else if (key === 'value' && 'value' in node) node.value = value;
      else if (key === 'checked' || key === 'disabled' || key === 'hidden' || key === 'selected') node[key] = Boolean(value);
      else if (key === 'ref' && typeof value === 'function') value(node);
      else if (value === true) node.setAttribute(key, '');
      else node.setAttribute(key, String(value));
    }
  }

  append(node, children);
  return node;
}

function applyStyle(node, style) {
  for (const [key, value] of Object.entries(style)) {
    if (value === null || value === undefined) continue;
    if (key.startsWith('--')) node.style.setProperty(key, String(value));
    else node.style[key] = typeof value === 'number' ? `${value}px` : value;
  }
}

export function append(node, children) {
  for (const child of children.flat(Infinity)) {
    if (child === null || child === undefined || child === false || child === true) continue;
    node.appendChild(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
  return node;
}

export function fragment(...children) {
  const frag = document.createDocumentFragment();
  append(frag, children);
  return frag;
}

export function on(node, event, handler, options) {
  node.addEventListener(event, handler, options);
  return () => node.removeEventListener(event, handler, options);
}

/** Replace a container's children in one paint. */
export function render(node, ...children) {
  clear(node);
  append(node, children);
  return node;
}

export function qs(selector, root = document) {
  return root.querySelector(selector);
}

export function qsa(selector, root = document) {
  return [...root.querySelectorAll(selector)];
}

/** Debounce for search inputs. */
export function debounce(fn, ms = 180) {
  let timer = null;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
}
