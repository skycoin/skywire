/* Desk shell — makes the SITE ROOT the desk surface. The root page greets
 * the visitor with the playground (/playground/: the whole skywire binary
 * compiled to wasm behind a websh terminal + nested browser) opened as a
 * layer over the page, with a terminal that has just run `skywire --help`.
 * No visor runs unless the visitor starts one in that terminal.
 *
 * Layer geometry: the desk fills the viewport BELOW the Material header
 * and nav tabs, so the docs entry points stay visible and clickable at
 * the root; the "browse this page" button on the desk bar minimizes the
 * layer to reveal the index content rendered underneath.
 *
 * How persistence works: the site uses Material's instant loading
 * (navigation.instant in mkdocs.yml), which swaps page content via
 * fetch + history.pushState instead of full page loads. This script
 * appends its elements — and its <style>, which must NOT live in <head>
 * because instant loading rewrites head content (live-verified: the
 * panel lost position:fixed after one navigation) — directly to <body>,
 * outside what the theme replaces. So the layer, and the iframe with
 * the running wasm inside it, survive doc navigation as the same DOM
 * objects. Navigating off the root auto-minimizes the layer to a
 * `>_ terminal` pill (bottom right); returning Home re-surfaces it.
 * Non-root pages never load the iframe by themselves — plain doc
 * reading costs nothing extra.
 *
 * The script may be re-executed on instant navigation; the singleton
 * below makes that a route-change notification instead of a re-mount.
 * Direct URL hits on any docs page still render standalone, and with
 * JS disabled the site degrades to plain static pages.
 */
