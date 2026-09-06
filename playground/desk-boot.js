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
//   deskWasmURL     the desk-host module ('wasm-visor.wasm.gz'; .gz inflated)
//   wasmURL         the skywire command module for skywireExec
//   wasmExecURL     Go's wasm_exec.js for command instances
//   winboxURL       the window-manager module
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
//   native          the page is served by a NATIVE hypervisor — see below
//   dashboardURL    native mode: the same-origin dashboard URL for the
//                   dashboard window ('/#/?embed=1')
//   onStatus(msg)   boot progress for the page's overlay
(function () {
	'use strict';
	if (globalThis.skywireDeskBoot) return;

	var SESSION_KEY = 'skywire-desk-session';

	function gunzipFetch(url) {
		return fetch(url).then(function (r) {
			if (!r.ok) throw new Error('fetch ' + url + ': HTTP ' + r.status);
			if (!/\.gz(\?|$)/.test(url)) return r.arrayBuffer();
			var inflated = r.body.pipeThrough(new DecompressionStream('gzip'));
			return new Response(inflated, { headers: { 'Content-Type': 'application/wasm' } }).arrayBuffer();
		});
	}

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
	function saveSession() {
		try {
			var up = !!(globalThis.vnet && globalThis.vnet.listening(3435));
			if (up) { visorSawUp = true; }
			if (!up && !visorSawUp) { return; } // still booting (or never started) — keep the previous verdict
			if (!up && visorCrashed()) { up = true; } // crashed ≠ stopped: restart on the next load
			localStorage.setItem(SESSION_KEY, JSON.stringify({
				visorRunning: up,
				at: Date.now(),
			}));
		} catch (e) { /* storage denied — session just resets to defaults */ }
	}

	globalThis.skywireDeskBoot = function (opts) {
		opts = opts || {};
		var status = opts.onStatus || function () {};
		var hvPort = opts.hvPort || 8001;
		// 0 disables. Native mode leaves it off: a native hypervisor serves its
		// own docs and has no vnet to bind.
		var docsPort = opts.docsPort === 0 ? 0 : (opts.docsPort || 8002);
		// The one browser window, whichever surface opens it first. The docs do
		// not wait on the hypervisor: with no visor started — the docs-site case —
		// nothing ever listens on hvPort, and a docs tab gated on that would
		// never appear.
		var deskWin = null;

		// Native mode: the page is served by a NATIVE hypervisor, whose visor
		// is the host process — the desk is a shell OVER that visor, never a
		// second one, so nothing wasm-shaped boots here (no jsfs snapshot, no
		// desk-host module, no vnet, no skywireExec). The server injects
		// skywire-browse-launcher.js beside this file; that launcher mounts
		// the panel with its /api-backed providers (the same ones the Angular
		// dashboard gets) and publishes it as __skywireDesk. All that is left
		// to do is open the dashboard window: the SAME-ORIGIN Angular UI in an
		// iframe (embed=1 rides in the hash so the iframe's own injected
		// launcher hides its taskbar — a desk never nests inside a desk
		// window, the same guard the ☰ chat/log windows use).
		if (opts.native) {
			status('starting the desk…');
			return Promise.all([
				waitFor(function () { return globalThis.__skywireDesk; }, 'the desk panel'),
				waitFor(function () { return typeof globalThis.WinBox === 'function'; }, 'the window manager'),
			]).then(function (r) {
				var panel = r[0];
				try {
					// Join the panel's own stacking context (its windows root)
					// so focus raises the dashboard above/below sibling windows
					// like any other desk window; keep clear of the taskbar on
					// whichever edge it is docked (applyDock pins top OR bottom
					// to "0", persisted across reloads).
					var root = document.getElementById('skywire-skynet-root') || undefined;
					var bar = document.getElementById('skywire-skynet-taskbar');
					var barH = bar ? (bar.offsetHeight || 36) : 0;
					// applyDock writes bottom:"0" for a bottom dock (read back as
					// "0px" by most engines) and "auto" for a top dock.
					var bd = bar ? bar.style.bottom : 'auto';
					var topDocked = !(bd === '0' || bd === '0px');
					var wb = new WinBox({
						title: 'dashboard',
						url: opts.dashboardURL || '/#/?embed=1',
						root: root,
						top: topDocked ? barH : 0,
						bottom: topDocked ? 0 : barH,
						// The panel's window chrome (dark header + pointer
						// events); no-full keeps "maximize" in-tab.
						'class': ['skywire-wb', 'no-full'],
						x: 'center', y: 'center', width: '85%', height: '85%',
					});
					if (wb.maximize) wb.maximize(true);
				} catch (e) { console.error('dashboard window:', e); }
				return { panel: panel, startedVisor: false };
			});
		}

		skywireExec.wasmURL = opts.wasmURL || 'skywire.wasm.gz';
		skywireExec.wasmExecURL = opts.wasmExecURL || 'wasm_exec.js';
		globalThis.__WINBOX_WASM_URL__ = opts.winboxURL || 'winbox.wasm';

		// The vnet service worker: registered up front (it takes a moment to
		// activate), so by the time anything opens a loopback window the
		// nested browser can use real /vnet/<port>/ URLs — native rendering
		// for the hypervisor UI. Resolves false where SWs are unavailable;
		// the browser falls back to its transcoder, as before.
		var swReady = (globalThis.vnet && globalThis.vnet.enableSW)
			? globalThis.vnet.enableSW(opts.vnetSWURL || 'vnet-sw.js').catch(function () { return false; })
			: Promise.resolve(false);

		status('restoring filesystem…');
		return jsfs.persist.enable(opts.persistDB || 'skywire-desk', {
			// Persist identity + config + user files; NEVER the runtime
			// stores. A bbolt database snapshotted mid-write restores corrupt
			// and hangs its consumer on the next boot (the hypervisor module
			// stalling on a restored users.db) — caches rebuild, keys don't.
			exclude: function (p) {
				return /\.db$/.test(p) || p.indexOf('/opt/skywire/local/') === 0 || p === '/opt/skywire/local';
			},
		}).then(function (p) {
			status((p.restored ? 'filesystem restored — ' : '') + 'starting the desk…');
			// The desk host: the wasm-visor binary in-page. It installs the
			// shell + the skywireVisor API and waits — boot() is never called,
			// so no visor runs except the one started IN a terminal.
			return gunzipFetch(opts.deskWasmURL || 'wasm-visor.wasm.gz');
		}).then(function (buf) {
			// Where the hypervisor UI lives, as an ABSOLUTE same-origin URL —
			// the browser normalises a scheme-less address to http://, so a
			// bare path would not survive being opened as a tab, and the
			// DirectLoader that renders it natively matches on origin.
			//
			// The vnet ROOT with ?embed=1, not "/dashboard": the service worker
			// rewrites a framed page's <base href> to the "/vnet/<port>/"
			// prefix, so any deeper path is normalised away before the document
			// finishes loading — a "/dashboard" URL lands back on the root and
			// renders the desk inside the desk. At the root the hypervisor
			// serves the dashboard instead whenever the request is framed, and
			// embed=1 also rides in the hash so the injected launcher inside
			// knows not to grow a taskbar of its own.
			globalThis.__DESK_DASHBOARD_URL__ = globalThis.location.origin +
				'/vnet/' + hvPort + '/?embed=1#/?embed=1';
			var go = new Go();
			return WebAssembly.instantiate(buf, go.importObject).then(function (r) {
				go.run(r.instance).catch(function (e) { console.error('desk host:', e); });
				return Promise.all([
					waitFor(function () { return globalThis.skywireShell; }, 'the shell'),
					waitFor(function () { return globalThis.__skywireDesk; }, 'the desk panel'),
					waitFor(function () { return typeof globalThis.WinBox === 'function'; }, 'the window manager'),
				]);
			});
		}).then(function () {
			var sv = globalThis.skywireVisor;
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
			var panel = globalThis.__skywireDesk;
			// selfPK cache: poll the visor's /api/about once its HV listens;
			// re-check occasionally in case the operator restarts the visor
			// under a different identity.
			var deskSelfPK = '';
			(function pollPK() {
				if (globalThis.vnet && globalThis.vnet.listening(hvPort)) {
					globalThis.vnet.httpFetch(hvPort, 'GET', '/api/about', null, {}).then(function (r) {
						try {
							var a = JSON.parse(new TextDecoder().decode(r.body));
							if (a && a.public_key) deskSelfPK = a.public_key;
						} catch (e) { /* ignore */ }
					}).catch(function () { /* ignore */ });
				}
				setTimeout(pollPK, deskSelfPK ? 60000 : 3000);
			})();

			// Session: remember across reloads whether a visor was RUNNING when
			// the page went away — a visor the operator stopped stays stopped.
			// Start the docs server. It is a plain HTTP server over the embedded
			// prose and the live cobra tree — no visor needed, nothing to wait
			// for — so it is launched unconditionally and the tab that shows it
			// opens with the rest once its port answers. Failure is silent by
			// design: no docs tab is a smaller loss than a desk that will not boot.
			if (docsPort && globalThis.skywireExec) {
				try {
					skywireExec(['doc', 'serve', '--addr', '127.0.0.1:' + docsPort], {})
						.catch(function (e) { console.warn('doc serve:', e); });
				} catch (e) { console.warn('doc serve:', e); }
			}

			var session = loadSession();
			if (opts.autostartVisor) {
				addEventListener('pagehide', saveSession);
				addEventListener('visibilitychange', function () {
					if (document.visibilityState === 'hidden') saveSession();
				});
				// Track up-transitions continuously so a save after a stop can
				// tell "operator stopped it" from "it never came up".
				setInterval(function () {
					if (globalThis.vnet && globalThis.vnet.listening(3435)) { visorSawUp = true; }
				}, 2000);
			}

			var startVisor = !!opts.autostartVisor && !(session && session.visorRunning === false);
			var startedVisor = false;
			if (opts.autostartVisor) {
				// The visor terminal opens either way — running autoconfig
				// (which ends by starting the visor in the foreground), or idle
				// at the prompt when the operator had stopped it.
				panel.openConsole({ title: 'visor', initCmd: startVisor ? 'skywire autoconfig' : '' });
				startedVisor = startVisor;
			}
			if (opts.helpTerminal !== false) {
				// bg: a background TAB of the terminal window — the visor's log
				// stays front, and this tab's session (and its `skywire --help`)
				// only starts when first clicked.
				panel.openConsole({ title: 'skywire', initCmd: 'skywire --help', bg: !!startedVisor });
			}
			if (opts.hvWindow) {
				// The hypervisor UI comes up on the virtual loopback a while
				// after boot (its module waits on the visor tree + dmsg).
				// Open the window once something actually listens — armed even
				// when the session suppressed the autostart, so a visor the
				// operator starts BY HAND still gets its UI window. Autostarted
				// visors get a bounded wait; the manual case waits as long as
				// the page lives.
				(function waitHV(n) {
					if (globalThis.vnet && globalThis.vnet.listening(hvPort)) {
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
									try { win.openTab('vnet:' + hvPort, '/?embed=1#/?embed=1', 'http', false); } catch (e2) {}
								} else {
									win = panel.openWindow(true, globalThis.__DESK_DASHBOARD_URL__);
									deskWin = win;
								}
								if (win && win.openTab) {
									try { win.openTab('home.dmsg', '/', 'http', true); } catch (e2) {}
									try { win.openTab('status.skysocks', '/', 'http', true); } catch (e2) {}
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
										var fr = document.querySelector('iframe[src^="/vnet/' + hvPort + '"]');
										if (!fr || !fr.contentWindow || !fr.contentWindow.document.fonts) return;
										if (!fr.contentWindow.document.fonts.check('24px "Material Icons"')) {
											fr.contentWindow.location.reload();
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

			return { panel: panel, startedVisor: startedVisor };
		});
	};
})();
