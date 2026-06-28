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
// The cache name is bumped by editing CACHE_VERSION; activate() purges old caches.

const CACHE_VERSION = 'skywire-wasm-visor-v1';
const PRECACHE = [
  './',
  'manifest.webmanifest',
  'icon-192.png',
  'icon-512.png',
  'wasm_exec.js',
  'hv-boot.js',
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
  if (req.method !== 'GET') { return; }
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) { return; } // never touch cross-origin

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

function putInCache(req, res) {
  // Only cache complete, basic (same-origin) 200s; clone before the body is used.
  if (res && res.status === 200 && res.type === 'basic') {
    const copy = res.clone();
    caches.open(CACHE_VERSION).then((cache) => cache.put(req, copy));
  }
  return res;
}
