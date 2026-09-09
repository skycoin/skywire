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
		opts = opts || {};
		if (framedWithoutEmbed()) {
			location.replace(location.pathname + '?embed=1' + (location.hash || ''));
			return new Promise(function () {}); // never settles; the document is going away
		}
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
			// Where the skywire commands RUN. A Go/wasm visor never lets its
			// runtime idle — profiled on this page 2026-09-07, 95% of
			// one-second samples over 16.5 minutes sat above 90% of a core,
			// with findRunnable/stealWork/nanotime1 and NOT ONE application or
			// GC frame in the symbolized profile — so on the main thread it
			// starves the compositor and dragging a window stutters. Given a
			// Worker, every command's runtime spins over there instead and this
			// thread only draws. hv-boot.js has refused an in-page visor for
			// this exact reason since the legacy page; the desk had regressed
			// it.
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
				// The desk host: the wasm-visor binary in-page. It installs the
				// shell + the skywireVisor API and waits — boot() is never called,
				// so no visor runs except the one started IN a terminal.
				return gunzipFetch(opts.deskWasmURL || 'wasm-visor.wasm.gz');
			}).then(function (buf) {
				// Where the hypervisor UI lives, spelled CANONICALLY as
				// vnet:<port> rather than as the served form
				// "<origin>/vnet/<port>/". Both reach the same page, but only
				// the canonical spelling keeps the plumbing out of the UI:
				// DirectLoader (cmd/wasm-visor/desk_js.go) claims vnet:<port>
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
						// The browser's proxy setting (netscrape's ⚙ panel) picks the
						// exit: a named skysocks exit, this visor's own egress
						// ("direct"), or empty for the visor's default.
						var p = globalThis.__netscrapeProxy || {};
						var exit = '';
						if (p.mode === 'direct') exit = deskSelfPK;
						else if (p.mode === 'exit' && /^[0-9a-f]{66}$/i.test(p.exit || '')) exit = p.exit;
						return Promise.resolve(sv.fetchClearnet(exit, 'GET', url, null)).then(respond);
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
						if (/\.(dmsg|skynet|skysocks)$/i.test(host) || /^[0-9a-f]{66}$/i.test(host)) {
							return browsePost('/api/browse/fetch', { host: host, port: u.port ? (parseInt(u.port, 10) || 80) : 80, method: 'GET', path: (u.pathname || '/') + (u.search || '') });
						}
						var p = globalThis.__netscrapeProxy || {};
						var req = { method: 'GET', url: u.href };
						if (p.mode === 'direct' && globalThis.__SKYWIRE_LOCAL_PK__) { req.exit_pk = globalThis.__SKYWIRE_LOCAL_PK__; }
						else if (p.mode === 'exit' && /^[0-9a-f]{66}$/i.test(p.exit || '')) { req.exit_pk = p.exit; }
						return browsePost('/api/browse/clearnet', req);
					};
				}
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
					var autoconfigCmd = 'skywire autoconfig';
					if (opts.attach && opts.attach.pk) {
						// Attached to the hypervisor that served this page: the visor's one
						// transport is a WebSocket back to this origin — an address the page
						// already has, so no address-resolver lookup — and it does not go
						// looking for public peers; everything else rides that socket.
						var tpURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + (opts.attach.path || '/tp/ws');
						autoconfigCmd += ' --disable-public-autoconn --ws-peer ' + opts.attach.pk + '@' + tpURL;
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
					startedVisor = startVisor;
				}
				if (opts.helpTerminal !== false) {
					// bg: a background TAB of the terminal window — the visor's log
					// stays front, and this tab's session (and its `skywire --help`)
					// only starts when first clicked.
					panel.openConsole({ title: 'skywire', initCmd: 'skywire --help', bg: !!startedVisor });
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
