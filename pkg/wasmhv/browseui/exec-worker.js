// pkg/wasmhv/browseui/exec-worker.js c3-vis-wasm
// The worker half of "skywire commands run OFF the main thread". Tail of the
// worker bundle (jsfs + the skywire seeding + vnet + proc + skywire-exec are
// concatenated ahead of it by browseui/embed.go, so this thread owns a whole
// bottle of its own), and the far end of exec-remote.js's protocol.
//
// A worker is the right home for a wasm visor and a poor home for a shell, so
// the split is: the page keeps the UI — the desk, the terminals, the nested
// browser, all of cmd/wasm-visor — and this thread keeps every Go runtime that
// is a skywire COMMAND. The page's own jsfs stays where it is, seeded and
// in-memory; this worker's jsfs is the one the visor writes and the one that
// holds the IndexedDB snapshot, so the identity in /opt/skywire survives a
// reload exactly as before, just from over here.
//
// The filesystems are therefore SEPARATE, and that is a real consequence, not
// an oversight: `cat /opt/skywire/skywire.json` typed at the desk's shell reads
// the page's tree, which the visor no longer writes to. Sharing one jsfs across
// the boundary is what bottle's fsbridge.js does, and it needs SharedArrayBuffer
// (Atomics.wait, forbidden on the main thread and required by a Go runtime's
// synchronous syscalls) — which needs cross-origin isolation, which the desk
// does not have and will not take on for this. Every skywire command runs HERE,
// so the tree the binary reads and the tree it writes are always the same one;
// only the shell's own builtins see a different one.
//
// Protocol (page ⇄ worker), page first:
//   → {t:'init', persistDB, wasmURL, wasmExecURL}   restore the FS, bind the module
//   ← {t:'ready', restored}
//   → {t:'spawn', id, args, env}                    run `skywire <args...>`
//   ← {t:'out'|'err', id, b}                        stdout/stderr chunks
//   ← {t:'exit', id, code} | {t:'fail', id, msg}
//   → {t:'kill', id}                                the instance's own interrupt
//   ← {t:'vlisten'|'vunlisten', port}               this thread's vnet claims
//   → {t:'vopen', cid, port} ⇄ {t:'vdata', cid, b} ⇄ {t:'vclose', cid}
//   ← {t:'log', level, line}                        console output
(function () {
	'use strict';
	// Page-side load of the bundle is a no-op: everything below is worker-only.
	if (typeof importScripts !== 'function' || typeof self === 'undefined' || typeof self.postMessage !== 'function') return;
	if (!globalThis.jsfs || !globalThis.proc || !globalThis.vnet || !globalThis.skywireExec) return;

	var v = globalThis.vnet;

	function post(m, transfer) {
		try { self.postMessage(m, transfer || []); } catch (e) { /* page gone */ }
	}

	// sendable hands a chunk's buffer over instead of copying it — only when
	// the view owns the whole buffer, or its siblings would travel with it.
	function sendable(b) {
		return (b && b.buffer && b.byteOffset === 0 && b.byteLength === b.buffer.byteLength)
			? [b.buffer] : [];
	}

	// ---- console forwarding ------------------------------------------------
	// The visor's JS-side errors (a failed WebSocket dial, a rejected fetch)
	// would otherwise land only in a worker inspector nobody has open. Mirror
	// them to the page so the desk's log window and a CDP probe see them, and
	// still call the real console for worker devtools.
	var real = { log: console.log, warn: console.warn, error: console.error };
	function forward(level, args) {
		try {
			var line = Array.prototype.map.call(args, function (a) {
				if (typeof a === 'string') return a;
				try { return JSON.stringify(a); } catch (e) { return String(a); }
			}).join(' ');
			post({ t: 'log', level: level, line: line });
		} catch (e) { /* never let logging break the program */ }
	}
	console.log = function () { forward('log', arguments); real.log.apply(console, arguments); };
	console.warn = function () { forward('warn', arguments); real.warn.apply(console, arguments); };
	console.error = function () { forward('error', arguments); real.error.apply(console, arguments); };

	// ---- vnet claim advertisement -----------------------------------------
	// A listener bound in this thread is invisible to the page, and the page is
	// where every desk consumer asks (vnet.httpFetch, the vnet service worker,
	// desk-boot's vnet.listening(3435) gate). So every claim made here is
	// announced, and exec-remote.js mirrors it onto the page's port table with
	// a forwarding handler.
	//
	// Wrapping the three mutators is exact rather than approximate: vnet.js
	// deletes from its port map in unlisten() and releaseOwner() and nowhere
	// else, so no poll is needed to notice a claim going away. releaseOwner is
	// the one that matters in practice — a wasm instance that exits cannot
	// unlisten itself, and proc calls it on every reap.
	var advertised = {};
	var rawListen = v.listen.bind(v);
	var rawUnlisten = v.unlisten.bind(v);
	var rawRelease = v.releaseOwner ? v.releaseOwner.bind(v) : null;

	v.listen = function (port, onconn, owner) {
		var ok = rawListen(port, onconn, owner);
		if (ok && !advertised[port]) { advertised[port] = true; post({ t: 'vlisten', port: port }); }
		return ok;
	};
	v.unlisten = function (port) {
		rawUnlisten(port);
		if (advertised[port]) { delete advertised[port]; post({ t: 'vunlisten', port: port }); }
	};
	if (rawRelease) {
		v.releaseOwner = function (owner) {
			var n = rawRelease(owner);
			// vnet has no port enumeration, so reconcile what we announced
			// against what is still bound.
			Object.keys(advertised).forEach(function (p) {
				var port = parseInt(p, 10);
				if (!v.listening(port)) { delete advertised[p]; post({ t: 'vunlisten', port: port }); }
			});
			return n;
		};
	}

	// ---- bridged conns -----------------------------------------------------
	// conns: bridge conn id -> this thread's vnet conn id. The worker is always
	// the DIALER (side 'a'): the page accepts, we dial the local listener.
	var conns = {};

	function pumpWorker(cid) {
		var id = conns[cid];
		if (id === undefined) return;
		for (;;) {
			var b = v.recv(id, 'a');
			if (b) { post({ t: 'vdata', cid: cid, b: b }, sendable(b)); continue; }
			// EOF: the listener closed its end, so close ours as well — vnet
			// drops a conn only when both sides are closed, and a half-closed
			// one is a map entry that never goes away.
			if (v.eof(id, 'a')) {
				post({ t: 'vclose', cid: cid });
				delete conns[cid];
				try { v.close(id, 'a'); } catch (e) { /* already gone */ }
				return;
			}
			v.onReadable(id, 'a', function () { pumpWorker(cid); });
			return;
		}
	}

	function openConn(cid, port) {
		var id = v.dial(port, 'exec-worker-bridge');
		if (id < 0) {
			// Refused: the claim was released between the page's accept and
			// this dial. Closing is the whole answer — the page's peer reads
			// EOF, which is what a connection to a dead listener should look
			// like.
			post({ t: 'vclose', cid: cid });
			return;
		}
		conns[cid] = id;
		pumpWorker(cid);
	}

	// ---- exec --------------------------------------------------------------
	// instances: exec id -> { interrupt }, the handle skywireExec hands over
	// synchronously so a page-side Ctrl+C reaches the running command's own
	// registered interrupt (pkg/cmdutil/signal_js.go) — a foreground visor
	// shuts down exactly as it would on SIGINT.
	var instances = {};
	// pendingKill: a Ctrl+C that beat its command's registration. The page hands
	// the interrupt to its caller SYNCHRONOUSLY (the contract
	// skywirecmd_js.go relies on), so a kill can be posted before the spawn it
	// belongs to has been processed over here — and dropping it would leave a
	// visor running that the operator has already stopped.
	var pendingKill = {};

	function interrupt(id) {
		var inst = instances[id];
		if (!inst || typeof inst.interrupt !== 'function') return false;
		try { inst.interrupt(); } catch (e) { /* already gone */ }
		return true;
	}

	function spawn(m) {
		var id = m.id;
		var hooks = {
			stdout: function (b) { post({ t: 'out', id: id, b: b }, sendable(b)); },
			stderr: function (b) { post({ t: 'err', id: id, b: b }, sendable(b)); },
			instance: function (inst) {
				instances[id] = inst;
				if (pendingKill[id]) { delete pendingKill[id]; interrupt(id); }
			},
		};
		if (m.env) hooks.env = m.env;
		try {
			globalThis.skywireExec(m.args || [], hooks).then(function (code) {
				delete instances[id]; delete pendingKill[id];
				post({ t: 'exit', id: id, code: code });
			}, function (e) {
				delete instances[id]; delete pendingKill[id];
				post({ t: 'fail', id: id, msg: (e && e.message) || String(e) });
			});
		} catch (e) {
			delete instances[id]; delete pendingKill[id];
			post({ t: 'fail', id: id, msg: (e && e.message) || String(e) });
		}
	}

	// ---- persistence -------------------------------------------------------
	// The same exclusion the desk has always used, moved to the side that now
	// owns the snapshot: identity and config persist, runtime stores do not. A
	// bbolt database snapshotted mid-write restores corrupt and hangs its
	// consumer on the next boot (observed: the hypervisor module stalling on a
	// restored users.db). Caches rebuild; keys don't.
	function excluded(p) {
		return /\.db$/.test(p) || p.indexOf('/opt/skywire/local/') === 0 || p === '/opt/skywire/local';
	}

	function init(m) {
		if (m.wasmURL) globalThis.skywireExec.wasmURL = m.wasmURL;
		if (m.wasmExecURL) globalThis.skywireExec.wasmExecURL = m.wasmExecURL;
		if (!m.persistDB) { post({ t: 'ready', restored: false }); return; }
		globalThis.jsfs.persist.enable(m.persistDB, { exclude: excluded }).then(function (p) {
			post({ t: 'ready', restored: !!(p && p.restored) });
		}, function (e) {
			// A denied or corrupt IndexedDB is not a reason to have no visor:
			// the filesystem stays in memory for this session.
			console.warn('exec-worker: persistence unavailable:', (e && e.message) || e);
			post({ t: 'ready', restored: false });
		});
	}

	self.onmessage = function (ev) {
		var m = ev.data || {};
		switch (m.t) {
		case 'init': init(m); return;
		case 'spawn': spawn(m); return;
		case 'kill':
			if (!interrupt(m.id)) pendingKill[m.id] = true;
			return;
		case 'vopen': openConn(m.cid, m.port); return;
		case 'vdata': {
			var idv = conns[m.cid];
			if (idv === undefined) return;
			if (!v.send(idv, 'a', m.b)) { post({ t: 'vclose', cid: m.cid }); delete conns[m.cid]; }
			return;
		}
		case 'vclose': {
			var idc = conns[m.cid];
			if (idc === undefined) return;
			delete conns[m.cid];
			try { v.close(idc, 'a'); } catch (e) { /* already gone */ }
			return;
		}
		default: return;
		}
	};
})();
