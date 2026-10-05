// vnet-sw.js — the service-worker half of the virtual loopback network.
//
// vnet.js gives a page an in-memory port table, which covers dialers that
// live in the page: wasm instances, page scripts, the transcoding browser.
// What it cannot cover is the browser engine itself — an <iframe> whose
// document wants to load its own subresources (module graphs, XHR, fonts)
// with NATIVE resolution needs real same-origin URLs, not a transcoder.
//
// This worker provides those URLs. It intercepts GET /vnet/<port>/<path>
// (the prefix is configurable at register time via ?prefix=), forwards each
// request to a window client over a MessageChannel, and the page answers
// from its vnet port table (vnet.enableSW installs the responder). The
// result: iframe.src = '/vnet/8001/' renders a server running INSIDE the
// page as if it were a normal same-origin site — module imports, XHR and
// history all behave natively.
//
// Navigations get one adjustment: <base href> is rewritten (or injected) to
// point at the request's own directory under /vnet/<port>/, so a document
// written for a server root resolves its relative URLs back into the
// worker's scope — at whatever depth it was served from.
//
// Multi-tab caveat (v1): requests are offered to every window client of the
// origin and the first non-refusing answer wins. Two tabs both listening on
// the same virtual port are ambiguous — the same way two page-loads of one
// visor identity already are.
'use strict';

const PREFIX = (() => {
	try {
		const p = new URL(self.location.href).searchParams.get('prefix');
		if (p && /^(\/[A-Za-z0-9._-]+)+\/$/.test(p)) return p;
	} catch (e) { /* fall through */ }
	return '/vnet/';
})();

// COI: whether to stamp the cross-origin-isolation headers on what this
// worker synthesizes, set at register time via ?coi=1 when the registering
// page is itself isolated.
//
// It is not unconditional. COEP require-corp is INHERITED BY EMBEDDED
// DOCUMENTS, so a vnet-served page inside an isolated parent is refused
// unless it carries COEP of its own — that is what this is for. But the same
// header then demands CORP from that page's own cross-origin subresources,
// and pages served here legitimately pull remote images. Stamping only when
// the parent is already isolated keeps the requirement where it is unavoidable.
const COI = (() => {
	try { return new URL(self.location.href).searchParams.get('coi') === '1'; } catch (e) { return false; }
})();

// How long ONE client gets to answer before it is treated as not listening.
// Configurable at register time via ?timeout=<ms>, because the right value is a
// property of what the page SERVES, not of this worker: a page whose slowest
// in-page endpoint takes 10s needs a budget above that or its own dashboard
// 504s, while a page serving small JSON would rather fail over sooner.
// Clamped to 1s..120s so a typo cannot wedge the path or disable it.
const ASK_TIMEOUT_MS = (() => {
	try {
		const t = parseInt(new URL(self.location.href).searchParams.get('timeout'), 10);
		if (Number.isFinite(t)) return Math.min(120000, Math.max(1000, t));
	} catch (e) { /* fall through */ }
	return 8000;
})();

self.addEventListener('install', (e) => { self.skipWaiting(); });
self.addEventListener('activate', (e) => { e.waitUntil(self.clients.claim()); });

// askClient offers a request to one window client and resolves with its first
// reply, or null when it refuses or does not answer in time. A page that
// streams replies with a head first and keeps the channel open for the body;
// later messages are buffered until the caller reads them with onMore.
function askClient(client, req) {
	return new Promise((resolve) => {
		const ch = new MessageChannel();
		let answer = null;
		const queue = [];
		let sink = null;
		const timer = setTimeout(() => { if (!answer) { answer = 'timeout'; resolve(null); } }, ASK_TIMEOUT_MS);
		ch.port1.onmessage = (ev) => {
			const m = ev.data || {};
			if (answer && answer !== 'timeout') {
				if (sink) sink(m); else queue.push(m);
				return;
			}
			if (answer === 'timeout') { if (m.head) ch.port1.postMessage({ cancel: true }); return; }
			clearTimeout(timer);
			if (m.refused) { answer = 'refused'; resolve(null); return; }
			answer = {
				first: m,
				port: ch.port1,
				onMore(fn) { sink = fn; while (queue.length) fn(queue.shift()); },
				cancel() { try { ch.port1.postMessage({ cancel: true }); } catch (e) { /* closed */ } },
			};
			resolve(answer);
		};
		client.postMessage({ type: 'vnet-fetch', stream: true, port: req.port, method: req.method, path: req.path, headers: req.headers, body: req.body }, [ch.port2]);
	});
}

