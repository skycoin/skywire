// pkg/wasmhv/browseui/winbox-loader.js c3-vis-wasm
// Starts the window-manager wasm module (cmd/winbox-wasm, a Go port of
// WinBox.js via github.com/0magnet/winbox-go) and publishes the global
// `WinBox` constructor that browse.js builds every mini-desktop window on.
//
// The constructor exists only once the module's main has run, which a <script>
// tag did not require of the JS library this replaces. Nothing may open a
// window before then, so this publishes globalThis.__winboxReady and BOTH
// launchers (pkg/wasmhv BrowseLauncherJS and pkg/visor nativeBrowseLauncherJS)
// wait for `typeof WinBox === "function"` in the readiness poll they already
// run before mounting the panel. Gating the panel gates every window, since
// windows are only ever opened from it.
(function () {
  // No document means no window manager to install (this bundle is precached
  // by the service worker, which never executes it).
  if (typeof document === "undefined") { return; }
  if (typeof globalThis.WinBox === "function") {
    globalThis.__winboxReady = Promise.resolve(globalThis.WinBox);
    return;
  }

  var resolve, reject;
  globalThis.__winboxReady = new Promise(function (a, b) { resolve = a; reject = b; });

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

  // Single-file mode (cli hv gen) has no server to fetch from, so the gzipped
  // module travels inside the page as base64 and is inflated here — the same
  // shape the generated file uses for the visor blob itself.
  function moduleBytes() {
    var b64 = globalThis.__SKYWIRE_WINBOX_WASM_B64__;
    if (b64) {
      var bin = Uint8Array.from(atob(b64), function (c) { return c.charCodeAt(0); });
      return new Response(new Blob([bin]).stream().pipeThrough(new DecompressionStream("gzip"))).arrayBuffer();
    }
    var url = globalThis.__SKYWIRE_WINBOX_WASM_URL__ || new URL("winbox.wasm", document.baseURI).href;
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
    // A failure here means no windows at all, so say so rather than leaving the
    // launcher polling forever with no explanation.
    try { console.error("skywire: window manager (winbox.wasm) failed to start —", e); } catch (_) {}
    reject(e);
  });
})();
