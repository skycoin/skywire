// vnet.js — a virtual loopback network for the page.
//
// The missing OS layer for running localhost-shaped software in the browser:
// on a host, one process LISTENS on a loopback port and another DIALS it.
// Wasm instances have no shared network — Go's js runtime simulates loopback
// only WITHIN one instance — so this provides the between-instances piece: a
// page-global port table with in-memory duplex byte pipes. The Go side (the
// vnet subpackage) adapts these to net.Listener / net.Conn, so REAL server
// and client code — an RPC server in one instance, its CLI in another, an
// http.Server a page script fetches from — works unmodified across
// instances. Page JS can resolve http://127.0.0.1:<port> against the same
// table via httpFetch below.
//
// Same-realm v1: all endpoints live in one JS realm (the page). The API is
// callback-based so a MessagePort bridge can extend it across Workers later.
//
// Sides: the dialer is side 'a', the accepter side 'b'. q.a holds bytes
// readable BY side a (written by b), and vice versa.
(function () {
	'use strict';
	if (globalThis.vnet) return;

	let nextID = 1;
	const ports = new Map(); // port -> { onconn(connId) }
	const conns = new Map(); // id -> { q:{a:[],b:[]}, wake:{a:null,b:null}, closed:{a:false,b:false} }

	function peer(side) { return side === 'a' ? 'b' : 'a'; }

	function wake(c, side) {
		const cb = c.wake[side];
		if (cb) { c.wake[side] = null; queueMicrotask(cb); }
	}

	globalThis.vnet = {
		// listen claims a port; onconn(connId) fires per inbound dial (the
		// accepter is side 'b' of that conn). Returns false if taken.
		listen(port, onconn) {
			if (ports.has(port)) return false;
			ports.set(port, { onconn });
			return true;
		},

		unlisten(port) { ports.delete(port); },

		listening(port) { return ports.has(port); },

		// dial connects to a listening port; returns the conn id (dialer is
		// side 'a') or -1 (connection refused).
		dial(port) {
			const l = ports.get(port);
			if (!l) return -1;
			const id = nextID++;
			conns.set(id, { q: { a: [], b: [] }, wake: { a: null, b: null }, closed: { a: false, b: false } });
			queueMicrotask(() => l.onconn(id));
			return id;
		},

		// send appends bytes for the peer. Returns false when the peer end is
		// closed (EPIPE) or the conn is gone.
		send(id, side, bytes) {
			const c = conns.get(id);
			if (!c || c.closed[peer(side)]) return false;
			c.q[peer(side)].push(bytes.slice());
			wake(c, peer(side));
			return true;
		},

		// recv pops one readable chunk for side, or returns null: would-block
		// when the conn is open, EOF when the peer closed and the queue is dry.
		recv(id, side) {
			const c = conns.get(id);
			if (!c) return null;
			const q = c.q[side];
			if (q.length > 0) return q.shift();
			return null;
		},

		// eof reports "peer closed and nothing left to read".
		eof(id, side) {
			const c = conns.get(id);
			if (!c) return true;
			return c.closed[peer(side)] && c.q[side].length === 0;
		},

		// onReadable registers a ONE-SHOT wakeup for when side has data (or
		// EOF). Fires immediately (async) if already readable.
		onReadable(id, side, cb) {
			const c = conns.get(id);
			if (!c) { queueMicrotask(cb); return; }
			if (c.q[side].length > 0 || c.closed[peer(side)]) { queueMicrotask(cb); return; }
			c.wake[side] = cb;
		},

		// close shuts this side; the peer reads EOF after draining. When both
		// sides are closed the conn is dropped.
		close(id, side) {
			const c = conns.get(id);
			if (!c) return;
			c.closed[side] = true;
			wake(c, peer(side));
			if (c.closed.a && c.closed.b) conns.delete(id);
		},

		// httpFetch performs ONE HTTP request against a virtual-loopback port
		// and resolves {status, body:Uint8Array, headers:{lowercased:value}} —
		// a fetch-like result shape, so a page-side virtual browser can
		// treat http://127.0.0.1:<port> as just another channel. Speaks
		// HTTP/1.0 with Connection: close (no chunked encoding — EOF delimits
		// the body), which Go's http.Server answers natively.
		httpFetch(port, method, path, body, headers) {
			return new Promise((resolve, reject) => {
				const id = this.dial(port);
				if (id < 0) { reject(new Error('connection refused: 127.0.0.1:' + port)); return; }
				let done = false;
				const timer = setTimeout(() => {
					if (done) return;
					done = true;
					this.close(id, 'a');
					reject(new Error('timeout: 127.0.0.1:' + port));
				}, 30000);
				const te = new TextEncoder();
				let req = (method || 'GET') + ' ' + (path || '/') + ' HTTP/1.0\r\nHost: 127.0.0.1:' + port + '\r\n';
				const h = headers || {};
				for (const k in h) { if (Object.prototype.hasOwnProperty.call(h, k)) req += k + ': ' + h[k] + '\r\n'; }
				let bodyBytes = null;
				if (body != null) {
					bodyBytes = (body instanceof Uint8Array) ? body : te.encode(String(body));
					req += 'Content-Length: ' + bodyBytes.length + '\r\n';
				}
				req += 'Connection: close\r\n\r\n';
				this.send(id, 'a', te.encode(req));
				if (bodyBytes && bodyBytes.length) this.send(id, 'a', bodyBytes);
				// Receive: parse the status line + headers as soon as they're
				// complete, then finish once Content-Length bytes of body have
				// arrived — servers that keep the connection alive (Go answers
				// even an HTTP/1.0 Connection: close request without closing
				// promptly) would hang an EOF-only reader. EOF remains the
				// fallback delimiter when Content-Length is absent.
				const chunks = [];
				let total = 0;
				let parsed = null; // {status, headers, bodyStart, contentLength}
				const concat = () => {
					const all = new Uint8Array(total);
					let off = 0;
					for (const c of chunks) { all.set(c, off); off += c.length; }
					return all;
				};
				const tryParseHead = (all) => {
					let sep = -1; // header/body split at CRLFCRLF
					for (let i = 0; i + 3 < all.length; i++) {
						if (all[i] === 13 && all[i + 1] === 10 && all[i + 2] === 13 && all[i + 3] === 10) { sep = i; break; }
					}
					if (sep < 0) return null;
					const head = new TextDecoder().decode(all.subarray(0, sep));
					const lines = head.split('\r\n');
					const status = parseInt(lines[0].split(' ')[1] || '0', 10) || 0;
					const hs = {};
					for (let i = 1; i < lines.length; i++) {
						const ci = lines[i].indexOf(':');
						if (ci > 0) hs[lines[i].slice(0, ci).trim().toLowerCase()] = lines[i].slice(ci + 1).trim();
					}
					const cl = /^\d+$/.test(hs['content-length'] || '') ? parseInt(hs['content-length'], 10) : -1;
					return { status: status, headers: hs, bodyStart: sep + 4, contentLength: cl };
				};
				const finish = (all) => {
					if (done) return;
					done = true;
					clearTimeout(timer);
					this.close(id, 'a');
					if (!all) all = concat();
					if (!parsed) parsed = tryParseHead(all);
					if (!parsed) { reject(new Error('malformed HTTP response from 127.0.0.1:' + port)); return; }
					let body = all.subarray(parsed.bodyStart);
					if (parsed.contentLength >= 0 && body.length > parsed.contentLength) body = body.subarray(0, parsed.contentLength);
					resolve({ status: parsed.status, body: body, headers: parsed.headers });
				};
				const pump = () => {
					if (done) return;
					for (;;) {
						const b = this.recv(id, 'a');
						if (b) {
							chunks.push(b);
							total += b.length;
							if (!parsed || parsed.contentLength >= 0) {
								const all = concat();
								if (!parsed) parsed = tryParseHead(all);
								if (parsed && parsed.contentLength >= 0 && total - parsed.bodyStart >= parsed.contentLength) { finish(all); return; }
							}
							continue;
						}
						if (this.eof(id, 'a')) { finish(null); return; }
						this.onReadable(id, 'a', pump);
						return;
					}
				};
				pump();
			});
		},
	};
})();
