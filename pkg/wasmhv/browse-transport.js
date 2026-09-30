// pkg/wasmhv/browse-transport.js c4-app-skynet
// Skywire's transport for the real-origin browser. Loads on the visor app origin
// V, after realorigin's responder.js, and is the only skywire-specific half of
// that bridge: the responder owns the trust boundary — origin validation, id ->
// target resolution, handing the frame a port bound to one target — and calls in
// here to actually fetch.
//
// A browse frame supplies a path. The target comes from the descriptor the
// browser registered, so a frame can never ask for another site's content, and
// nothing here ever sees the identity key.
//
// It calls globalThis.__netscrapeFetch — the ONE mesh/clearnet transport the
// desk installs (desk-boot.js), the same one netscrape's transcoder uses. It
// used to call globalThis.skywireVisor.fetchDmsg instead, which is the function
// table of the legacy in-page visor (cmd/wasm-visor). The SERVED desk publishes
// no skywireVisor at all — its visor is a separate instance in the exec worker,
// reached only over the virtual loopback — so every fetch through here answered
// "visor not ready" and the real-origin browser could not work on `hv serve`.
// Sharing one transport is also what keeps the two render paths from drifting.
(function () {
  'use strict';
  if (globalThis.__skywireBrowseTransportInstalled) { return; }
  globalThis.__skywireBrowseTransportInstalled = true;

  function isBrowseOrigin(origin) {
    try {
      var suffix = (globalThis.__SKYWIRE_BROWSE_ORIGIN__ && globalThis.__SKYWIRE_BROWSE_ORIGIN__.suffix) || '.mesh.localhost';
      return new URL(origin).hostname.endsWith(suffix);
    } catch (e) { return false; }
  }

  // headersOf flattens a Response's headers into the plain object realorigin
  // hands back to the browse frame's service worker.
  function headersOf(res) {
    var out = {};
    try { res.headers.forEach(function (v, k) { out[k] = v; }); } catch (e) { /* no iterator */ }
    return out;
  }

  // urlFor maps a request arriving from browse origin B onto the address the
  // transport understands. B's hostname is a content-addressed hash and means
  // nothing to the mesh, so the DESCRIPTOR supplies the real target and B
  // supplies only the path — the property that stops one frame asking for
  // another site's content.
  function urlFor(descriptor, req) {
    var path = '/';
    try { var x = new URL(req.url); path = x.pathname + x.search; } catch (e) { path = req.path || '/'; }
    if (descriptor.net === 'skysocks') {
      // An absolute cross-origin request is already a real URL and passes
      // through; one aimed at B is rebased onto the site it stands for.
      try {
        var u = new URL(req.url);
        if (!isBrowseOrigin(u.origin)) { return req.url; }
      } catch (e) { /* relative — rebase it */ }
      return descriptor.base + path;
    }
    // dmsg / skynet: the descriptor host already carries its network suffix
    // (normResolverHost appends it), which is how __netscrapeFetch routes.
    return 'http://' + descriptor.host + path;
  }

  function fetchFor(descriptor, req) {
    var t = globalThis.__netscrapeFetch;
    if (typeof t !== 'function') { return Promise.reject(new Error('no mesh transport on this page')); }
    var target = urlFor(descriptor, req);
    return Promise.resolve(t(target, {
      method: req.method || 'GET',
      headers: req.headers || {},
      // bottle's httpExchange wants a Uint8Array (it sets Content-Length from
      // .length); realorigin hands over an ArrayBuffer.
      body: req.body ? new Uint8Array(req.body) : null,
    })).then(function (res) {
      return res.arrayBuffer().then(function (buf) {
        // x-realorigin-url names the real address, so the frame can report it
        // as its referrer: some sites refuse to run framed unless their own
        // site sent them (realorigin bootstrap.html).
        var h = headersOf(res);
        h['x-realorigin-url'] = target;
        return { status: res.status, headers: h, body: new Uint8Array(buf) };
      });
    });
  }

  // The visor's skysocks-lite path calls __skywireProxyLog(winId, line) for every
  // route-setup and exit-selection step — the same trace `skywire cli proxy start
  // --verbose` prints. Wrap it so those lines reach both places that want them:
  // the browser's per-window panes, and the interstitial of whichever browse
  // frame is still waiting on its first response.
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