(function () {
  'use strict';

  // FRAMED: do nothing. The desk opens the docs in a netscrape tab, and
  // desk_js.go's DirectLoader claims same-origin URLs so that tab renders the
  // real page — scripts and all, this one included. Without this guard the
  // docs would mount a desk inside the desk's own browser, which would open
  // the docs, without end. Reading the docs framed is the point; running a
  // second shell inside them is not.
  try {
    if (window.top !== window.self) { return; }
  } catch (e) { return; } // cross-origin top — framed by someone else

  if (window.__skwDeskShell) { window.__skwDeskShell.onNavigate(); return; }

  // Site root from this script's own URL (".../javascripts/desk-shell.js"),
  // so the shell works at any mount point (gh-pages /skywire/, local /).
  var script = document.currentScript || document.querySelector('script[src*="desk-shell"]');
  var rootURL = script ? script.src.replace(/javascripts\/desk-shell\.js.*$/, '') : '/';
  var rootPath = rootURL.replace(/^[a-z]+:\/\/[^/]*/, '');
  var pgURL = rootURL + 'playground/';

  var css = [
    // z-index 3: above page content, below the Material header (z 4) so
    // the search dropdown paints over the desk layer.
    '#skw-desk-panel{position:fixed;left:0;right:0;bottom:0;top:0;z-index:3;',
    'display:none;flex-direction:column;background:#0e0c14;',
    'border-top:2px solid var(--md-primary-fg-color,#3f51b5);',
    'box-shadow:0 -4px 16px rgba(0,0,0,.4)}',
    '#skw-desk-panel.open{display:flex}',
    '#skw-desk-bar{display:flex;align-items:center;gap:.8em;padding:.3em .8em;',
    'background:#17141f;color:#cdd2da;font:12px var(--md-code-font-family,monospace)}',
    '#skw-desk-bar .t{color:#9d7cff;font-weight:600;margin-right:auto}',
    '#skw-desk-bar button{background:none;border:none;color:#9aa0a6;',
    'font:inherit;cursor:pointer;padding:.2em .4em}',
    '#skw-desk-bar button:hover{color:#fff}',
    '#skw-desk-frame{flex:1;border:0;width:100%;background:#0e0c14}',
    '#skw-desk-pill{position:fixed;bottom:14px;right:14px;z-index:3;',
    'font:600 13px/1 var(--md-code-font-family,monospace);cursor:pointer;',
    'padding:.6em .9em;border:none;border-radius:2em;display:none;',
    'background:var(--md-primary-fg-color,#3f51b5);color:var(--md-primary-bg-color,#fff);',
    'box-shadow:var(--md-shadow-z2,0 2px 6px rgba(0,0,0,.3))}',
    '#skw-desk-pill:hover{filter:brightness(1.15)}'
  ].join('');
  var style = document.createElement('style');
  style.textContent = css;
  document.body.appendChild(style);

  var panel = document.createElement('div');
  panel.id = 'skw-desk-panel';
  panel.innerHTML =
    '<div id="skw-desk-bar">' +
    '<span class="t">skywire terminal</span>' +
    '<button id="skw-desk-min" title="minimize (keeps running) and read the docs underneath">&#x2013; browse this page</button>' +
    '</div>';
  document.body.appendChild(panel);

  var pill = document.createElement('button');
  pill.id = 'skw-desk-pill';
  pill.type = 'button';
  pill.textContent = '>_ terminal';
  pill.title = 'surface the skywire terminal (keeps its state while you read)';
  document.body.appendChild(pill);

  var frame = null;         // created lazily — only the root (or a click) pays for wasm
  var userMinimized = false; // an explicit minimize wins over root auto-open

  function normPath(p) { return p.replace(/index\.html$/, ''); }
  function atRoot() { return normPath(location.pathname) === normPath(rootPath); }

  // The layer starts below the Material header + nav tabs so the docs
  // entry points stay reachable while the terminal greets. Measured, not
  // hardcoded — tabs collapse into the drawer on mobile.
  function topOffset() {
    var y = 0;
    ['.md-header', '.md-tabs'].forEach(function (sel) {
      var el = document.querySelector(sel);
      if (!el) return;
      var r = el.getBoundingClientRect();
      if (r.height > 0 && r.bottom > y) y = r.bottom;
    });
    return Math.max(0, Math.round(y));
  }
  function place() { panel.style.top = topOffset() + 'px'; }

  function open() {
    if (!frame) {
      frame = document.createElement('iframe');
      frame.id = 'skw-desk-frame';
      frame.src = pgURL;
      frame.setAttribute('allow', 'clipboard-read; clipboard-write');
      panel.appendChild(frame);
    }
    place();
    panel.classList.add('open');
    pill.style.display = 'none';
  }
  function minimize() {
    panel.classList.remove('open'); // display:none — iframe stays alive
    if (frame) pill.style.display = 'block';
  }

  panel.querySelector('#skw-desk-min').addEventListener('click', function () {
    userMinimized = true;
    minimize();
  });
  pill.addEventListener('click', function () {
    userMinimized = false;
    open();
  });
  window.addEventListener('resize', function () {
    if (panel.classList.contains('open')) place();
  });

  // Surface the desk on clicks on same-tab links to /playground/ (the
  // "Terminal" nav item, the index cards) instead of leaving the shell.
  // Capture phase so it wins over instant loading. Direct URL hits on
  // /playground/ still work — the standalone page redirects to the root.
  document.addEventListener('click', function (e) {
    var a = e.target && e.target.closest && e.target.closest('a[href]');
    if (!a || a.target === '_blank' || e.ctrlKey || e.metaKey || e.shiftKey || e.button !== 0) return;
    var href = a.href.split('#')[0].split('?')[0];
    if (href === pgURL || href === pgURL + 'index.html' || href + '/' === pgURL) {
      e.preventDefault();
      e.stopPropagation();
      userMinimized = false;
      open();
    }
  }, true);

  function onNavigate() {
    if (atRoot()) {
      if (!userMinimized) open(); else minimize();
    } else {
      if (panel.classList.contains('open')) { userMinimized = false; minimize(); }
      else if (frame) pill.style.display = 'block';
    }
  }
  window.__skwDeskShell = { onNavigate: onNavigate };

  // Material's instant loading publishes document$ (fires after every
  // content swap, and once on subscribe). Belt and suspenders with the
  // re-execution path at the top of this file.
  if (window.document$ && typeof window.document$.subscribe === 'function') {
    window.document$.subscribe(function () { onNavigate(); });
  } else {
    onNavigate();
  }
})();
