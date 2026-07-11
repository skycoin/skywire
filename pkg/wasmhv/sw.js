// sw.js — service worker for the standalone wasm-visor PWA.
//
// Goal: make the served wasm-visor installable + offline-capable WITHOUT ever
// serving a stale wasm/UI to an online user (autoupdate.js polls /wasm-version
// and self-reloads, so freshness when online is mandatory).
//
// Strategy:
//   - content-hashed Angular bundles (…/main.<hash>.js, styles.<hash>.css, lazy
//     chunks) are immutable → cache-first (fast, never stale by construction).
//   - everything else (index, /wasm-visor.wasm, /wasm-version, hv-boot.js, …) is
//     network-first: fresh when online, falls back to the last cached copy only
//     when the network is unavailable (offline launch).
//
// The cache name embeds the build fingerprint (the placeholder below is
// substituted by hv serve with the same /wasm-version hash the page boots with).
// So every new
// skywire build ships a byte-different sw.js → the browser detects a new worker →
// install() re-precaches the fresh shell and activate() purges the prior build's
// cache. Routine deploys need no manual edit here.

const CACHE_VERSION = 'skywire-wasm-visor-__BUILD__';
const PRECACHE = [
  './',
  'manifest.webmanifest',
  'icon-192.png',
  'icon-512.png',
  'wasm_exec.js',
  'hv-boot.js',
  'worker.js',
  'browse.js',
];

// Content-hashed (immutable) build assets: Angular emits main.<hash>.js,
// styles.<hash>.css, <chunk>.<hash>.js — an 8+ hex-char segment between dots.
const IMMUTABLE = /\.[0-9a-f]{8,}\.(js|css)$/i;

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_VERSION)
      .then((cache) => cache.addAll(PRECACHE).catch(() => undefined))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE_VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) { return; } // never touch cross-origin

  // The bundled skycoin-web wallet (served same-origin at /wallet/) reaches its
  // fibercoin node via same-origin /api/v1|v2/* requests (skycoin API). Route
  // those over dmsg to the configured coin node, through a window client that
  // owns the wasm visor's fetchDmsg — this is what lets the same-origin wallet
  // read/write the chain with no backend. Handles GET + POST (txn submit), so
  // this must precede the GET-only guard below. Non-coin /api/* (the HV UI's own
  // API) is answered in-page by override.js and never becomes a network request,
  // so it doesn't reach here.
  if (/^\/api\/v[12]\//.test(url.pathname)) {
    event.respondWith(coinFetch(req));
    return;
  }

  if (req.method !== 'GET') { return; }

  // The auto-updater's build-version poll MUST always reach the real server — a
  // cached value would mask a new build forever. Don't intercept it at all
  // (return without respondWith() → the browser does its normal network fetch).
  if (url.pathname === '/wasm-version') { return; }

  if (IMMUTABLE.test(url.pathname)) {
    // cache-first: hashed bundles can't change under a fixed URL.
    event.respondWith(
      caches.match(req).then((hit) => hit || fetch(req).then((res) => putInCache(req, res)))
    );
    return;
  }

  // network-first: online → fresh (autoupdate stays correct); offline → cache.
  event.respondWith(
    fetch(req)
      .then((res) => putInCache(req, res))
      .catch(() => caches.match(req).then((hit) => hit || caches.match('./')))
  );
});

// coinFetch relays one wallet node-API request to a window client that owns the
// wasm visor's dmsg fetch (the SW can't call skywireVisor directly). The client
// resolves it over dmsg to the configured coin node and replies over a
// MessageChannel port. Returns a synthetic JSON Response — the wallet's XHR sees
// a normal same-origin response, unaware the bytes crossed the mesh.
async function coinFetch(req) {
  try {
    const url = new URL(req.url);
    const method = req.method;
    const body = (method === 'GET' || method === 'HEAD') ? null : await req.text();
    const clients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    if (!clients.length) {
      return jsonResp(503, { error: 'no visor client to route coin API' });
    }
    const data = await new Promise((resolve) => {
      const ch = new MessageChannel();
      const timer = setTimeout(() => resolve({ status: 504, body: '{"error":"coin fetch timeout"}' }), 30000);
      ch.port1.onmessage = (e) => { clearTimeout(timer); resolve(e.data || {}); };
      clients[0].postMessage({ type: 'coin-fetch', method: method, path: url.pathname + url.search, body: body }, [ch.port2]);
    });
    return new Response(data.body != null ? data.body : '', {
      status: data.status || 502,
      headers: { 'Content-Type': 'application/json' },
    });
  } catch (e) {
    return jsonResp(502, { error: String(e) });
  }
}

function jsonResp(status, obj) {
  return new Response(JSON.stringify(obj), { status: status, headers: { 'Content-Type': 'application/json' } });
}

function putInCache(req, res) {
  // Only cache complete, basic (same-origin) 200s; clone before the body is used.
  if (res && res.status === 200 && res.type === 'basic') {
    const copy = res.clone();
    caches.open(CACHE_VERSION).then((cache) => cache.put(req, copy));
  }
  return res;
}
