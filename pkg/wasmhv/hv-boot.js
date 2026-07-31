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
  // Capture any deep-link params NOW — this is the first script, so it runs
  // before Angular's hash router boots and drops location.search. browse.js
  // (mountPanel → readDeepLink) reads window.__SKYWIRE_DEEPLINK__ on load:
  //   ?skynet=<target>   open a dmsg site full-page (clearnet→dmsg gateway)
  //   ?skydm=<pk>        open a 1:1 skychat pre-addressed to <pk> ("message me")
  //   ?skygroup=<invite> join + open a federated group chat from an invite blob
  //   &kiosk=1           full-page (hide the HV taskbar, maximize)
  // so a clearnet Caddy redirect can land a visitor straight in a site OR a chat.
  try {
    var dlp = new URLSearchParams(window.location.search || '');
    var dlTarget = dlp.get('skynet');
    var dlDm = dlp.get('skydm');
    var dlGroup = dlp.get('skygroup');
    if (dlTarget || dlDm || dlGroup) {
      window.__SKYWIRE_DEEPLINK__ = {
        target: dlTarget || '',
        skydm: dlDm || '',
        skygroup: dlGroup || '',
        kiosk: dlp.get('kiosk') === '1' || dlp.get('kiosk') === 'true',
      };
    }
  } catch (e) {}
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

  // Go<->TinyGo wasm-visor selection. The chosen variant is persisted per-origin
  // in localStorage; the SK above is variant-INDEPENDENT, so switching keeps the
  // same identity/PK. Workers can't read localStorage, so the variant travels to
  // worker.js as a `?variant=` query param on its URL (and on the wasm/wasm_exec
  // fetches it makes). An empty value = the server's default blob. A SharedWorker
  // is keyed by (url,name); the variant is in BOTH so switching spins up a fresh
  // worker on the new blob instead of reusing the old-variant one.
  var VARIANT_KEY = 'skywire-visor-variant';
  function loadVariant() { try { return localStorage.getItem(VARIANT_KEY) || ''; } catch (e) { return ''; } }
  function variantQS() { var v = loadVariant(); return v ? ('?variant=' + encodeURIComponent(v)) : ''; }
  function variantSuffix() { var v = loadVariant(); return v ? ('-' + v) : ''; }
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

  // --- SharedWorker host (preferred) ---
  //
  // ONE wasm-visor per origin, shared by every same-origin tab (worker.js as a
  // SharedWorker). The visor boots once on the first tab, stays alive while any tab
  // is connected, and stops when the last tab closes. Opening/switching/closing tabs
  // does NOT re-boot it — the running dmsg client, router and identity are preserved
  // (this is what the Web Locks single-instance guard could NOT do; with a shared
  // visor that guard is obsolete and is skipped on this path). Every tab installs a
  // main-thread `globalThis.skywireVisor` proxy whose methods are postMessage
  // round-trips over the tab's MessagePort; all call sites already treat the API as
  // async, so the proxy is transparent. STUN + WebRTC (no RTCPeerConnection in any
  // worker) are delegated by the worker to whichever tab it elected as agent — this
  // tab handles them iff it receives {t:'stun-ip'}/{t:'rtc'} for it. Falls back to
  // the dedicated Worker (+ singleton guard) if SharedWorker is unavailable.
  function bootInSharedWorker(sk) {
    return new Promise(function (resolve, reject) {
      if (typeof SharedWorker === 'undefined') { reject(new Error('SharedWorker unavailable')); return; }
      var sw;
      try { sw = new SharedWorker('worker.js' + variantQS(), { name: 'skywire-wasm-visor' + variantSuffix() }); } catch (e) { reject(e); return; }
      var port = sw.port;
      var nextId = 1, pending = Object.create(null);
      var upTimer = setTimeout(function () { reject(new Error('shared worker did not report ready in time')); }, 45000);

      function makeProxy(methods) {
        var proxy = {};
        methods.forEach(function (fn) {
          proxy[fn] = function () {
            var callArgs = Array.prototype.slice.call(arguments);
            return new Promise(function (res, rej) {
              var id = nextId++;
              pending[id] = { res: res, rej: rej };
              try { port.postMessage({ t: 'call', id: id, fn: fn, args: callArgs }); }
              catch (e) { delete pending[id]; rej(e); }
            });
          };
        });
        return proxy;
      }

      port.onmessage = function (ev) {
        var m = ev.data || {};
        switch (m.t) {
          case 'log':
            try { (m.level === 'error' ? console.error : m.level === 'warn' ? console.warn : console.log)(m.line); } catch (e) {}
            try { if (typeof self.__skylog === 'function') self.__skylog(m.line); } catch (e) {}
            break;
          case 'up':
            clearTimeout(upTimer);
            // The worker has already booted the shared visor; install the proxy and
            // resolve with the booted PK (do NOT call boot() again — that's the
            // whole point: one boot, shared).
            self.skywireVisor = makeProxy(m.methods);
            resolve(m.pk || '');
            break;
          case 'ret':
            if (pending[m.id]) { pending[m.id].res(m.val); delete pending[m.id]; }
            break;
          case 'err':
            if (pending[m.id]) { pending[m.id].rej(new Error(m.msg)); delete pending[m.id]; }
            break;
          case 'fatal':
            clearTimeout(upTimer);
            reject(new Error(m.msg));
            break;
          case 'stun-ip':
            // This tab is the elected agent: run STUN here and return the public IP.
            runStunIP(m.ice).then(function (ip) {
              try { port.postMessage({ t: 'stun-ip-result', id: m.id, ip: ip || '' }); } catch (e) {}
            });
            break;
          case 'rtc':
            // This tab is the elected agent: host the real RTCPeerConnection. `port`
            // stands in for the worker connection (same postMessage contract).
            rtcHandle(port, m);
            break;
          case 'voicePlay':
            // A decoded voice frame from the worker's Sink — hand it to the
            // main-thread WebAudio proxy (voice-audio.js) for playback.
            try { if (typeof globalThis.__skyvoiceOnPlay === 'function') globalThis.__skyvoiceOnPlay(m.b); } catch (e) {}
            break;
        }
      };
      port.start();
      // Let voice-audio.js send captured mic frames to the worker's Source.
      globalThis.__skyvoiceToWorker = function (bytes) { try { port.postMessage({ t: 'voiceMic', b: bytes }); } catch (e) {} };

      // First tab supplies the key + seed config; the worker boots the visor once.
      try { port.postMessage({ t: 'init', sk: sk, seedpk: CFG.seedpk || '', seedws: CFG.seedws || '', disc: CFG.disc || '' }); }
      catch (e) { clearTimeout(upTimer); reject(e); return; }

      // Report visibility so the worker prefers a foreground tab as WebRTC/STUN
      // agent (background tabs are throttled). And tell the worker when this tab is
      // unloading so it can re-elect the agent / drop the port promptly.
      function reportVis() {
        try { port.postMessage({ t: 'vis', state: (document.visibilityState || 'visible') }); } catch (e) {}
      }
      try {
        document.addEventListener('visibilitychange', reportVis);
        reportVis();
        self.addEventListener('pagehide', function () { try { port.postMessage({ t: 'bye' }); } catch (e) {} });
      } catch (e) {}
    });
  }

  // --- Web Worker host (dedicated; fallback) ---
  //
  // The Go/wasm runtime runs the visor's occasionally-blocking work (dmsg/WS/WT
  // dials, route setup, SOCKS handshakes). On the page's MAIN thread that starves
  // the UI event loop — a slow route setup once froze the whole HV UI. Running the
  // runtime in a dedicated Web Worker (worker.js) keeps the UI responsive: every
  // globalThis.skywireVisor call becomes a postMessage round-trip to the worker.
  // All call sites already treat the API as async (Promises / fire-and-forget), so
  // the proxy is transparent. Used when SharedWorker is unavailable; falls back to
  // the in-page runtime if Workers are unavailable or the worker fails to come up.
  function bootInWorker(sk) {
    return new Promise(function (resolve, reject) {
      if (typeof Worker === 'undefined') { reject(new Error('Web Workers unavailable')); return; }
      var worker;
      try { worker = new Worker('worker.js' + variantQS()); } catch (e) { reject(e); return; }
      var nextId = 1, pending = Object.create(null);
      var upTimer = setTimeout(function () { reject(new Error('worker did not report ready in time')); }, 30000);

      // Build the main-thread proxy: one forwarding function per worker API method.
      function makeProxy(methods) {
        var proxy = {};
        methods.forEach(function (fn) {
          proxy[fn] = function () {
            var callArgs = Array.prototype.slice.call(arguments);
            return new Promise(function (res, rej) {
              var id = nextId++;
              pending[id] = { res: res, rej: rej };
              try { worker.postMessage({ t: 'call', id: id, fn: fn, args: callArgs }); }
              catch (e) { delete pending[id]; rej(e); }
            });
          };
        });
        return proxy;
      }

      worker.onmessage = function (ev) {
        var m = ev.data || {};
        switch (m.t) {
          case 'log':
            // Re-emit worker/visor output on the PAGE console so the HV-UI log
            // window (which captures console.*) shows it, and feed __skylog.
            try { (m.level === 'error' ? console.error : m.level === 'warn' ? console.warn : console.log)(m.line); } catch (e) {}
            try { if (typeof self.__skylog === 'function') self.__skylog(m.line); } catch (e) {}
            break;
          case 'up':
            clearTimeout(upTimer);
            // Install the proxy as globalThis.skywireVisor — browse.js, the Angular
            // backend, the skychat component, etc. call it exactly as before. The
            // worker booted the visor itself (from the {t:'init'} below), so resolve
            // with the booted PK; no separate boot() call.
            self.skywireVisor = makeProxy(m.methods);
            resolve(m.pk || '');
            break;
          case 'ret':
            if (pending[m.id]) { pending[m.id].res(m.val); delete pending[m.id]; }
            break;
          case 'err':
            if (pending[m.id]) { pending[m.id].rej(new Error(m.msg)); delete pending[m.id]; }
            break;
          case 'fatal':
            clearTimeout(upTimer);
            reject(new Error(m.msg));
            break;
          case 'stun-ip':
            // The worker has no RTCPeerConnection; run STUN here (main thread) and
            // return the discovered public IP so the visor learns it on HTTPS.
            runStunIP(m.ice).then(function (ip) {
              try { worker.postMessage({ t: 'stun-ip-result', id: m.id, ip: ip || '' }); } catch (e) {}
            });
            break;
          case 'rtc':
            // WebRTC transport bridge: the worker's proxy RTCPeerConnection forwards
            // here, where real RTCPeerConnection/RTCDataChannel objects live.
            rtcHandle(worker, m);
            break;
          case 'voicePlay':
            try { if (typeof globalThis.__skyvoiceOnPlay === 'function') globalThis.__skyvoiceOnPlay(m.b); } catch (e) {}
            break;
        }
      };
      // Let voice-audio.js send captured mic frames to the worker's Source.
      globalThis.__skyvoiceToWorker = function (bytes) { try { worker.postMessage({ t: 'voiceMic', b: bytes }); } catch (e) {} };
      worker.onerror = function (e) {
        clearTimeout(upTimer);
        reject(new Error('worker error: ' + ((e && e.message) || 'load failed')));
      };
      // Supply the key + seed config; the worker boots the visor once, then reports
      // 'up' with the booted PK. Same protocol as the SharedWorker path.
      try { worker.postMessage({ t: 'init', sk: sk, seedpk: CFG.seedpk || '', seedws: CFG.seedws || '', disc: CFG.disc || '' }); }
      catch (e) { clearTimeout(upTimer); reject(e); }
    });
  }

  // runStunIP opens an RTCPeerConnection against the given STUN server URLs, gathers
  // ICE candidates, and resolves with the server-reflexive (public) IP — or '' if
  // none is found within the timeout. Main-thread only (RTCPeerConnection); the
  // worker reaches it via the stun-ip message bridge above.
  function runStunIP(iceUrls) {
    return new Promise(function (resolve) {
      if (typeof RTCPeerConnection === 'undefined' || !iceUrls || !iceUrls.length) { resolve(''); return; }
      var pc;
      try { pc = new RTCPeerConnection({ iceServers: [{ urls: iceUrls }] }); }
      catch (e) { resolve(''); return; }
      var done = false;
      function finish(ip) {
        if (done) return;
        done = true;
        try { pc.onicecandidate = null; pc.close(); } catch (e) {}
        resolve(ip || '');
      }
      pc.onicecandidate = function (ev) {
        if (!ev || !ev.candidate) return;
        // "candidate:<f> <comp> <proto> <prio> <ip> <port> typ (srflx|prflx) ..."
        var mm = /(\d+\.\d+\.\d+\.\d+) \d+ typ (?:srflx|prflx)/.exec(ev.candidate.candidate || '');
        if (mm) finish(mm[1]);
      };
      try {
        pc.createDataChannel('ip'); // datachannel triggers ICE gathering
        pc.createOffer().then(function (o) { return pc.setLocalDescription(o); }).catch(function () { finish(''); });
      } catch (e) { finish(''); }
      setTimeout(function () { finish(''); }, 8000);
    });
  }

  // --- WebRTC transport bridge (main-thread side) ---
  //
  // The worker's proxy RTCPeerConnection (worker.js __skywireRTC) forwards every
  // op here, where the real RTCPeerConnection/RTCDataChannel live. Events flow back
  // to the worker (icecandidate, datachannel, dc open/message/close/error). One real
  // PeerConnection per proxy, keyed by pcId. See pkg/transport/network/webrtc_browser.go.
  var rtcMainPCs = {};       // pcId -> { pc, dcs: {dcId -> RTCDataChannel} }
  var rtcRemoteDcSeq = 1;    // ids for peer-initiated (ondatachannel) channels

  function rtcWireDC(worker, pcId, dcId, dc) {
    var rec = rtcMainPCs[pcId];
    if (!rec) return;
    try { dc.binaryType = 'arraybuffer'; } catch (e) {}
    rec.dcs[dcId] = dc;
    dc.onopen = function () { try { worker.postMessage({ t: 'rtc', op: 'dcOpen', pcId: pcId, dcId: dcId }); } catch (e) {} };
    dc.onmessage = function (ev) {
      var data = ev.data;
      try {
        worker.postMessage({ t: 'rtc', op: 'dcMessage', pcId: pcId, dcId: dcId, data: data },
          (data instanceof ArrayBuffer) ? [data] : []);
      } catch (e) {}
    };
    dc.onclose = function () { try { worker.postMessage({ t: 'rtc', op: 'dcClose', pcId: pcId, dcId: dcId }); } catch (e) {} };
    dc.onerror = function () { try { worker.postMessage({ t: 'rtc', op: 'dcError', pcId: pcId, dcId: dcId }); } catch (e) {} };
    if (dc.readyState === 'open') { try { worker.postMessage({ t: 'rtc', op: 'dcOpen', pcId: pcId, dcId: dcId }); } catch (e) {} }
  }

  function rtcPcMethod(pc, m) {
    switch (m.method) {
      case 'createOffer': return pc.createOffer().then(function (o) { return { type: o.type, sdp: o.sdp }; });
      case 'createAnswer': return pc.createAnswer().then(function (o) { return { type: o.type, sdp: o.sdp }; });
      case 'setLocalDescription': return pc.setLocalDescription(m.arg).then(function () { return null; });
      case 'setRemoteDescription': return pc.setRemoteDescription(m.arg).then(function () { return null; });
      case 'addIceCandidate': return pc.addIceCandidate(m.arg).then(function () { return null; });
      default: return Promise.reject(new Error('unknown rtc method ' + m.method));
    }
  }

  function rtcHandle(worker, m) {
    var rec, dc;
    switch (m.op) {
      case 'newPC': {
        if (typeof RTCPeerConnection === 'undefined') return;
        var cfg = {};
        if (m.iceServers && m.iceServers.length) { cfg.iceServers = m.iceServers; }
        var pc;
        try { pc = new RTCPeerConnection(cfg); } catch (e) { return; }
        rec = { pc: pc, dcs: {} };
        rtcMainPCs[m.pcId] = rec;
        pc.onicecandidate = function (ev) {
          var c = (ev && ev.candidate) ? { candidate: ev.candidate.candidate, sdpMid: ev.candidate.sdpMid, sdpMLineIndex: ev.candidate.sdpMLineIndex } : null;
          try { worker.postMessage({ t: 'rtc', op: 'icecandidate', pcId: m.pcId, candidate: c }); } catch (e) {}
        };
        pc.ondatachannel = function (ev) {
          var dcId = 'r' + (rtcRemoteDcSeq++);
          rtcWireDC(worker, m.pcId, dcId, ev.channel);
          try { worker.postMessage({ t: 'rtc', op: 'datachannel', pcId: m.pcId, dcId: dcId }); } catch (e) {}
        };
        break;
      }
      case 'createDC': {
        rec = rtcMainPCs[m.pcId]; if (!rec) break;
        try { dc = rec.pc.createDataChannel(m.label, m.opts || {}); } catch (e) { break; }
        rtcWireDC(worker, m.pcId, m.dcId, dc);
        break;
      }
      case 'pcCall': {
        rec = rtcMainPCs[m.pcId];
        if (!rec) { try { worker.postMessage({ t: 'rtc', op: 'ret', callId: m.callId, ok: false, msg: 'no such pc' }); } catch (e) {} break; }
        rtcPcMethod(rec.pc, m).then(function (val) {
          try { worker.postMessage({ t: 'rtc', op: 'ret', callId: m.callId, ok: true, val: val }); } catch (e) {}
        }, function (err) {
          try { worker.postMessage({ t: 'rtc', op: 'ret', callId: m.callId, ok: false, msg: String((err && err.message) || err) }); } catch (e) {}
        });
        break;
      }
      case 'dcSend': { rec = rtcMainPCs[m.pcId]; dc = rec && rec.dcs[m.dcId]; if (dc) { try { dc.send(m.data); } catch (e) {} } break; }
      case 'dcClose': { rec = rtcMainPCs[m.pcId]; dc = rec && rec.dcs[m.dcId]; if (dc) { try { dc.close(); } catch (e) {} } break; }
      case 'pcClose': {
        rec = rtcMainPCs[m.pcId];
        if (rec) { try { rec.pc.close(); } catch (e) {} delete rtcMainPCs[m.pcId]; try { worker.postMessage({ t: 'rtc', op: 'pcGone', pcId: m.pcId }); } catch (e) {} }
        break;
      }
    }
  }

  // --- In-page host (fallback) ---
  //
  // The original path: load + run the runtime on the main thread. Used when Workers
  // are unavailable or worker.js can't be loaded (e.g. exotic embeddings). Keeps the
  // page working, at the cost of the main-thread-freeze risk the worker path avoids.
  async function bootInPage(sk) {
    await loadScript('wasm_exec.js' + variantQS());
    var go = new Go();
    if (go.importObject.gojs && !go.importObject.gojs['runtime.getRandomData']) {
      go.importObject.gojs['runtime.getRandomData'] = function (ptr, len) {
        crypto.getRandomValues(new Uint8Array(go._inst.exports.memory.buffer, ptr >>> 0, len >>> 0));
      };
    }
    var buf = await fetch('wasm-visor.wasm' + variantQS()).then(function (r) { return r.arrayBuffer(); });
    var res = await WebAssembly.instantiate(buf, go.importObject);
    go.run(res.instance); // installs globalThis.skywireVisor
    while (!self.skywireVisor || !self.skywireVisor.boot) {
      await new Promise(function (r) { setTimeout(r, 10); });
    }
    return await self.skywireVisor.boot(sk, CFG.seedpk || '', CFG.seedws || '', CFG.disc || '');
  }

  CFG.ready = (async function () {
    var sk = await resolveSK();
    var pk;
    // Preferred: ONE shared visor per origin (SharedWorker). No single-instance
    // guard needed — there is only ever one dmsg client, living in the worker; tabs
    // are thin views that come and go without re-booting it.
    try {
      pk = await bootInSharedWorker(sk);
      try { console.log('[hv-boot] wasm-visor booted as ' + pk + ' (shared across tabs; SharedWorker; UI off the runtime thread; /api → in-wasm core)'); } catch (e) {}
      return pk;
    } catch (eShared) {
      try { console.warn('[hv-boot] shared-worker boot unavailable (' + ((eShared && eShared.message) || eShared) + '); falling back to per-tab worker + single-instance guard'); } catch (e2) {}
    }
    // Fallback: per-tab dedicated worker, guarded by Web Locks so two tabs don't run
    // the same identity at once. Only reached where SharedWorker is unavailable.
    await becomeLeader('skywire-wasm-visor');
    try {
      pk = await bootInWorker(sk);
      try { console.log('[hv-boot] wasm-visor booted as ' + pk + ' (per-tab Web Worker; UI off the runtime thread; /api → in-wasm core)'); } catch (e) {}
    } catch (e) {
      try { console.warn('[hv-boot] worker boot unavailable (' + ((e && e.message) || e) + '); falling back to in-page runtime'); } catch (e2) {}
      pk = await bootInPage(sk);
      try { console.log('[hv-boot] wasm-visor booted as ' + pk + ' (in-page runtime; edge + hypervisor; /api → in-wasm core)'); } catch (e2) {}
    }
    return pk;
  })();

  // Minimal Go<->TinyGo switcher. Shown only when the server advertises >1 embedded
  // variant (a TinyGo-only build hides it). Selecting a variant persists it and
  // reloads; the SK is variant-independent, so the page reboots the OTHER blob as
  // the SAME PK — the A/B test harness. Lives here (not Angular) so it works in the
  // served PWA and under --harness alike, with no UI rebuild.
  function injectVariantToggle() {
    // Only on the TOP-level page. When the app is iframed at ?embed=1 (the ☰
    // chat/log WinBox windows host the Angular tab that way), this boot script
    // runs inside the frame too — without this guard the Go/TinyGo selector
    // renders a second time inside the chat iframe. The toggle belongs to the
    // one shared visor, so one instance on the outermost page is correct.
    try { if (window.top !== window.self) { return; } } catch (e) { return; } // cross-origin frame → treat as embedded
    if ((location.hash || '').indexOf('embed=1') >= 0) { return; }
    try {
      fetch('wasm-variants.json', { cache: 'no-store' }).then(function (r) { return r.json(); }).then(function (info) {
        var avail = (info && info.available) || [];
        if (avail.length < 2) { return; }
        var cur = loadVariant() || (info && info.default) || avail[0];
        function build() {
          if (!document.body || document.getElementById('sky-variant-toggle')) { return; }
          var wrap = document.createElement('div');
          wrap.id = 'sky-variant-toggle';
          wrap.style.cssText = 'position:fixed;right:8px;bottom:8px;z-index:2147483647;font:12px system-ui,sans-serif;background:rgba(20,20,24,.9);color:#ddd;padding:4px 8px;border-radius:6px;border:1px solid #555;box-shadow:0 1px 4px rgba(0,0,0,.4)';
          var lab = document.createElement('span'); lab.textContent = 'visor: '; wrap.appendChild(lab);
          var sel = document.createElement('select');
          sel.title = 'wasm-visor toolchain — reloads with the same identity';
          sel.style.cssText = 'background:#111;color:#ddd;border:1px solid #555;border-radius:4px;font:inherit';
          avail.forEach(function (v) {
            var o = document.createElement('option'); o.value = v; o.textContent = v;
            if (v === cur) { o.selected = true; }
            sel.appendChild(o);
          });
          sel.addEventListener('change', function () {
            try { localStorage.setItem(VARIANT_KEY, sel.value); } catch (e) {}
            location.reload();
          });
          wrap.appendChild(sel);
          document.body.appendChild(wrap);
        }
        if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', build); } else { build(); }
      }).catch(function () {});
    } catch (e) {}
  }
  injectVariantToggle();
})();
