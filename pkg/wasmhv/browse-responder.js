// pkg/wasmhv/browse-responder.js c4-app-skynet
// Mesh-fetch responder, injected into the VISOR app origin V (the page that hosts
// the WinBox browser and holds the first-party, booted globalThis.skywireVisor).
// It is the trust boundary for the real-origin mesh browser (RFC §4b): a browse
// origin B (<pk>.mesh.localhost), embedded as an iframe inside V, posts
// {type:'mesh-hello'}; we validate its origin, hand back a private MessagePort,
// and service fetch requests on it via V's OWN skywireVisor — B never gets the
// key, only a "fetch this mesh URL" capability.
//
// This replaces the old cross-origin helper iframe, which Storage Partitioning
// put in a different partition than V's SharedWorker visor (so it couldn't reach
// the booted visor). The first-party parent (V) has no such problem.
(function () {
  'use strict';
  if (window.__meshResponderInstalled) { return; }
  window.__meshResponderInstalled = true;

  // --- verbose proxy-log fan-out -------------------------------------------
  // The wasm visor's skysocks-lite path calls globalThis.__skywireProxyLog(winId,
  // line) for every route-setup / exit-selection / request step (see emitProxyLog
  // in cmd/wasm-visor/skysocks_js.go) — the same trace `skywire cli proxy start
  // --verbose` prints. We install a superset sink: it preserves browse.js's
  // per-window pane routing (__skywireBrowserPanes) AND fans every line out to a
  // set of subscribers, so the mesh browser's interstitial can show the live,
  // step-by-step connection log while a navigation is in flight. Order-independent
  // vs browse.js (whichever loads first): ours replicates pane routing, and
  // browse.js only installs its own if none exists.
  var logSubs = (globalThis.__meshLogSubs = globalThis.__meshLogSubs || new Set());
  if (!(globalThis.__skywireProxyLog && globalThis.__skywireProxyLog.__meshWrapped)) {
    var sink = function (winId, line) {
      try { var p = (globalThis.__skywireBrowserPanes || {})[winId]; if (p) { p(line); } } catch (e) {}
      logSubs.forEach(function (fn) { try { fn(winId, line); } catch (e) {} });
    };
    sink.__meshWrapped = true;
    globalThis.__skywireProxyLog = sink;
  }

  // isBrowseOrigin: only serve real-origin browse frames. Local: *.mesh.localhost.
  // Hosted browse domains would be added here (kept in sync with the server's
  // isMeshBrowseHost / the configured browse suffix).
  function isBrowseOrigin(origin) {
    try {
      var suffix = (globalThis.__SKYWIRE_BROWSE_ORIGIN__ && globalThis.__SKYWIRE_BROWSE_ORIGIN__.suffix) || '.mesh.localhost';
      return new URL(origin).hostname.endsWith(suffix);
    } catch (e) { return false; }
  }

  function pathOf(u, fb) {
    try { var x = new URL(u); return x.pathname + x.search; } catch (e) { return fb || '/'; }
  }

  function toArrayBuffer(body) {
    if (!body) { return null; }
    if (body instanceof ArrayBuffer) { return body; }
    if (body.buffer) { return body.buffer.slice(body.byteOffset || 0, (body.byteOffset || 0) + body.byteLength); }
    return null;
  }

  function serve(desc, req) {
    var v = globalThis.skywireVisor;
    if (!v || !v.fetchDmsg) { return Promise.reject(new Error('visor not ready')); }
    var method = req.method || 'GET';
    var headers = req.headers || {};
    var bodyU8 = req.body ? new Uint8Array(req.body) : null;
    if (desc.net === 'skysocks') {
      // Map a request to origin B back to its real clearnet URL; an absolute
      // cross-origin request is already a real clearnet URL and passes through.
      var realUrl = req.url;
      try {
        var u = new URL(req.url);
        if (isBrowseOrigin(u.origin)) { realUrl = desc.base + u.pathname + u.search; }
      } catch (e) {}
      return v.fetchClearnet('', method, realUrl, bodyU8, 'browse', headers);
    }
    return v.fetchDmsg(desc.host, method, pathOf(req.url, req.path), bodyU8, headers);
  }

  window.addEventListener('message', function (e) {
    var d = e.data || {};
    if (d.type !== 'mesh-hello') { return; }
    if (!isBrowseOrigin(e.origin) || !e.source) { return; }
    var desc = (globalThis.__meshOrigins || {})[String(d.shortid || '')];
    if (!desc) { try { e.source.postMessage({ type: 'mesh-helper-ready', error: 'unknown browse origin' }, e.origin); } catch (x) {} return; }
    var mc = new MessageChannel();
    // Stream the visor's verbose route-setup log to THIS browse frame while its
    // first (top-navigation) request is in flight — that IS the interstitial
    // window. A subresource fetch on the loaded page reuses the same port, but by
    // then the interstitial is gone (document.write replaced the shell), so we
    // unsubscribe once the first response resolves.
    var firstDone = false;
    var logSub = function (winId, line) {
      try { mc.port1.postMessage({ progress: { winId: winId, line: line } }); } catch (x) {}
    };
    logSubs.add(logSub);
    function stopLog() { if (!firstDone) { firstDone = true; try { logSubs.delete(logSub); } catch (x) {} } }
    mc.port1.onmessage = function (ev) {
      var q = ev.data || {};
      serve(desc, q.req || {}).then(function (r) {
        r = r || {};
        var ab = toArrayBuffer(r.body);
        mc.port1.postMessage({ id: q.id, status: r.status || 200, headers: r.headers || {}, body: ab }, ab ? [ab] : []);
        stopLog();
      }).catch(function (err) {
        mc.port1.postMessage({ id: q.id, error: String((err && err.message) || err) });
        stopLog();
      });
    };
    // Hand the browse frame its private request port (targetOrigin = the frame's
    // own origin, so only it receives the capability).
    e.source.postMessage({ type: 'mesh-helper-ready' }, e.origin, [mc.port2]);
  });
})();
