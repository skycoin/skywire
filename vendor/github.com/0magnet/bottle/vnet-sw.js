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

self.addEventListener('install', (e) => { self.skipWaiting(); });
self.addEventListener('activate', (e) => { e.waitUntil(self.clients.claim()); });

function askClient(client, req) {
	return new Promise((resolve) => {
		const ch = new MessageChannel();
		let done = false;
		const timer = setTimeout(() => { if (!done) { done = true; resolve(null); } }, 8000);
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
		for (const c of clis) {
			const m = await askClient(c, req);
			if (!m) continue;
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
		return new Response('vnet: no page answered for port ' + port, { status: 504, headers: { 'content-type': 'text/plain' } });
	})());
});
