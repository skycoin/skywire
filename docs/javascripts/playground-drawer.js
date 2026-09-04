/* Playground drawer — mounts the skywire playground (/playground/: the
 * whole skywire binary compiled to wasm behind a terminal + nested
 * browser) as a persistent bottom panel on every docs page.
 *
 * How persistence works: the site uses Material's instant loading
 * (navigation.instant in mkdocs.yml), which swaps page content via
 * fetch + history.pushState instead of full page loads. This script
 * appends its elements directly to <body>, outside the components the
 * theme replaces, so the drawer — and the iframe with the running wasm
 * visor inside it — survives doc navigation. The iframe is created
 * lazily on first open, so plain doc reading loads nothing extra.
 * Filesystem state inside the playground persists in IndexedDB
 * (jsfs persistDB, enabled by playground/index.html), so a visor's
 * identity also survives full reloads.
 *
 * The script may be re-executed on instant navigation; the guard below
 * makes that a no-op.
 */
(function () {
  'use strict';
  if (window.__skwPlaygroundDrawer) return;
  window.__skwPlaygroundDrawer = true;

  // Site root from this script's own URL (".../javascripts/playground-drawer.js"),
  // so the drawer works at any mount point (gh-pages /skywire/, local preview /).
  var script = document.currentScript || document.querySelector('script[src*="playground-drawer"]');
  var root = script ? script.src.replace(/javascripts\/playground-drawer\.js.*$/, '') : '/';
  var pgURL = root + 'playground/';

  var css = [
    '#skw-pg-btn{position:fixed;bottom:14px;right:14px;z-index:200;',
    'font:600 13px/1 var(--md-code-font-family,monospace);cursor:pointer;',
    'padding:.6em .9em;border:none;border-radius:2em;',
    'background:var(--md-primary-fg-color,#3f51b5);color:var(--md-primary-bg-color,#fff);',
    'box-shadow:var(--md-shadow-z2,0 2px 6px rgba(0,0,0,.3))}',
    '#skw-pg-btn:hover{filter:brightness(1.15)}',
    '#skw-pg-panel{position:fixed;left:0;right:0;bottom:0;height:60vh;z-index:201;',
    'display:none;flex-direction:column;background:#0e0c14;',
    'border-top:2px solid var(--md-primary-fg-color,#3f51b5);',
    'box-shadow:0 -4px 16px rgba(0,0,0,.4)}',
    '#skw-pg-panel.open{display:flex}',
    '#skw-pg-panel.tall{height:94vh}',
    '#skw-pg-bar{display:flex;align-items:center;gap:.8em;padding:.3em .8em;',
    'background:#17141f;color:#cdd2da;font:12px var(--md-code-font-family,monospace)}',
    '#skw-pg-bar .t{color:#9d7cff;font-weight:600;margin-right:auto}',
    '#skw-pg-bar a,#skw-pg-bar button{background:none;border:none;color:#9aa0a6;',
    'font:inherit;cursor:pointer;text-decoration:none;padding:.2em .4em}',
    '#skw-pg-bar a:hover,#skw-pg-bar button:hover{color:#fff}',
    '#skw-pg-frame{flex:1;border:0;width:100%;background:#0e0c14}'
  ].join('');
  // The style element must NOT live in <head>: Material's instant loading
  // rewrites head content on navigation, which dropped these rules and left
  // the (surviving, body-appended) panel in static flow at the bottom of the
  // document — live-verified: position:fixed reverted to static after one
  // in-docs navigation. Appended to <body> alongside the panel instead, so
  // the styles persist exactly as long as the elements they style.
  var style = document.createElement('style');
  style.textContent = css;
  document.body.appendChild(style);

  var panel = document.createElement('div');
  panel.id = 'skw-pg-panel';
  panel.innerHTML =
    '<div id="skw-pg-bar">' +
    '<span class="t">skywire playground</span>' +
    '<button id="skw-pg-grow" title="toggle height">&#x2195; height</button>' +
    '<a href="' + pgURL + '" target="_blank" rel="noopener" title="open standalone (state does not transfer)">&#x2197; standalone</a>' +
    '<button id="skw-pg-hide" title="hide (keeps running)">&#x2013; hide</button>' +
    '</div>';
  document.body.appendChild(panel);

  var btn = document.createElement('button');
  btn.id = 'skw-pg-btn';
  btn.type = 'button';
  btn.textContent = '>_ playground';
  btn.title = 'open the skywire playground (runs a real visor in this tab)';
  document.body.appendChild(btn);

  var frame = null;
  function open() {
    if (!frame) {
      frame = document.createElement('iframe');
      frame.id = 'skw-pg-frame';
      frame.src = pgURL;
      frame.setAttribute('allow', 'clipboard-read; clipboard-write');
      panel.appendChild(frame);
    }
    panel.classList.add('open');
    btn.style.display = 'none';
  }
  function hide() {
    panel.classList.remove('open'); // display:none — iframe stays alive
    btn.style.display = '';
  }
  btn.addEventListener('click', open);
  panel.querySelector('#skw-pg-hide').addEventListener('click', hide);
  panel.querySelector('#skw-pg-grow').addEventListener('click', function () {
    panel.classList.toggle('tall');
  });

  // Render /playground/ inside the shell: clicks on same-tab links to it
  // (e.g. the nav tab) open the drawer instead of leaving the docs.
  // Runs in the capture phase so it wins over instant loading. Direct
  // URL hits and target=_blank links still get the standalone page.
  document.addEventListener('click', function (e) {
    var a = e.target && e.target.closest && e.target.closest('a[href]');
    if (!a || a.target === '_blank' || e.ctrlKey || e.metaKey || e.shiftKey || e.button !== 0) return;
    var href = a.href.split('#')[0].split('?')[0];
    if (href === pgURL || href === pgURL + 'index.html' || href + '/' === pgURL) {
      e.preventDefault();
      e.stopPropagation();
      open();
    }
  }, true);
})();
