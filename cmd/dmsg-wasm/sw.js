// sw.js — Service Worker that runs the WASM dmsg client and proxies every
// in-scope request over dmsg to a hypervisor (or any visor) by public key.
//
// This is what makes the UNMODIFIED hypervisor Angular UI load in a browser
// "by PK": the SW intercepts all navigations + fetches under its scope and
// routes them over dmsg (via skywireDmsg.fetch) to the target's dmsg-HTTP
// listener (HypervisorConfig.DmsgUIPort). The page origin only needs to serve
// the four local assets below; everything else — index.html, the JS/CSS
// bundles, /api/*, /assets/* — arrives over dmsg.
//
// Config arrives as query params on the registration URL (self.location.search):
//   seedpk, seedws, disc  — dmsg bootstrap (seed WS server + dmsg-discovery)
//   hvpk, hvport          — the hypervisor to proxy to (DmsgUIPort)
//   sk                    — optional secret key (blank = ephemeral identity)

/* global Go */
importScripts('wasm_exec.js');

const P = new URLSearchParams(self.location.search);
const SEED_PK = P.get('seedpk');
const SEED_WS = P.get('seedws');
const DISC = P.get('disc');
const HV = P.get('hvpk') + ':' + (P.get('hvport') || '8000');
const SK = P.get('sk') || '';

// Assets served from the page origin (NOT proxied over dmsg).
const LOCAL = new Set(['/sw.js', '/wasm_exec.js', '/dmsg.wasm', '/hv.html', '/']);

// Boot the WASM dmsg client and connect. Resolves once skywireDmsg is connected;
// the fetch handler awaits this so the very first request blocks until dmsg is up.
const ready = (async () => {
  const go = new Go();
  const res = await WebAssembly.instantiateStreaming(fetch('dmsg.wasm'), go.importObject);
  go.run(res.instance); // sets self.skywireDmsg, then runs forever — do NOT await
  while (!self.skywireDmsg) { await new Promise(r => setTimeout(r, 10)); }
  const myPK = await self.skywireDmsg.connect(SK, SEED_PK, SEED_WS, DISC);
  console.log('[sw] dmsg connected as', myPK, '→ proxying everything to hv', HV);
  return myPK;
})();

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil(self.clients.claim()));

self.addEventListener('fetch', e => {
  const url = new URL(e.request.url);
  // Only proxy same-origin requests; let cross-origin + local assets pass through.
  if (url.origin !== self.location.origin) return;
  if (LOCAL.has(url.pathname) && url.pathname !== '/') return;

  e.respondWith((async () => {
    try {
      await ready;
      const headers = {};
      for (const [k, v] of e.request.headers) headers[k] = v;
      let body = null;
      if (e.request.method !== 'GET' && e.request.method !== 'HEAD') {
        body = await e.request.text();
      }
      const r = await self.skywireDmsg.fetch(
        HV, e.request.method, url.pathname + url.search, body, headers);
      return new Response(r.body, { status: r.status, headers: r.headers });
    } catch (err) {
      return new Response('dmsg proxy error: ' + err, { status: 502 });
    }
  })());
});
