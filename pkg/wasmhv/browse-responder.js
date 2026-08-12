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
      return v.fetchClearnet('', method, realUrl, bodyU8, '', headers);
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
    mc.port1.onmessage = function (ev) {
      var q = ev.data || {};
      serve(desc, q.req || {}).then(function (r) {
        r = r || {};
        var ab = toArrayBuffer(r.body);
        mc.port1.postMessage({ id: q.id, status: r.status || 200, headers: r.headers || {}, body: ab }, ab ? [ab] : []);
      }).catch(function (err) {
        mc.port1.postMessage({ id: q.id, error: String((err && err.message) || err) });
      });
    };
    // Hand the browse frame its private request port (targetOrigin = the frame's
    // own origin, so only it receives the capability).
    e.source.postMessage({ type: 'mesh-helper-ready' }, e.origin, [mc.port2]);
  });
})();