// noBodyStatus is a status a Response may not carry a body with.
function noBodyStatus(s) { return s === 101 || s === 204 || s === 205 || s === 304; }

// rewriteBase points a document's <base> at the DIRECTORY the request came
// from, not at the port root.
//
// The root is right only for a single-page server. For a site with depth it
// silently breaks every relative link below the top: a page served at
// /vnet/8002/cli/README.md linking to "dmsg/README.md" resolved to
// /vnet/8002/dmsg/README.md — the base had thrown the "cli/" away. Measured
// on skywire's doc serve, where only the landing page's links worked.
function rewriteBase(html, port, path) {
	const p = String(path || '/').split('?')[0];
	const href = PREFIX + port + p.slice(0, p.lastIndexOf('/') + 1);
	const tag = '<base href="' + href + '">' + wsShimTag();
	if (/<base\b[^>]*>/i.test(html)) return html.replace(/<base\b[^>]*>/i, () => tag);
	if (/<head\b[^>]*>/i.test(html)) return html.replace(/(<head\b[^>]*>)/i, (h) => h + tag);
	return tag + html;
}

// wsShimTag is a script that routes the page's WebSockets to a virtual port
// through the page that runs vnet (vnet.webSocket), because a service worker
// cannot carry a WebSocket. Other WebSockets are left alone. It runs before
// the page's own scripts, right after the <base>.
function wsShimTag() {
	return '<script>(' + wsShim.toString() + ')(' + JSON.stringify(PREFIX) + ');</' + 'script>';
}

function wsShim(P) {
	try {
		var N = window.WebSocket, h = null, w = window;
		for (var i = 0; i < 8 && w; i++) {
			try { if (w.vnet && typeof w.vnet.webSocket === 'function') { h = w.vnet; break; } } catch (e) { /* cross-origin */ }
			if (w === w.parent) break;
			w = w.parent;
		}
		if (!h) { try { if (window.opener && window.opener.vnet && window.opener.vnet.webSocket) h = window.opener.vnet; } catch (e) { /* cross-origin */ } }
		if (!h || !N) return;
		var W = function (u, p) {
			var x = new URL(u, location.href);
			if (x.host === location.host && x.pathname.indexOf(P) === 0) {
				var r = x.pathname.slice(P.length).match(/^(\d+)(\/.*)?$/);
				if (r) return h.webSocket(+r[1], (r[2] || '/') + x.search, p, x.href, window);
			}
			return p === undefined ? new N(u) : new N(u, p);
		};
		W.CONNECTING = 0; W.OPEN = 1; W.CLOSING = 2; W.CLOSED = 3;
		W.prototype = N.prototype;
		window.WebSocket = W;
	} catch (e) { /* leave the native WebSocket */ }
}

