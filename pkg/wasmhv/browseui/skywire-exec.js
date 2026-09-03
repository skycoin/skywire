// pkg/wasmhv/browseui/skywire-exec.js — execute the full skywire CLI wasm
// module per command invocation, OS-style: the module is fetched and compiled
// once, then each command instantiates it fresh with its own argv/env and
// exits when main returns. All instances share the page's jsfs (jsfs.js), so
// `skywire cli config gen -rp` writes /opt/skywire/skywire.json and the
// shell's cat/jq read it back.
//
// globalThis.skywireExec(args, hooks) -> Promise<exitCode>
//   args:  ["cli","config","gen","-rp"]   (argv[0] "skywire" is implied)
//   hooks: { stdout(Uint8Array), stderr(Uint8Array) }  — the command's output
//
// Requires: jsfs installed (jsfs.js), Go's wasm_exec.js loaded (the standard
// Go class — the skywire.wasm blob is a standard-Go build), and the module
// served at skywireExec.wasmURL (default /skywire.wasm; 404 = feature off).
(function () {
	'use strict';
	if (globalThis.skywireExec) return;

	let modPromise = null;

	async function compileOnce() {
		if (!modPromise) {
			modPromise = (async () => {
				const url = skywireExec.wasmURL;
				const resp = await fetch(url);
				if (!resp.ok) throw new Error('fetch ' + url + ': HTTP ' + resp.status);
				// A .gz module (static hosting that can't set Content-Encoding —
				// GitHub Pages serving the raw 158MB is over its file cap, the
				// ~40MB gzip isn't) is inflated here via DecompressionStream.
				if (/\.gz(\?|$)/.test(url)) {
					const inflated = resp.body.pipeThrough(new DecompressionStream('gzip'));
					return WebAssembly.compileStreaming
						? WebAssembly.compileStreaming(new Response(inflated, { headers: { 'Content-Type': 'application/wasm' } }))
						: WebAssembly.compile(await new Response(inflated).arrayBuffer());
				}
				return WebAssembly.compileStreaming
					? WebAssembly.compileStreaming(Promise.resolve(resp))
					: WebAssembly.compile(await resp.arrayBuffer());
			})().catch((e) => { modPromise = null; throw e; });
		}
		return modPromise;
	}

	// ensureGoLoader loads Go's wasm_exec.js on demand: the DOM realm only
	// needs it once a command actually runs (the visor's copy lives in the
	// SharedWorker realm). jsfs must already be installed — wasm_exec's own
	// fs stub only fills in when globalThis.fs is absent.
	let goLoaderPromise = null;
	function ensureGoLoader() {
		if (typeof Go === 'function') return Promise.resolve();
		if (!goLoaderPromise) {
			goLoaderPromise = new Promise((resolveP, rejectP) => {
				const s = document.createElement('script');
				s.src = skywireExec.wasmExecURL;
				s.onload = () => (typeof Go === 'function')
					? resolveP()
					: rejectP(new Error(skywireExec.wasmExecURL + ' loaded but defines no Go class'));
				s.onerror = () => rejectP(new Error('failed to load ' + skywireExec.wasmExecURL));
				document.head.appendChild(s);
			}).catch((e) => { goLoaderPromise = null; throw e; });
		}
		return goLoaderPromise;
	}

	let execSeq = 0;

	async function skywireExec(args, hooks) {
		if (!globalThis.jsfs || !globalThis.jsfs.installed) {
			throw new Error('jsfs is not installed — load jsfs.js before running commands');
		}
		await ensureGoLoader();
		const mod = await compileOnce();
		const iid = 'x' + (++execSeq);
		const go = new Go();
		go.argv = ['skywire', ...args];
		go.env = {
			HOME: '/home/user', USER: 'user', PWD: globalThis.jsfs.getCwd(),
			PATH: '/opt/skywire/bin:/usr/bin:/bin', TMPDIR: '/tmp', TERM: 'xterm-256color',
			COLUMNS: '100', LINES: '30',
		};
		// hooks.env: per-invocation environment overrides — the shell passes
		// the terminal's live COLUMNS/LINES so help styling (colors, the rain
		// backdrop width) matches the window it renders in.
		if (hooks && hooks.env) {
			for (const k in hooks.env) {
				if (Object.prototype.hasOwnProperty.call(hooks.env, k)) go.env[k] = String(hooks.env[k]);
			}
		}
		// Ctrl+C parity: the instance registers a JS-callable interrupt under
		// this id (cmdutil.SignalContext under js), and hooks.instance hands
		// the caller a way to invoke it — a foreground `skywire visor` then
		// shuts down exactly as it would on SIGINT.
		go.env.SKYWIRE_EXEC_ID = iid;
		if (hooks && typeof hooks.instance === 'function') {
			hooks.instance({
				interrupt() {
					try {
						const reg = globalThis.__skywireSignals;
						const f = reg && reg[iid];
						if (f) f();
					} catch (e) { /* instance already gone */ }
				},
			});
		}
		let code = 0;
		go.exit = (c) => { code = c; };
		const stdio = globalThis.jsfs.stdio;
		const prev = { stdout: stdio.stdout, stderr: stdio.stderr, stdin: stdio.stdin };
		// Deliver output hooks on a MICROTASK, never synchronously: a hook that
		// writes into another wasm instance (the shell's terminal) would
		// otherwise nest that instance's frames on top of this command's still-
		// running wasm stack — two Go runtimes deep, which overflows the JS
		// call stack and corrupts whichever runtime the RangeError lands in.
		// The microtask runs once this instance yields, with a clean stack.
		const deferred = (fn) => (buf) => {
			const copy = buf.slice();
			queueMicrotask(() => { try { fn(copy); } catch (e) { /* sink gone */ } });
		};
		if (hooks && hooks.stdout) stdio.stdout = deferred(hooks.stdout);
		// stderrTail: a small ring of the command's last stderr bytes, kept
		// REGARDLESS of hooks — when a long-running instance (the desk's
		// foreground visor) dies, its panic went only to an xterm nobody was
		// scrolled to. The tail is mirrored to the console below so a crash
		// is diagnosable from DevTools / a CDP probe.
		let stderrTail = '';
		const tailDec = new TextDecoder();
		const keepTail = (buf) => {
			try {
				stderrTail = (stderrTail + tailDec.decode(buf, { stream: true })).slice(-4096);
			} catch (e) { /* ignore */ }
		};
		{
			const userErr = (hooks && hooks.stderr) || null;
			const fallbackErr = prev.stderr; // no hook → keep the page's default sink
			stdio.stderr = deferred((buf) => {
				keepTail(buf);
				if (userErr) { userErr(buf); } else if (fallbackErr) { try { fallbackErr(buf); } catch (e) { /* sink gone */ } }
			});
		}
		if (hooks && hooks.stdin) stdio.stdin = hooks.stdin;
		let runErr = null;
		try {
			const inst = await WebAssembly.instantiate(mod, go.importObject);
			await go.run(inst);
		} catch (e) {
			runErr = e;
			throw e;
		} finally {
			stdio.stdout = prev.stdout; stdio.stderr = prev.stderr; stdio.stdin = prev.stdin;
			try {
				const reg = globalThis.__skywireSignals;
				if (reg && reg[iid]) delete reg[iid]; // never call into an exited instance
			} catch (e) { /* ignore */ }
			// Release this instance's vnet claims: a dead program cannot
			// unlisten its ports, and zombie entries fake liveness (and hold
			// the port against a rebind) forever.
			try {
				if (globalThis.vnet && globalThis.vnet.releaseOwner) {
					const n = globalThis.vnet.releaseOwner(iid);
					if (n > 0) console.warn('[skywire-exec ' + iid + '] released ' + n + ' vnet claim(s) on exit');
				}
			} catch (e) { /* ignore */ }
			// Mirror abnormal endings to the console with the stderr tail.
			if (runErr || code !== 0) {
				try {
					console.error('[skywire-exec ' + iid + '] ' + (runErr ? 'crashed: ' + (runErr.message || runErr) : 'exited code ' + code)
						+ (stderrTail ? '\n--- last stderr ---\n' + stderrTail : ''));
				} catch (e) { /* ignore */ }
			}
		}
		return code;
	}
	skywireExec.wasmURL = '/skywire.wasm';
	// The standard-Go loader; the served page pairs it with the blob variant.
	skywireExec.wasmExecURL = '/wasm_exec.js?variant=go';

	// available() resolves true when the module is served (used by the shell
	// to decide whether to register the command at all).
	skywireExec.available = async function () {
		try {
			const resp = await fetch(skywireExec.wasmURL, { method: 'HEAD' });
			return resp.ok;
		} catch (_) { return false; }
	};

	globalThis.skywireExec = skywireExec;
})();
