// worker.js — hosts the Go/wasm skywire-visor in a dedicated Web Worker, OFF the
// browser's main thread.
//
// WHY: the Go/wasm runtime — and the visor's occasionally-blocking work (dmsg/WS/WT
// dials, route setup, SOCKS handshakes) — runs on whatever thread hosts the runtime.
// On the page's MAIN thread that starves the UI event loop: during the 2026-07-03
// demo a slow skysocks route setup froze the whole HV UI (unclickable, even CDP
// evals hung) until the tab was reloaded. A dedicated worker gives the runtime its
// own thread, so the UI stays responsive no matter what the visor is doing.
//
// hv-boot.js spawns this worker and installs a main-thread `globalThis.skywireVisor`
// PROXY whose every method is a postMessage round-trip to here (see hv-boot.js).
// The message protocol (main ⇄ worker):
//   main → worker:  {t:'call', id, fn, args}          invoke skywireVisor[fn](...args)
//   worker → main:  {t:'up', methods}                 runtime up; here are the API names
//                   {t:'ret', id, val}                call id resolved with val
//                   {t:'err', id, msg}                call id rejected with msg
//                   {t:'log', level, line}            forward console output to the page
//                   {t:'fatal', msg}                  runtime failed to start
//
// The Go side needs NO changes: syscall/js, fetch, WebSocket and WebTransport all
// work in a dedicated worker, and self.location is same-origin as the page (so the
// HTTPS mixed-content gating in bootEdge stays correct). The one browser API NOT
// exposed to workers is RTCPeerConnection — the STUN self-IP probe and the WebRTC
// transport degrade gracefully (both are Truthy()-guarded in Go). That is fine:
// WS/WT are the browser carriers and WebRTC is neither default-on nor the freeze
// cause. (Reviving WebRTC in worker mode would need a main-thread PeerConnection
// proxy; deferred.)
(function () {
  // Forward everything the Go runtime writes to the console (fmt.Println → stdout →
  // console.log, and vlog) to the main thread, so the HV-UI log window — which
  // captures the PAGE console — still shows visor logs even though the runtime now
  // runs off-thread. Override BEFORE loading wasm_exec.js so its stdout wiring is
  // captured too. Each override still calls the real console fn (worker devtools).
  var real = { log: console.log.bind(console), error: console.error.bind(console), warn: console.warn.bind(console) };
  function forward(level, args) {
    try {
      var line = Array.prototype.map.call(args, function (a) {
        if (typeof a === 'string') { return a; }
        try { return JSON.stringify(a); } catch (e) { return String(a); }
      }).join(' ');
      self.postMessage({ t: 'log', level: level, line: line });
    } catch (e) { /* postMessage can fail on teardown — ignore */ }
  }
  console.log = function () { forward('log', arguments); real.log.apply(null, arguments); };
  console.error = function () { forward('error', arguments); real.error.apply(null, arguments); };
  console.warn = function () { forward('warn', arguments); real.warn.apply(null, arguments); };

  importScripts('wasm_exec.js');
  var go = new Go();
  // TinyGo's wasm_exec.js omits the gojs getRandomData import the crypto-using Go
  // runtime needs to seed itself; inject it only when absent (mirrors hv-boot.js).
  if (go.importObject.gojs && !go.importObject.gojs['runtime.getRandomData']) {
    go.importObject.gojs['runtime.getRandomData'] = function (ptr, len) {
      crypto.getRandomValues(new Uint8Array(go._inst.exports.memory.buffer, ptr >>> 0, len >>> 0));
    };
  }

  var api = null;        // globalThis.skywireVisor once the runtime installs it
  var queued = [];       // calls that arrive before the runtime is up

  function dispatch(id, fn, args) {
    var f = api && api[fn];
    if (typeof f !== 'function') {
      self.postMessage({ t: 'err', id: id, msg: 'skywireVisor.' + fn + ' is not a function' });
      return;
    }
    var out;
    try { out = f.apply(api, args || []); }
    catch (e) { self.postMessage({ t: 'err', id: id, msg: String((e && e.message) || e) }); return; }
    // Every visor method is either sync (value/undefined) or returns a Promise;
    // Promise.resolve normalizes both. undefined → null so the wire stays valid.
    Promise.resolve(out).then(function (val) {
      self.postMessage({ t: 'ret', id: id, val: val === undefined ? null : val });
    }, function (err) {
      self.postMessage({ t: 'err', id: id, msg: String((err && err.message) || err) });
    });
  }

  self.onmessage = function (ev) {
    var m = ev.data || {};
    if (m.t !== 'call') { return; }
    if (!api) { queued.push(m); return; }
    dispatch(m.id, m.fn, m.args);
  };

  fetch('wasm-visor.wasm').then(function (r) { return r.arrayBuffer(); }).then(function (buf) {
    return WebAssembly.instantiate(buf, go.importObject);
  }).then(function (res) {
    go.run(res.instance); // installs self.skywireVisor, then parks on select{}
    (function waitReady() {
      if (self.skywireVisor && self.skywireVisor.boot) {
        api = self.skywireVisor;
        self.postMessage({ t: 'up', methods: Object.keys(api) });
        for (var i = 0; i < queued.length; i++) { dispatch(queued[i].id, queued[i].fn, queued[i].args); }
        queued = [];
      } else {
        setTimeout(waitReady, 10);
      }
    })();
  }).catch(function (e) {
    self.postMessage({ t: 'fatal', msg: String((e && e.message) || e) });
  });
})();
