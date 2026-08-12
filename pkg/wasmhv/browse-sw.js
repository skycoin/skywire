// pkg/wasmhv/browse-sw.js c4-app-skynet
// Transport Service Worker for the real-origin mesh browser (RFC §4b:
// pkg/wasmhv/browseui/RFC-real-origin-browser.md). It runs on an ISOLATED browse
// origin B = "<pkslug>.mesh.localhost" (or a hosted <pk>.<domain>), NOT on the
// visor origin V. Its whole job is to be the network layer for B: every
// subresource/fetch/XHR the rendered mesh page makes is intercepted here and
// fulfilled through the IN-TAB wasm visor's own transports — the page never talks
// to a server for content, and B (untrusted mesh content) can never reach V's
// identity key.
//
// It is DISTINCT from pkg/wasmhv/sw.js (the PWA app-shell cache for V). This one
// is a transport relay, scoped to B.
//
// Bridge chain (see the bootstrap shell, browse-bootstrap.html):
//   SW.fetch → postMessage(controlling B page, MessageChannel) → the B page
//   relays to a hidden V-origin helper iframe (postMessage w/ targetOrigin) →
//   the iframe calls skywireVisor.fetchDmsg / fetchClearnet (its SharedWorker
//   visor) → response streams back the same chain → SW builds a Response.
//
// Navigations are NOT handled here — they hit the server shell, which injects the
// real mesh HTML into B via document.write (a real origin, unlike the old
// sandboxed srcdoc). Only non-navigation requests are relayed, so there is no
// "who serves the first page" chicken-and-egg.

'use strict';

self.addEventListener('install', function () {
  self.skipWaiting();
});

self.addEventListener('activate', function (e) {
  e.waitUntil(self.clients.claim());
});

// bridgeFetch asks a controlling B page to fulfil req through the wasm visor and
// resolves to a Response. Uses a per-request MessageChannel so concurrent
// subresource loads don't collide.
function bridgeFetch(req, clientId) {
  return (async function () {
    var client = null;
    if (clientId) {
      client = await self.clients.get(clientId);
    }
    if (!client) {
      var all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      client = all && all[0];
    }
    if (!client) {
      return new Response('mesh browser: no controlling page to relay through', { status: 503 });
    }

    var bodyBuf = null;
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      try { bodyBuf = await req.clone().arrayBuffer(); } catch (e) { bodyBuf = null; }
    }
    var headers = {};
    req.headers.forEach(function (v, k) { headers[k] = v; });

    return await new Promise(function (resolve) {
      var mc = new MessageChannel();
      var settled = false;
      var timer = setTimeout(function () {
        if (settled) return;
        settled = true;
        resolve(new Response('mesh browser: upstream timeout', { status: 504 }));
      }, 60000);

      mc.port1.onmessage = function (ev) {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        var r = ev.data || {};
        if (r.error) {
          resolve(new Response('mesh browser: ' + r.error, { status: 502 }));
          return;
        }
        // r.headers is a plain object; r.body is an ArrayBuffer (transferred).
        var h = new Headers();
        if (r.headers) {
          Object.keys(r.headers).forEach(function (k) {
            // strip hop-by-hop / framing headers the browser must recompute
            var lk = k.toLowerCase();
            if (lk === 'content-length' || lk === 'transfer-encoding' || lk === 'connection') return;
            try { h.set(k, r.headers[k]); } catch (e) { /* invalid header name */ }
          });
        }
        resolve(new Response(r.body || null, { status: r.status || 200, headers: h }));
      };

      client.postMessage({
        type: 'mesh-fetch',
        req: { url: req.url, method: req.method, headers: headers, body: bodyBuf }
      }, bodyBuf ? [mc.port2, bodyBuf] : [mc.port2]);
    });
  })();
}

self.addEventListener('fetch', function (e) {
  var req = e.request;
  // Navigations are served by the server shell (which injects the real mesh HTML
  // into this origin). Everything else — subresources, fetch(), XHR, imports,
  // streaming media — is relayed through the in-tab visor.
  if (req.mode === 'navigate') {
    return;
  }
  e.respondWith(bridgeFetch(req, e.clientId));
});
