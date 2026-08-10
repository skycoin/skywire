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
      var h = new URL(origin).hostname;
      return /\.mesh\.localhost$/.test(h);
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

  function serve(req) {
    var v = globalThis.skywireVisor;
    if (!v || !v.fetchDmsg) { return Promise.reject(new Error('visor not ready')); }
    var method = req.method || 'GET';
    var headers = req.headers || {};
    var bodyU8 = req.body ? new Uint8Array(req.body) : null;
    if (req.net === 'skysocks') {
      // clearnet egress via skysocks-lite (empty exit = auto pool w/ failover)
      return v.fetchClearnet('', method, req.url, bodyU8, '', headers);
    }
    // mesh over dmsg (wasm has no separate skynet round-tripper today → dmsg
    // serves dmsg + auto).
    return v.fetchDmsg(req.host, method, pathOf(req.url, req.path), bodyU8, headers);
  }

  window.addEventListener('message', function (e) {
    var d = e.data || {};
    if (d.type !== 'mesh-hello') { return; }
    if (!isBrowseOrigin(e.origin) || !e.source) { return; }
    var mc = new MessageChannel();
    mc.port1.onmessage = function (ev) {
      var q = ev.data || {};
      serve(q.req || {}).then(function (r) {
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
