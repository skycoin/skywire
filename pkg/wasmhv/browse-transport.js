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

  // siteHost is the host the frame stands for; sameSite says whether a request
  // goes to it (or a subdomain, either way round). The page's cookies live on
  // the frame's one origin, so they travel only to its own site — never to a
  // third party the page also fetches from.
  function siteHost(descriptor) {
    try { return descriptor.net === 'skysocks' ? new URL(descriptor.base).hostname : String(descriptor.host || ''); } catch (e) { return ''; }
  }
  function sameSite(descriptor, url) {
    var a = siteHost(descriptor).toLowerCase().replace(/^www\./, ''), b;
    try { b = new URL(url).hostname.toLowerCase().replace(/^www\./, ''); } catch (e) { return false; }
    return !!a && (a === b || b.endsWith('.' + a) || a.endsWith('.' + b));
  }

  function fetchFor(descriptor, req) {
    var t = globalThis.__netscrapeFetch;
    if (typeof t !== 'function') { return Promise.reject(new Error('no mesh transport on this page')); }
    var target = urlFor(descriptor, req);
    var own = sameSite(descriptor, target);
    var headers = Object.assign({}, req.headers || {});
    if (!own) { delete headers['x-realorigin-cookie']; }
    return Promise.resolve(t(target, {
      method: req.method || 'GET',
      headers: headers,
      // bottle's httpExchange wants a Uint8Array (it sets Content-Length from
      // .length); realorigin hands over an ArrayBuffer.
      body: req.body ? new Uint8Array(req.body) : null,
    })).then(function (res) {
      // x-realorigin-url names the real address — after redirects, when the
      // transport says where it landed — so the frame can report it as its
      // referrer: some sites refuse to run framed unless their own site sent
      // them (realorigin bootstrap.html).
      var h = headersOf(res);
      h['x-realorigin-url'] = h['x-browse-final-url'] || target;
      delete h['x-browse-final-url'];
      if (!own) { delete h['x-realorigin-set-cookie']; }
      // The body stays a stream: the responder transfers it to the frame and
      // the service worker answers with it, so a large download is never
      // held whole.
      return { status: res.status, headers: h, body: res.body || null };
    });
  }

  // websocketFor hands a frame's WebSocket to the desk's relay. A socket aimed
  // at B itself (a page building its URL from location) is rebased onto the
  // real site, as urlFor does for fetches; the Origin sent is the real site's.
  function websocketFor(descriptor, ws, port) {
    var t = globalThis.__netscrapeWebSocket;
    if (typeof t !== 'function') {
      try {
        port.postMessage({ t: 'error', message: 'no websocket transport on this page' });
        port.postMessage({ t: 'close', code: 1006, reason: '', wasClean: false });
      } catch (e) { /* frame gone */ }
      return;
    }
    var url = ws.url;
    try {
      var u = new URL(ws.url);
      if (isBrowseOrigin(u.origin.replace(/^ws/, 'http'))) {
        var path = u.pathname + u.search;
        url = descriptor.net === 'skysocks' ? descriptor.base.replace(/^http/, 'ws') + path : 'ws://' + descriptor.host + path;
      }
    } catch (e) { /* the relay reports a bad url */ }
    var origin = descriptor.net === 'skysocks' ? descriptor.base : 'http://' + descriptor.host;
    t({ url: url, protocols: ws.protocols || [], origin: origin }, port);
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

  // Web Push for a hypervisor in a browse frame. A cross-origin frame cannot ask
  // for notification permission, so it asks this page, which subscribes a
  // push-only worker of its own (push-sw.js) with the frame's VAPID key and
  // hands the subscription back for the frame's hypervisor to store. Each frame
  // origin gets its own scope, since one registration holds one subscription
  // and the desk's own hypervisor keeps the root one. The scope also carries the
  // site's address, so a notification click can open it again.
  var siteOf = Object.create(null);
  function b64url(s) {
    return btoa(unescape(encodeURIComponent(s))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function keyBytes(k) {
    var raw = atob(String(k).replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - String(k).length % 4) % 4));
    var out = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) { out[i] = raw.charCodeAt(i); }
    return out;
  }
  function sameKey(a, b) {
    if (!a) { return false; }
    var x = new Uint8Array(a);
    return x.length === b.length && x.every(function (v, i) { return v === b[i]; });
  }
  function pushReg(origin) {
    var base = location.origin + '/push/' + new URL(origin).hostname.split('.')[0] + '/';
    return navigator.serviceWorker.getRegistrations().then(function (list) {
      return { base: base, reg: list.find(function (r) { return r.scope.indexOf(base) === 0; }) || null };
    });
  }
  function activated(reg) {
    if (reg.active) { return Promise.resolve(reg); }
    var w = reg.installing || reg.waiting;
    if (!w) { return Promise.reject(new Error('the push worker did not install')); }
    return new Promise(function (resolve, reject) {
      w.addEventListener('statechange', function () {
        if (w.state === 'activated') { resolve(reg); }
        if (w.state === 'redundant') { reject(new Error('the push worker failed to install')); }
      });
    });
  }
  function pushSubscribe(origin, key) {
    return Notification.requestPermission().then(function (perm) {
      if (perm !== 'granted') { throw new Error('Notifications are blocked for this desk. Allow them in the browser settings and try again.'); }
      return pushReg(origin);
    }).then(function (f) {
      var site = siteOf[origin] ? new URL(siteOf[origin]).origin + '/' : '';
      var scope = f.base + (site ? b64url(site) + '/' : '');
      var stale = f.reg && f.reg.scope !== scope ? f.reg.unregister() : Promise.resolve();
      return stale.then(function () { return navigator.serviceWorker.register('/push-sw.js', { scope: scope }); });
    }).then(activated).then(function (reg) {
      var k = keyBytes(key);
      return reg.pushManager.getSubscription().then(function (sub) {
        if (sub && sameKey(sub.options.applicationServerKey, k)) { return sub; }
        return (sub ? sub.unsubscribe() : Promise.resolve()).then(function () {
          return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: k });
        });
      });
    }).then(function (sub) { return { sub: sub.toJSON() }; });
  }
  function pushEndpoint(origin) {
    return pushReg(origin).then(function (f) {
      return f.reg ? f.reg.pushManager.getSubscription() : null;
    }).then(function (sub) { return { endpoint: sub ? sub.endpoint : '' }; });
  }
  globalThis.addEventListener('message', function (e) {
    var d = e.data || {};
    if (!isBrowseOrigin(e.origin) || !e.source) { return; }
    if (d.type === 'realorigin-location' && d.url) { siteOf[e.origin] = String(d.url); return; }
    if (d.type !== 'skywire-push') { return; }
    var src = e.source;
    var reply = function (m) {
      m.type = 'skywire-push-reply';
      m.id = d.id;
      try { src.postMessage(m, e.origin); } catch (x) { /* frame gone */ }
    };
    if (!('serviceWorker' in navigator) || !('PushManager' in globalThis)) {
      reply({ error: 'This browser does not support push notifications.' });
      return;
    }
    reply({ ack: true });
    (d.op === 'subscribe' ? pushSubscribe(e.origin, d.key) : pushEndpoint(e.origin)).then(reply, function (err) {
      reply({ error: String((err && err.message) || err) });
    });
  });
  // push-sw.js asks the page to reopen the site a clicked notification came from.
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.addEventListener('message', function (e) {
      var d = e.data || {};
      if (d.type !== 'skywire-push-open' || !d.url) { return; }
      try { globalThis.__skywireDesk.launch('browser', String(d.url)); } catch (x) { /* no desk */ }
    });
    try { navigator.serviceWorker.startMessages(); } catch (x) { /* already started */ }
  }

  var cfg = (globalThis.__SKYWIRE_BROWSE_ORIGIN__ || {});
  globalThis.realOrigin.configure({
    suffix: cfg.suffix || '.mesh.localhost',
    fetch: fetchFor,
    websocket: websocketFor,
  });
})();
