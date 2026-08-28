// pkg/wasmhv/browse-sw-loader.js c3-vis-wasm
// Loader for the Go/wasm transport service worker (cmd/wasm-visor/browsesw_js.go).
//
// Served at the browse origin's worker path INSTEAD of realorigin's JS worker
// when `hv serve --browse-origin-wasm` is set. Off by default, and deliberately
// so: the JS worker's security property is that a hundred readable lines on the
// untrusted origin name no transport, and a wasm module cannot make that claim.
//
// The listeners are registered HERE, synchronously, and not by the Go module. A
// service worker only receives functional events on listeners added during the
// initial synchronous evaluation of its script; the wasm module is instantiated
// asynchronously, so a listener it added itself would miss the first fetch after
// every cold start — a 404 fallthrough that reads like a routing bug rather than
// an error. respondWith() accepts a promise, so waiting for the boot inside the
// handler is both legal and the whole trick.
'use strict';

self.__SKYWIRE_WASM_ROLE__ = 'browse-sw';

importScripts('__WASM_EXEC__');

var ready = (function () {
  var go = new Go();
  return WebAssembly.instantiateStreaming(fetch('__WASM_URL__'), go.importObject)
    .then(function (r) {
      // go.run resolves only when the program exits; the module keeps itself
      // alive, so start it and carry on rather than awaiting it.
      go.run(r.instance);
      if (typeof self.__realOriginSWFetch !== 'function') {
        throw new Error('browse-sw: the module installed no fetch handler');
      }
      return true;
    });
})();

self.addEventListener('install', function () { self.skipWaiting(); });
self.addEventListener('activate', function (e) { e.waitUntil(self.clients.claim()); });

self.addEventListener('fetch', function (e) {
  // Navigations are not handled, exactly as in the JS worker: a worker that
  // served the first page would have to be installed by a page it had not
  // served yet, so there is no base case. The server serves the shell instead.
  if (e.request.mode === 'navigate') {
    return;
  }
  var clientId = e.clientId || '';
  e.respondWith(
    ready.then(function () {
      return self.__realOriginSWFetch(e.request, clientId);
    }).catch(function (err) {
      return new Response('browse-sw: ' + err, { status: 502 });
    })
  );
});
