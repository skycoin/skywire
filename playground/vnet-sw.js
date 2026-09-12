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
// point at /vnet/<port>/, so a document written for a server root resolves
// its relative URLs back into the worker's scope.
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

function askClient(client, req) {
	return new Promise((resolve) => {
		const ch = new MessageChannel();
		let done = false;
		const timer = setTimeout(() => { if (!done) { done = true; resolve(null); } }, ASK_TIMEOUT_MS);
		ch.port1.onmessage = (ev) => {
			if (done) return;
			done = true;
			clearTimeout(timer);
			const m = ev.data || {};
			resolve(m.refused ? null : m);
		};
		client.postMessage({ type: 'vnet-fetch', port: req.port, method: req.method, path: req.path, headers: req.headers, body: req.body }, [ch.port2]);
	});
}

function rewriteBase(html, port) {
	const href = PREFIX + port + '/';
	if (/<base\b[^>]*>/i.test(html)) return html.replace(/<base\b[^>]*>/i, '<base href="' + href + '">');
	if (/<head\b[^>]*>/i.test(html)) return html.replace(/(<head\b[^>]*>)/i, '$1<base href="' + href + '">');
	return '<base href="' + href + '">' + html;
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
		const m = await firstAnswer(clis.map((c) => askClient(c, req)));
		{
			if (!m) {
				return new Response('vnet: no page answered for port ' + port, { status: 504, headers: { 'content-type': 'text/plain' } });
			}
			const respHeaders = new Headers();
			const hs = m.headers || {};
			for (const k in hs) { if (Object.prototype.hasOwnProperty.call(hs, k)) { try { respHeaders.set(k, hs[k]); } catch (e) { /* forbidden name */ } } }
			respHeaders.set('cache-control', 'no-store');
			let bodyBytes = m.body instanceof Uint8Array ? m.body : new Uint8Array(0);
			const ct = (hs['content-type'] || '').toLowerCase();
			if (event.request.mode === 'navigate' && ct.indexOf('text/html') >= 0) {
				const html = rewriteBase(new TextDecoder().decode(bodyBytes), port);
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
