// hv-boot.js — the clean boot bootstrap for the in-tab wasm-visor hypervisor UI.
// Loaded as the FIRST <script> in <head>, before the Angular bundle, so that:
//   1. window.__SKYWIRE_HV__.visor is set before Angular's SkywireHttpBackend is
//      constructed (it reads the flag to switch /api routing to the in-tab core);
//   2. the wasm-visor begins booting immediately, with the boot promise exposed
//      as window.__SKYWIRE_HV__.ready — SkywireHttpBackend awaits it before the
//      first /api call, so the dashboard's initial requests block until the
//      visor is up rather than failing.
//
// This REPLACES override.js's fetch/XHR monkey-patch: routing is now owned by the
// Angular HttpBackend (DI-injected, testable). hv-boot.js only boots the client.
//
// Config is window.__SKYWIRE_HV__ = {sk?, encsk?, seedpk?, seedws?, disc?}. All
// optional: an empty config boots an ephemeral-key visor that multi-seeds from
// the embedded dmsg-server set and defaults discovery to the deployment's — so
// the bare served HTML is a working serverless visor with zero configuration.
(function () {
  var CFG = (window.__SKYWIRE_HV__ = window.__SKYWIRE_HV__ || {});
  // This tab is a full wasm-VISOR (edge + router + its own hypervisor); /api
  // resolves against the in-wasm core (globalThis.skywireVisor.hvApi).
  CFG.visor = true;

  function loadScript(src) {
    return new Promise(function (res, rej) {
      var s = document.createElement('script');
      s.src = src;
      s.onload = res;
      s.onerror = function () { rej(new Error('failed to load ' + src)); };
      document.head.appendChild(s);
    });
  }

  // PERSISTED IDENTITY: keep the visor's key (and therefore PK) stable across
  // refreshes by storing it in localStorage (scoped to the served origin).
  // Ctrl+Shift+R clears the HTTP cache but NOT localStorage, so a refresh keeps
  // the same visor while picking up freshly-served wasm. On file:// localStorage
  // is browser-dependent, which is why the SERVED model is the reliable one.
  var SK_KEY = 'skywire-visor-sk';
  function loadStoredSK() { try { return localStorage.getItem(SK_KEY) || ''; } catch (e) { return ''; } }
  function storeSK(hex) { try { localStorage.setItem(SK_KEY, hex); } catch (e) {} }
  function newSKHex() {
    var b = crypto.getRandomValues(new Uint8Array(32));
    return Array.prototype.map.call(b, function (x) { return ('0' + x.toString(16)).slice(-2); }).join('');
  }

  // resolveSK returns the dmsg secret-key hex: plaintext CFG.sk (user-supplied),
  // else a password-decrypted CFG.encsk (AES-GCM/PBKDF2; format base64 of
  // [16-byte salt | 12-byte iv | ciphertext]), else a localStorage-persisted
  // ephemeral key (generated + saved on first load, reused after).
  async function resolveSK() {
    if (CFG.sk) return CFG.sk;
    if (!CFG.encsk) {
      var stored = loadStoredSK();
      if (stored) return stored;
      var hex = newSKHex();
      storeSK(hex);
      return hex;
    }
    var pw = window.prompt('Enter password to unlock the visor key:');
    if (pw === null) throw new Error('password entry cancelled');
    var raw = Uint8Array.from(atob(CFG.encsk), function (c) { return c.charCodeAt(0); });
    var salt = raw.slice(0, 16), iv = raw.slice(16, 28), ct = raw.slice(28);
    var baseKey = await crypto.subtle.importKey('raw', new TextEncoder().encode(pw), 'PBKDF2', false, ['deriveKey']);
    var aesKey = await crypto.subtle.deriveKey(
      { name: 'PBKDF2', salt: salt, iterations: 200000, hash: 'SHA-256' },
      baseKey, { name: 'AES-GCM', length: 256 }, false, ['decrypt']);
    var pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: iv }, aesKey, ct);
    return new TextDecoder().decode(pt);
  }

  // --- single-instance guard (Web Locks leader election) ---
  //
  // The key above is persisted in localStorage, so EVERY tab of this origin boots
  // the SAME identity. Two tabs running one PK = two dmsg clients fighting over
  // server sessions (dmsg is newest-session-wins → they evict each other and flap)
  // and colliding on AR / transport / route state. Web Locks elects one leader
  // tab; others show a notice and WAIT on the lock, so closing the leader hands
  // off automatically (a waiting tab acquires it and boots). Where Web Locks is
  // unsupported we proceed best-effort. (Fuller fix: one visor in a SharedWorker
  // with tabs as MessagePort clients — survives tab churn with no re-boot.)
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
  function becomeLeader(name) {
    if (!(navigator.locks && navigator.locks.request)) {
      try { console.log('[hv-boot] Web Locks unsupported; single-instance guard disabled'); } catch (e) {}
      return Promise.resolve();
    }
    return new Promise(function (resolveLeader) {
      navigator.locks.request(name, { mode: 'exclusive', ifAvailable: true }, function (probe) {
        if (!probe) { showSingletonNotice(); }
      });
      navigator.locks.request(name, { mode: 'exclusive' }, function () {
        hideSingletonNotice();
        resolveLeader();
        return new Promise(function () {}); // hold the lock for this tab's lifetime
      });
    });
  }

  CFG.ready = (async function () {
    await loadScript('wasm_exec.js');
    var go = new Go();
    // TinyGo's wasm_exec.js omits the gojs getRandomData import the crypto-using
    // Go runtime needs to seed itself; inject it only when absent.
    if (go.importObject.gojs && !go.importObject.gojs['runtime.getRandomData']) {
      go.importObject.gojs['runtime.getRandomData'] = function (ptr, len) {
        crypto.getRandomValues(new Uint8Array(go._inst.exports.memory.buffer, ptr >>> 0, len >>> 0));
      };
    }
    var buf = await fetch('wasm-visor.wasm').then(function (r) { return r.arrayBuffer(); });
    var res = await WebAssembly.instantiate(buf, go.importObject);
    go.run(res.instance); // installs globalThis.skywireVisor
    while (!self.skywireVisor || !self.skywireVisor.boot) {
      await new Promise(function (r) { setTimeout(r, 10); });
    }
    var sk = await resolveSK();
    // One tab per origin may run this (localStorage-shared) identity; wait here
    // until this tab is the leader before booting any dmsg client.
    await becomeLeader('skywire-wasm-visor');
    var pk = await self.skywireVisor.boot(sk, CFG.seedpk || '', CFG.seedws || '', CFG.disc || '');
    try { console.log('[hv-boot] wasm-visor booted as ' + pk + ' (edge + hypervisor; /api → in-wasm core)'); } catch (e) {}
    return pk;
  })();
})();
