// pkg/wasmhv/browseui/vnet.js — a virtual loopback network for the page.
//
// The missing OS layer for running skywire in the browser like on Linux: on a
// host, `skywire visor` LISTENS on localhost ports (visor RPC :3435, the
// hypervisor UI :8000) and `skywire cli` / the browser DIAL them. Wasm
// instances have no shared network — Go's js runtime simulates loopback only
// WITHIN one instance — so this provides the between-instances piece: a
// page-global port table with in-memory duplex byte pipes. The Go side
// (pkg/vnet) adapts these to net.Listener / net.Conn, so the REAL skywire
// code paths — the visor's RPC server, the CLI's RPC dial, the hypervisor's
// HTTP server — work unmodified across instances. The nested browser can
// resolve http://127.0.0.1:<port> against the same table.
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
	};
})();
