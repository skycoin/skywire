// sw.js — the transport service worker for a real-origin browse frame.
//
// It runs on an isolated browse origin B ("<id>.<suffix>"), never on the app
// origin A that holds the credentials. Its whole job is to be the network layer
// for B: every subresource, fetch, XHR, import and media range the rendered page
// asks for is intercepted here and fulfilled by asking the app to do it. The
// page never talks to a server for its content.
//
// Nothing in this file names a transport, and that is deliberate rather than
// incidental: this worker runs inside the untrusted origin, so it must not be
// able to reach the credential — which means it must not know what the
// credential is for. The transport lives on the other side of the bridge.
//
// Bridge chain (see bootstrap.html):
//   sw.fetch → postMessage(controlling B page, MessageChannel) → the B page
//   relays to its parent, the app origin A → A's responder runs the embedder's
//   transport → the response streams back the same chain → a real Response.
//
// Navigations are NOT handled here. They hit the server, which serves the same
// bootstrap shell for every path, and the shell writes the fetched document into
// this origin. A worker that served the first page would have to be installed by
// a page it had not served yet, so there would be no base case.

'use strict';

self.addEventListener('install', function () {
  self.skipWaiting();
});

self.addEventListener('activate', function (e) {
  e.waitUntil(self.clients.claim());
});

// bridgeFetch asks a controlling B page to fulfil req and resolves to a
// Response. A per-request MessageChannel keeps concurrent subresource loads from
// colliding.
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
      return new Response('real-origin: no controlling page to relay through', { status: 503 });
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
        resolve(new Response('real-origin: upstream timeout', { status: 504 }));
      }, 60000);

      mc.port1.onmessage = function (ev) {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        var r = ev.data || {};
        if (r.error) {
          resolve(new Response('real-origin: ' + r.error, { status: 502 }));
          return;
        }
        // r.headers is a plain object; r.body is a transferred ArrayBuffer.
        var h = new Headers();
        if (r.headers) {
          Object.keys(r.headers).forEach(function (k) {
            // Drop the hop-by-hop and framing headers: the browser recomputes
            // its own, and a stale content-length truncates the body.
            var lk = k.toLowerCase();
            if (lk === 'content-length' || lk === 'transfer-encoding' || lk === 'connection') return;
            try { h.set(k, r.headers[k]); } catch (e) { /* invalid header name */ }
          });
        }
        resolve(new Response(r.body || null, { status: r.status || 200, headers: h }));
      };

      client.postMessage({
        type: 'realorigin-fetch',
        req: { url: req.url, method: req.method, headers: headers, body: bodyBuf }
      }, bodyBuf ? [mc.port2, bodyBuf] : [mc.port2]);
    });
  })();
}

self.addEventListener('fetch', function (e) {
  var req = e.request;
  if (req.mode === 'navigate') {
    return;
  }
  e.respondWith(bridgeFetch(req, e.clientId));
});
