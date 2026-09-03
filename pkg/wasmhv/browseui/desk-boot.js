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
			var go = new Go();
			return WebAssembly.instantiate(buf, go.importObject).then(function (r) {
				go.run(r.instance).catch(function (e) { console.error('desk host:', e); });
				return Promise.all([
					waitFor(function () { return globalThis.skywireShell; }, 'the shell'),
					waitFor(function () { return globalThis.SkywireBrowse; }, 'browse.js'),
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
			var panel = globalThis.SkywireBrowse.mountPanel(document, {
				noTour: true, // the tour narrates the classic visor page
				fetchDmsg: function (pkHost, method, path, body) {
					if (viaResolver()) {
						return globalThis.vnet.socksHttpFetch(RESOLVER_PORT, resolverHost(pkHost) + ':80', method, path, body, {});
					}
					return sv.fetchDmsg.apply(null, arguments);
				},
				dmsgStatus: function () {
					// The visor's resolver listening IS mesh readiness for this
					// page (dmsgweb waits on dmsg internally); without it fall
					// back to the in-page core's own status.
					if (viaResolver()) { return { dmsg_connected: true }; }
					try { return sv.status() || {}; } catch (e) { return {}; }
				},
				serveContent: function (m) { return sv.serveContent(m); },
				selfPK: function () {
					// Sync by contract; served from a cache the poller below
					// fills from THE visor's /api/about the moment its
					// hypervisor listens. The in-page core's status() is only
					// a fallback for a page that booted it.
					if (deskSelfPK) return deskSelfPK;
					try { return sv.status().pk || ''; } catch (e) { return ''; }
				},
				api: function (m, path, body) {
					// The one visor's hypervisor API on the virtual loopback.
					if (globalThis.vnet && globalThis.vnet.listening(hvPort)) {
						return globalThis.vnet.httpFetch(hvPort, m, path, body, {}).then(function (r) {
							return { status: r.status, body: new TextDecoder().decode(r.body) };
						});
					}
					return sv.hvApi(m, path, body).then(function (r) {
						return { status: r.status, body: new TextDecoder().decode(r.body) };
					});
				},
			});
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
			globalThis.__skywireDesk = panel;

			// Session: remember across reloads whether a visor was RUNNING when
			// the page went away — a visor the operator stopped stays stopped.
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
								var win = panel.openWindow(true); // skipLanding
								win.browser.browseTo('127.0.0.1:' + hvPort, '/');
								// Default background tabs (browser-style): the
								// deployment landing page and the proxy status
								// page ride along behind the hypervisor UI.
								if (win.openTab) {
									try { win.openTab('home.dmsg', '/', 'http', true); } catch (e2) {}
									try { win.openTab('status.skysocks', '/', 'http', true); } catch (e2) {}
								}
								if (win.wb && win.wb.maximize) win.wb.maximize(true);
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
			return { panel: panel, startedVisor: startedVisor };
		});
	};
})();
