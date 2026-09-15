// pkg/wasmhv/browseui/desk-boot.js c3-vis-wasm
// The shared desk boot: one parameterized entry point behind both desk-first
// pages — the docs-site playground (no visor by default) and the converged
// wasm-visor page (visor terminal + hypervisor UI window). Keeping the boot
// in ONE file keeps the two pages from drifting.
//
//   skywireDeskBoot(opts) -> Promise<{panel, startedVisor}>
//
// opts (all optional):
//   persistDB       IndexedDB name for the jsfs snapshot ('skywire-desk')
//   wasmURL         the skywire command module for skywireExec — also the
//                   desk host, run out of it as `skywire desk-host`
//   wasmExecURL     Go's wasm_exec.js for command instances
//   autostartVisor  open a visor terminal running `skywire autoconfig` —
//                   unless the operator STOPPED the visor before the last
//                   reload (the session remembers; the terminal still opens,
//                   idle at the prompt, exactly as it was left)
//   helpTerminal    open a terminal that has just run `skywire --help` (default true)
//   hvWindow        once the visor's hypervisor UI listens on the virtual
//                   loopback, open a browser window on it, maximized on top
//   hvPort          hypervisor UI port on the virtual loopback (8001)
//   docsPort        port for `skywire doc serve` on the virtual loopback
//                   (8002). The desk starts it and opens it as a browser tab,
//                   so the CLI reference and the prose are readable BESIDE a
//                   terminal you can run the documented commands in. 0 = off.
//   execWorkerURL   the worker bundle that hosts the skywire commands
//                   ('skywire-worker.js'). Where it is served, every command —
//                   the visor above all — runs on that thread instead of this
//                   one; where it is not, the commands stay in-page exactly as
//                   they were. Nothing selects between the two but whether the
//                   asset answers.
//   dashboardURL    the page the browser window opens on. A hypervisor that
//                   serves this desk names its own same-origin page here;
//                   the default is the in-tab visor's UI on vnet:<hvPort>.
//   terminalURL     the host's pty page (xterm over a websocket). When set,
//                   the terminal app is that page and it opens at boot.
//   onStatus(msg)   boot progress for the page's overlay
(function () {
	'use strict';
	if (globalThis.skywireDeskBoot) return;

	var SESSION_KEY = 'skywire-desk-session';

	function waitFor(fn, what, tries) {
		return new Promise(function (res, rej) {
			var n = 0;
			(function poll() {
				var v = fn();
				if (v) { res(v); return; }
				if (++n > (tries || 600)) { rej(new Error('timed out waiting for ' + what)); return; }
				setTimeout(poll, 50);
			})();
		});
	}

	function loadSession() {
		try { return JSON.parse(localStorage.getItem(SESSION_KEY) || 'null'); } catch (e) { return null; }
	}
	// visorSawUp: whether THIS page ever observed its visor listening. The
	// session records "stopped" ONLY after up-then-down — a save that fires
	// while the visor is still booting (visibilitychange the moment another
	// tab covers the page) must not masquerade as an operator stop, or every
	// later load skips the autostart forever.
	var visorSawUp = false;
	// visorCrashed: whether the visor's exec instance ended ABNORMALLY (panic
	// or nonzero exit). A crash is not an operator stop — recording it as one
	// suppressed the autostart on every later load, so an overnight panic
	// left the desk permanently idle (observed live). Only a clean exit
	// (code 0, no crash) counts as "the operator stopped it".
	function visorCrashed() {
		try {
			// proc.tails, aliased by skywire-exec.js. Records are plain
			// {argv, tail, filtered, exitInfo}; argv is the FULL argv, so
			// argv[0] is "skywire" and the subcommand is argv[1].
			var reg = globalThis.__skywireExecTails || {};
			var crashed = false;
			Object.keys(reg).forEach(function (k) {
				var a = (reg[k].argv || []).join(' ');
				var xi = reg[k].exitInfo;
				if (/autoconfig/.test(a) && xi && (xi.crashed || xi.code !== 0)) { crashed = true; }
			});
			return crashed;
		} catch (e) { return false; }
	}
	// visorStoppedByOperator: the MOST RECENT autoconfig instance ended
	// cleanly (code 0, no crash). That is the operator stopping the visor, and
	// the liveness watchdog must leave it stopped. A crash, or no record at
	// all, is not a stop — an exec worker that went away takes its registry
	// with it, and treating that silence as "the operator meant this" is what
	// would keep a dead desk dead.
	function visorStoppedByOperator() {
		try {
			var reg = globalThis.__skywireExecTails || {};
			var best = -1, bestClean = false;
			Object.keys(reg).forEach(function (k) {
				var rec = reg[k];
				if (!/autoconfig/.test((rec.argv || []).join(' '))) { return; }
				// Keys are w1, w2, … wN — compare the number, not the string,
				// or w10 sorts before w2 and an old record wins.
				var n = parseInt(String(k).replace(/^\D+/, ''), 10);
				if (!isFinite(n) || n < best) { return; }
				best = n;
				bestClean = !!(rec.exitInfo && !rec.exitInfo.crashed && rec.exitInfo.code === 0);
			});
			return bestClean;
		} catch (e) { return false; }
	}
	// notifyReload: the same bargain autoupdate.js offers — say what is about
	// to happen, count down, and let the operator decline. Used where the desk
	// wants a RELOAD rather than an in-place restart: a reload is the only way
	// to pick up a new wasm module (the exec worker is created once per page
	// load and dies with the page), so a silent one would swap the code under
	// someone mid-task, and a silent refusal would strand them on a build that
	// cannot work.
	//
	// Declining is per-prompt, not a global setting: this is not the
	// autoupdate toggle and must not disable it. The caller re-arms on its own
	// schedule if the condition persists.
	var reloadNotice = null;
	function notifyReload(title, detail, secs, onDecline) {
		if (reloadNotice) { return; } // one at a time — never stack these
		secs = secs || 10;
		var box = document.createElement('div');
		reloadNotice = box;
		box.style.cssText = 'position:fixed;z-index:2147483647;right:16px;bottom:16px;max-width:340px;' +
			'background:#2b2540;color:#eee;font:13px/1.4 system-ui,sans-serif;padding:12px 14px;' +
			'border:1px solid #6c5ce7;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.4)';
		var cd = document.createElement('span');
		cd.textContent = String(secs);
		var head = document.createElement('b');
		head.textContent = title;
		var body = document.createElement('div');
		body.style.margin = '6px 0 8px';
		body.textContent = detail;
		var line = document.createElement('div');
		line.appendChild(document.createTextNode('Reloading in '));
		line.appendChild(cd);
		line.appendChild(document.createTextNode('s…'));
		var now = document.createElement('button');
		now.textContent = 'Reload now';
		now.style.marginRight = '8px';
		var not = document.createElement('button');
		not.textContent = 'Not now';
		var btns = document.createElement('div');
		btns.style.marginTop = '8px';
		btns.appendChild(now);
		btns.appendChild(not);
		box.appendChild(head);
		box.appendChild(body);
		box.appendChild(line);
		box.appendChild(btns);
		try { document.body.appendChild(box); } catch (e) { reloadNotice = null; return; }
		function close() { try { box.remove(); } catch (e) {} reloadNotice = null; }
		function go() { clearInterval(t); close(); setTimeout(function () { location.reload(); }, 200); }
		var t = setInterval(function () {
			secs -= 1;
			cd.textContent = String(secs);
			if (secs <= 0) { go(); }
		}, 1000);
		now.onclick = go;
		not.onclick = function () {
			clearInterval(t); close();
			console.log('[desk] reload declined: ' + title);
			if (typeof onDecline === 'function') { try { onDecline(); } catch (e) {} }
		};
	}

	// servedVersionDiffers resolves true when the server is serving a wasm
	// module other than the one this page booted with. Same fingerprint
	// autoupdate.js polls; asked only at the moment it matters, so this adds
	// no steady-state traffic.
	function servedVersionDiffers() {
		var booted = globalThis.__SKYWIRE_WASM_VERSION__ || '';
		if (!booted) { return Promise.resolve(false); }
		return fetch('wasm-version', { cache: 'no-store' }).then(function (r) {
			return r.ok ? r.text() : null;
		}).then(function (latest) {
			return !!latest && latest.trim() !== booted;
		}).catch(function () { return false; });
	}
	// attachOrigin: the same-origin transport endpoint this tab's visor dials,
	// when it has one. Set during boot; empty for a standalone page with no
	// host behind it.
	var attachOrigin = '';
	// stopVerdict: WHY the visor stopped, decided the moment it goes down
	// rather than at teardown. 'operator' — it went down while its host was
	// still answering, so something in the tab stopped it on purpose.
	// 'environment' — the host was gone too, which is not a decision anybody
	// made and must not suppress the next autostart.
	//
	// saveSession used to form this verdict itself, at pagehide, from "down
	// after having been up". Those two causes are identical by then: a visor
	// the operator stopped and a visor whose host restarted underneath it both
	// exit 0. The observed failure was the second one being recorded as the
	// first — a host restart, then a reload, and the desk came back with the
	// autostart suppressed and the liveness watchdog standing down with it,
	// because the watchdog is gated on the same flag.
	var stopVerdict = null;
	// hostAnswers resolves true when the attach origin is still serving. Any
	// HTTP response counts, including the 426 a plain GET to a WebSocket route
	// returns — the question is whether the host is there, not what it said.
	// A page with no attach origin has no host to lose, so nothing to blame.
	function hostAnswers() {
		if (!attachOrigin) { return Promise.resolve(true); }
		return fetch(attachOrigin, { method: 'GET', cache: 'no-store' })
			.then(function () { return true; })
			.catch(function () { return false; });
	}
	// proxyIsServing reports whether THIS tab has a resolving proxy listening,
	// which is the precondition for status.skysocks.
	//
	// That page is not fetched from anywhere: proxyinterstitial answers it in
	// process as a reserved host, before the port gate and before the
	// exit-reachability check, so it renders even when the exit is down — but
	// only if a proxy is running to answer it. A desk that never started one
	// has nothing to serve the page, and opening the tab anyway left a dead
	// tab whose failure the caller's catch swallowed.
	//
	// 4445 is the dmsgweb resolver, the same port the mesh-browse path checks.
	function proxyIsServing() {
		try {
			return !!(globalThis.vnet && globalThis.vnet.listening(4445));
		} catch (e) {
			return false;
		}
	}
	// openPairingDocsIfUnpaired: the desk a VISOR serves (the :8000 case, where
	// this page stands in for the host's native hypervisor UI) is not the same
	// visor as the host. The tab runs its own, and until the operator approves
	// that key on the machine the tab cannot drive it — the transport is up,
	// the dashboard renders, and every RPC the tab makes is refused. Nothing on
	// the desk said so, and the ☰ pair window only tells you once you know to
	// look for it.
	//
	// So open the prose that spells the procedure out, at its section, as an
	// ordinary tab beside the dashboard. Only when there IS a host to pair with
	// (__SKYWIRE_LOCAL_PK__ is what a visor-served page injects), only when the
	// host says this tab is not paired, and only when the docs are being
	// served — a page with no docs port has nothing to open.
	//
	// /pair/status is pre-auth by design: a tab that has no session yet still
	// has to be able to learn its own standing.
	function openPairingDocsIfUnpaired(panel, win, docsPort) {
		if (!docsPort || !win || !win.openTab) { return; }
		if (typeof globalThis.__SKYWIRE_LOCAL_PK__ !== 'string') { return; }
		if (!panel || typeof panel.exec !== 'function') { return; }
		// Everything here waits for something that is not ready when the tabs
		// open. On a visor-served desk the tab block does not wait for the
		// in-tab visor at all — dashboardURL is set, so it fires at once — and
		// the first version of this asked the visor for its key right then, got
		// "RPC connection failed", failed the key check and gave up for good.
		// Nothing retried, so the pairing tab never opened on the one surface
		// it exists for.
		function waitFor(ready, then, tries) {
			if (ready()) { then(); return; }
			if (tries > 0) { setTimeout(function () { waitFor(ready, then, tries - 1); }, 1000); }
		}
		var rpcUp = function () { return !!(globalThis.vnet && globalThis.vnet.listening(3435)); };
		var docsUp = function () { return !!(globalThis.vnet && globalThis.vnet.listening(docsPort)); };
		waitFor(rpcUp, function () {
			Promise.resolve(panel.exec('skywire cli visor pk'))
				.then(function (r) {
					var pk = ((r && r.out) || '').trim();
					if (!/^[0-9a-f]{66}$/.test(pk)) { return null; }
					// /pair/status is pre-auth by design: a tab with no session
					// still has to be able to learn its own standing.
					return fetch(location.origin + '/pair/status?pk=' + pk, { cache: 'no-store' })
						.then(function (resp) { return resp.ok ? resp.json() : null; });
				})
				.then(function (st) {
					if (!st || st.paired) { return; }
					// The docs server binds in seconds but not instantly, and a
					// tab opened before it listens is a dead tab nobody reloads.
					waitFor(docsUp, function () {
						try { win.openTab('vnet:' + docsPort, '/prose/guides/hypervisor.md#pairing-a-desk-tab', 'http', true); } catch (e) {}
					}, 60);
				})
				.catch(function () { /* no pairing surface here — say nothing */ });
		}, 180);
	}
	function saveSession() {
		try {
			var up = !!(globalThis.vnet && globalThis.vnet.listening(3435));
			if (up) { visorSawUp = true; }
			if (!up && !visorSawUp) { return; } // still booting (or never started) — keep the previous verdict
			if (!up && visorCrashed()) { up = true; } // crashed ≠ stopped: restart on the next load
			// The host went away under it. Nobody decided that, so it must not read
			// as a decision — record it as running and let the next load start it.
			if (!up && stopVerdict === 'environment') { up = true; }
			localStorage.setItem(SESSION_KEY, JSON.stringify({
				visorRunning: up,
				at: Date.now(),
			}));
		} catch (e) { /* storage denied — session just resets to defaults */ }
	}

	// framedWithoutEmbed: this desk page is running INSIDE a frame and its URL
	// carries no embed marker — which can only mean a server served the desk
	// where it meant to serve the dashboard.
	//
	// Both servers decide by `Sec-Fetch-Dest: iframe` OR `?embed=1`, and through
	// bottle's vnet service worker only the second survives: Sec-Fetch-* are
	// forbidden header names, unreadable from a service worker, so vnet-sw.js
	// cannot forward them however much it would like to. Any in-frame
	// navigation that drops the query — the Angular router rewrites the frame's
	// URL to its current route within seconds of loading — therefore comes back
	// as a DESK, and a desk inside the desk's own dashboard window is never what
	// anyone wanted. The servers say so themselves, in the comment above each
	// `framed` test.
	//
	// So the page corrects its own address and lets the server answer again.
	// Bounded by construction: the retry carries embed=1, so the second load
	// cannot reach here.
	function framedWithoutEmbed() {
		try {
			if (self === top) return false;
			if (/(^|[?&])embed=1(&|$)/.test(location.search)) return false;
			return true;
		} catch (e) { return false; } // cross-origin top — not our frame to judge
	}

	globalThis.skywireDeskBoot = function (opts) {
		// Drop the pre-vault plaintext key an older build left behind. It does
		// not derive this visor's public key and nothing reads it — a dead
		// 32-byte secret sitting in storage only ever benefits whoever takes
		// the device. Cheap, unconditional, and independent of whether the
		// operator has turned on a passphrase.
		if (globalThis.SkywireIdentityVault) { globalThis.SkywireIdentityVault.dropLegacyKey(); }

		// Boot guard. The chain below waits on the desk host and the shell and
		// has no terminal catch, so a module that never installs them leaves the
		// page looking alive — taskbar up, dashboard aimed at vnet:8001 — with
		// no visor, no error and no recovery but a manual reload. That is the
		// state this was found in, and the liveness watchdog further down cannot
		// help: it lives inside this boot, so a boot that never finishes never
		// arms it.
		//
		// Armed on ATTEMPT, not on load: a page that merely includes this script
		// without calling in is left alone. One prompt only — if the operator
		// declines, the desk stays as it is rather than nagging.
		var BOOT_DEADLINE_MS = 60000; // waitFor gives up around 30s; leave room past it
		setTimeout(function () {
			if (globalThis.__skywireDesk) { return; }
			console.warn('skywire desk: no desk panel after ' + (BOOT_DEADLINE_MS / 1000) + 's');
			notifyReload('The desk did not finish starting',
				'Reloading fetches the module again and starts over.', 15);
		}, BOOT_DEADLINE_MS);
		opts = opts || {};
		if (framedWithoutEmbed()) {
			location.replace(location.pathname + '?embed=1' + (location.hash || ''));
			return new Promise(function () {}); // never settles; the document is going away
		}
		var status = opts.onStatus || function () {};
		var hvPort = opts.hvPort || 8001;
		// 0 disables.
		var docsPort = opts.docsPort === 0 ? 0 : (opts.docsPort || 8002);
		// The one browser window, whichever surface opens it first. The docs do
		// not wait on the hypervisor: with no visor started — the docs-site case —
		// nothing ever listens on hvPort, and a docs tab gated on that would
		// never appear.
		var deskWin = null;

		// hostBridge: the capability probe behind the whole host/in-tab choice.
		// A page served by a NATIVE hypervisor answers a plain GET /ws with 426
		// Upgrade Required; nothing else on any desk origin does. Where one
		// answers, claim the visor's virtual-loopback ports for the HOST visor
		// (hvws-vnet.js) — after which every panel that already speaks vnet
		// reaches the host process unchanged, because from the page's side a
		// bridged port is indistinguishable from a wasm visor's.
		//
		// Nothing here is a flag or a build variant: a page that has no /ws
		// gets null and boots exactly as it does today.
		function hostBridge() {
			if (!globalThis.SkywireHVWS || !globalThis.SkywireHVBridge || !globalThis.vnet) {
				return Promise.resolve(false);
			}
			// The PROBE only — one cheap GET. The socket itself is opened later,
			// once the desk module is up: opening it here made boot wait on the
			// bridge's connect timeout before fetching the module, and a socket
			// that never opened left the desk without its host for good.
			return globalThis.SkywireHVWS.probe().catch(function () { return false; });
		}

		// execWorker: the Worker every skywire command runs in once it is up,
		// or null where this page cannot host one (see workerExec below).
		var execWorker = null;

		// workerExec moves the skywire CLI — and therefore the visor — off the
		// page main thread. Resolves the installed worker, or null to keep the
		// in-page skywireExec.
		function workerExec() {
			if (!globalThis.SkywireExecWorker) return Promise.resolve(null);
			return globalThis.SkywireExecWorker.install({
				url: opts.execWorkerURL || 'skywire-worker.js',
				persistDB: opts.persistDB || 'skywire-desk',
				wasmURL: skywireExec.wasmURL,
				wasmExecURL: skywireExec.wasmExecURL,
			}).catch(function (e) {
				console.warn('exec worker:', e);
				return null;
			});
		}

		// bootWasm: the standalone desk — the tab IS the host. A wasm visor
		// runs in a terminal, claims the virtual-loopback ports, and every
		// panel reaches it through vnet. Unchanged by the host bridge above:
		// where a page can run its own visor there is no /ws to bridge to, so
		// the probe simply comes back empty.
		function bootWasm(bridged) {
			// The host's pty page, published BEFORE the module runs so installDesk
			// registers it as the terminal app.
			if (opts.terminalURL) {
				try { globalThis.__SKYWIRE_PTY_URL__ = new URL(opts.terminalURL, location.href).href; } catch (e) { /* keep unset */ }
			}
			// The dashboard the browser window opens on. A hypervisor serving this
			// desk names its own page: same-origin, rendered natively by the
			// DirectLoader with no service worker in the path — which matters on a
			// LAN address over plain http, where no service worker can register.
			// The default is the in-tab visor's UI on the virtual loopback.
			var dashURL = 'http://vnet:' + hvPort + '/?embed=1#/?embed=1';
			if (opts.dashboardURL) {
				try { dashURL = new URL(opts.dashboardURL, location.href).href; } catch (e) { /* keep vnet */ }
			}
			skywireExec.wasmURL = opts.wasmURL || 'skywire.wasm.gz';
			skywireExec.wasmExecURL = opts.wasmExecURL || 'wasm_exec.js';
			// The desk host: `skywire desk-host` out of the ONE command module,
			// spawned on THIS realm through the page's process layer (bottle
			// proc.js) — the same URL, compile cache and argv/env contract as
			// every command the terminal runs. It installs the shell, the
			// browser and the desk chrome and parks; no visor runs in it. The
			// in-page skywireExec is captured now, before the exec worker
			// replaces the global with its remote shim: the desk host draws, so
			// it runs where the document is, never in the worker.
			var localExec = globalThis.skywireExec;
			function startDeskHost() {
				if (!localExec || typeof localExec.spawn !== 'function') {
					return Promise.reject(new Error('no process layer to spawn the desk host on'));
				}
				var p = localExec.spawn(['desk-host'], {});
				p.exited.then(function (code) {
					console.error('desk host exited (' + code + ') — the desk surfaces are gone; reload the page');
				}, function (e) { console.error('desk host:', e); });
				return Promise.resolve();
			}

			// The vnet service worker: registered up front (it takes a moment to
			// activate), so by the time anything opens a loopback window the
			// nested browser can use real /vnet/<port>/ URLs — native rendering
			// for the hypervisor UI. Resolves false where SWs are unavailable;
			// the browser falls back to its transcoder, as before.
			var swReady = (globalThis.vnet && globalThis.vnet.enableSW)
				? globalThis.vnet.enableSW(opts.vnetSWURL || 'vnet-sw.js').catch(function () { return false; })
				: Promise.resolve(false);

			status('restoring filesystem…');
			// Where the skywire commands RUN. A Go/wasm visor never lets its
			// runtime idle — profiled on this page 2026-09-07, 95% of
			// one-second samples over 16.5 minutes sat above 90% of a core,
			// with findRunnable/stealWork/nanotime1 and NOT ONE application or
			// GC frame in the symbolized profile — so on the main thread it
			// starves the compositor and dragging a window stutters. Given a
			// Worker, every command's runtime spins over there instead and this
			// thread only draws. The retired legacy page refused an in-page
			// visor for this exact reason; the desk had regressed it.
			//
			// A capability, not a setting: no Worker, no vnet, or no
			// /skywire-worker.js served and install() resolves null, after
			// which everything below is the in-page path exactly as before.
			// The worker owns the IndexedDB snapshot when it comes up (one
			// writer, and it is the side the visor writes from), so the page
			// enables persistence only when there is no worker.
			return workerExec().then(function (w) {
				execWorker = w;
				if (w) return { restored: w.restored };
				return jsfs.persist.enable(opts.persistDB || 'skywire-desk', {
					// Persist identity + config + user files; NEVER the runtime
					// stores. A bbolt database snapshotted mid-write restores corrupt
					// and hangs its consumer on the next boot (the hypervisor module
					// stalling on a restored users.db) — caches rebuild, keys don't.
					exclude: function (p) {
						return /\.db$/.test(p) || p.indexOf('/opt/skywire/local/') === 0 || p === '/opt/skywire/local';
					},
				});
			}).then(function (p) {
				status((p.restored ? 'filesystem restored — ' : '') + 'starting the desk…');
				// Where the hypervisor UI lives, spelled CANONICALLY as
				// vnet:<port> rather than as the served form
				// "<origin>/vnet/<port>/". Both reach the same page, but only
				// the canonical spelling keeps the plumbing out of the UI:
				// DirectLoader (pkg/wasmhv/deskhost/desk_js.go) claims vnet:<port>
				// and hands netscrape the service-worker URL as the iframe SRC,
				// leaving the canonical form in the address bar. The served form
				// is instead passed straight through as "already served", which
				// is what made the hypervisor tab read
				// "http://<host>:<port>/vnet/8001/?embed=1#/?embed=1".
				//
				// The distinction is not cosmetic. That leading 127.0.0.1 is the
				// HOST's loopback — the origin the desk is served from — while
				// the port after /vnet/ is the TAB's own virtual loopback. They
				// are different networks, and concatenating them reads as one
				// address. Spelling it "vnet" is what disambiguates the two, and
				// is why vnetTarget accepts localhost/127.0.0.1 as INPUT (real
				// server software binds those) while vnet:<port> is canonical.
				// `skywire doc serve`'s tab below is already addressed this way.
				//
				// The vnet ROOT with ?embed=1, not "/dashboard": the service worker
				// rewrites a framed page's <base href> to the "/vnet/<port>/"
				// prefix, so any deeper path is normalised away before the document
				// finishes loading — a "/dashboard" URL lands back on the root and
				// renders the desk inside the desk. At the root the hypervisor
				// serves the dashboard instead whenever the request is framed, and
				// embed=1 also rides in the hash so the injected launcher inside
				// knows not to grow a taskbar of its own.
				globalThis.__DESK_DASHBOARD_URL__ = dashURL;
				return startDeskHost();
			}).then(function () {
				return Promise.all([
					waitFor(function () { return globalThis.skywireShell; }, 'the shell'),
					waitFor(function () { return globalThis.__skywireDesk; }, 'the desk panel'),
				]);
			}).then(function () {
				// skywireVisor is the legacy in-page visor's function table. The
				// desk host publishes none (its visor is the terminal instance),
				// so the fallbacks below that reach for it simply do not apply.
				var sv = globalThis.skywireVisor || {};
				// Served by a hypervisor: claim the visor's virtual-loopback ports
				// for the HOST visor now that the module's vnet is up. 3435 is the
				// visor's control surface and hvPort its hypervisor UI — the two
				// ports the desk asks about; the bridge claims only what is free.
				// Nothing waits on the socket: vnet.listening() flips when it
				// opens, and everything below already polls that.
				if (bridged && globalThis.SkywireHVBridge) {
					globalThis.SkywireHVBridge.install({ ports: [3435, hvPort] }).then(function (b) {
						if (!b) console.warn('host bridge: /ws did not open; the desk has no visor to reach');
					});
				}
				// The desk's REAL visor is the root binary running in a terminal —
				// a separate wasm instance the page cannot call. The desk host's
				// own skywireVisor core is deliberately never booted here, so the
				// nested browser reaches the mesh THROUGH the running visor's
				// resolving proxy on the virtual loopback (dmsgweb, vnet:4445,
				// chained to skynetweb + the proxy client): dmsg/skynet fetches go
				// SOCKS5-over-vnet. Falls back to the in-page core for a page that
				// booted it (nothing on the desk does today).
				var RESOLVER_PORT = 4445;
				function viaResolver() {
					return !!(globalThis.vnet && globalThis.vnet.listening(RESOLVER_PORT) && globalThis.vnet.socksHttpFetch);
				}
				function resolverHost(pkHost) {
					var h = String(pkHost || '');
					// bare 66-hex PK → PK.dmsg (the resolver matches by suffix)
					if (/^[0-9a-f]{66}$/i.test(h)) return h + '.dmsg';
					return h;
				}
				// The desk's browser transport: mesh fetches go through the RUNNING
				// visor's resolver on the virtual loopback (the terminal instance —
				// this page's own core never boots), falling back to the in-page
				// core's providers for a page that booted it. Installed BEFORE any
				// browser window opens so the loader's page-core default never
				// takes hold (gobrowser-loader only fills __netscrapeFetch when
				// nothing did).
				// The visor's OWN loopback — where its apps listen (hypervisor,
				// resolver, proxy, status pages). In this tab that is the page's
				// vnet port table, so `vnet:<port>` (canonical) and `<port>.vnet`
				// name it; 127.0.0.1 / localhost keep resolving here too, as the
				// retired JS engine had them. Returns 0 for anything else.
				function vnetPort(u) {
					var h = String(u.hostname || '').toLowerCase();
					var m = /^(\d+)\.vnet$/.exec(h);
					if (m) return parseInt(m[1], 10) || 0;
					if (h === 'vnet' || h === 'localhost' || h === '127.0.0.1' || h === '::1' || h === '[::1]') {
						return u.port ? (parseInt(u.port, 10) || 0) : 80;
					}
					return 0;
				}
					// The browser's proxy setting (netscrape's ⚙ field) is one
					// [scheme://]host:port, or empty for the visor's default egress.
					// proxyPort reads it: {addr, port, err} — port is set when the
					// address is this tab's own loopback (vnet:<port>, localhost:<port>),
					// the one proxy a page can speak SOCKS5 to itself; err names an
					// address that parses as nothing.
					function proxyPort(p) {
						var addr = String((p && p.proxy) || '').trim();
						if (!addr) return { addr: '', port: 0 };
						var s = /:\/\//.test(addr) ? addr : 'socks5://' + addr;
						var pu;
						try { pu = new URL(s); } catch (e) { return { addr: addr, port: 0, err: 'proxy: not [scheme://]host:port: ' + addr }; }
						if (!pu.port) return { addr: addr, port: 0, err: 'proxy: port required: ' + addr };
						return { addr: addr, port: vnetPort(pu) };
					}
					function proxyError(msg) {
						return new Response('<body style="font:14px sans-serif;padding:2em;color:#a33">' + msg + '</body>',
							{ status: 502, headers: new Headers({ 'content-type': 'text/html' }) });
					}
				globalThis.__netscrapeFetch = function (url) {
					var u;
					try { u = new URL(url, 'http://x/'); } catch (e) { return fetch(url); }
					var mesh = /\.(dmsg|skysocks|skynet)$/i.test(u.hostname) || /^[0-9a-f]{66}$/i.test(u.hostname);
					var path = (u.pathname || '/') + (u.search || '');
					function respond(r) {
						var h = new Headers();
						if (r && r.headers) { try { for (var k in r.headers) h.set(k, r.headers[k]); } catch (e) { /* ignore */ } }
						return new Response((r && r.body) || new Uint8Array(0), { status: (r && r.status) || 200, headers: h });
					}
					// Loopback first, and it NEVER falls through: a loopback address
					// that reached the clearnet branch would be dialed by the exit,
					// against the exit's own localhost — wrong, and a surprise. When
					// nothing listens on the vnet port, say so.
					var lp = vnetPort(u);
					if (lp) {
						if (globalThis.vnet && globalThis.vnet.listening(lp)) {
							return Promise.resolve(globalThis.vnet.httpFetch(lp, 'GET', path, null, {})).then(respond);
						}
						return Promise.resolve(new Response(
							'<body style="font:14px sans-serif;padding:2em;color:#a33">nothing is listening on vnet port ' + lp +
							' — this tab\'s visor has no such app running. (Loopback addresses resolve to this page\'s vnet, never to a remote exit.)</body>',
							{ status: 502, headers: new Headers({ 'content-type': 'text/html' }) }));
					}
					if (mesh && viaResolver()) {
						return Promise.resolve(globalThis.vnet.socksHttpFetch(RESOLVER_PORT, resolverHost(u.hostname) + ':80', 'GET', path, null, {})).then(respond);
					}
					if (mesh && sv.fetchDmsg) {
						return Promise.resolve(sv.fetchDmsg(u.hostname, 'GET', path, null)).then(respond);
					}
						// A loopback proxy is this tab's own visor (its skysocks-client on
						// the virtual loopback): the page speaks SOCKS5 to it directly, for
						// http — it cannot do TLS through it. A page cannot reach any other
						// proxy; the host-bridged desk below hands those to the visor.
						var pp = proxyPort(globalThis.__netscrapeProxy);
						if (pp.err) return Promise.resolve(proxyError(pp.err));
						if (pp.port) {
							if (u.protocol === 'https:') return Promise.resolve(proxyError('https through a loopback proxy needs the visor: use a proxy address the host can dial, or leave the field empty'));
							if (!(globalThis.vnet && globalThis.vnet.listening(pp.port))) return Promise.resolve(proxyError('nothing is listening on vnet port ' + pp.port));
							return Promise.resolve(globalThis.vnet.socksHttpFetch(pp.port, u.hostname + ':' + (u.port || 80), 'GET', path, null, {})).then(respond);
						}
						if (pp.addr) return Promise.resolve(proxyError('a browser visor can only use a proxy on its own loopback (vnet:&lt;port&gt;); ' + pp.addr + ' is not one'));
						if (sv.fetchClearnet) {
							return Promise.resolve(sv.fetchClearnet('', 'GET', url, null)).then(respond);
						}
					return fetch(url);
				};
				// The desk chrome comes from the library (0magnet/desk), mounted by
				// the desk host module itself (installDesk); its façade carries the
				// openConsole/openWindow contract this boot drives. The dashboard ☰
				// entry reads __DESK_DASHBOARD_URL__ (set above) — the running
				// visor's hypervisor UI over the vnet service worker.
				// Over the host bridge the tab runs no visor of its own, so netscrape's
				// transport is the hypervisor's browse API: mesh hosts (.dmsg /
				// .skynet / .skysocks, or a bare PK) through /api/browse/fetch — a
				// skynet route first, dmsg-HTTP as the fallback — and everything else
				// through /api/browse/clearnet, honoring the browser's proxy setting.
				// Same-origin pages never get here: the DirectLoader renders them
				// natively. Without this the browser fell back to a same-origin
				// /fetch proxy the hypervisor has never had (seen live as a 404 body).
				if (bridged) {
					var b64Bytes = function (b64) {
						var s = atob(b64 || '');
						var out = new Uint8Array(s.length);
						for (var i = 0; i < s.length; i++) { out[i] = s.charCodeAt(i); }
						return out;
					};
					var browsePost = function (path, req) {
						return fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(req) })
							.then(function (res) {
								return res.json().then(function (j) {
									if (!res.ok) {
										return new Response('skywire browse: ' + ((j && j.error) || res.status), { status: 502, headers: { 'content-type': 'text/plain' } });
									}
									var h = new Headers();
									if (j && j.header) { for (var k in j.header) { try { h.set(k, j.header[k]); } catch (e) { /* forbidden name */ } } }
									return new Response(b64Bytes(j && j.body), { status: (j && j.status_code) || 200, headers: h });
								});
							});
					};
					globalThis.__netscrapeFetch = function (url) {
						var u;
						try { u = new URL(url, location.href); } catch (e) { return fetch(url); }
						var host = u.hostname || '';
						// Same-origin URLs — this page's own assets, above all the favicon
						// of a natively rendered same-origin tab — and the tab's own
						// loopback (vnet:<port>) are fetched here, not sent to the host to
						// dial as clearnet: the host would reach for a loopback of its own.
						if (u.origin === location.origin) return fetch(url);
						var lp = vnetPort(u);
						if (lp) {
							if (globalThis.vnet && globalThis.vnet.listening(lp)) {
								return Promise.resolve(globalThis.vnet.httpFetch(lp, 'GET', (u.pathname || '/') + (u.search || ''), null, {})).then(function (r) {
									var h = new Headers();
									if (r && r.headers) { try { for (var k in r.headers) h.set(k, r.headers[k]); } catch (e) { /* ignore */ } }
									return new Response((r && r.body) || new Uint8Array(0), { status: (r && r.status) || 200, headers: h });
								});
							}
							return Promise.resolve(proxyError('nothing is listening on vnet port ' + lp));
						}
						if (/\.(dmsg|skynet|skysocks)$/i.test(host) || /^[0-9a-f]{66}$/i.test(host)) {
							return browsePost('/api/browse/fetch', { host: host, port: u.port ? (parseInt(u.port, 10) || 80) : 80, method: 'GET', path: (u.pathname || '/') + (u.search || '') });
						}
							// The proxy field: a loopback address is this tab's own visor's
							// SOCKS on the virtual loopback (http only — the page cannot do
							// TLS through it); anything else the host visor dials.
							var pp = proxyPort(globalThis.__netscrapeProxy);
							if (pp.err) return Promise.resolve(proxyError(pp.err));
							if (pp.port && u.protocol !== 'https:' && globalThis.vnet && globalThis.vnet.listening(pp.port)) {
								return Promise.resolve(globalThis.vnet.socksHttpFetch(pp.port, host + ':' + (u.port || 80), 'GET', (u.pathname || '/') + (u.search || ''), null, {})).then(function (r) {
									var h = new Headers();
									if (r && r.headers) { try { for (var k in r.headers) h.set(k, r.headers[k]); } catch (e) { /* ignore */ } }
									return new Response((r && r.body) || new Uint8Array(0), { status: (r && r.status) || 200, headers: h });
								});
							}
							var req = { method: 'GET', url: u.href };
							if (pp.addr && !pp.port) { req.proxy = pp.addr; }
							return browsePost('/api/browse/clearnet', req);
					};
				}
				var panel = globalThis.__skywireDesk;
				// Session: remember across reloads whether a visor was RUNNING when
				// the page went away — a visor the operator stopped stays stopped.
				var session = loadSession();
				if (opts.autostartVisor) {
					addEventListener('pagehide', saveSession);
					addEventListener('visibilitychange', function () {
						if (document.visibilityState === 'hidden') saveSession();
					});
				}

				var startVisor = !!opts.autostartVisor && !(session && session.visorRunning === false);
				var startedVisor = false;
				if (opts.autostartVisor) {
					// The visor terminal opens either way — running autoconfig
					// (which ends by starting the visor in the foreground), or idle
					// at the prompt when the operator had stopped it.
					var autoconfigCmd = 'skywire autoconfig';
					if (opts.attach && opts.attach.pk && bridged) {
						// Attached to the hypervisor that served this page: the visor's one
						// transport is a WebSocket back to this origin — an address the page
						// already has, so no address-resolver lookup — and it does not go
						// looking for public peers; everything else rides that socket.
						var tpURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + (opts.attach.path || '/tp/ws');
						// The same endpoint for the liveness probe: whether the host still
						// answers is how a stop gets attributed (see stopVerdict).
						attachOrigin = new URL(opts.attach.path || '/tp/ws', location.href).href;
						autoconfigCmd += ' --disable-public-autoconn --ws-peer ' + opts.attach.pk + '@' + tpURL;
					} else if (opts.attach && opts.attach.pk) {
						// ROAMING. The page was served by a hypervisor — it carries an
						// attach PK — but that hypervisor is not answering now. Pinning
						// the visor to a WebSocket back to an origin with nothing behind
						// it would leave it with one dead transport and no way to find a
						// peer, which is what a PWA installed from the hypervisor did as
						// soon as it left the LAN.
						//
						// So drop the attach flags and let the visor come up the way a
						// standalone one does: public autoconnect finds peers, and relay
						// nomination reaches dmsg through them. Same identity, same
						// storage, different peers.
						//
						// The mode is chosen per load rather than stored, because the
						// config is regenerated on every load anyway — coming home puts
						// the host back in reach and the next load attaches again with
						// nothing to undo. What this DOES change while roaming is that
						// the visor publishes a dmsg client entry, so its key becomes
						// dialable; attached it stays unpublished. Its transports are in
						// the transport discovery either way.
						console.info('skywire desk: hypervisor ' + opts.attach.pk.slice(0, 8) +
							'… not answering — starting the visor in roaming mode');
					}
					// ?loglvl=debug on the page URL boots the visor at that log level: the
					// config is regenerated on every load, so this is the one place a
					// level for the tab's visor can be set. Letters only, so the URL
					// cannot inject anything else into the command.
					var loglvl = new URLSearchParams(location.search).get('loglvl');
					if (loglvl && /^[a-z]+$/.test(loglvl)) {
						autoconfigCmd += ' --loglvl ' + loglvl;
					}
					panel.openConsole({ title: 'visor', initCmd: startVisor ? autoconfigCmd : '' });
					// Liveness watchdog. The session flag is only ever read to
					// SUPPRESS an autostart, so nothing noticed a visor that was
					// gone while the page stayed open: the desk kept pointing its
					// dashboard at vnet:8001 with nothing listening, indefinitely,
					// while localStorage still said visorRunning:true (observed
					// live). Poll the RPC port rather than trust that flag.
					//
					// Restarts are gated on this load having asked for one; the
					// up-transition tracking below runs either way, since saveSession
					// needs it to tell an operator stop from a visor that never came up.
					// A clean exit is the operator stopping it and is left alone;
					// a crash, or a worker that went away with its registry, is
					// not. Attempts are bounded and spaced because each one opens
					// a terminal tab — a visor that cannot start must not paper
					// the desk with them.
					{
						var WATCH_MS = 2000;
						var GRACE_TICKS = 45; // ~90s: a cold boot waits on the visor tree + dmsg
						var DOWN_TICKS = 3;   // ~6s down before it counts as gone, not a blip
						var MAX_RESTARTS = 3;
						var ticks = 0, downFor = 0, restarts = 0, cooldown = 0, wasDown = false;
						function restartInPlace() {
							try {
								console.warn('skywire desk: visor not listening on 3435 — restarting (' +
									restarts + '/' + MAX_RESTARTS + ')');
								panel.openConsole({ title: 'visor', initCmd: autoconfigCmd });
							} catch (e) { console.warn('skywire desk: visor restart failed:', e); }
						}
						setInterval(function () {
							var up = !!(globalThis.vnet && globalThis.vnet.listening(3435));
							ticks++;
							if (up) { visorSawUp = true; downFor = 0; wasDown = false; return; }

							// Attribute the stop the moment it happens, while the host's
							// reachability still says which kind it was. Once per
							// transition — wasDown latches so a visor that stays down
							// does not re-probe every tick.
							if (!wasDown && visorSawUp) {
								wasDown = true;
								hostAnswers().then(function (ok) {
									stopVerdict = ok ? 'operator' : 'environment';
								});
							}
							// Tracking runs whenever an autostart was possible — saveSession
							// needs the up-transition either way. Only the RESTART is gated on
							// this load having actually asked for one.
							if (!startVisor) { return; }
							if (cooldown > 0) { cooldown--; return; }
							// Never came up yet and still inside the boot grace.
							if (!visorSawUp && ticks < GRACE_TICKS) { return; }
							if (++downFor < DOWN_TICKS) { return; }
							downFor = 0;
							if (visorStoppedByOperator()) { return; }
							if (restarts >= MAX_RESTARTS) { return; }
							restarts++;
							cooldown = 15; // ~30s to let the restart come up before judging it
							// A restart runs in the worker this page already has, on the
							// module it booted with. If the server has moved on, that
							// re-runs superseded code — and the exec worker is created
							// once per page load, so a reload is the ONLY way to reach
							// the new module. Offer that instead, with the countdown the
							// operator can decline; declining falls back to restarting
							// what we have, which is better than leaving it down.
							servedVersionDiffers().then(function (differs) {
								if (differs) {
									notifyReload('Visor stopped — a newer build is available',
										'Reloading starts it on the new version. Staying restarts the current one.',
										10, restartInPlace);
									return;
								}
								restartInPlace();
							});
						}, WATCH_MS);
					}
					startedVisor = startVisor;
				}
				if (opts.helpTerminal !== false) {
					// bg: a background TAB of the terminal window — the visor's log
					// stays front, and this tab's session (and its `skywire --help`)
					// only starts when first clicked.
					panel.openConsole({ title: 'skywire', initCmd: 'skywire --help', bg: !!startedVisor });
				}
				// The docs server, in a terminal tab of its own — BEHIND the help
				// tab, so the reader still lands on `skywire --help`.
				//
				// It used to run through a bare skywireExec, which bound the port
				// but put the process nowhere a reader could see: on the docs site
				// the server was invisible, and the only way to tell it was alive
				// was that a tab eventually appeared. A websh tab is the same
				// process with its output somewhere — it can be read, Ctrl-C'd and
				// started again, which is what a terminal on a desk is for.
				//
				// initCmd runs on open, not on first click, so the port still binds
				// immediately and the docs browser tab below opens on schedule.
				if (docsPort && typeof panel.openConsole === 'function') {
					try {
						panel.openConsole({
							title: 'docs',
							initCmd: 'skywire doc serve --addr 127.0.0.1:' + docsPort,
							bg: true,
						});
					} catch (e) { console.warn('doc serve:', e); }
				}
				if (opts.terminalURL && typeof panel.launch === 'function') {
					// The host's pty as the terminal window — opened before the
					// dashboard, so the dashboard keeps focus.
					try { panel.launch('terminal'); } catch (e) { console.warn('terminal:', e); }
				}
				if (opts.hvWindow) {
					// The hypervisor UI comes up on the virtual loopback a while
					// after boot (its module waits on the visor tree + dmsg).
					// Open the window once something actually listens — armed even
					// when the session suppressed the autostart, so a visor the
					// operator starts BY HAND still gets its UI window. Autostarted
					// visors get a bounded wait; the manual case waits as long as
					// the page lives.
					// A same-origin dashboard (a hypervisor serving this desk) is ready the
					// moment the desk is: it is this origin's own page and needs no visor on
					// the virtual loopback to render. Only the in-tab visor's UI has to wait
					// for its port to listen.
					(function waitHV(n) {
						if (opts.dashboardURL || (globalThis.vnet && globalThis.vnet.listening(hvPort))) {
							// Hold the open until the service-worker question is
							// settled either way — opening a beat earlier would
							// route this first load through the transcoder.
							swReady.then(function () {
								try {
									// ONE browser window, everything in TABS: the
									// hypervisor UI first, then the deployment landing
									// page and the proxy status page behind it. The
									// dashboard tab renders NATIVELY — desk_js.go gives
									// netscrape a DirectLoader that claims same-origin
									// /vnet/ URLs, so the Angular app loads as an
									// ordinary document instead of being transcoded
									// into a sandbox that would strip its same-origin.
									// Share the window if the docs got here first — doc serve
									// binds in seconds and the hypervisor takes a while, so
									// that is the ordinary order, and opening a second window
									// unconditionally is how this produced two (measured).
									var win = deskWin;
									if (win && win.openTab) {
										try { var du = new URL(dashURL); win.openTab(du.host, du.pathname + du.search + du.hash, du.protocol.replace(':', ''), false); } catch (e2) {}
									} else {
										win = panel.openWindow(true, globalThis.__DESK_DASHBOARD_URL__);
										deskWin = win;
									}
									if (win && win.openTab) {
										// home.dmsg always: BrowseFetch answers it in-process as
										// the resolver's synthetic alias directory, before any
										// resolve or dial, so it renders with no proxy, no route
										// and no transport.
										try { win.openTab('home.dmsg', '/', 'http', true); } catch (e2) {}
										// status.skysocks only when something serves it. It is a
										// real page from a running skysocks-client, not a
										// synthetic one — and this desk does not start a proxy.
										// Opened unconditionally it produced a tab that could
										// never load, with the failure swallowed by the catch
										// below, which reads as a broken desk rather than as an
										// app that was never started.
										if (proxyIsServing()) {
											try { win.openTab('status.skysocks', '/', 'http', true); } catch (e2) {}
										}
										// Behind both: the pairing procedure, but only when the host says
										// this tab is not approved yet. See openPairingDocsIfUnpaired.
										openPairingDocsIfUnpaired(panel, win, docsPort);
									}
									// One-shot subresources (the Material icon font above
									// all) are a load-lottery on first paint: the tab opens
									// the moment the port listens, dozens of asset fetches
									// land on the still-busy visor, and whichever ones time
									// out are never retried — APIs re-poll, fonts don't, so
									// the dashboard renders icon NAMES as text. Self-heal
									// once: if the icon font hasn't arrived after the page
									// has had time to settle, reload the frame — by then the
									// visor is warm and the same fetches complete (verified
									// live: a manual frame reload cured it every time).
									setTimeout(function () {
										try {
											// *= not ^=: DirectLoader hands netscrape an ABSOLUTE
										// src ("<origin>/vnet/<port>/…"), so anchoring to a
										// leading "/vnet/" matched nothing and this repair
										// never ran.
										var fr = document.querySelector('iframe[src*="/vnet/' + hvPort + '/"]');
											if (!fr || !fr.contentWindow || !fr.contentWindow.document.fonts) return;
											if (!fr.contentWindow.document.fonts.check('24px "Material Icons"')) {
												// NOT reload(): by now the Angular router has
												// rewritten the frame's URL to its current route
												// (".../vnet/8001/#/nodes/list/1") and the
												// ?embed=1 that opened it is GONE. Reloading that
												// URL asks the hypervisor for its ROOT with no
												// embed marker, and the marker is the only one it
												// can see — bottle's vnet service worker cannot
												// forward Sec-Fetch-Dest (a forbidden header name,
												// unreadable from a SW), so the server's "am I
												// framed?" test has nothing else to go on and it
												// serves its DESK page. That is the "desk inside
												// the desk" this window exists to avoid, and it is
												// what the self-heal itself was producing.
												var w2 = fr.contentWindow;
												w2.location.replace(w2.location.pathname + '?embed=1' + (w2.location.hash || ''));
											}
										} catch (e3) { /* cross-origin or torn-down frame — leave it */ }
									}, 25000);
								} catch (e) { console.error('hv window:', e); }
							});
							return;
						}
						if (startVisor && n > 360) return; // ~3min — the autostarted visor never served a UI
						setTimeout(function () { waitHV(n + 1); }, 500);
					})(0);
				}
				// The docs tab, on its own schedule. `skywire doc serve` needs no
				// visor and no dmsg — it renders the live cobra tree and the
				// embedded prose — so it binds in seconds, usually well before any
				// hypervisor UI. Gating it on hvPort would mean no docs tab at all
				// wherever no visor is started, which is exactly the docs-site case
				// this exists for. Shares the browser window when one exists and
				// opens its own otherwise; whichever surface is ready first makes it.
				if (docsPort) {
					(function waitDocs(n) {
						if (globalThis.vnet && globalThis.vnet.listening(docsPort)) {
							swReady.then(function () {
								try {
									// vnet:<port> — the canonical spelling, and what the
									// address bar shows. desk_js.go's DirectLoader claims it
									// and hands netscrape the service-worker URL that serves
									// it, so the tab renders natively without the plumbing
									// leaking into the UI.
									var docsURL = 'http://vnet:' + docsPort + '/';
									if (deskWin && deskWin.openTab) {
										// bg: the hypervisor window is already front when we
										// are sharing it.
										deskWin.openTab('vnet:' + docsPort, '/', 'http', true);
										return;
									}
									deskWin = panel.openWindow(true, docsURL);
								} catch (e) { console.warn('docs window:', e); }
							});
							return;
						}
						if (n > 120) return; // ~60s — doc serve never bound its port
						setTimeout(function () { waitDocs(n + 1); }, 500);
					})(0);
				}

				// execWorker is null on the in-page fallback — a caller (and a
				// CDP probe) can tell which thread the visor is on from it.
				return { panel: panel, startedVisor: startedVisor, execWorker: execWorker };
			});
		}

		// Which visor this desk is a shell over is decided by a CAPABILITY, not
		// by configuration: hostBridge probes for a /ws on this origin and, if
		// one answers, claims the visor's vnet ports for the host visor. Either
		// way the SAME desk boots — one desk, one code path; the bridge only
		// decides where the panels' traffic goes and whether a visor of the
		// tab's own is offered at all.
		return hostBridge().then(bootWasm);
	};
})();
