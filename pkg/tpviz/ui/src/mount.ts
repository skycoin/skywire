// mount() — the embeddable entry for the transport-graph UI.
//
// The tpviz UI historically ran only as its own page (index.html + this bundle,
// served at /tp-viz/). Per docs/design/gui-embedding-standardization.md we make
// it a *mountable* bundle: the SAME artifact can render standalone, inside an
// Angular SPA tab, or inside a WinBox `mount:` window — no iframe, no break-out
// server path. mount(root) injects the app's own DOM + a scoped copy of its CSS
// into `root` and wires it up; unmount() tears it down.

import indexHtml from '../index.html';
import { wireEventListeners } from './events';
import { setHostOpts } from './persist';

// scopeSelector rewrites one selector so it only matches inside `scope`. Global
// selectors (body/html/:root/*) become the scope root itself so the tpviz reset
// + base styles don't leak onto the host SPA.
function scopeSelector(sel: string, scope: string): string {
  sel = sel.trim();
  if (!sel) { return sel; }
  if (sel === 'body' || sel === 'html' || sel === ':root') { return scope; }
  if (sel === '*') { return scope + ' *'; }
  if (sel.startsWith('body ')) { return scope + sel.slice(4); }
  if (sel.startsWith('html ')) { return scope + sel.slice(4); }
  return scope + ' ' + sel;
}

// scopeCss prefixes every rule's selector with `scope` so the whole stylesheet is
// confined to the mounted container. @keyframes / @font-face inner blocks are left
// untouched; @media / @supports are recursed into. A small hand-rolled walker is
// enough for the flat tpviz stylesheet and avoids pulling in a CSS parser.
function scopeCss(css: string, scope: string): string {
  const out: string[] = [];
  let i = 0;
  const n = css.length;
  const readBlock = (): string => {
    let depth = 0;
    const start = i;
    for (; i < n; i++) {
      if (css[i] === '{') { depth++; } else if (css[i] === '}') {
        depth--;
        if (depth === 0) { i++; return css.slice(start, i); }
      }
    }
    return css.slice(start, i);
  };
  while (i < n) {
    const start = i;
    while (i < n && css[i] !== '{' && css[i] !== '}') { i++; }
    const prelude = css.slice(start, i).trim();
    if (i >= n) { if (prelude) { out.push(prelude); } break; }
    if (css[i] === '}') { i++; continue; } // stray close
    const block = readBlock(); // '{ ... }' inclusive
    if (/^@(keyframes|-webkit-keyframes|font-face|page|charset|import)/i.test(prelude)) {
      out.push(prelude + block);
    } else if (/^@(media|supports|container|layer)/i.test(prelude)) {
      const inner = block.slice(block.indexOf('{') + 1, block.lastIndexOf('}'));
      out.push(prelude + '{' + scopeCss(inner, scope) + '}');
    } else if (prelude.startsWith('@')) {
      out.push(prelude + block);
    } else if (prelude) {
      const sel = prelude.split(',').map((s) => scopeSelector(s, scope)).join(', ');
      out.push(sel + block);
    }
  }
  return out.join('\n');
}

const SCOPE_CLASS = 'tpviz-root';
let mountedStyle: HTMLStyleElement | null = null;

export interface TpvizMountHandle { unmount(): void }

// mount renders the transport-graph UI into `root` (in the host's own document/JS
// context — no iframe). opts.view seeds the initial view (globe/flat/webgl);
// opts.onViewChange lets the host reflect view switches into its own URL. Returns
// a handle whose unmount() clears the DOM + styles.
export function mount(root: HTMLElement, opts?: { view?: string; onViewChange?: (v: string) => void }): TpvizMountHandle {
  setHostOpts(opts);
  const doc = new DOMParser().parseFromString(indexHtml, 'text/html');

  // Scope the app's <style> to the container and inject it once.
  const rawCss = Array.from(doc.querySelectorAll('style')).map((s) => s.textContent || '').join('\n');
  if (!mountedStyle) {
    mountedStyle = document.createElement('style');
    mountedStyle.setAttribute('data-tpviz', '');
    document.head.appendChild(mountedStyle);
  }
  // Scoped app CSS + embedding overrides: the standalone app sizes #container to
  // 100vw/100vh; inside a host tab/window it must fill the mount container instead.
  mountedStyle.textContent = scopeCss(rawCss, '.' + SCOPE_CLASS) +
    `\n.${SCOPE_CLASS}{position:relative;width:100%;height:100%;overflow:hidden;}` +
    `\n.${SCOPE_CLASS} #container{width:100%!important;height:100%!important;}`;

  // Inject the app body (minus <script>s — this bundle is already loaded) into root.
  root.classList.add(SCOPE_CLASS);
  doc.body.querySelectorAll('script').forEach((s) => s.remove());
  root.innerHTML = doc.body.innerHTML;

  wireEventListeners();
  return {
    unmount() {
      root.innerHTML = '';
      root.classList.remove(SCOPE_CLASS);
      if (mountedStyle) { mountedStyle.remove(); mountedStyle = null; }
    },
  };
}
