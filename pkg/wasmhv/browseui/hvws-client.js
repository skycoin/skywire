// pkg/wasmhv/browseui/hvws-client.js c3-vis-wasm
// The page-side client for /ws — the hypervisor's own /api surface carried as
// a message transport (pkg/visor/hypervisor_ws.go).
//
// The endpoint takes a frame naming a method, a path and a body and replays it
// through the hypervisor's OWN chi router, so a response over this socket is
// byte-identical to the HTTP call it stands in for. That request-in /
// response-out shape is an exact impedance match for a virtual-loopback port,
// which is what hvws-vnet.js builds on top of this: with the two together, a
// desk page served by a NATIVE hypervisor reaches the HOST visor through the
// same vnet calls the in-tab wasm visor answers, and no panel has to know
// which kind of visor is behind them.
//
//	SkywireHVWS.probe()          -> Promise<boolean>   is there a /ws here?
//	SkywireHVWS.open(opts)       -> client
//	client.request(m, p, b, h)   -> Promise<{status, body:string, headers}>
//	client.state                 'connecting' | 'open' | 'closed'
//	client.onstate = fn(state)
//	client.close()
//
// Only ONE socket is needed per page: the server replays up to wsMaxInFlight
// frames concurrently, so requests are correlated by id rather than serialized
// or spread over several connections.
(function () {
	'use strict';
	if (globalThis.SkywireHVWS) return;

	var PATH = '/ws';
	// A replayed request still traverses /api, which carries a 30s
	// middleware.Timeout server-side; giving up a little sooner keeps a
	// timed-out call from outliving the response that is about to arrive.
	var REQ_TIMEOUT_MS = 25000;
	var BACKOFF_MIN_MS = 250;
	var BACKOFF_MAX_MS = 8000;
	// Frames written while the socket is down. Bounded: a socket that stays
	// down must not let a polling panel grow the page's heap without limit.
	var MAX_QUEUE = 64;

	function wsURL(path) {
		var l = globalThis.location;
		return (l.protocol === 'https:' ? 'wss://' : 'ws://') + l.host + (path || PATH);
	}

	// probe answers "is this page served by something that has a /ws?" in ONE
	// cheap request, without opening a socket to find out. The endpoint answers
	// a plain GET with 426 Upgrade Required (coder/websocket's rejection of a
	// non-upgrade request); an origin that has no such route answers 404, and a
	// static host — the docs site — answers 404 too. That is the whole
	// capability test behind the desk's host-vs-in-tab decision: no flag, no
	// build tag, no configuration.
	//
	// 426 specifically, not "anything but 404": with EnableAuth on, an
	// unauthenticated caller gets 401 from the same route, and a socket opened
	// on that answer would be closed again immediately. A page whose operator
	// is not logged in has no business claiming the visor's ports.
	function probe(path) {
		try {
			return fetch(path || PATH, { method: 'GET', cache: 'no-store', credentials: 'same-origin' })
				.then(function (r) { return r.status === 426; })
				.catch(function () { return false; });
		} catch (e) {
			return Promise.resolve(false);
		}
	}

	function Client(opts) {
		opts = opts || {};
		this.path = opts.path || PATH;
		this.state = 'closed';
		this.onstate = opts.onstate || function () {};
		this.ws = null;
		this.nextID = 1;
		this.pending = new Map();
		this.queue = [];
		this.backoff = BACKOFF_MIN_MS;
		this.retryTimer = null;
		this.stopped = false;
		this.connect();
	}

	Client.prototype.setState = function (s) {
		if (this.state === s) return;
		this.state = s;
		try { this.onstate(s); } catch (e) { console.error('hvws: state listener:', e); }
	};

	Client.prototype.connect = function () {
		if (this.stopped || this.ws) return;
		var self = this;
		var sock;
		try {
			sock = new WebSocket(wsURL(this.path));
		} catch (e) {
			this.retry();
			return;
		}
		sock.binaryType = 'arraybuffer';
		this.ws = sock;
		this.setState('connecting');

		sock.onopen = function () {
			if (self.ws !== sock) return;
			self.backoff = BACKOFF_MIN_MS;
			self.setState('open');
			var q = self.queue;
			self.queue = [];
			q.forEach(function (f) {
				try { sock.send(f); } catch (e) { console.warn('hvws: send:', e); }
			});
		};
		sock.onmessage = function (ev) { self.onFrame(ev.data); };
		sock.onerror = function () { /* onclose always follows; nothing to add */ };
		sock.onclose = function () {
			if (self.ws !== sock) return;
			self.ws = null;
			self.setState('closed');
			// Fail every in-flight request. A reconnected socket is a NEW
			// server-side connection with its own handler goroutines; a reply
			// to an id issued on the old one is never coming, so a caller left
			// waiting would hang until its own timeout for no reason.
			var p = self.pending;
			self.pending = new Map();
			p.forEach(function (e) {
				clearTimeout(e.timer);
				e.reject(new Error('/ws: connection closed'));
			});
			// Queued frames were rejected with the rest of `pending` above.
			self.queue = [];
			self.retry();
		};
	};

	Client.prototype.retry = function () {
		if (this.stopped || this.retryTimer) return;
		var self = this;
		// Jitter: several tabs of the same visor must not all reconnect on the
		// same tick after the visor restarts.
		var wait = this.backoff + Math.floor(Math.random() * (this.backoff / 2));
		this.backoff = Math.min(this.backoff * 2, BACKOFF_MAX_MS);
		this.retryTimer = setTimeout(function () {
			self.retryTimer = null;
			self.connect();
		}, wait);
	};

	Client.prototype.onFrame = function (data) {
		var m;
		try {
			m = JSON.parse(typeof data === 'string' ? data : new TextDecoder().decode(data));
		} catch (e) {
			return;
		}
		// "event" is reserved by the wire format for server push; there is no
		// consumer yet, and an unknown type must never be mistaken for a reply.
		if (!m || (m.type && m.type !== 'res')) return;
		var e = this.pending.get(m.id);
		// id 0 is the server's answer to a malformed envelope — it names no
		// request, so there is nothing to settle.
		if (!e) return;
		this.pending.delete(m.id);
		clearTimeout(e.timer);
		e.resolve({ status: m.status || 0, body: m.body || '', headers: m.headers || {} });
	};

	// request replays one HTTP call over the socket. Bodies are strings both
	// ways — the hypervisor API is JSON in and JSON out.
	Client.prototype.request = function (method, path, body, headers) {
		var self = this;
		return new Promise(function (resolve, reject) {
			if (self.stopped) { reject(new Error('/ws: client closed')); return; }
			var id = self.nextID++;
			var frame = JSON.stringify({
				type: 'req',
				id: id,
				method: (method || 'GET').toUpperCase(),
				path: path,
				body: (body == null ? null : String(body)),
				headers: headers || {},
			});
			var timer = setTimeout(function () {
				self.pending.delete(id);
				reject(new Error('/ws: timeout on ' + path));
			}, REQ_TIMEOUT_MS);
			self.pending.set(id, { resolve: resolve, reject: reject, timer: timer });

			if (self.ws && self.state === 'open') {
				try {
					self.ws.send(frame);
				} catch (e) {
					self.pending.delete(id);
					clearTimeout(timer);
					reject(e);
				}
				return;
			}
			if (self.queue.length >= MAX_QUEUE) {
				self.pending.delete(id);
				clearTimeout(timer);
				reject(new Error('/ws: not connected (queue full)'));
				return;
			}
			self.queue.push(frame);
		});
	};

	Client.prototype.close = function () {
		this.stopped = true;
		if (this.retryTimer) { clearTimeout(this.retryTimer); this.retryTimer = null; }
		var sock = this.ws;
		this.ws = null;
		if (sock) { try { sock.close(); } catch (e) { /* already gone */ } }
		var p = this.pending;
		this.pending = new Map();
		p.forEach(function (e) {
			clearTimeout(e.timer);
			e.reject(new Error('/ws: client closed'));
		});
		this.queue = [];
		this.setState('closed');
	};

	globalThis.SkywireHVWS = {
		probe: probe,
		url: wsURL,
		open: function (opts) { return new Client(opts); },
	};
})();
