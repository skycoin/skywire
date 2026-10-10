// pkg/wasmhv/browseui/exec-worker.js c3-vis-wasm
// The worker half of "skywire commands run OFF the main thread". Tail of the
// worker bundle (jsfs + the skywire seeding + vnet + proc + skywire-exec are
// concatenated ahead of it by browseui/embed.go, so this thread owns a whole
// bottle of its own), and the far end of exec-remote.js's protocol.
//
// A worker is the right home for a wasm visor and a poor home for a shell, so
// the split is: the page keeps the UI — the desk, the terminals, the nested
// browser, the desk host — and this thread keeps every Go runtime that
// is a skywire COMMAND. The page's own jsfs stays where it is, seeded and
// in-memory; this worker's jsfs is the one the visor writes and the one that
// holds the IndexedDB snapshot, so the identity in /opt/skywire survives a
// reload exactly as before, just from over here.
//
// The two trees are SEPARATE. Sharing one jsfs across the boundary is what
// bottle's fsbridge.js does, and it needs SharedArrayBuffer (Atomics.wait,
// forbidden on the main thread and required by a Go runtime's synchronous
// syscalls), which needs cross-origin isolation the desk does not have. What
// each side owns is lent to the other instead (fs-tree.js): the page mounts
// this thread's /opt/skywire and /mnt, and this thread mounts the page's /home,
// where the desk shell keeps its files. A command starts in the shell's cwd.
//
// Protocol (page ⇄ worker), page first:
//   → {t:'init', persistDB, wasmURL, wasmExecURL}   restore the FS, bind the module
//   ← {t:'ready', restored}
//   → {t:'spawn', id, args, env, cwd}               run `skywire <args...>`
//   ← {t:'out'|'err', id, b}                        stdout/stderr chunks
//   ← {t:'exit', id, code} | {t:'fail', id, msg}
//   → {t:'kill', id}                                the instance's own interrupt
//   ← {t:'vlisten'|'vunlisten', port}               this thread's vnet claims
//   → {t:'vopen', cid, port} ⇄ {t:'vdata', cid, b} ⇄ {t:'vclose', cid}
//   ← {t:'log', level, line}                        console output
//   ⇄ {t:'fs', id, op, args} ⇄ {t:'fsr', id, err, res}   a call on the other's tree
(function () {
	'use strict';
	// Page-side load of the bundle is a no-op: everything below is worker-only.
	if (typeof importScripts !== 'function' || typeof self === 'undefined' || typeof self.postMessage !== 'function') return;
	if (!globalThis.jsfs || !globalThis.proc || !globalThis.vnet || !globalThis.skywireExec || !globalThis.SkywireFSTree) return;

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
		if (m.cwd) hooks.cwd = m.cwd;
		if (m.tty) {
			hooks.stdin = 'pipe';
			hooks.tty = { cols: m.tty.cols, rows: m.tty.rows, onRaw: function (on) { post({ t: 'raw', id: id, on: !!on }); } };
		}
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
		PAGE_TREES.forEach(tree.mount);
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

	// ---- WebRTC through the page -------------------------------------------
	// A Web Worker has no RTCPeerConnection, so the visor here could not make
	// a webrtc transport at all ("unknown network type"). __skywireRTC.newPC
	// returns a proxy peer connection whose methods and events travel to a real
	// one on the page (exec-remote.js); the Go carrier
	// (pkg/transport/network/webrtc_browser.go) uses it as it would the real
	// thing. Installed before any command runs, so the transport layer's
	// capability probe sees it.
	var rtcPcSeq = 1, rtcDcSeq = 1, rtcCallSeq = 1;
	var rtcPCs = {};   // pcId -> { obj, dcs: { dcId -> proxy channel } }
	var rtcCalls = {}; // callId -> { resolve, reject }
	function rtcPcCall(pcId, method, arg) {
		return new Promise(function (resolve, reject) {
			var callId = rtcCallSeq++;
			rtcCalls[callId] = { resolve: resolve, reject: reject };
			post({ t: 'rtc', op: 'pcCall', pcId: pcId, callId: callId, method: method, arg: arg });
		});
	}
	function rtcMakeDC(pcId, dcId) {
		return {
			binaryType: 'arraybuffer', readyState: 'connecting', bufferedHigh: false, onbufferedamountlow: null,
			onopen: null, onmessage: null, onclose: null, onerror: null,
			send: function (data) {
				var buf = data;
				if (ArrayBuffer.isView(data)) { buf = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength); }
				post({ t: 'rtc', op: 'dcSend', pcId: pcId, dcId: dcId, data: buf }, (buf instanceof ArrayBuffer) ? [buf] : []);
			},
			close: function () { post({ t: 'rtc', op: 'dcClose', pcId: pcId, dcId: dcId }); }
		};
	}
	self.__skywireRTC = {
		newPC: function (iceServers) {
			var pcId = rtcPcSeq++;
			var rec = { dcs: {} };
			var pc = {
				onicecandidate: null, ondatachannel: null,
				createDataChannel: function (label, opts) {
					var dcId = rtcDcSeq++;
					var dc = rtcMakeDC(pcId, dcId);
					rec.dcs[dcId] = dc;
					post({ t: 'rtc', op: 'createDC', pcId: pcId, dcId: dcId, label: label, opts: opts });
					return dc;
				},
				createOffer: function () { return rtcPcCall(pcId, 'createOffer', null); },
				createAnswer: function () { return rtcPcCall(pcId, 'createAnswer', null); },
				setLocalDescription: function (d) { return rtcPcCall(pcId, 'setLocalDescription', d); },
				setRemoteDescription: function (d) { return rtcPcCall(pcId, 'setRemoteDescription', d); },
				addIceCandidate: function (c) { return rtcPcCall(pcId, 'addIceCandidate', c); },
				close: function () { post({ t: 'rtc', op: 'pcClose', pcId: pcId }); }
			};
			rec.obj = pc;
			rtcPCs[pcId] = rec;
			var ice = [];
			try { for (var i = 0; iceServers && i < iceServers.length; i++) { ice.push({ urls: iceServers[i].urls }); } } catch (e) { /* none */ }
			post({ t: 'rtc', op: 'newPC', pcId: pcId, iceServers: ice });
			return pc;
		}
	};
	// rtcEvent applies an event from the page's real peer connection to its
	// proxy, calling the handler Go set on it.
	function rtcEvent(m) {
		var rec = rtcPCs[m.pcId], dc;
		switch (m.op) {
		case 'ret': {
			var c = rtcCalls[m.callId];
			if (c) { delete rtcCalls[m.callId]; if (m.ok) { c.resolve(m.val); } else { c.reject(new Error(m.msg || 'rtc failed')); } }
			return;
		}
		case 'icecandidate':
			if (rec && typeof rec.obj.onicecandidate === 'function') { rec.obj.onicecandidate({ candidate: m.candidate }); }
			return;
		case 'datachannel':
			if (rec) {
				dc = rtcMakeDC(m.pcId, m.dcId);
				rec.dcs[m.dcId] = dc;
				if (typeof rec.obj.ondatachannel === 'function') { rec.obj.ondatachannel({ channel: dc }); }
			}
			return;
		case 'dcOpen': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.readyState = 'open'; if (typeof dc.onopen === 'function') { dc.onopen({}); } } return;
		case 'dcMessage': dc = rec && rec.dcs[m.dcId]; if (dc && typeof dc.onmessage === 'function') { dc.onmessage({ data: m.data }); } return;
		case 'dcClose': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.readyState = 'closed'; if (typeof dc.onclose === 'function') { dc.onclose({}); } } return;
		case 'dcError': dc = rec && rec.dcs[m.dcId]; if (dc && typeof dc.onerror === 'function') { dc.onerror({}); } return;
		// The page's channel queue passed its high-water mark, or drained below the
		// low one; Write waits in between (webrtc_browser.go).
		case 'dcHigh': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.bufferedHigh = true; } return;
		case 'dcLow': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.bufferedHigh = false; if (typeof dc.onbufferedamountlow === 'function') { dc.onbufferedamountlow({}); } } return;
		case 'pcGone': delete rtcPCs[m.pcId]; return;
		}
	}

	// tree lends this thread's /opt/skywire and /mnt to the page and mounts
	// the page's /home here, so a command sees the files the desk shell sees.
	var PAGE_TREES = ['/home'];
	var tree = globalThis.SkywireFSTree(post);

	self.onmessage = function (ev) {
		var m = ev.data || {};
		switch (m.t) {
		case 'init': init(m); return;
		case 'fs': tree.call(m); return;
		case 'fsr': tree.answer(m); return;
		case 'spawn': spawn(m); return;
		case 'stdin': {
			var si = instances[m.id];
			if (si && si.stdin) { try { si.stdin.write(m.b); } catch (e) { /* gone */ } }
			return;
		}
		case 'stdinclose': {
			var sc = instances[m.id];
			if (sc && sc.stdin) { try { sc.stdin.close(); } catch (e) { /* gone */ } }
			return;
		}
		case 'resize': {
			var rz = instances[m.id];
			if (rz && typeof rz.resize === 'function') { try { rz.resize(m.c, m.r); } catch (e) { /* gone */ } }
			return;
		}
		case 'kill':
			if (!interrupt(m.id)) pendingKill[m.id] = true;
			return;
		case 'rtc': rtcEvent(m); return;
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
