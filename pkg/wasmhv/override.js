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

  // file:// history-API guard. When the single-file UI is opened from file://, the
  // document has an OPAQUE origin ('null'), where History.pushState/replaceState
  // reject any state whose URL differs from the current document in more than the
  // fragment — throwing SecurityError. Angular's router (hash mode, useHash:true)
  // still calls replaceState with a path-bearing URL on init, so on file:// it
  // throws and cascades into "Illegal invocation" errors that abort UI load. Hash
  // routing only needs location.hash, and the stored state object is non-essential,
  // so wrap both to retry state-only and finally swallow — the route still applies
  // via the hash. Scoped to file:// so served (http/https) origins are untouched.
  if (location.protocol === 'file:' && window.history) {
    ['pushState', 'replaceState'].forEach(function (m) {
      var orig = window.history[m] ? window.history[m].bind(window.history) : null;
      if (!orig) return;
      window.history[m] = function (state, title, url) {
        try { return orig(state, title, url); }
        catch (e) {
          try { return orig(state, title); }   // retry without the URL (state only)
          catch (e2) { /* swallow: hash routing still navigates via location.hash */ }
        }
      };
    });
    log('file:// history-API guard installed (opaque-origin pushState/replaceState)');
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

  // --- single-instance guard (Web Locks leader election) ---
  //
  // A persistent identity (CFG.sk / CFG.encsk) must run in exactly ONE tab per
  // origin. Two tabs booting the same key = two dmsg clients claiming one PK,
  // fighting over server sessions (dmsg is newest-session-wins, so they evict
  // each other and flap) and colliding on AR / transport / route state. Web Locks
  // elects a single leader tab; other tabs show a notice and WAIT on the lock, so
  // if the leader tab closes one of them acquires it and boots automatically
  // (failover). An ephemeral identity (no key) gets a fresh PK per tab, so there
  // is nothing to collide and the guard is skipped.
  //
  // This is the minimal fix. The fuller architecture (one visor in a SharedWorker
  // with tabs as MessagePort clients) keeps the visor alive across tab churn
  // without a re-boot/re-register on every leadership handoff — a follow-up.
  function showSingletonNotice() {
    function add() {
      if (document.getElementById('skywire-singleton-notice')) return;
      var d = document.createElement('div');
      d.id = 'skywire-singleton-notice';
      d.style.cssText = 'position:fixed;inset:0;z-index:2147483647;background:#15151f;color:#cdd6e0;' +
        'font:14px/1.6 system-ui,sans-serif;display:flex;align-items:center;justify-content:center;text-align:center;padding:2rem';
      d.innerHTML = '<div style="max-width:34rem"><h2 style="margin:0 0 .5rem;font-weight:600">' +
        'Skywire is already running in another tab</h2><p style="opacity:.8">This visor identity can ' +
        'run in only one tab at a time. Close the other tab to use it here — this tab takes over ' +
        'automatically if that one closes.</p></div>';
      document.body.appendChild(d);
    }
    if (document.body) add(); else document.addEventListener('DOMContentLoaded', add);
  }
  function hideSingletonNotice() {
    var d = document.getElementById('skywire-singleton-notice');
    if (d && d.parentNode) d.parentNode.removeChild(d);
  }

  // becomeLeader resolves when THIS tab holds the named exclusive lock (now if
  // free, or later when the current holder's tab closes), and keeps the lock held
  // for the tab's lifetime. Resolves immediately (best-effort) where Web Locks is
  // unsupported. Shows/hides the "running in another tab" notice around the wait.
  function becomeLeader(name) {
    if (!(navigator.locks && navigator.locks.request)) {
      log('Web Locks unsupported; single-instance guard disabled (best-effort)');
      return Promise.resolve();
    }
    return new Promise(function (resolveLeader) {
      // Non-blocking probe: if the lock is already held, surface the notice now.
      navigator.locks.request(name, { mode: 'exclusive', ifAvailable: true }, function (probe) {
        if (!probe) { log('another tab holds the visor lock; waiting'); showSingletonNotice(); }
        // returning undefined releases the probe's hold immediately
      });
      // Queued acquisition: granted now (free) or after the holder's tab closes.
      // The unresolved returned promise holds the lock until THIS tab is unloaded.
      navigator.locks.request(name, { mode: 'exclusive' }, function () {
        hideSingletonNotice();
        log('acquired single-instance visor lock; this tab is the leader');
        resolveLeader();
        return new Promise(function () {});
      });
    });
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
      // Persistent identity → enforce one-tab-per-origin before booting any dmsg
      // client. Ephemeral (no key) tabs each get a unique PK, so they may all run.
      if (CFG.sk || CFG.encsk) {
        await becomeLeader('skywire-wasm-visor');
      }
      if (CFG.visor) {
        while (!self.skywireVisor) { await new Promise(function (r) { setTimeout(r, 10); }); }
        // Coerce optional seed/disc config to '' — an undefined js.Value stringifies
        // to the literal "<undefined>" on the Go side, which boot then rejects as a
        // "bad seed server pk". Empty string = "no seed, use the embedded set".
        var vpk = await self.skywireVisor.boot(sk, CFG.seedpk || '', CFG.seedws || '', CFG.disc || '');
        log('wasm-visor booted as ' + vpk + ' (edge + hypervisor; /api → in-wasm core)');
        return;
      }
      while (!self.skywireDmsg) { await new Promise(function (r) { setTimeout(r, 10); }); }
      var pk = await self.skywireDmsg.connect(sk, CFG.seedpk || '', CFG.seedws || '', CFG.disc || '');
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
    try {
      // Anchor relative URLs (Angular assets: 'assets/i18n/en.json', ApiService's
      // 'api/…') at the app ROOT, not location.href. On file:// location.pathname is
      // the .html file path, so a relative 'assets/…' would resolve to '/tmp/assets/…'
      // and miss the inlined __SKYWIRE_ASSETS__ map (keyed '/assets/…'). The app is
      // always served at '/'. On http/https this is identical to the old behavior.
      var base = (location.origin && location.origin !== 'null') ? location.origin + '/' : 'file:///';
      var u = new URL(url, base);
      return u.pathname + u.search;
    } catch (e) { return url; }
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
  //
  // /api requests are routed by the Angular SkywireHttpBackend (DI-injected, an
  // Observable path that NEVER touches XHR — it awaits __SKYWIRE_HV__.ready, set
  // below, then calls skywireVisor.hvApi). So this shim only has to serve the
  // runtime assets (i18n JSON, images) that `cli hv gen` inlines into the file.
  //
  // CRITICAL: it must stay a REAL XMLHttpRequest. zone.js (loaded by Angular)
  // patches XHR and, when Angular sends a request, calls the NATIVE send on the
  // instance — which throws "TypeError: Illegal invocation" on a non-XHR fake
  // object. (The previous shim replaced XMLHttpRequest with a plain object and so
  // broke the whole UI on file://.) Instead, SUBCLASS the real XHR and, for an
  // inlined asset, rewrite the request to a data: URL the native XHR machinery
  // fetches itself — zone.js-compatible, and data: works from file://. Anything
  // else falls through to the native XHR unchanged.
  function assetDataURL(path) {
    var m = self.__SKYWIRE_ASSETS__;
    if (!m) return null;
    var p = path.split('?')[0].split('#')[0];
    var bare = p.replace(/^\//, '');
    var a = m[p] || m[bare] || m['/' + bare];
    if (!a) return null;
    // Reuse the stored base64 verbatim (no decode+re-encode → no huge-array btoa).
    return 'data:' + (a.ct || 'application/octet-stream') + ';base64,' + a.b;
  }
  try {
    var RealXHR = window.XMLHttpRequest;
    window.XMLHttpRequest = class extends RealXHR {
      open(method, url) {
        try {
          if (isLocal(url)) {
            var d = assetDataURL(pathOf(url));
            if (d) { return super.open('GET', d); } // serve the inlined asset natively
          }
        } catch (e) { /* fall through to native open */ }
        return super.open.apply(this, arguments);
      }
    };
  } catch (e) {
    log('XHR subclass unsupported; leaving native XHR in place: ' + ((e && e.message) || e));
  }

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

  // Kick off the boot now and expose the promise as __SKYWIRE_HV__.ready, so the
  // Angular SkywireHttpBackend (which owns /api) awaits the in-tab visor coming up
  // before its first call. (The old shim booted lazily from inside the XHR path;
  // now that /api no longer flows through this shim, boot must be kicked here.)
  window.__SKYWIRE_HV__ = CFG;
  CFG.ready = ensure();

  // (The bundled wallet's node API is intercepted inside the wallet iframe by the
  // dmsg fetch shim injected by serve.go's /wallet/ handler — no SW bridge here.)

  log('HTTP-over-dmsg override installed (hypervisor ' + CFG.pk + ')');
})();
