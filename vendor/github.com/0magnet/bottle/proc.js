// proc.js — the third leg of bottle: processes for wasm tabs.
//
// jsfs fakes the filesystem and vnet fakes the network; a Unix-shaped
// orchestrator — a shell, `go build`, make — also needs fork/exec. A tab has
// an exact analog: instantiating another wasm module IS spawning a process.
// proc makes that a primitive.
//
//   globalThis.proc.spawn({argv, env, cwd, stdout, stderr, stdin})
//     -> { pid, exited: Promise<exitCode> }
//
// - argv[0] is resolved against jsfs (absolute, cwd-relative, or PATH-walked);
//   the file's bytes ARE the program. Compiled modules are cached by path so
//   repeat spawns skip the compile.
// - The child shares globalThis.fs (jsfs) and globalThis.vnet — that sharing
//   is the whole point: a parent writes $WORK, the child compiler reads it.
// - stdio is per-process. fds 1/2 route to the caller's stdout/stderr sinks
//   and fd 0 pulls from stdin; unset streams inherit the page defaults. The
//   active set is swapped around each wasm execution slice (each _resume is
//   synchronous and atomic on the one JS thread), so interleaved processes
//   never cross streams. Pipe them together with proc.pipe().
// - wait is the child's exit promise; the Go runtime's wasmExit resolves it.
//
// Requires jsfs.js (globalThis.fs, jsfs.stdio) and wasm_exec.js (globalThis.Go)
// loaded first. Load proc.js after both.
(function () {
	if (globalThis.proc && globalThis.proc.installed) return;
	if (!globalThis.fs || !globalThis.jsfs) throw new Error("proc.js: load jsfs.js first");
	if (typeof globalThis.Go !== "function") throw new Error("proc.js: load wasm_exec.js first");

	const jsfs = globalThis.jsfs;
	const stdio = jsfs.stdio;

	// The stdio set of the process whose execution slice is currently running.
	// jsfs.stdio's methods delegate here so a child's fd writes reach its own
	// sinks with no per-call fd bookkeeping. Installed lazily at the first
	// spawn — capturing whatever stdio the page has set by then as the page
	// defaults — and re-asserted each spawn, so a page that assigns
	// jsfs.stdio.* after proc.js loads is respected, not clobbered.
	let active = null;
	let pageDefaults = null;
	function installDelegator() {
		if (stdio.stdout && stdio.stdout.__procDelegator) return;
		pageDefaults = { stdout: stdio.stdout, stderr: stdio.stderr, stdin: stdio.stdin };
		const out = (b) => (active ? active.stdout : pageDefaults.stdout)(b);
		const err = (b) => (active ? active.stderr : pageDefaults.stderr)(b);
		const inp = () => (active ? active.stdin : pageDefaults.stdin)();
		out.__procDelegator = err.__procDelegator = inp.__procDelegator = true;
		stdio.stdout = out;
		stdio.stderr = err;
		stdio.stdin = inp;
	}

	const moduleCache = new Map(); // path -> WebAssembly.Module

	function readProgram(argv0, cwd, env) {
		// Resolve argv[0] to bytes in jsfs. Absolute or cwd-relative first,
		// then a PATH walk (env.PATH, colon-separated) as a shell would.
		const tryPath = (p) => {
			const bytes = jsfs.readFile(p);
			return bytes ? { path: p, bytes } : null;
		};
		if (argv0.startsWith("/")) return tryPath(argv0);
		if (argv0.includes("/")) return tryPath(join(cwd || jsfs.getCwd(), argv0));
		for (const dir of ((env && env.PATH) || "/bin").split(":")) {
			const hit = tryPath(join(dir, argv0));
			if (hit) return hit;
		}
		return null;
	}

	function join(a, b) {
		if (b.startsWith("/")) return b;
		return (a.endsWith("/") ? a : a + "/") + b;
	}

	let nextPID = 2; // 1 is the page's own root program by convention

	function spawn(opts) {
		installDelegator();
		opts = opts || {};
		const argv = opts.argv || [];
		if (!argv.length) throw new Error("proc.spawn: empty argv");
		const cwd = opts.cwd || jsfs.getCwd();
		const env = opts.env || {};

		const prog = readProgram(argv[0], cwd, env);
		const pid = nextPID++;
		if (!prog) {
			// No such file: a real ENOENT, surfaced as a nonzero exit so a
			// shell prints "not found" rather than hanging.
			(opts.stderr || pageDefaults.stderr)(new TextEncoder().encode(argv[0] + ": not found\n"));
			return { pid, exited: Promise.resolve(127) };
		}

		const myStdio = {
			stdout: opts.stdout || pageDefaults.stdout,
			stderr: opts.stderr || pageDefaults.stderr,
			stdin: opts.stdin || pageDefaults.stdin,
		};

		const go = new Go();
		go.argv = argv.slice();
		go.env = Object.assign({}, env);
		let exitCode = 0;
		go.exit = (c) => { exitCode = c; };

		// Wrap _resume so this process's stdio is the active set for the exact
		// span of each synchronous execution slice, then restored. Covers the
		// initial run and every timer/promise-driven re-entry.
		const rawResume = go._resume.bind(go);
		go._resume = function () {
			// A child's Go runtime schedules timer callbacks (sysmon, the
			// scheduler); one can fire AFTER the program exits, and stock
			// wasm_exec throws "already exited" from _resume, uncaught, which
			// would take the page down. Swallow those late resumes.
			if (go.exited) return;
			const prev = active;
			active = myStdio;
			const prevCwd = jsfs.getCwd();
			jsfs.setCwd(cwd); // each process has its own working directory
			try {
				return rawResume();
			} finally {
				active = prev;
				jsfs.setCwd(prevCwd);
			}
		};

		const exited = (async () => {
			let mod = moduleCache.get(prog.path);
			if (!mod) {
				mod = await WebAssembly.compile(prog.bytes);
				moduleCache.set(prog.path, mod);
			}
			const inst = await WebAssembly.instantiate(mod, go.importObject);
			const prev = active;
			active = myStdio;
			const prevCwd = jsfs.getCwd();
			jsfs.setCwd(cwd);
			try {
				await go.run(inst); // resolves when the program exits
			} finally {
				active = prev;
				jsfs.setCwd(prevCwd);
				// Cancel any timer callbacks the child's Go runtime still had
				// pending. Stock wasm_exec never clears _scheduledTimeouts on
				// exit, and its setTimeout handler spins
				//   while (this._scheduledTimeouts.has(id)) this._resume()
				// — which, once the program has exited, loops forever calling a
				// _resume that returns immediately (see the guard above),
				// pegging the thread and hanging the page. A short-lived child
				// (compile -V=full) rarely has one pending; a real compile
				// does, which is why it hung and version queries did not.
				if (go._scheduledTimeouts) {
					for (const h of go._scheduledTimeouts.values()) {
						try { clearTimeout(h); } catch (e) {}
					}
					go._scheduledTimeouts.clear();
				}
			}
			return exitCode;
		})();

		return { pid, exited };
	}

	// pipeSink(fd) returns a plain stdout/stderr sink that writes a child's
	// output into a jsfs pipe (whose read end the parent holds). It is an
	// ordinary JS function, NOT a Go callback, so a child writing its stdout
	// never re-enters the parent's Go runtime — the bytes cross in jsfs.
	function pipeSink(fd) {
		return function (chunk) {
			try { globalThis.fs.writeSync(fd, chunk); } catch (e) { /* read end gone */ }
		};
	}

	// pipeSource(fd) returns a stdin puller draining a jsfs pipe. jsfs pipe
	// reads are async (they block until data), but proc's stdin puller is
	// synchronous, so this returns only what is already queued and never
	// blocks — enough for the toolchain, whose children do not read stdin.
	function pipeSource(fd) {
		return function () {
			return null; // best-effort: no synchronous drain of a blocking pipe
		};
	}

	// ---- off-thread children ----------------------------------------------
	//
	// spawn() above runs a child on the page's one JS thread, which is what makes
	// its stdio and cwd juggling correct — and what makes a long compile freeze
	// the tab for the length of the build. spawnWorker runs the same child in a
	// Worker instead. The child keeps the parent's filesystem: fsbridge hands it
	// a blocking, synchronous view of the page's jsfs, which is exactly the
	// contract Go's syscall layer expects, so `go build` in a worker still sees
	// the files the shell wrote a moment ago on the main thread.
	//
	// Two things do NOT cross the bridge:
	//   - stdout/stderr, which post back as messages and are handed to this
	//     process's sinks on the page. Routing them through the filesystem would
	//     land them in the OWNER's active stdio, which belongs to whatever the
	//     main thread happens to be running.
	//   - stdin, which reads EOF, matching pipeSource: the toolchain's children
	//     do not read stdin, and a blocking stdin would need a second channel.
	//
	// The compiled module is posted rather than the program bytes: a
	// WebAssembly.Module is structured-cloneable, so the page's compile cache is
	// reused and a spawn costs an instantiate rather than a recompile.

	// Asset URLs default to the directory proc.js itself was loaded from, so a
	// page that serves bottle's files together needs no configuration.
	const BASE = (typeof document !== 'undefined' && document.currentScript && document.currentScript.src)
		? document.currentScript.src.replace(/[^/]*$/, '')
		: (globalThis.location ? globalThis.location.origin + '/' : '/');
	const assets = { fsbridge: BASE + 'fsbridge.js', wasmExec: BASE + 'wasm_exec.js' };

	const WORKER_SRC = `
self.onmessage = async (ev) => {
  const m = ev.data;
  try {
    importScripts(m.assets.fsbridge, m.assets.wasmExec);
    fsbridge.install(m.sab, { cwd: m.cwd });

    // stdout/stderr go home as messages, not through the filesystem. The buffer
    // is copied first: it is a view onto the wasm instance's memory, and
    // posting a view clones the WHOLE memory behind it.
    const bridged = globalThis.fs;
    const rawWriteSync = bridged.writeSync.bind(bridged);
    const rawWrite = bridged.write.bind(bridged);
    const rawRead = bridged.read.bind(bridged);
    const say = (fd, b) => postMessage(fd === 1 ? { out: b.slice() } : { err: b.slice() });

    bridged.writeSync = (fd, buf) => {
      if (fd === 1 || fd === 2) { say(fd, buf); return buf.length; }
      return rawWriteSync(fd, buf);
    };
    bridged.write = (fd, buf, offset, length, position, cb) => {
      if (fd === 1 || fd === 2) {
        say(fd, buf.subarray(offset, offset + length));
        queueMicrotask(() => cb(null, length));
        return;
      }
      return rawWrite(fd, buf, offset, length, position, cb);
    };
    bridged.read = (fd, buf, offset, length, position, cb) => {
      if (fd === 0) { queueMicrotask(() => cb(null, 0)); return; }  // stdin: EOF
      return rawRead(fd, buf, offset, length, position, cb);
    };

    const go = new Go();
    go.argv = m.argv.slice();
    go.env = Object.assign({}, m.env);
    let code = 0;
    go.exit = (c) => { code = c; };

    // Same late-resume guard as the main-thread path: a timer callback can fire
    // after the program exits, and stock wasm_exec throws "already exited".
    const rawResume = go._resume.bind(go);
    go._resume = function () { if (go.exited) return; return rawResume(); };

    const inst = await WebAssembly.instantiate(m.mod, go.importObject);
    await go.run(inst);

    if (go._scheduledTimeouts) {
      for (const h of go._scheduledTimeouts.values()) { try { clearTimeout(h); } catch (e) {} }
      go._scheduledTimeouts.clear();
    }
    postMessage({ exit: code });
  } catch (e) {
    postMessage({ err: new TextEncoder().encode('proc: ' + ((e && e.stack) || e) + '\\n') });
    postMessage({ exit: 1 });
  }
};
`;

	let workerURL = null;
	function workerBlobURL() {
		if (!workerURL) workerURL = URL.createObjectURL(new Blob([WORKER_SRC], { type: 'text/javascript' }));
		return workerURL;
	}

	// spawnWorker mirrors spawn's contract — {pid, exited} — so a caller swaps
	// one for the other without knowing which thread the child landed on.
	function spawnWorker(opts) {
		installDelegator();
		opts = opts || {};
		const argv = opts.argv || [];
		if (!argv.length) throw new Error('proc.spawnWorker: empty argv');
		if (typeof SharedArrayBuffer === 'undefined' || !globalThis.crossOriginIsolated) {
			throw new Error('proc.spawnWorker: needs cross-origin isolation (COOP/COEP) for SharedArrayBuffer');
		}
		if (!globalThis.fsbridge) throw new Error('proc.spawnWorker: fsbridge.js not loaded');

		const cwd = opts.cwd || jsfs.getCwd();
		const env = opts.env || {};
		const prog = readProgram(argv[0], cwd, env);
		const pid = nextPID++;
		const myStdio = {
			stdout: opts.stdout || pageDefaults.stdout,
			stderr: opts.stderr || pageDefaults.stderr,
		};
		if (!prog) {
			myStdio.stderr(new TextEncoder().encode(argv[0] + ': not found\n'));
			return { pid, exited: Promise.resolve(127) };
		}

		const sab = new SharedArrayBuffer(opts.sabBytes || (1 << 20));
		const stopServing = globalThis.fsbridge.serve(sab);
		const w = new Worker(workerBlobURL());
		let settled = false;

		const exited = new Promise((resolve) => {
			const finish = (code) => {
				if (settled) return;
				settled = true;
				stopServing();
				w.terminate();
				resolve(code);
			};
			w.onmessage = (ev) => {
				const m = ev.data;
				if (m.out) { myStdio.stdout(m.out); return; }
				if (m.err) { myStdio.stderr(m.err); return; }
				if (m.exit !== undefined) finish(m.exit);
			};
			w.onerror = (e) => {
				myStdio.stderr(new TextEncoder().encode('proc: worker: ' + (e.message || e) + '\n'));
				finish(1);
			};
		});

		// Compile once per program and reuse the page's cache; a Module clones
		// across to the worker, so a spawn costs an instantiate, not a compile.
		(async () => {
			try {
				let mod = moduleCache.get(prog.path);
				if (!mod) {
					mod = await WebAssembly.compile(prog.bytes);
					moduleCache.set(prog.path, mod);
				}
				w.postMessage({ sab, mod, argv: argv.slice(), env, cwd, assets });
			} catch (e) {
				myStdio.stderr(new TextEncoder().encode('proc: ' + ((e && e.message) || e) + '\n'));
				w.dispatchEvent(new ErrorEvent('error', { message: String((e && e.message) || e) }));
			}
		})();

		return { pid, exited };
	}

	globalThis.proc = { installed: true, spawn, spawnWorker, pipeSink, pipeSource, assets };
})();