self.addEventListener('fetch', (event) => {
	const url = new URL(event.request.url);
	if (url.origin !== self.location.origin || !url.pathname.startsWith(PREFIX)) return;
	const rest = url.pathname.slice(PREFIX.length);
	const slash = rest.indexOf('/');
	const portStr = slash < 0 ? rest : rest.slice(0, slash);
	const port = parseInt(portStr, 10);
	if (!port || String(port) !== portStr) return;
	const path = (slash < 0 ? '/' : rest.slice(slash)) + (url.search || '');

	event.respondWith((async () => {
		const headers = {};
		for (const [k, v] of event.request.headers.entries()) {
			// Hop-by-hop / browser-managed headers stay out of the virtual wire.
			if (/^(host|connection|content-length|accept-encoding|upgrade|via)$/i.test(k)) continue;
			headers[k] = v;
		}
		// Sec-Fetch-Dest cannot reach the loop above: it is a forbidden header
		// name, so the browser keeps it out of Request.headers and a worker
		// rebuilding the request has no way to read or forward it. Everything
		// served through here loses it, while the same request made over the
		// network keeps it.
		//
		// request.destination carries the same fact and IS readable, so pass it
		// on under a name of our own. A server behind the virtual wire that
		// needs to know it is being framed otherwise has only a query parameter
		// to go on, and a client-side router drops that on its first
		// navigation — so a reload of the frame is served something different
		// from what the first load got. (skywire's hypervisor UI chooses
		// dashboard-vs-desk on exactly this, and reloaded into the wrong one.)
		if (event.request.destination) headers['x-vnet-fetch-dest'] = event.request.destination;
		let body = null;
		if (!/^(GET|HEAD)$/i.test(event.request.method)) {
			try { body = new Uint8Array(await event.request.arrayBuffer()); } catch (e) { body = null; }
		}
		const req = { port: port, method: event.request.method, path: path, headers: headers, body: body };

		const clis = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
		// Ask every client AT ONCE and take the first real answer.
		//
		// This used to await each client in turn. A client that has no vnet
		// responder installed never replies at all — an iframe rendering a
		// page, say — so it costs the FULL timeout before the next client is
		// tried, and the page that would have answered in milliseconds is
		// reached only after the silent ones have each burned it. A desk with
		// three iframes could spend 24s on an 8s budget, and a navigation that
		// exceeded it got the "no page answered" body permanently, with no
		// retry, while the server behind it was healthy.
		//
		// Asking in parallel makes the latency the FASTEST answering client
		// rather than the sum of the silent ones, which is what the multi-tab
		// note at the top of this file already describes as the contract.
		const asks = clis.map((c) => askClient(c, req));
		const a = await firstAnswer(asks);
		// A second tab answering the same port would hold its stream open for
		// nothing; tell every answer but the one used to stop.
		for (const p of asks) p.then((x) => { if (x && x !== a) x.cancel(); });
		{
			if (!a) {
				return new Response('vnet: no page answered for port ' + port, { status: 504, headers: { 'content-type': 'text/plain' } });
			}
			const m = a.first;
			const respHeaders = new Headers();
			const hs = m.headers || {};
			for (const k in hs) { if (Object.prototype.hasOwnProperty.call(hs, k)) { try { respHeaders.set(k, hs[k]); } catch (e) { /* forbidden name */ } } }
			respHeaders.set('cache-control', 'no-store');
			if (COI) {
				respHeaders.set('Cross-Origin-Embedder-Policy', 'require-corp');
				respHeaders.set('Cross-Origin-Opener-Policy', 'same-origin');
				respHeaders.set('Cross-Origin-Resource-Policy', 'same-origin');
			}
			const ct = (hs['content-type'] || '').toLowerCase();
			const rewrite = event.request.mode === 'navigate' && ct.indexOf('text/html') >= 0;
			if (m.head) {
				respHeaders.delete('transfer-encoding');
				respHeaders.delete('connection');
				if (noBodyStatus(m.status)) {
					a.cancel();
					return new Response(null, { status: m.status, headers: respHeaders });
				}
				if (!rewrite) {
					const body = new ReadableStream({
						start(ctrl) {
							a.onMore((x) => {
								try {
									if (x.chunk) ctrl.enqueue(new Uint8Array(x.chunk));
									if (x.end) ctrl.close();
								} catch (e) { /* reader gone */ }
							});
						},
						cancel() { a.cancel(); },
					});
					return new Response(body, { status: m.status || 200, headers: respHeaders });
				}
				// A page to rewrite has to be whole first.
				m.body = await new Promise((resolve) => {
					const parts = [];
					let n = 0;
					a.onMore((x) => {
						if (x.chunk) { const c = new Uint8Array(x.chunk); parts.push(c); n += c.length; }
						if (x.end) {
							const all = new Uint8Array(n);
							let off = 0;
							for (const c of parts) { all.set(c, off); off += c.length; }
							resolve(all);
						}
					});
				});
			}
			let bodyBytes = m.body instanceof Uint8Array ? m.body : new Uint8Array(0);
			if (noBodyStatus(m.status)) return new Response(null, { status: m.status, headers: respHeaders });
			if (rewrite) {
				const html = rewriteBase(new TextDecoder().decode(bodyBytes), port, path);
				bodyBytes = new TextEncoder().encode(html);
				respHeaders.delete('content-length');
			}
			return new Response(bodyBytes, { status: m.status || 200, headers: respHeaders });
		}
	})());
});

// firstAnswer resolves with the first promise to produce a truthy value, or
// null once every one has settled without producing one. Unlike Promise.race
// it ignores the losers' nulls, and unlike Promise.all it does not wait for
// the slowest — which is the whole point when the slow ones are clients that
// will never reply and only run out their timeout.
function firstAnswer(promises) {
	if (!promises.length) return Promise.resolve(null);
	return new Promise((resolve) => {
		let outstanding = promises.length;
		let settled = false;
		const lose = () => { if (!settled && --outstanding === 0) { settled = true; resolve(null); } };
		for (const p of promises) {
			p.then((v) => {
				if (settled) return;
				if (v) { settled = true; resolve(v); return; }
				lose();
			}, lose);
		}
	});
}
