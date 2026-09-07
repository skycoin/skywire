// proc.js — the third leg of bottle: processes for wasm tabs.
//
// jsfs fakes the filesystem and vnet fakes the network; a Unix-shaped
// orchestrator — a shell, `go build`, make — also needs fork/exec. A tab has
// an exact analog: instantiating another wasm module IS spawning a process.
// proc makes that a primitive.
//
//   globalThis.proc.spawn({argv, env, cwd, stdout, stderr, stdin})
//     -> { pid, id, exited: Promise<exitCode>, kill() }
//
// - argv[0] is resolved against jsfs (absolute, cwd-relative, or PATH-walked);
//   the file's bytes ARE the program. Compiled modules are cached by path so
//   repeat spawns skip the compile. A program too big to hold as bytes is
//   bound to its path with registerModule / registerURL instead.
// - The child shares globalThis.fs (jsfs) and globalThis.vnet — that sharing
//   is the whole point: a parent writes $WORK, the child compiler reads it.
//   When it exits, the vnet claims it could not unlisten are released for it.
// - stdio is per-process. fds 1/2 route to the caller's stdout/stderr sinks
//   and fd 0 pulls from stdin; unset streams inherit the page defaults. The
//   active set is swapped around each wasm execution slice (each _resume is
//   synchronous and atomic on the one JS thread), so interleaved processes
//   never cross streams. Pipe them together with proc.pipe().
// - wait is the child's exit promise; the Go runtime's wasmExit resolves it.
// - every process has an id (opts.id, else "p<pid>"), handed to the child in
//   the env vars named by opts.idEnv (default ["BOTTLE_PID"]). It keys kill()
//   (proc.signals), the vnet claim release, and the post-mortem stderr ring
//   (proc.tails, opted into with opts.tail).
//
// Requires jsfs.js (globalThis.fs, jsfs.stdio). Go's wasm_exec.js may be
// loaded ahead of proc.js or left to the first spawn, which fetches
// proc.assets.wasmExec — importScripts in a worker, a <script> in a page.
(function () {
	if (globalThis.proc && globalThis.proc.installed) return;
	if (!globalThis.fs || !globalThis.jsfs) throw new Error("proc.js: load jsfs.js first");

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

	// deliver hands one write to a sink on a MICROTASK, never synchronously,
	// and always as a COPY.
	//
	// The copy is not optional: the buffer handed to a write is a view onto the
	// writing instance's linear memory, which the instance reuses the moment it
	// resumes. A sink that keeps it (a ring, a postMessage, a terminal that
	// batches) otherwise sees whatever the child wrote next.
	//
	// The deferral is not optional either: a sink that writes into ANOTHER wasm
	// instance — a terminal emulator that is itself a Go program — would, if
	// called inline, nest that instance's frames on top of the writing
	// instance's still-running wasm stack. Two Go runtimes deep on one JS stack
	// overflows it, and the RangeError lands in whichever runtime happens to be
	// executing, corrupting it. A microtask runs once the writer yields, on a
	// clean stack, and microtasks are FIFO so byte order is preserved.
	function deliver(sink, b) {
		if (!sink) return;
		const copy = b.slice();
		queueMicrotask(() => { try { sink(copy); } catch (e) { /* sink gone */ } });
	}

	function installDelegator() {
		if (stdio.stdout && stdio.stdout.__procDelegator) return;
		pageDefaults = { stdout: stdio.stdout, stderr: stdio.stderr, stdin: stdio.stdin };
		const out = (b) => deliver(active ? active.stdout : pageDefaults.stdout, b);
		const err = (b) => deliver(active ? active.stderr : pageDefaults.stderr, b);
		// stdin stays synchronous: it is a PULL the runtime makes mid-slice and
		// must answer from the caller's own stack.
		const inp = () => (active ? active.stdin : pageDefaults.stdin)();
		out.__procDelegator = err.__procDelegator = inp.__procDelegator = true;
		stdio.stdout = out;
		stdio.stderr = err;
		stdio.stdin = inp;
	}

	const moduleCache = new Map(); // path -> WebAssembly.Module
	const registered = new Set(); // paths bound to a module with no jsfs bytes

	// registerModule pre-binds a compiled module to a path, so spawn can run a
	// program whose bytes were never materialized in jsfs.
	//
	// The normal path — bytes in jsfs, compiled on first spawn — assumes a
	// program small enough to hold twice (once as bytes, once compiled). A large
	// module served over HTTP does not fit that: WebAssembly.compileStreaming
	// compiles it as it downloads and never holds the whole thing, and writing
	// the bytes into jsfs purely to satisfy resolution would give back exactly
	// the buffer streaming avoided (skywire.wasm is ~158MB raw, ~40MB gzipped).
	// Register the compiled module under the path it would have occupied and
	// spawn resolves it without ever reading bytes.
	function registerModule(path, mod) {
		if (!path || !mod) return false;
		moduleCache.set(path, mod);
		registered.add(path);
		return true;
	}

	const loaders = new Map(); // path -> () => Promise<WebAssembly.Module>

	// compileURL fetches and compiles a module by URL, streaming: the bytes are
	// compiled as they arrive and the whole file is never held. A ".gz" URL is
	// inflated through DecompressionStream on the way past — static hosting
	// that cannot set Content-Encoding (GitHub Pages, where a 158MB raw module
	// is over the file cap but the ~40MB gzip is not) is otherwise unusable.
	async function compileURL(url) {
		const resp = await fetch(url);
		if (!resp.ok) throw new Error("fetch " + url + ": HTTP " + resp.status);
		let src = resp;
		if (/\.gz(\?|$)/.test(url)) {
			const inflated = resp.body.pipeThrough(new DecompressionStream("gzip"));
			src = new Response(inflated, { headers: { "Content-Type": "application/wasm" } });
		}
		if (WebAssembly.compileStreaming) return WebAssembly.compileStreaming(Promise.resolve(src));
		return WebAssembly.compile(await src.arrayBuffer());
	}

	// registerURL binds a path to a module SERVED at url, compiled on the first
	// spawn that resolves it and cached from then on. registerModule with the
	// fetch deferred: nothing is downloaded by a page that never runs the
	// program, and the compile is shared by concurrent spawns.
	function registerURL(path, url) {
		if (!path || !url) return false;
		registered.add(path);
		moduleCache.delete(path);
		let p = null;
		loaders.set(path, () => (p || (p = compileURL(url).catch((e) => { p = null; throw e; }))));
		return true;
	}

	// ensureGo loads Go's wasm_exec.js if the page has not. Lazy because the
	// loader is only needed once a program actually runs, and realm-aware: a
	// worker has no document to append a <script> to, and importScripts is not
	// defined in a page.
	let goLoader = null;
	function ensureGo() {
		if (typeof globalThis.Go === "function") return Promise.resolve();
		if (!goLoader) {
			const url = assets.wasmExec;
			goLoader = (typeof importScripts === "function"
				? new Promise((res) => { importScripts(url); res(); })
				: new Promise((res, rej) => {
					const s = document.createElement("script");
					s.src = url;
					s.onload = res;
					s.onerror = () => rej(new Error("failed to load " + url));
					document.head.appendChild(s);
				})
			).then(() => {
				if (typeof globalThis.Go !== "function") throw new Error(url + " loaded but defines no Go class");
			}).catch((e) => { goLoader = null; throw e; });
		}
		return goLoader;
	}

	function readProgram(argv0, cwd, env) {
		// A registered module has no bytes to read; resolve it by path alone,
		// honoring the same absolute / cwd-relative / PATH-walk order.
		const reg = (p) => registered.has(p) ? { path: p, bytes: null } : null;
		if (argv0.startsWith("/")) { const r = reg(argv0); if (r) return r; }
		else if (argv0.includes("/")) { const r = reg(join(cwd || jsfs.getCwd(), argv0)); if (r) return r; }
		else for (const dir of ((env && env.PATH) || "/bin").split(":")) {
			const r = reg(join(dir, argv0)); if (r) return r;
		}

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

	// ---- signals, tails, and staying collectable ---------------------------
	//
	// Two registries outlive the processes they describe, and BOTH are built by
	// module-scope factories, never inline in spawn.
	//
	// Why that matters: a closure created inside spawn captures spawn's whole
	// scope, which holds `go` — and `go` holds the WebAssembly.Instance, and the
	// instance holds its entire linear memory. Store such a closure in a
	// page-lifetime registry and an exited program's heap is pinned for the life
	// of the page. Measured on a real page: a one-shot command cost ~93MB that
	// never came back. Close over a small plain record instead and the frame
	// dies when the call returns.
	//
	// signals: id -> handler. A child registers its own interrupt here (a Go
	// program from its signal package, keyed by the id it was handed in
	// opts.idEnv); kill() invokes it. Cleared on exit — never call into an
	// instance that is gone.
	const signals = Object.create(null);

	// tails: id -> { argv, tail, filtered, exitInfo }. A plain record, no
	// closures at all. Opt in with opts.tail (bytes); opts.tailFilter (a RegExp)
	// additionally keeps matching LINES in a second, slower-churning ring, for
	// the rare line that explains a failure and would otherwise scroll out of
	// the main ring in seconds under debug spam.
	const tails = Object.create(null);

	function makeKill(id) {
		return function () {
			try {
				const h = signals[id];
				if (h) h();
				return !!h;
			} catch (e) { return false; } // instance already gone
		};
	}

	// tailSink wraps a stderr sink so the bytes are also kept in rec. Built HERE
	// so it closes over rec (plain), the caller's sink, and two primitives —
	// nothing that can reach a spawn frame.
	function tailSink(rec, sink, limit, filter) {
		const dec = new TextDecoder();
		let lineBuf = "";
		return function (buf) {
			try {
				const s = dec.decode(buf, { stream: true });
				rec.tail = (rec.tail + s).slice(-limit);
				if (filter) {
					// Capped the same as the ring: a program that writes a lot
					// of stderr with no newline in it must not grow this without
					// bound while it waits for one.
					lineBuf = (lineBuf + s).slice(-limit);
					let nl;
					while ((nl = lineBuf.indexOf("\n")) >= 0) {
						const line = lineBuf.slice(0, nl);
						lineBuf = lineBuf.slice(nl + 1);
						if (filter.test(line)) rec.filtered = (rec.filtered + line + "\n").slice(-limit);
					}
				}
			} catch (e) { /* never let bookkeeping break the write */ }
			if (sink) sink(buf);
		};
	}

	// reap runs the page-side cleanup every exiting process needs: drop its
	// signal handler and release the vnet claims it could not unlisten itself.
	// A dead program's listener entry otherwise fakes liveness forever and holds
	// the port against a rebind. Module scope, and takes only the id.
	function reap(id) {
		try { delete signals[id]; } catch (e) { /* ignore */ }
		try {
			const v = globalThis.vnet;
			if (v && v.releaseOwner) {
				const n = v.releaseOwner(id);
				// Worth saying out loud: a program that exits still holding
				// ports was killed, or crashed, or forgot to close them.
				if (n > 0) console.warn("[proc " + id + "] released " + n + " vnet claim(s) on exit");
				return n;
			}
		} catch (e) { /* ignore */ }
		return 0;
	}

	function spawn(opts) {
		installDelegator();
		opts = opts || {};
		const argv = opts.argv || [];
		if (!argv.length) throw new Error("proc.spawn: empty argv");
		const cwd = opts.cwd || jsfs.getCwd();
		const env = opts.env || {};

		const prog = readProgram(argv[0], cwd, env);
		const pid = nextPID++;
		const id = opts.id || ("p" + pid);
		if (!prog) {
			// No such file: a real ENOENT, surfaced as a nonzero exit so a
			// shell prints "not found" rather than hanging.
			(opts.stderr || pageDefaults.stderr)(new TextEncoder().encode(argv[0] + ": not found\n"));
			return { pid, id, exited: Promise.resolve(127), kill: () => false };
		}

		// The post-mortem ring, when asked for. Registered BEFORE the program
		// starts so a crash in its first slice is still readable afterwards.
		let rec = null;
		if (opts.tail) {
			rec = { argv: argv.slice(), tail: "", filtered: "", exitInfo: null };
			tails[id] = rec;
		}

		const myStdio = {
			stdout: opts.stdout || pageDefaults.stdout,
			stderr: opts.stderr || pageDefaults.stderr,
			stdin: opts.stdin || pageDefaults.stdin,
		};
		if (rec) myStdio.stderr = tailSink(rec, myStdio.stderr, opts.tail, opts.tailFilter || null);

		let exitCode = 0;
		let crashed = false;
		const exited = (async () => {
			// The Go loader and the program compile in parallel; both may be a
			// network fetch on the first spawn.
			const [, mod] = await Promise.all([ensureGo(), resolveModule(prog)]);

			const go = new Go();
			go.argv = argv.slice();
			go.env = Object.assign({}, env);
			// Tell the child its process id, under whatever names it looks for.
			// It is the key it registers its interrupt handler under and the
			// owner tag its vnet claims carry, so kill() and reap() find them.
			for (const k of idEnvNames(opts)) go.env[k] = id;
			go.exit = (c) => { exitCode = c; };

			// Wrap _resume so this process's stdio is the active set for the
			// exact span of each synchronous execution slice, then restored.
			// Covers the initial run and every timer/promise-driven re-entry.
			const rawResume = go._resume.bind(go);
			go._resume = function () {
				// A child's Go runtime schedules timer callbacks (sysmon, the
				// scheduler); one can fire AFTER the program exits, and stock
				// wasm_exec throws "already exited" from _resume, uncaught,
				// which would take the page down. Swallow those late resumes.
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

			const inst = await WebAssembly.instantiate(mod, go.importObject);
			const prev = active;
			active = myStdio;
			const prevCwd = jsfs.getCwd();
			jsfs.setCwd(cwd);
			try {
				await go.run(inst); // resolves when the program exits
			} catch (e) {
				crashed = true;
				throw e;
			} finally {
				active = prev;
				jsfs.setCwd(prevCwd);
				reap(id);
				if (rec) rec.exitInfo = { code: exitCode, crashed: crashed };
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
		})().catch((e) => {
			// A failure to fetch, compile or instantiate never reaches the run
			// phase's cleanup, so do it here: the ring must still record how
			// this process ended, and nothing should be left registered.
			reap(id);
			if (rec && !rec.exitInfo) rec.exitInfo = { code: exitCode || 1, crashed: true };
			throw e;
		});
		return { pid, id, exited, kill: makeKill(id) };
	}

	// idEnvNames normalises opts.idEnv: a string, a list, or the default.
	function idEnvNames(opts) {
		const v = opts.idEnv;
		if (!v) return ["BOTTLE_PID"];
		return typeof v === "string" ? [v] : v;
	}

	// resolveModule turns a resolved program into a compiled module: the cache
	// first, then a registered URL loader, then the bytes from jsfs.
	async function resolveModule(prog) {
		const hit = moduleCache.get(prog.path);
		if (hit) return hit;
		const load = loaders.get(prog.path);
		if (load) {
			const mod = await load();
			moduleCache.set(prog.path, mod);
			return mod;
		}
		if (!prog.bytes) throw new Error("proc: no module registered for " + prog.path);
		const mod = await WebAssembly.compile(prog.bytes);
		moduleCache.set(prog.path, mod);
		return mod;
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
		const env = Object.assign({}, opts.env || {});
		const prog = readProgram(argv[0], cwd, env);
		const pid = nextPID++;
		const id = opts.id || ('p' + pid);
		for (const k of idEnvNames(opts)) env[k] = id;
		let rec = null;
		if (opts.tail) {
			rec = { argv: argv.slice(), tail: '', filtered: '', exitInfo: null };
			tails[id] = rec;
		}
		const myStdio = {
			stdout: opts.stdout || pageDefaults.stdout,
			stderr: opts.stderr || pageDefaults.stderr,
		};
		if (rec) myStdio.stderr = tailSink(rec, myStdio.stderr, opts.tail, opts.tailFilter || null);
		if (!prog) {
			myStdio.stderr(new TextEncoder().encode(argv[0] + ': not found\n'));
			if (rec) rec.exitInfo = { code: 127, crashed: false };
			return { pid, id, exited: Promise.resolve(127), kill: () => false };
		}

		const sab = new SharedArrayBuffer(opts.sabBytes || (1 << 20));
		const stopServing = globalThis.fsbridge.serve(sab);
		const w = new Worker(workerBlobURL());
		let settled = false;

		let terminate = null;
		const exited = new Promise((resolve) => {
			const finish = (code, wasCrash) => {
				if (settled) return;
				settled = true;
				stopServing();
				w.terminate();
				reap(id);
				if (rec) rec.exitInfo = { code: code, crashed: !!wasCrash };
				resolve(code);
			};
			// A worker child cannot be signaled — it is off-thread and its Go
			// runtime is busy — so kill() terminates it. 130 is what a shell
			// reports for a program killed by an interrupt.
			terminate = () => { if (settled) return false; finish(130, false); return true; };
			w.onmessage = (ev) => {
				const m = ev.data;
				if (m.out) { myStdio.stdout(m.out); return; }
				if (m.err) { myStdio.stderr(m.err); return; }
				if (m.exit !== undefined) finish(m.exit);
			};
			w.onerror = (e) => {
				myStdio.stderr(new TextEncoder().encode('proc: worker: ' + (e.message || e) + '\n'));
				finish(1, true);
			};
		});

		// Compile once per program and reuse the page's cache; a Module clones
		// across to the worker, so a spawn costs an instantiate, not a compile.
		(async () => {
			try {
				const mod = await resolveModule(prog);
				w.postMessage({ sab, mod, argv: argv.slice(), env, cwd, assets });
			} catch (e) {
				myStdio.stderr(new TextEncoder().encode('proc: ' + ((e && e.message) || e) + '\n'));
				w.dispatchEvent(new ErrorEvent('error', { message: String((e && e.message) || e) }));
			}
		})();

		return { pid, id, exited, kill: () => terminate() };
	}

	globalThis.proc = {
		installed: true,
		spawn, spawnWorker, pipeSink, pipeSource, assets,
		registerModule, registerURL, compileURL,
		// The two page-lifetime registries. Exposed so a page can name them
		// under its own globals (a child's Go signal handler registers into
		// proc.signals by whatever alias the page gave it) and so an operator
		// or a probe can read a dead process's last words from proc.tails.
		signals, tails,
	};
})();
