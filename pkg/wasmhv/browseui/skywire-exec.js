// pkg/wasmhv/browseui/skywire-exec.js — the skywire CLI as a PROCESS on
// bottle's process layer.
//
// This file used to be a second process layer beside bottle's: its own module
// cache, streaming fetch/compile, stdio swapping, exit capture, vnet claim
// release, interrupt registry and stderr ring. None of that was skywire's —
// bottle owns jsfs and vnet, so a process's bookkeeping across them belongs
// there, and it is bottle's proc.js now. What is left here is the part that
// really is skywire's: where the module is served, the package-install PATH
// it answers to, the environment a skywire command expects, the names
// skywire's Go code and its page consumers already use, and which log lines
// are worth keeping.
//
// globalThis.skywireExec(args, hooks) -> Promise<exitCode>
//   args:  ["cli","config","gen","-rp"]   (argv[0] "skywire" is implied)
//   hooks: { stdout(Uint8Array), stderr(Uint8Array), stdin(), env, instance }
//
// Requires jsfs.js and proc.js installed (both are in the browse bundle) and
// the module served at skywireExec.wasmURL (default /skywire.wasm; 404 = the
// feature is simply off and the shell registers no `skywire` command).
(function () {
	'use strict';
	if (globalThis.skywireExec) return;

	// Where the CLI lives in the package tree seed-skywire.js lays down. The
	// module is REGISTERED at that path rather than written to it: it is
	// ~158MB raw (~40MB gzipped) and proc streams it straight into the
	// compiler, so the bytes never exist as a buffer at all.
	var PROG = '/opt/skywire/bin/skywire';

	// The page-lifetime registries are bottle's, published under the names
	// skywire already uses for them — ALIASES, not copies, so the Go side
	// writes into the very object proc reads:
	//   __skywireSignals    pkg/cmdutil/signal_js.go registers each instance's
	//                       interrupt here, keyed by SKYWIRE_EXEC_ID; proc's
	//                       kill() invokes it and drops it on exit.
	//   __skywireExecTails  the post-mortem stderr rings desk-boot.js reads to
	//                       tell a CRASHED visor from a stopped one, and
	//                       ctl-bridge.js mirrors to /ctl/log.
	if (globalThis.proc) {
		globalThis.__skywireSignals = globalThis.proc.signals;
		globalThis.__skywireExecTails = globalThis.proc.tails;
	}

	// ROUTERISH selects the lines kept in the second, slower-churning ring.
	// The full ring churns through its window in seconds under dmsg DEBUG
	// spam, so the one error that explains a failed dial is gone before anyone
	// looks; these lines are rare and survive.
	var ROUTERISH = /(router|route_setup|RouteGroup|routegroup|setupclient|rule|cascade|rsn)/i;
	var TAIL_BYTES = 16384;

	var boundURL = null;
	function bind() {
		if (boundURL === skywireExec.wasmURL) return;
		globalThis.proc.registerURL(PROG, skywireExec.wasmURL);
		// The loader for THIS module: the standard-Go wasm_exec.js, which is
		// not necessarily the one the page loaded for its own blob. proc
		// fetches it on the first spawn if the realm has no Go class yet.
		globalThis.proc.assets.wasmExec = skywireExec.wasmExecURL;
		boundURL = skywireExec.wasmURL;
	}

	// mirror reports an abnormal ending to the console with the stderr tail.
	// A long-running instance's panic otherwise goes only to an xterm nobody
	// is scrolled to; here it is diagnosable from DevTools or a CDP probe.
	// Module scope, and it reads the ring back out of proc.tails — it never
	// closes over the spawn that produced it.
	function mirror(id, code, err) {
		try {
			var rec = globalThis.proc.tails[id];
			var tail = (rec && rec.tail) || '';
			console.error('[skywire-exec ' + id + '] ' +
				(err ? 'crashed: ' + (err.message || err) : 'exited code ' + code) +
				(tail ? '\n--- last stderr ---\n' + tail : ''));
		} catch (e) { /* ignore */ }
	}

	var execSeq = 0;

	// spawnLocal starts one command as a process on THIS realm's process
	// layer and returns proc's handle ({pid, id, exited, kill}). skywireExec
	// below is the same thing reduced to an exit code; the desk boot takes the
	// handle itself for the one command that must run where the document is
	// and never exits — `skywire desk-host`, the desk's surfaces.
	function spawnLocal(args, hooks) {
		if (!globalThis.jsfs || !globalThis.jsfs.installed) {
			throw new Error('jsfs is not installed — load jsfs.js before running commands');
		}
		if (!globalThis.proc) {
			throw new Error('bottle proc.js is not loaded — no process layer');
		}
		bind();
		hooks = hooks || {};
		var id = 'x' + (++execSeq);
		var env = {
			HOME: '/home/user', USER: 'user', PWD: globalThis.jsfs.getCwd(),
			PATH: '/opt/skywire/bin:/usr/bin:/bin', TMPDIR: '/tmp', TERM: 'xterm-256color',
			COLUMNS: '100', LINES: '30',
		};
		// hooks.env: per-invocation overrides — the shell passes the terminal's
		// live COLUMNS/LINES so help styling (colors, the rain backdrop width)
		// matches the window it renders in.
		if (hooks.env) {
			for (var k in hooks.env) {
				if (Object.prototype.hasOwnProperty.call(hooks.env, k)) env[k] = String(hooks.env[k]);
			}
		}
		var p = globalThis.proc.spawn({
			argv: ['skywire'].concat(args),
			env: env,
			id: id,
			// SKYWIRE_EXEC_ID is the name pkg/cmdutil/signal_js.go looks the id
			// up under; BOTTLE_PID is proc's own, which bottle's vnet adapter
			// reads to tag this instance's port claims.
			idEnv: ['BOTTLE_PID', 'SKYWIRE_EXEC_ID'],
			stdout: hooks.stdout || null,
			stderr: hooks.stderr || null,
			stdin: hooks.stdin || null,
			// The ring is kept REGARDLESS of hooks: it is the only readable
			// copy of a long-running instance's log.
			tail: TAIL_BYTES,
			tailFilter: ROUTERISH,
		});
		// Ctrl+C parity: proc's kill() calls the interrupt the instance
		// registered under this id, so a foreground `skywire visor` shuts down
		// exactly as it would on SIGINT. Handed over SYNCHRONOUSLY, before the
		// command starts, which is the contract skywirecmd_js.go relies on.
		if (typeof hooks.instance === 'function') {
			hooks.instance({ interrupt: p.kill });
		}
		return p;
	}

	function skywireExec(args, hooks) {
		var p;
		try { p = spawnLocal(args, hooks); } catch (e) { return Promise.reject(e); }
		return p.exited.then(function (code) {
			if (code !== 0) mirror(p.id, code, null);
			return code;
		}, function (e) {
			mirror(p.id, null, e);
			throw e;
		});
	}
	skywireExec.spawn = spawnLocal;

	skywireExec.wasmURL = '/skywire.wasm';
	// The standard-Go loader; the served page pairs it with the blob variant.
	skywireExec.wasmExecURL = '/wasm_exec.js?variant=go';

	// available() resolves true when the module is served (used by the shell
	// to decide whether to register the command at all).
	skywireExec.available = function () {
		return fetch(skywireExec.wasmURL, { method: 'HEAD' })
			.then(function (resp) { return resp.ok; })
			.catch(function () { return false; });
	};

	globalThis.skywireExec = skywireExec;
})();
