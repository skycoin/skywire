// dist/winbox-loader.js
// Starts the window-manager wasm module (cmd/winbox-js) and publishes the
// global `WinBox` constructor pages build their windows on.
//
// The constructor exists only once the module's main has run, which a <script>
// tag did not require of the JS library this replaces. Nothing may open a
// window before then, so this publishes globalThis.__winboxReady for page code
// to await (or poll `typeof WinBox === "function"`).
//
// Module bytes come from, in order:
//   globalThis.__WINBOX_WASM_B64__ — gzipped module inlined as base64
//     (single-file pages with no server to fetch from);
//   globalThis.__WINBOX_WASM_URL__ — explicit URL;
//   "winbox.wasm" resolved against document.baseURI.
(function () {
  // No document means no window manager to install (e.g. the bundle is
  // precached by a service worker, which never executes it).
  if (typeof document === "undefined") { return; }
  if (typeof globalThis.WinBox === "function") {
    globalThis.__winboxReady = Promise.resolve(globalThis.WinBox);
    return;
  }

  var resolve, reject;
  globalThis.__winboxReady = new Promise(function (a, b) { resolve = a; reject = b; });
  // cmd/winbox-js invokes __winboxResolve once the constructor is installed —
  // the direct path; the settle() poll below stays as a fallback.
  globalThis.__winboxResolve = function (wb) { resolve(wb || globalThis.WinBox); };

  // go.run() hands control back as soon as the module's main blocks, but the
  // constructor is installed from Go — wait for it to actually appear rather
  // than assuming it is there on the turn run() returns.
  function settle() {
    var tries = 0;
    (function poll() {
      if (typeof globalThis.WinBox === "function") { resolve(globalThis.WinBox); return; }
      if (++tries > 300) { reject(new Error("winbox.wasm ran but installed no WinBox")); return; }
      setTimeout(poll, 10);
    })();
  }

  function moduleBytes() {
    var b64 = globalThis.__WINBOX_WASM_B64__;
    if (b64) {
      var bin = Uint8Array.from(atob(b64), function (c) { return c.charCodeAt(0); });
      return new Response(new Blob([bin]).stream().pipeThrough(new DecompressionStream("gzip"))).arrayBuffer();
    }
    var url = globalThis.__WINBOX_WASM_URL__ || new URL("winbox.wasm", document.baseURI).href;
    return fetch(url).then(function (r) {
      if (!r.ok) { throw new Error("winbox.wasm: HTTP " + r.status); }
      return r.arrayBuffer();
    });
  }

  var Go = globalThis.__winboxGo;
  if (typeof Go !== "function") {
    reject(new Error("winbox loader: winbox-exec.js did not load"));
    return;
  }

  moduleBytes().then(function (buf) {
    var go = new Go();
    return WebAssembly.instantiate(buf, go.importObject).then(function (res) {
      go.run(res.instance);
      settle();
    });
  }).catch(function (e) {
    // A failure here means no windows at all, so say so rather than leaving
    // page code polling forever with no explanation.
    try { console.error("winbox: window manager (winbox.wasm) failed to start —", e); } catch (_) {}
    reject(e);
  });
})();
