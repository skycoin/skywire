// pkg/wasmhv/browse-transport.js c4-app-skynet
// Skywire's transport for the real-origin browser. Loads on the visor app origin
// V, after realorigin's responder.js, and is the only skywire-specific half of
// that bridge: the responder owns the trust boundary — origin validation, id ->
// target resolution, handing the frame a port bound to one target — and calls in
// here to actually fetch.
//
// A browse frame supplies a path. The target comes from the descriptor browse.js
// registered, so a frame can never ask for another site's content, and nothing
// here ever sees the identity key: it calls V's own first-party skywireVisor,
// which holds it.
(function () {
  'use strict';
  if (globalThis.__skywireBrowseTransportInstalled) { return; }
  globalThis.__skywireBrowseTransportInstalled = true;

  function pathOf(u, fb) {
    try { var x = new URL(u); return x.pathname + x.search; } catch (e) { return fb || '/'; }
  }

  function isBrowseOrigin(origin) {
    try {
      var suffix = (globalThis.__SKYWIRE_BROWSE_ORIGIN__ && globalThis.__SKYWIRE_BROWSE_ORIGIN__.suffix) || '.mesh.localhost';
      return new URL(origin).hostname.endsWith(suffix);
    } catch (e) { return false; }
  }

  // fetchFor is the transport realorigin calls. descriptor is what browse.js
  // registered: {net:'dmsg'|'skynet', host} or {net:'skysocks', base}.
  function fetchFor(descriptor, req) {
    var v = globalThis.skywireVisor;
    if (!v || !v.fetchDmsg) { return Promise.reject(new Error('visor not ready')); }
    var method = req.method || 'GET';
    var headers = req.headers || {};
    var bodyU8 = req.body ? new Uint8Array(req.body) : null;
    if (descriptor.net === 'skysocks') {
      // Map a request aimed at origin B back to its real clearnet URL. An
      // absolute cross-origin request is already a real URL and passes through.
      var realUrl = req.url;
      try {
        var u = new URL(req.url);
        if (isBrowseOrigin(u.origin)) { realUrl = descriptor.base + u.pathname + u.search; }
      } catch (e) {}
      return v.fetchClearnet('', method, realUrl, bodyU8, 'browse', headers);
    }
    return v.fetchDmsg(descriptor.host, method, pathOf(req.url, req.path), bodyU8, headers);
  }

  // The visor's skysocks-lite path calls __skywireProxyLog(winId, line) for every
  // route-setup and exit-selection step — the same trace `skywire cli proxy start
  // --verbose` prints. Wrap it so those lines reach both places that want them:
  // browse.js's per-window panes, and the interstitial of whichever browse frame
  // is still waiting on its first response.
  //
  // Order-independent with browse.js, whichever loads first: this replicates the
  // pane routing, and browse.js only installs its own sink if none exists.
  if (!(globalThis.__skywireProxyLog && globalThis.__skywireProxyLog.__browseWrapped)) {
    var sink = function (winId, line) {
      try {
        var pane = (globalThis.__skywireBrowserPanes || {})[winId];
        if (pane) { pane(line); }
      } catch (e) {}
      try { globalThis.realOrigin.progress(line); } catch (e) {}
    };
    sink.__browseWrapped = true;
    globalThis.__skywireProxyLog = sink;
  }

  var cfg = (globalThis.__SKYWIRE_BROWSE_ORIGIN__ || {});
  globalThis.realOrigin.configure({
    suffix: cfg.suffix || '.mesh.localhost',
    fetch: fetchFor,
  });
})();
