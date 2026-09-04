// proc.js — the third leg of bottle: processes for wasm tabs.
//
// jsfs fakes the filesystem and vnet fakes the network; a Unix-shaped
// orchestrator — a shell, `go build`, make — also needs fork/exec. A tab has
// an exact analogue: instantiating another wasm module IS spawning a process.
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

	globalThis.proc = { installed: true, spawn, pipeSink, pipeSource };
})();
