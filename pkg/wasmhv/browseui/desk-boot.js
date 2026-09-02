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
	function saveSession() {
		try {
			var up = !!(globalThis.vnet && globalThis.vnet.listening(3435));
			if (up) { visorSawUp = true; }
			if (!up && !visorSawUp) { return; } // still booting (or never started) — keep the previous verdict
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

		skywireExec.wasmURL = opts.wasmURL || 'skywire.wasm.gz';
		skywireExec.wasmExecURL = opts.wasmExecURL || 'wasm_exec.js';
		globalThis.__WINBOX_WASM_URL__ = opts.winboxURL || 'winbox.wasm';

		status('restoring filesystem…');
		return jsfs.persist.enable(opts.persistDB || 'skywire-desk').then(function (p) {
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
			var panel = globalThis.SkywireBrowse.mountPanel(document, {
				noTour: true, // the tour narrates the classic visor page
				fetchDmsg: function () { return sv.fetchDmsg.apply(null, arguments); },
				serveContent: function (m) { return sv.serveContent(m); },
				selfPK: function () { try { return sv.status().pk || ''; } catch (e) { return ''; } },
				api: function (m, path, body) {
					return sv.hvApi(m, path, body).then(function (r) {
						return { status: r.status, body: new TextDecoder().decode(r.body) };
					});
				},
			});
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
				panel.openConsole({ title: 'skywire', initCmd: 'skywire --help' });
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
						try {
							var win = panel.openWindow(true); // skipLanding
							win.browser.browseTo('127.0.0.1:' + hvPort, '/');
							if (win.wb && win.wb.maximize) win.wb.maximize(true);
						} catch (e) { console.error('hv window:', e); }
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
