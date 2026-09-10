// pkg/wasmhv/browseui/exec-remote.js c3-vis-wasm
// The page half of "skywire commands run OFF the main thread".
//
// WHY. The desk runs its visor as a skywireExec process — the root binary's
// Go/wasm module, `skywire autoconfig`, in a terminal. Until now that ran on
// the PAGE MAIN THREAD, and a Go runtime with a live visor in it never idles:
// profiled 2026-09-07 over 16.5 minutes, 95% of one-second samples were above
// 90% CPU, and the symbolized profile contained no application frame and no GC
// frame at all — only runtime.findRunnable / runtime.stealWork /
// runtime.nanotime1, the scheduler looking for work, finding none, and reading
// the clock through a JS crossing. Nothing in skywire can be optimized to fix
// that: the cost is the runtime's, and the only cure is to put it on a thread
// that is not drawing the UI. The retired legacy page refused to boot a visor
// in-page for exactly this reason; the desk regressed the property and this
// restores it.
//
// WHAT. install() starts ONE dedicated Worker (exec-worker.js, bundled with
// jsfs + vnet + proc + skywire-exec) and REPLACES globalThis.skywireExec with a
// same-contract shim that runs each command over there. Nothing above it
// changes: the desk host's shell still calls
// skywireExec(args, hooks) and still gets hooks.instance({interrupt})
// synchronously and a Promise<exitCode> back.
//
// NO SharedArrayBuffer, no COOP/COEP. bottle's own proc.spawnWorker needs
// cross-origin isolation because its child shares the page's jsfs through
// Atomics.wait; a visor does not need the page's filesystem, so the worker
// carries its own jsfs and owns the IndexedDB snapshot outright. This is plain
// postMessage.
//
// THE PART THAT IS NOT OBVIOUS: vnet does not cross a worker boundary. The
// desk is vnet-shaped end to end — panels call vnet.httpFetch(port, …), the
// service worker resolves /vnet/<port>/…, desk-boot gates on
// vnet.listening(3435) — and a visor inside a worker binds those ports in the
// WORKER's port table, which the page cannot see. A visor that looked healthy
// while nothing in the page could reach it is the exact silent failure to
// avoid. So the worker ADVERTISES every claim it makes, the page claims the
// same port locally, and each inbound conn is forwarded byte-for-byte over
// postMessage to a dial on the far side. Bytes, not HTTP: 3435 speaks Go's
// net/rpc gob stream and 4445 speaks SOCKS5, and neither survives an
// HTTP-shaped bridge (which is why hvws-vnet.js, whose backend really is
// request/response, refuses a non-HTTP dialer instead of bridging it).
//
//	SkywireExecWorker.install({url, persistDB, wasmURL, wasmExecURL})
//	  -> Promise<{restored, ports(), close()} | null>
//
// null means "this page cannot host one" (no Worker constructor, no vnet, the
// script 404s, the worker never reported ready) — every caller then keeps the
// in-page skywireExec it already had, unchanged.
(function () {
	'use strict';
	if (globalThis.SkywireExecWorker) return;

	// The stderr ring size skywire-exec.js asks proc for, mirrored here because
	// with the process off-thread there is no proc record on this side and
	// ctl-bridge.js + desk-boot.js both read one.
	var TAIL_BYTES = 16384;
	var ROUTERISH = /(router|route_setup|RouteGroup|routegroup|setupclient|rule|cascade|rsn)/i;

	// READY_MS: how long a worker gets to compile nothing at all and answer.
	// It only has to load its own bundle and open IndexedDB — the wasm module
	// is not touched until the first spawn — so this is generous.
	var READY_MS = 20000;

	function abs(u) {
		try { return new URL(u, globalThis.location.href).href; } catch (e) { return u; }
	}

	function install(opts) {
		opts = opts || {};
		var v = globalThis.vnet;
		// Capability probe, not a flag: a realm with no Worker constructor, or
		// no vnet to forward the visor's ports into, cannot host one.
		if (typeof Worker !== 'function' || !v || !globalThis.skywireExec) {
			return Promise.resolve(null);
		}
		var url = opts.url || 'skywire-worker.js';

		// HEAD first. `new Worker()` on a 404 fails asynchronously through an
		// error event, which would leave the boot waiting out READY_MS for a
		// worker that was never served (the docs playground before its assets
		// are staged, any deployment that ships an older bundle).
		return fetch(url, { method: 'HEAD' }).then(function (r) {
			if (!r.ok) return null;
			return start(url, opts, v);
		}).catch(function () { return null; });
	}

	function start(url, opts, v) {
		var w;
		try { w = new Worker(url); } catch (e) { return Promise.resolve(null); }

		// ---- exec bookkeeping ------------------------------------------
		// live: exec id -> { out, err, resolve, reject, rec }
		var live = {};
		var seq = 0;
		// tails is published as globalThis.__skywireExecTails, the same
		// registry name (and record shape) skywire-exec.js aliases proc.tails
		// to: { argv, tail, filtered, exitInfo:{code,crashed} }. desk-boot.js
		// reads it to tell a CRASHED visor from one the operator stopped, and
		// ctl-bridge.js mirrors the ring to /ctl/log. The records are built
		// here from the stderr stream we already receive, because proc's own
		// ring now lives in the worker where neither consumer can see it.
		var tails = {};

		// ---- vnet bridging ---------------------------------------------
		// claimed: ports this bridge holds on the PAGE's port table.
		var claimed = {};
		// conns: bridge conn id -> { id: page-side vnet conn id, port }.
		var conns = {};
		var connSeq = 0;
		var dec = new TextDecoder();

		function post(m, transfer) {
			try { w.postMessage(m, transfer || []); } catch (e) { /* worker gone */ }
		}

		// sendable decides whether a chunk's buffer can be handed over rather
		// than copied. Only when the view owns the whole buffer — a subarray
		// would take its siblings with it.
		function sendable(b) {
			return (b && b.buffer && b.byteOffset === 0 && b.byteLength === b.buffer.byteLength)
				? [b.buffer] : [];
		}

		// pumpPage drains the page side of a bridged conn toward the worker.
		// Side 'b': the page is the ACCEPTER here (the visor's listener lives
		// in the worker, so on this side the page only ever accepts).
		function pumpPage(cid) {
			var c = conns[cid];
			if (!c) return;
			for (;;) {
				var b = v.recv(c.id, 'b');
				if (b) {
					// recv shifts the queue's own copy out — nothing else holds
					// it, so its buffer travels instead of being copied again.
					post({ t: 'vdata', cid: cid, b: b }, sendable(b));
					continue;
				}
				// EOF here means the page-side DIALER closed, so nothing is
				// reading this pipe any more: shut our end too. vnet drops a
				// conn only once both sides are closed, and a half-closed one
				// left behind is a permanent entry in the page's conn map —
				// one per request through /vnet/<port>/, which the dashboard
				// makes continuously.
				if (v.eof(c.id, 'b')) { post({ t: 'vclose', cid: cid }); dropConn(cid, true); return; }
				v.onReadable(c.id, 'b', function () { pumpPage(cid); });
				return;
			}
		}

		function dropConn(cid, closeLocal) {
			var c = conns[cid];
			if (!c) return;
			delete conns[cid];
			if (closeLocal) { try { v.close(c.id, 'b'); } catch (e) { /* already gone */ } }
		}

		function onAccept(port, id) {
			var cid = ++connSeq;
			conns[cid] = { id: id, port: port };
			post({ t: 'vopen', cid: cid, port: port });
			pumpPage(cid);
		}

		function claim(port) {
			if (claimed[port]) return;
			// Never evict: a host bridge (hvws-vnet.js) or an in-page server
			// that already holds the port keeps it, and the worker's listener
			// is simply unreachable from this page — which is visible rather
			// than silent, because vnet.listening() still answers for whoever
			// does hold it.
			var ok = v.listen(port, function (id) { onAccept(port, id); }, 'skywire-exec-worker');
			if (!ok) {
				console.warn('[exec-worker] vnet port ' + port + ' is already claimed in this page — the worker\'s listener is not reachable');
				return;
			}
			claimed[port] = true;
		}

		function release(port) {
			if (!claimed[port]) return;
			delete claimed[port];
			try { v.unlisten(port); } catch (e) { /* ignore */ }
			// Close the conns that were riding that port — and only those. The
			// far side is gone; their peers must read EOF rather than block on
			// a listener that will never answer again. Conns on the ports the
			// worker still holds are untouched.
			Object.keys(conns).forEach(function (cid) {
				if (conns[cid] && conns[cid].port === port) dropConn(cid, true);
			});
		}

		function releaseAll() {
			Object.keys(claimed).forEach(function (p) { release(parseInt(p, 10)); });
		}

		// ---- the stderr ring, rebuilt page-side ------------------------
		function tailPush(rec, s) {
			rec.tail = (rec.tail + s).slice(-TAIL_BYTES);
			rec._line = (rec._line + s).slice(-TAIL_BYTES);
			var nl;
			while ((nl = rec._line.indexOf('\n')) >= 0) {
				var line = rec._line.slice(0, nl);
				rec._line = rec._line.slice(nl + 1);
				if (ROUTERISH.test(line)) rec.filtered = (rec.filtered + line + '\n').slice(-TAIL_BYTES);
			}
		}

		function finish(id, code, err) {
			var e = live[id];
			if (!e) return;
			delete live[id];
			if (e.rec && !e.rec.exitInfo) e.rec.exitInfo = { code: err ? (code || 1) : code, crashed: !!err };
			if (err) {
				console.error('[exec-worker ' + id + '] ' + err +
					(e.rec && e.rec.tail ? '\n--- last stderr ---\n' + e.rec.tail : ''));
				e.reject(new Error(err));
				return;
			}
			if (code !== 0) {
				console.error('[exec-worker ' + id + '] exited code ' + code +
					(e.rec && e.rec.tail ? '\n--- last stderr ---\n' + e.rec.tail : ''));
			}
			e.resolve(code);
		}

		// ---- the replacement skywireExec -------------------------------
		function remoteExec(args, hooks) {
			hooks = hooks || {};
			var id = 'w' + (++seq);
			var argv = ['skywire'].concat(args);
			var rec = { argv: argv.slice(), tail: '', filtered: '', exitInfo: null, _line: '' };
			tails[id] = rec;
			var p = new Promise(function (res, rej) {
				live[id] = { out: hooks.stdout || null, err: hooks.stderr || null, resolve: res, reject: rej, rec: rec };
			});
			// The interrupt is handed over SYNCHRONOUSLY, before the command
			// starts — the contract cmd/wasm-visor/skywirecmd_js.go relies on
			// for Ctrl+C. Here it posts a kill the worker turns into the same
			// registered interrupt the in-page path would have invoked.
			if (typeof hooks.instance === 'function') {
				hooks.instance({ interrupt: function () { post({ t: 'kill', id: id }); return true; } });
			}
			post({ t: 'spawn', id: id, args: args.slice(), env: hooks.env || null });
			return p;
		}
		remoteExec.wasmURL = globalThis.skywireExec.wasmURL;
		remoteExec.wasmExecURL = globalThis.skywireExec.wasmExecURL;
		// The module lives on the worker's side now, but the question callers
		// ask ("is there a skywire command on this page at all?") is still
		// answered by whether the module is served.
		remoteExec.available = function () {
			return fetch(remoteExec.wasmURL, { method: 'HEAD' })
				.then(function (r) { return r.ok; }).catch(function () { return false; });
		};
		remoteExec.inWorker = true;

		// ---- message pump ----------------------------------------------
		var readyRes = null;
		var settled = false;

		w.onmessage = function (ev) {
			var m = ev.data || {};
			switch (m.t) {
			case 'ready':
				if (settled) return;
				settled = true;
				readyRes({ restored: !!m.restored });
				return;
			case 'out': {
				var eo = live[m.id];
				if (eo && eo.out) { try { eo.out(m.b); } catch (e) { /* sink gone */ } }
				return;
			}
			case 'err': {
				var ee = live[m.id];
				if (ee) {
					if (ee.rec) { try { tailPush(ee.rec, dec.decode(m.b, { stream: true })); } catch (e) { /* ignore */ } }
					if (ee.err) { try { ee.err(m.b); } catch (e) { /* sink gone */ } }
				}
				return;
			}
			case 'exit': finish(m.id, m.code, null); return;
			case 'fail': finish(m.id, 1, m.msg || 'exec failed'); return;
			case 'vlisten': claim(m.port); return;
			case 'vunlisten': release(m.port); return;
			case 'vdata': {
				var cv = conns[m.cid];
				if (!cv) return;
				// send() false means the page-side peer hung up; tell the
				// worker so its dialer sees a closed pipe instead of writing
				// into a conn nobody reads.
				if (!v.send(cv.id, 'b', m.b)) { post({ t: 'vclose', cid: m.cid }); dropConn(m.cid, true); }
				return;
			}
			case 'vclose': dropConn(m.cid, true); return;
			case 'log':
				// Worker console output, mirrored so a desk page's own log
				// window and a CDP probe see the visor's JS-side errors.
				try { (console[m.level] || console.log).call(console, '[worker]', m.line); } catch (e) { /* ignore */ }
				return;
			default: return;
			}
		};

		w.onerror = function (e) {
			var msg = (e && e.message) || 'worker error';
			console.error('[exec-worker]', msg);
			if (!settled) { settled = true; readyRes(null); return; }
			// After ready, a worker-level error is fatal to everything running
			// in it: fail the live commands rather than leaving their promises
			// pending forever, and drop the port claims so vnet.listening()
			// stops asserting a visor that is gone.
			Object.keys(live).forEach(function (id) { finish(id, 1, msg); });
			releaseAll();
		};

		var ready = new Promise(function (res) { readyRes = res; });
		post({
			t: 'init',
			persistDB: opts.persistDB || '',
			wasmURL: abs(opts.wasmURL || globalThis.skywireExec.wasmURL),
			wasmExecURL: abs(opts.wasmExecURL || globalThis.skywireExec.wasmExecURL),
		});
		setTimeout(function () {
			if (settled) return;
			settled = true;
			readyRes(null);
		}, READY_MS);

		return ready.then(function (r) {
			if (!r) {
				console.warn('[exec-worker] worker did not come up — commands stay on the page main thread');
				try { w.terminate(); } catch (e) { /* ignore */ }
				return null;
			}
			remoteExec.wasmURL = abs(opts.wasmURL || globalThis.skywireExec.wasmURL);
			remoteExec.wasmExecURL = abs(opts.wasmExecURL || globalThis.skywireExec.wasmExecURL);
			globalThis.skywireExec = remoteExec;
			// The registries under the names skywire's page code already uses.
			// __skywireSignals is deliberately NOT re-aliased: the interrupt
			// registry that matters is the worker's (that is where
			// pkg/cmdutil/signal_js.go registers), and kill() reaches it
			// through the shim's interrupt above.
			globalThis.__skywireExecTails = tails;
			// A page going away should not leave a worker holding a dmsg
			// session and a registered identity for the browser to reap
			// whenever it feels like it.
			addEventListener('pagehide', function () { try { w.terminate(); } catch (e) { /* ignore */ } });
			return {
				restored: r.restored,
				worker: w,
				ports: function () { return Object.keys(claimed).map(Number); },
				close: function () { releaseAll(); try { w.terminate(); } catch (e) { /* ignore */ } },
			};
		});
	}

	globalThis.SkywireExecWorker = { install: install };
})();
