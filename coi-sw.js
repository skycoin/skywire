// coi-sw.js — cross-origin isolation without a server that can set headers.
//
// SharedArrayBuffer, and therefore fsbridge and off-thread compiles, are only
// available to a cross-origin-isolated page. Isolation is granted by two
// RESPONSE HEADERS (COOP and COEP), and GitHub Pages serves static files with
// no way to set them. A service worker can: it is allowed to synthesize the
// responses for its own clients, headers included.
//
// The cost is one reload. The very first navigation is served by Pages, without
// the headers, so the page is not isolated; the worker installs, claims the
// page, and the registration script below reloads once. From then on the
// navigation comes from here, carrying the headers, and the page is isolated.
// Code must therefore tolerate crossOriginIsolated being false on first paint —
// proc.spawnWorker does, by refusing and letting callers fall back to spawn.
//
// This does not conflict with vnet-sw.js. That one registers at the narrower
// /vnet/ scope and keeps serving those URLs; they are same-origin, and COEP
// require-corp only demands CORP from CROSS-origin subresources.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));

self.addEventListener('fetch', (event) => {
	const req = event.request;

	// Chrome issues only-if-cached requests with mode != same-origin during
	// navigation preload; passing them to fetch() throws.
	if (req.cache === 'only-if-cached' && req.mode !== 'same-origin') return;

	event.respondWith(
		fetch(req)
			.then((res) => {
				// An opaque response has no readable body or headers to copy;
				// hand it back untouched rather than replacing it with a blank.
				if (res.status === 0) return res;
				const headers = new Headers(res.headers);
				headers.set('Cross-Origin-Embedder-Policy', 'require-corp');
				headers.set('Cross-Origin-Opener-Policy', 'same-origin');
				headers.set('Cross-Origin-Resource-Policy', 'same-origin');
				return new Response(res.body, {
					status: res.status,
					statusText: res.statusText,
					headers,
				});
			})
			.catch((err) => new Response('coi-sw: ' + err, { status: 502 })),
	);
});
