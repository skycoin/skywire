// pkg/wasmhv/browseui/hvws-vnet.js c3-vis-wasm
// The vnet adapter for the HOST visor: claims the visor's virtual-loopback
// ports and serves them from the /ws socket (hvws-client.js).
//
// The desk never talks to a visor directly — everything goes through vnet, the
// page-side namespace both sides of the wasm/native split already agree on.
// Panels call vnet.httpFetch(<port>, …); the nested browser and the vnet
// service worker resolve /vnet/<port>/… against the same port table. So the
// whole of "make the desk work against the host visor" reduces to: put a
// listener on those ports whose handler is the host visor.
//
// That is what this does. Nothing above it changes — not one panel, not the
// service worker, not the browser's DirectLoader — because from the page's
// side a bridged port is indistinguishable from a wasm visor's.
//
// Two request classes, and they are routed differently on purpose:
//
//   /api/…      over /ws. The endpoint replays the frame through the
//               hypervisor's own router, so permissions, middleware and the
//               response body are the real thing.
//   everything  one same-origin fetch. /ws deliberately refuses any path
//   else        outside /api (wsAllowedPath), and it does not need to carry
//               them: this bridge only ever installs on a page the hypervisor
//               ITSELF served, so its assets — the Angular bundle, its fonts,
//               index.html — are already same-origin. That is what makes
//               /vnet/<hvPort>/ a complete, natively-resolving mirror of the
//               dashboard rather than an API-only port.
//
//	SkywireHVBridge.install({ports:[3435, 8001]}) -> Promise<bridge|null>
//
// The claim tracks the socket: ports are bound when /ws is open and released
// when it drops, so vnet.listening(<port>) keeps meaning what the desk reads
// it as — "a visor is up and reachable from this page" — instead of becoming a
// permanent lie the moment the host visor restarts.
(function () {
	'use strict';
	if (globalThis.SkywireHVBridge) return;

	var te = new TextEncoder();
	var td = new TextDecoder();

	// A reason phrase is decorative here (vnet's HTTP reader takes the status
	// from the second field and ignores the rest), but a readable one shows up
	// in a devtools network panel and in the browser's own error pages.
	var REASON = {
		200: 'OK', 204: 'No Content', 301: 'Moved Permanently', 302: 'Found',
		304: 'Not Modified', 400: 'Bad Request', 401: 'Unauthorized',
		403: 'Forbidden', 404: 'Not Found', 405: 'Method Not Allowed',
		500: 'Internal Server Error', 502: 'Bad Gateway', 503: 'Service Unavailable',
	};

	// Headers that belong to the real HTTP hop and would be nonsense — or
	// actively wrong — on the far side of a virtual pipe.
	var HOP_HEADERS = /^(host|connection|content-length|transfer-encoding|upgrade|via|accept-encoding|keep-alive|proxy-.*)$/i;

	// A request head larger than this is not one this bridge will ever serve;
	// the cap stops a peer that never sends CRLFCRLF from growing the buffer.
	var MAX_HEAD = 65536;

	function joinChunks(chunks, total) {
		var out = new Uint8Array(total);
		var off = 0;
		for (var i = 0; i < chunks.length; i++) {
			out.set(chunks[i], off);
			off += chunks[i].length;
		}
		return out;
	}

	function sieve(headers) {
		var out = {};
		for (var k in headers) {
			if (!Object.prototype.hasOwnProperty.call(headers, k)) continue;
			if (HOP_HEADERS.test(k)) continue;
			out[k] = headers[k];
		}
		return out;
	}

	// readRequest pulls ONE HTTP/1.x request off the accepter side of a vnet
	// conn and hands it to cb, or cb(null) when the peer is not speaking HTTP.
	//
	// The shape check matters beyond hygiene: 3435 is the visor's RPC port, and
	// on a wasm desk what listens there speaks Go's net/rpc GOB stream, not
	// HTTP. This bridge cannot carry gob — /ws is request/response, with no
	// streaming — so a gob dialer is refused at the first bytes and fails fast
	// with a closed pipe, instead of being answered with an HTTP response it
	// would try to decode as a gob header.
	function readRequest(v, id, cb) {
		var chunks = [];
		var total = 0;
		var head = null;
		var settled = false;

		function done(req) {
			if (settled) return;
			settled = true;
			cb(req);
		}

		function parseHead(buf) {
			var sep = -1;
			for (var i = 0; i + 3 < buf.length; i++) {
				if (buf[i] === 13 && buf[i + 1] === 10 && buf[i + 2] === 13 && buf[i + 3] === 10) { sep = i; break; }
			}
			if (sep < 0) {
				if (buf.length > MAX_HEAD) done(null);
				return null;
			}
			var lines = td.decode(buf.subarray(0, sep)).split('\r\n');
			var rl = /^([A-Z]{3,10}) (\S+) HTTP\/1\.[01]$/.exec(lines[0] || '');
			if (!rl) { done(null); return null; }
			var hs = {};
			for (var j = 1; j < lines.length; j++) {
				var ci = lines[j].indexOf(':');
				if (ci > 0) hs[lines[j].slice(0, ci).trim().toLowerCase()] = lines[j].slice(ci + 1).trim();
			}
			var cl = /^\d+$/.test(hs['content-length'] || '') ? parseInt(hs['content-length'], 10) : 0;
			return { method: rl[1], path: rl[2], headers: hs, bodyStart: sep + 4, contentLength: cl };
		}

		function emit(buf, upTo) {
			done({
				method: head.method,
				path: head.path,
				headers: head.headers,
				body: buf.subarray(head.bodyStart, upTo),
			});
		}

		function pump() {
			if (settled) return;
			for (;;) {
				var b = v.recv(id, 'b');
				if (b) {
					chunks.push(b);
					total += b.length;
					var buf = joinChunks(chunks, total);
					if (!head) head = parseHead(buf);
					if (settled) return;
					if (head && total - head.bodyStart >= head.contentLength) {
						emit(buf, head.bodyStart + head.contentLength);
						return;
					}
					continue;
				}
				if (v.eof(id, 'b')) {
					// EOF with a complete head: httpFetch writes
					// Content-Length, but a caller that closed early still gets
					// whatever body did arrive rather than a dropped request.
					if (head) { emit(joinChunks(chunks, total), total); } else { done(null); }
					return;
				}
				v.onReadable(id, 'b', pump);
				return;
			}
		}
		pump();
	}

	// writeResponse serializes one HTTP/1.0 response onto the pipe and closes
	// it. Connection: close with an explicit Content-Length is exactly what
	// vnet's reader expects; no chunked encoding exists on this wire.
	function writeResponse(v, id, status, headers, body) {
		var out = 'HTTP/1.0 ' + status + ' ' + (REASON[status] || 'Status') + '\r\n';
		var hs = headers || {};
		for (var k in hs) {
			if (!Object.prototype.hasOwnProperty.call(hs, k)) continue;
			if (HOP_HEADERS.test(k)) continue;
			// A header value carrying CR/LF would split the response into two.
			out += k + ': ' + String(hs[k]).replace(/[\r\n]+/g, ' ') + '\r\n';
		}
		out += 'Content-Length: ' + body.length + '\r\nConnection: close\r\n\r\n';
		v.send(id, 'b', te.encode(out));
		if (body.length) v.send(id, 'b', body);
		v.close(id, 'b');
	}

	function isAPI(path) {
		var p = String(path).split('?')[0].split('#')[0];
		return p === '/api' || p.indexOf('/api/') === 0;
	}

	// route answers one parsed request from the host visor.
	function route(client, req) {
		if (isAPI(req.path)) {
			var body = (req.body && req.body.length) ? td.decode(req.body) : null;
			return client.request(req.method, req.path, body, sieve(req.headers)).then(function (r) {
				return { status: r.status, headers: r.headers, body: te.encode(r.body || '') };
			});
		}
		var init = {
			method: req.method,
			credentials: 'same-origin',
			cache: 'no-store',
			headers: sieve(req.headers),
			// Follow redirects here rather than relaying a 3xx: the Location
			// the hypervisor writes is relative to ITS root, and the page that
			// receives it sits under a /vnet/<port>/ prefix the server never
			// saw, so a relayed redirect walks straight out of the prefix.
			redirect: 'follow',
		};
		if (req.method !== 'GET' && req.method !== 'HEAD' && req.body && req.body.length) {
			init.body = req.body;
		}
		return fetch(req.path, init).then(function (r) {
			return r.arrayBuffer().then(function (buf) {
				var hs = {};
				r.headers.forEach(function (val, key) { hs[key] = val; });
				return { status: r.status, headers: hs, body: new Uint8Array(buf) };
			});
		});
	}

	function serve(v, id, client) {
		readRequest(v, id, function (req) {
			if (!req) { v.close(id, 'b'); return; }
			route(client, req).then(function (r) {
				writeResponse(v, id, r.status, r.headers, r.body);
			}).catch(function (e) {
				writeResponse(v, id, 502, { 'content-type': 'text/plain' },
					te.encode('vnet→/ws bridge: ' + ((e && e.message) || e)));
			});
		});
	}

	// install opens the socket and, once it is up, claims the given virtual
	// ports for the host visor. Resolves the bridge, or null when there is no
	// /ws to bridge to (the standalone desk, the docs playground) or when every
	// requested port is already claimed by something in this page — an in-tab
	// wasm visor keeps its own ports; the host bridge never evicts it.
	function install(opts) {
		opts = opts || {};
		var v = globalThis.vnet;
		if (!v || !globalThis.SkywireHVWS) return Promise.resolve(null);
		var ports = opts.ports || [];
		var claimed = [];

		var client = globalThis.SkywireHVWS.open({ path: opts.path });

		function claim() {
			ports.forEach(function (port) {
				if (claimed.indexOf(port) >= 0) return;
				var ok = v.listen(port, function (id) { serve(v, id, client); });
				if (ok) { claimed.push(port); }
			});
		}
		function release() {
			claimed.forEach(function (port) { v.unlisten(port); });
			claimed = [];
		}

		client.onstate = function (s) {
			if (s === 'open') { claim(); } else { release(); }
		};

		var bridge = {
			client: client,
			ports: ports,
			claimed: function () { return claimed.slice(); },
			// fetch is the /api call as the desk's own code would make it —
			// handy for a caller that has a bridge in hand and does not want to
			// go back out through the vnet pipe to reach the same socket.
			fetch: function (m, p, b, h) { return client.request(m, p, b, h); },
			close: function () { release(); client.close(); },
		};

		return new Promise(function (resolve) {
			var settled = false;
			var timer = setTimeout(function () {
				if (settled) return;
				settled = true;
				// The probe said /ws was there, so keep the client (it will
				// reconnect and claim on its own); just do not make the boot
				// wait any longer for it.
				resolve(claimed.length ? bridge : null);
			}, opts.timeoutMs || 8000);
			var prev = client.onstate;
			client.onstate = function (s) {
				prev(s);
				if (s !== 'open' || settled) return;
				settled = true;
				clearTimeout(timer);
				resolve(bridge);
			};
			if (client.state === 'open') { client.onstate('open'); }
		});
	}

	globalThis.SkywireHVBridge = { install: install };
})();
