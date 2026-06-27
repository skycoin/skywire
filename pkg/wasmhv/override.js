// override.js — installed (as a classic <script>) BEFORE the inlined Angular
// module scripts, so Angular's HttpClient uses the shimmed XMLHttpRequest. It
// boots the WASM dmsg client and routes every same-origin HTTP request (the
// app's /api/* calls, asset fetches, etc.) over dmsg to the configured
// hypervisor PK via globalThis.skywireDmsg.fetch. No Service Worker, no server:
// this is what makes the inlined single-file UI work from file://.
//
// Config is injected as window.__SKYWIRE_HV__ = {pk, seedpk, seedws, disc, sk,
// encsk}. When encsk (password-encrypted secret key) is present, the page first
// prompts for a password and decrypts it with WebCrypto before connecting; the
// plaintext key never touches disk.
//
// CONTEXT DETECTION (the unified-UI primitive): the override only takes over
// when it has a mode to take over FOR — a remote hypervisor PK (CFG.pk, viewer
// mode) or CFG.standalone (this tab is the hypervisor). With neither set (or an
// explicit CFG.served), the shims are NOT installed and the wasm is NOT booted:
// native XHR/fetch reach whatever backend served the page, so the SAME artifact
// behaves as the ordinary visor-served hypervisor UI when a visor serves it, and
// as a serverless hypervisor when opened from file://. See
// docs/design/unified-wasm-hypervisor-ui.md.
(function () {
  var CFG = window.__SKYWIRE_HV__ || {};
  var readyP = null;

  function log(m) { try { console.log('[skywire-hv] ' + m); } catch (e) {} }

  // active = "do I have a mode to take over for?" When false, stay dormant and
  // let the serving backend's /api answer natively (served mode).
  var active = !CFG.served && (Boolean(CFG.pk) || Boolean(CFG.standalone) || Boolean(CFG.visor));
  if (!active) {
    log('dormant: no hypervisor mode configured; native fetch/XHR pass through to the serving backend');
    return;
  }

  // decryptKey returns the dmsg secret-key hex: plaintext CFG.sk if present,
  // else password-decrypts CFG.encsk (AES-GCM, PBKDF2-SHA256). Format of encsk:
  // base64 of [16-byte salt | 12-byte iv | ciphertext]. Empty → ephemeral key.
  async function resolveSK() {
    if (CFG.sk) return CFG.sk;
    if (!CFG.encsk) return ''; // ephemeral identity
    var pw = window.prompt('Enter password to unlock the hypervisor key:');
    if (pw === null) throw new Error('password entry cancelled');
    var raw = Uint8Array.from(atob(CFG.encsk), function (c) { return c.charCodeAt(0); });
    var salt = raw.slice(0, 16), iv = raw.slice(16, 28), ct = raw.slice(28);
    var enc = new TextEncoder();
    var baseKey = await crypto.subtle.importKey('raw', enc.encode(pw), 'PBKDF2', false, ['deriveKey']);
    var aesKey = await crypto.subtle.deriveKey(
      { name: 'PBKDF2', salt: salt, iterations: 200000, hash: 'SHA-256' },
      baseKey, { name: 'AES-GCM', length: 256 }, false, ['decrypt']);
    var pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: iv }, aesKey, ct);
    return new TextDecoder().decode(pt);
  }

  // ensure boots the wasm client + connects (once). The first shimmed request
  // awaits this, so the app's initial /api call blocks until dmsg is up.
  //
  // Three modes:
  //  - viewer (default): route /api over dmsg to a REMOTE hypervisor CFG.pk.
  //  - standalone (CFG.standalone): THIS tab IS the hypervisor (wasm dmsg client) —
  //    start serveHypervisor() so visors listing this PK dial in, and route /api
  //    to the in-wasm core (hvApi) instead of a remote fetch.
  //  - visor (CFG.visor): THIS tab is a full wasm-VISOR (TinyGo: edge + router +
  //    its own hypervisor). boot() brings the whole stack up (and the HV core);
  //    /api routes to the visor's in-wasm hvApi, which surfaces THIS visor plus
  //    any visors that dial in. globalThis.skywireVisor, not skywireDmsg.
  function ensure() {
    if (readyP) return readyP;
    readyP = (async function () {
      var sk = await resolveSK();
      if (CFG.visor) {
        while (!self.skywireVisor) { await new Promise(function (r) { setTimeout(r, 10); }); }
        var vpk = await self.skywireVisor.boot(sk, CFG.seedpk, CFG.seedws, CFG.disc);
        log('wasm-visor booted as ' + vpk + ' (edge + hypervisor; /api → in-wasm core)');
        return;
      }
      while (!self.skywireDmsg) { await new Promise(function (r) { setTimeout(r, 10); }); }
      var pk = await self.skywireDmsg.connect(sk, CFG.seedpk, CFG.seedws, CFG.disc);
      if (CFG.standalone) {
        await self.skywireDmsg.serveHypervisor();
        log('standalone hypervisor serving as ' + pk + ' (visors dial in on port 46)');
      } else {
        log('connected over dmsg as ' + pk + ' → hypervisor ' + CFG.pk);
      }
    })();
    return readyP;
  }

  // dispatch routes one same-origin request: a wasm-visor / standalone hypervisor
  // tab answers /api from its in-wasm core (hvApi); a viewer routes over dmsg to a
  // remote hypervisor (fetch). Both resolve to {status, body:Uint8Array, headers};
  // hvApi has no response headers of its own, so we synthesize a JSON content-type.
  function dispatch(method, path, body, headers) {
    if (CFG.visor) {
      return self.skywireVisor.hvApi(method, path, body == null ? null : String(body))
        .then(function (r) { return { status: r.status, body: r.body, headers: { 'Content-Type': 'application/json' } }; });
    }
    if (CFG.standalone) {
      return self.skywireDmsg.hvApi(method, path, body == null ? null : String(body))
        .then(function (r) { return { status: r.status, body: r.body, headers: { 'Content-Type': 'application/json' } }; });
    }
    return self.skywireDmsg.fetch(CFG.pk, method, path, body == null ? null : String(body), headers);
  }

  function isLocal(url) {
    try {
      var u = new URL(url, location.href);
      if (u.protocol === 'data:' || u.protocol === 'blob:') return false;
      return u.origin === location.origin || u.protocol === 'file:';
    } catch (e) { return String(url).charAt(0) === '/'; }
  }
  function pathOf(url) {
    try { var u = new URL(url, location.href); return u.pathname + u.search; }
    catch (e) { return url; }
  }

  // assetResponse serves a runtime asset (i18n JSON, images, fonts) inlined into
  // a standalone file by `cli hv gen` (self.__SKYWIRE_ASSETS__: path -> {ct,b}).
  // This lets the UI render translations + images with NO backend and NO dmsg
  // round-trip. Returns null for anything not in the map (e.g. /api/*).
  function assetResponse(path) {
    var m = self.__SKYWIRE_ASSETS__;
    if (!m) return null;
    var p = path.split('?')[0].split('#')[0];
    var bare = p.replace(/^\//, '');
    var a = m[p] || m[bare] || m['/' + bare];
    if (!a) return null;
    var bytes = Uint8Array.from(atob(a.b), function (c) { return c.charCodeAt(0); });
    return { status: 200, body: bytes, headers: { 'Content-Type': a.ct } };
  }

  // serve resolves one same-origin request: an inlined asset first (instant, no
  // dmsg), else the dmsg/hvApi dispatch once the client is up.
  function serve(method, path, body, headers) {
    var a = assetResponse(path);
    if (a) return Promise.resolve(a);
    return ensure().then(function () { return dispatch(method, path, body, headers); });
  }

  // --- XMLHttpRequest shim (Angular HttpClient's default backend) ---
  var RealXHR = window.XMLHttpRequest;
  function ShimXHR() {
    this._m = 'GET'; this._u = ''; this._h = {}; this.readyState = 0; this.status = 0;
    this.statusText = ''; this.responseText = ''; this.response = ''; this.responseType = '';
    this.withCredentials = false; this._cb = {}; this._respHeaders = ''; this._respMap = {};
  }
  ShimXHR.prototype.open = function (m, u) { this._m = m; this._u = u; this._set(1); };
  ShimXHR.prototype.setRequestHeader = function (k, v) { this._h[k] = v; };
  ShimXHR.prototype.getAllResponseHeaders = function () { return this._respHeaders; };
  ShimXHR.prototype.getResponseHeader = function (k) { return this._respMap[String(k).toLowerCase()] || null; };
  ShimXHR.prototype.abort = function () {};
  ShimXHR.prototype.addEventListener = function (ev, fn) { this._cb[ev] = fn; };
  ShimXHR.prototype.removeEventListener = function () {};
  ShimXHR.prototype._set = function (rs) {
    this.readyState = rs;
    if (this.onreadystatechange) this.onreadystatechange();
  };
  ShimXHR.prototype._emit = function (ev) {
    if (this._cb[ev]) this._cb[ev]({ type: ev });
    if (this['on' + ev]) this['on' + ev]({ type: ev });
  };
  ShimXHR.prototype.send = function (body) {
    var self_ = this;
    if (!isLocal(this._u)) { // non-local: fall back to the real XHR transparently
      var real = new RealXHR();
      real.open(this._m, this._u, true);
      for (var k in this._h) real.setRequestHeader(k, this._h[k]);
      real.onreadystatechange = function () {
        self_.readyState = real.readyState; self_.status = real.status;
        self_.responseText = real.responseText; self_.response = real.response;
        if (self_.onreadystatechange) self_.onreadystatechange();
        if (real.readyState === 4) self_._emit('load');
      };
      real.send(body); return;
    }
    serve(self_._m, pathOf(self_._u), body, self_._h).then(function (r) {
      self_.status = r.status; self_.statusText = '';
      var txt = new TextDecoder().decode(r.body);
      self_.responseText = txt;
      if (self_.responseType === 'json') {
        try { self_.response = JSON.parse(txt); } catch (e) { self_.response = null; }
      } else { self_.response = txt; }
      self_._respMap = {}; var rh = '';
      for (var k in r.headers) { self_._respMap[k.toLowerCase()] = r.headers[k]; rh += k + ': ' + r.headers[k] + '\r\n'; }
      self_._respHeaders = rh;
      self_._set(4);
      self_._emit('load');
    }).catch(function (e) {
      log('request error ' + self_._u + ': ' + e);
      self_.status = 0; self_._set(4); self_._emit('error');
    });
  };
  window.XMLHttpRequest = ShimXHR;

  // --- fetch shim (for code paths that use fetch directly) ---
  var realFetch = window.fetch ? window.fetch.bind(window) : null;
  window.fetch = function (input, init) {
    var url = (typeof input === 'string') ? input : (input && input.url);
    if (!isLocal(url)) return realFetch ? realFetch(input, init) : Promise.reject(new Error('no fetch'));
    init = init || {};
    var headers = {};
    if (init.headers) {
      if (init.headers.forEach) init.headers.forEach(function (v, k) { headers[k] = v; });
      else for (var k in init.headers) headers[k] = init.headers[k];
    }
    return serve(init.method || 'GET', pathOf(url), init.body, headers).then(function (r) {
      var h = new Headers();
      for (var k in r.headers) { try { h.set(k, r.headers[k]); } catch (e) {} }
      return new Response(r.body, { status: r.status, headers: h });
    });
  };

  log('HTTP-over-dmsg override installed (hypervisor ' + CFG.pk + ')');
})();
