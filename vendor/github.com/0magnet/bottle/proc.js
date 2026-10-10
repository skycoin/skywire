// proc.js — the third leg of bottle: processes for wasm tabs.
//
// jsfs fakes the filesystem and vnet fakes the network; a Unix-shaped
// orchestrator — a shell, `go build`, make — also needs fork/exec. A tab has
// an exact analog: instantiating another wasm module IS spawning a process.
// proc makes that a primitive.
//
//   globalThis.proc.spawn({argv, env, cwd, stdout, stderr, stdin, tty})
//     -> { pid, id, exited: Promise<exitCode>, kill(force), stdin, resize(c, r) }
//
// - argv[0] is resolved against jsfs (absolute, cwd-relative, or PATH-walked);
//   the file's bytes ARE the program. Compiled modules are cached by path so
//   repeat spawns skip the compile. A program too big to hold as bytes is
//   bound to its path with registerModule / registerURL instead, and a caller
//   that keeps programs elsewhere passes opts.bytes or opts.module. Bytes
//   with opts.stamp are compiled once per path and stamp: proc.cached(path,
//   stamp) says when a spawn needs no bytes at all.
// - The child shares globalThis.fs (jsfs) and globalThis.vnet — that sharing
//   is the whole point: a parent writes $WORK, the child compiler reads it.
//   When it exits, the vnet claims it could not unlisten are released for it.
// - stdio is per-process. fds 1/2 route to the caller's stdout/stderr sinks
//   and fd 0 pulls from stdin; unset streams inherit the page defaults. The
//   active set is swapped around each wasm execution slice (each _resume is
//   synchronous and atomic on the one JS thread), so interleaved processes
//   never cross streams. Pipe them together with proc.pipe().
// - opts.stdin "pipe" gives the child a stdin it can wait on, as a program
//   reading a terminal does: the handle's stdin.write(bytes) feeds it and
//   stdin.close() ends it. A function there is a synchronous puller instead.
// - opts.tty {cols, rows, onRaw} makes the child a terminal program: it finds
//   its size, sets raw mode and hears of resizes through proc.tty(its id), and
//   the parent resizes it with proc.resize(id, cols, rows).
// - kill() interrupts through the child's own handler when it registered one,
//   and otherwise, or with kill(true), stops it where it stands: exit 130.
// - wait is the child's exit promise; the Go runtime's wasmExit resolves it.
// - every process has an id (opts.id, else "p<pid>"), handed to the child in
//   the env vars named by opts.idEnv (default ["BOTTLE_PID"]). It keys kill()
//   (proc.signals), the vnet claim release, and the post-mortem stderr ring
//   (proc.tails, opted into with opts.tail).
//
// Requires jsfs.js (globalThis.fs, jsfs.stdio). Go's wasm_exec.js may be
// loaded ahead of proc.js or left to the first spawn, which fetches
// proc.assets.wasmExec — importScripts in a worker, a <script> in a page.
// A program TinyGo built runs under TinyGo's loader, proc.assets.wasmExecTinyGo,
// and either kind runs on a page built with the other.
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
		// A child with a stdin pipe has its fd 0 reads wait on it (jsfs).
		stdio.stdinPipe = () => (active && active.stdinPipe >= 0 ? active.stdinPipe : -1);
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

	// A program is run by the loader of the toolchain that built it: Go's
	// wasm_exec.js or TinyGo's, which import different functions and both name
	// their class globalThis.Go. A page built with one can run children built
	// with either: the page's own class serves its kind, and the other is
	// loaded on first need from proc.assets — wasmExecGo or wasmExecTinyGo —
	// taken from globalThis without replacing the page's.

	// kindOf tells a TinyGo module (it imports WASI) from a Go one.
	function kindOf(mod) {
		try {
			return WebAssembly.Module.imports(mod).some((i) => i.module === "wasi_snapshot_preview1") ? "tinygo" : "go";
		} catch (e) { return "go"; }
	}

	// classKind tells which toolchain a loader class serves, by the imports it offers.
	function classKind(cls) {
		try { return new cls().importObject.wasi_snapshot_preview1 ? "tinygo" : "go"; } catch (e) { return "go"; }
	}

	const goClasses = Object.create(null); // kind -> Promise<class>
	let loading = Promise.resolve(); // one loader script at a time: each sets globalThis.Go

	// goClass resolves the loader class for kind. Lazy because a loader is only
	// needed once a program of its kind runs, and realm-aware: a worker has no
	// document to append a <script> to, and importScripts is not defined in a page.
	function goClass(kind) {
		const page = typeof globalThis.Go === "function" ? globalThis.Go : null;
		if (page && classKind(page) === kind) return Promise.resolve(page);
		if (goClasses[kind]) return goClasses[kind];
		const url = kind === "tinygo" ? assets.wasmExecTinyGo : (assets.wasmExecGo || assets.wasmExec);
		const p = loading.then(() => {
			const before = globalThis.Go;
			return (typeof importScripts === "function"
				? new Promise((res) => { importScripts(url); res(); })
				: new Promise((res, rej) => {
					const s = document.createElement("script");
					s.src = url;
					s.onload = res;
					s.onerror = () => rej(new Error("failed to load " + url));
					document.head.appendChild(s);
				})
			).then(() => {
				const cls = globalThis.Go;
				if (before) globalThis.Go = before; // the page keeps its own
				if (typeof cls !== "function" || cls === before) throw new Error(url + " loaded but defines no Go class");
				if (classKind(cls) !== kind) throw new Error(url + " is not a " + kind + " loader");
				return cls;
			});
		});
		loading = p.catch(() => {});
		goClasses[kind] = p.catch((e) => { delete goClasses[kind]; throw e; });
		return goClasses[kind];
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

	// setCwd enters a directory a process holds. A process may remove its own
	// working directory, which jsfs cannot enter, so that slice keeps the old one.
	function setCwd(d) {
		try { jsfs.setCwd(d); } catch (e) { /* removed since */ }
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

	// stoppers: id -> the hard kill of a running main-thread child. Set by
	// spawn and deleted at reap, so it pins the program only while it runs.
	const stoppers = Object.create(null);

	// ttys: id -> the terminal record of a child spawned with opts.tty.
	const ttys = Object.create(null);

	function makeKill(id) {
		return function (force) {
			try {
				const h = signals[id];
				if (h && !force) { h(); return true; }
			} catch (e) { /* instance already gone */ }
			const stop = stoppers[id];
			return stop ? stop() : false;
		};
	}

	// makeTTY builds a child's terminal: its size, its raw flag, and the
	// methods the child calls through proc.tty(id). Module scope, closing over
	// the plain record only.
	//
	// Every call that crosses to the other side goes through a microtask. The
	// parent's onRaw is usually a Go function in the PARENT's runtime, and a
	// resize listener is one in the CHILD's; called inline, either would run a
	// second Go runtime on top of the first one's stack (see deliver).
	//
	// The terminal also carries the process's own keys and screen — read waits
	// on its stdin pipe, write goes to its stdout sink — so a program that has
	// no working os.Stdin, such as one stock TinyGo built, still has a tty.
	function makeTTY(o, io) {
		const t = {
			cols: Math.max(1, o.cols | 0) || 80,
			rows: Math.max(1, o.rows | 0) || 24,
			raw: false,
			onRaw: typeof o.onRaw === "function" ? o.onRaw : null,
			listeners: [],
		};
		t.api = {
			size: () => [t.cols, t.rows],
			raw: () => t.raw,
			setRaw(on) {
				on = !!on;
				if (t.raw === on) return;
				t.raw = on;
				const f = t.onRaw;
				if (f) queueMicrotask(() => { try { f(on); } catch (e) { /* parent gone */ } });
			},
			onResize(fn) { if (typeof fn === "function") t.listeners.push(fn); },
			// read(buf, cb) fills buf with what is typed, waiting for it, and
			// calls cb(err, n); n is 0 at the end of the input.
			read(buf, cb) {
				if (io.stdinPipe < 0 || !jsfs.isPipe(io.stdinPipe)) { queueMicrotask(() => cb(null, 0)); return; }
				globalThis.fs.read(io.stdinPipe, buf, 0, buf.length, null, cb);
			},
			write(buf) { deliver(io.stdout, buf); return buf.length; },
		};
		return t;
	}

	// resize changes a child terminal's size and tells the child, later.
	function resize(id, cols, rows) {
		const t = ttys[id];
		if (!t) return false;
		cols = Math.max(1, cols | 0);
		rows = Math.max(1, rows | 0);
		if (t.cols === cols && t.rows === rows) return true;
		t.cols = cols;
		t.rows = rows;
		for (const fn of t.listeners) {
			queueMicrotask(() => { if (ttys[id] === t) { try { fn(cols, rows); } catch (e) { /* child gone */ } } });
		}
		return true;
	}

	// self is the id of the process whose slice is running, or null on the
	// page's own time: how a child that was handed no environment finds itself.
	function self() {
		return active && active.id ? active.id : null;
	}

	// environ is the environment of the process whose slice is running — a
	// copy, with its id under the idEnv names — or null on the page's own time.
	// A program stock TinyGo built is handed none any other way.
	function environ() {
		return active && active.env ? Object.assign({}, active.env) : null;
	}

	// argv is the arguments of the process whose slice is running — a copy —
	// or null on the page's own time. A program TinyGo built is handed none
	// any other way: its os.Args is only a placeholder.
	function argvOf() {
		return active && active.argv ? active.argv.slice() : null;
	}

	// ttyOf is the child's side: its terminal, or null when it has none.
	function ttyOf(id) {
		const t = ttys[id];
		return t ? t.api : null;
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
		delete stoppers[id];
		delete ttys[id];
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
		let cwd = opts.cwd || jsfs.getCwd();
		const env = opts.env || {};

		const prog = programOf(opts, argv[0], cwd, env);
		const pid = nextPID++;
		const id = opts.id || ("p" + pid);
		if (!prog) {
			// No such file: a real ENOENT, surfaced as a nonzero exit so a
			// shell prints "not found" rather than hanging.
			(opts.stderr || pageDefaults.stderr)(new TextEncoder().encode(argv[0] + ": not found\n"));
			return { pid, id, exited: Promise.resolve(127), kill: makeKill(id), stdin: closedStdin, resize: makeResize(id) };
		}

		// The post-mortem ring, when asked for. Registered BEFORE the program
		// starts so a crash in its first slice is still readable afterwards.
		let rec = null;
		if (opts.tail) {
			rec = { argv: argv.slice(), tail: "", filtered: "", exitInfo: null };
			tails[id] = rec;
		}

		// A stdin pipe: the child holds the read end through stdio.stdinPipe,
		// the caller the write end through the handle's stdin.
		let stdinR = -1, stdinW = -1;
		if (opts.stdin === "pipe") [stdinR, stdinW] = jsfs.pipe();
		const myStdio = {
			stdout: opts.stdout || pageDefaults.stdout,
			stderr: opts.stderr || pageDefaults.stderr,
			stdin: typeof opts.stdin === "function" ? opts.stdin : pageDefaults.stdin,
			stdinPipe: stdinR,
			id,
			env: Object.assign({}, env),
			argv: argv.slice(),
		};
		if (rec) myStdio.stderr = tailSink(rec, myStdio.stderr, opts.tail, opts.tailFilter || null);
		for (const k of idEnvNames(opts)) myStdio.env[k] = id;
		if (opts.tty) ttys[id] = makeTTY(opts.tty, myStdio);

		let exitCode = 0;
		let crashed = false;
		// The hard kill. Until the program is running it only marks the spawn
		// cancelled; once it is, it ends the run where it stands (below).
		let cancelled = false;
		let stopRun = null;
		stoppers[id] = () => {
			if (cancelled) return false;
			cancelled = true;
			exitCode = 130;
			if (stopRun) stopRun();
			return true;
		};
		const exited = (async () => {
			// The Go loader and the program compile in parallel; both may be a
			// network fetch on the first spawn.
			const mod = await resolveModule(prog);
			const Go = await goClass(kindOf(mod));
			if (cancelled) return 130;

			const go = new Go();
			go.argv = argv.slice();
			go.env = Object.assign({}, env);
			// Tell the child its process id, under whatever names it looks for.
			// It is the key it registers its interrupt handler under and the
			// owner tag its vnet claims carry, so kill() and reap() find them.
			for (const k of idEnvNames(opts)) go.env[k] = id;
			let exitHooked = false;
			go.exit = (c) => { exitCode = c; exitHooked = true; };
			// Stock TinyGo writes fds 1 and 2 through WASI straight to the console,
			// past jsfs and so past this process's sinks. Send them through jsfs.
			let memory = null;
			const wasi = go.importObject.wasi_snapshot_preview1;
			if (wasi && wasi.fd_write) {
				const raw = wasi.fd_write;
				wasi.fd_write = function (fd, iovs, n, nwritten) {
					if ((fd !== 1 && fd !== 2) || !memory) return raw.apply(this, arguments);
					const dv = new DataView(memory.buffer);
					let total = 0;
					for (let i = 0; i < n; i++) {
						const p = dv.getUint32((iovs >>> 0) + i * 8, true);
						const len = dv.getUint32((iovs >>> 0) + i * 8 + 4, true);
						if (len) globalThis.fs.writeSync(fd, new Uint8Array(memory.buffer, p, len));
						total += len;
					}
					dv.setUint32(nwritten >>> 0, total, true);
					return 0;
				};
			}

			// enter and leave bracket one execution slice. The process gets its
			// stdio and directory, the caller gets its own back, and a chdir sticks.
			const enter = () => {
				const t = { active, cwd: jsfs.getCwd() };
				active = myStdio;
				setCwd(cwd);
				t.entered = jsfs.getCwd();
				return t;
			};
			const leave = (t) => {
				const now = jsfs.getCwd();
				if (now !== t.entered) cwd = now;
				active = t.active;
				setCwd(t.cwd);
			};

			// Wrap _resume so this process's stdio is the active set for the
			// exact span of each synchronous execution slice, then restored.
			// Covers the initial run and every timer/promise-driven re-entry.
			// TinyGo's loader runs a timer wakeup through _wake rather than
			// _resume, so that entry gets the same wrapper.
			for (const name of ["_resume", "_wake"]) {
				if (typeof go[name] !== "function") continue;
				const raw = go[name].bind(go);
				go[name] = function () {
					// A child's Go runtime schedules timer callbacks (sysmon, the
					// scheduler); one can fire AFTER the program exits, and stock
					// wasm_exec throws "already exited" from _resume, uncaught,
					// which would take the page down. Swallow those late resumes.
					if (go.exited) return;
					const t = enter();
					try {
						return raw();
					} finally {
						leave(t);
					}
				};
			}

			const real = await WebAssembly.instantiate(mod, go.importObject);
			memory = real.exports.memory || real.exports.mem || null;
			if (cancelled) return 130;
			// TinyGo's loader runs a timer wakeup by calling the instance's
			// go_scheduler export straight from setTimeout, past every wrapper on
			// go. Hand run an instance whose export is wrapped too: one whose
			// prototype is the real one, so it is still a WebAssembly.Instance.
			let inst = real;
			if (typeof real.exports.go_scheduler === "function") {
				const ex = Object.assign({}, real.exports);
				const raw = real.exports.go_scheduler;
				ex.go_scheduler = function () {
					if (go.exited) return;
					const t = enter();
					try { return raw(); } finally { leave(t); }
				};
				inst = Object.create(real, { exports: { value: ex } });
			}
			// A hard kill marks the program exited — the _resume guard above then
			// turns every later callback into it into a no-op — cancels its
			// timers, and ends the wait for it. Called from the caller's stack,
			// never from inside the child's own slice, since one thread runs
			// one of them at a time.
			const killed = new Promise((res) => {
				stopRun = () => {
					go.exited = true;
					clearTimers(go);
					res();
				};
			});
			// run executes the first slice before it returns its promise, so the
			// page's stdio and directory come back as soon as that slice ends.
			const t = enter();
			let running;
			try { running = go.run(inst); } finally { leave(t); }
			try {
				// The program exits, or is killed. TinyGo's loader reports the
				// exit code as run's result rather than through go.exit.
				const ran = await Promise.race([running.then((c) => ({ c })), killed.then(() => null)]);
				if (ran && typeof ran.c === "number" && !exitHooked) exitCode = ran.c;
			} catch (e) {
				crashed = true;
				throw e;
			} finally {
				reap(id);
				stopRun = null;
				closeStdin(stdinR, stdinW);
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
				clearTimers(go);
				// JS callbacks the program registered can outlive it and keep go
				// reachable, and go.mem alone pins the whole wasm memory.
				go.mem = null;
				delete go._inst;
			}
			return exitCode;
		})().catch((e) => {
			// A failure to fetch, compile or instantiate never reaches the run
			// phase's cleanup, so do it here: the ring must still record how
			// this process ended, and nothing should be left registered.
			reap(id);
			closeStdin(stdinR, stdinW);
			if (rec && !rec.exitInfo) rec.exitInfo = { code: exitCode || 1, crashed: true };
			throw e;
		});
		return { pid, id, exited, kill: makeKill(id), stdin: stdinW >= 0 ? stdinOf(stdinW) : closedStdin, resize: makeResize(id) };
	}

	// makeResize is the handle's resize, built here so the handle a caller keeps
	// closes over the id alone and never over a spawn frame.
	function makeResize(id) {
		return (cols, rows) => resize(id, cols, rows);
	}

	// closedStdin is the stdin of a child spawned without a pipe.
	const closedStdin = Object.freeze({ write: () => false, close: () => {} });

	// stdinOf is the caller's end of a child's stdin pipe. A write after the
	// child has gone reports false rather than throwing.
	function stdinOf(w) {
		let open = true;
		return {
			write(b) {
				if (!open) return false;
				try { globalThis.fs.writeSync(w, b); return true; } catch (e) { return false; }
			},
			close() {
				if (!open) return;
				open = false;
				try { globalThis.fs.pipeRelease(w); } catch (e) { /* gone */ }
			},
		};
	}

	// closeStdin drops a finished child's stdin pipe. A read it was still
	// waiting in is forgotten, not answered: there is no program to answer.
	function closeStdin(r, w) {
		if (r >= 0) jsfs.pipeDrop(r);
		else if (w >= 0) jsfs.pipeDrop(w);
	}

	// clearTimers cancels the timer callbacks a Go runtime still had pending.
	function clearTimers(go) {
		if (!go._scheduledTimeouts) return;
		for (const h of go._scheduledTimeouts.values()) {
			try { clearTimeout(h); } catch (e) { /* ignore */ }
		}
		go._scheduledTimeouts.clear();
	}

	// idEnvNames normalises opts.idEnv: a string, a list, or the default.
	function idEnvNames(opts) {
		const v = opts.idEnv;
		if (!v) return ["BOTTLE_PID"];
		return typeof v === "string" ? [v] : v;
	}

	// resolveModule turns a resolved program into a compiled module: the cache
	// first, then a registered URL loader, then the bytes from jsfs.
	// Programs a caller keeps outside jsfs, compiled and kept by path for as
	// long as their stamp (whatever tells one version from the next: a size and
	// a time, say) stays the same. path -> { stamp, mod }
	const stamped = new Map();

	// cached reports whether path's program, as of stamp, is compiled and
	// kept, so a caller can spawn it with no bytes at all.
	function cached(path, stamp) {
		const e = stamped.get(path);
		return !!(e && e.stamp === stamp);
	}

	// programOf is what spawn runs: a compiled module given, a program kept
	// by stamp, bytes given, or argv[0] resolved in jsfs.
	function programOf(opts, argv0, cwd, env) {
		if (opts.module) return { path: argv0, module: opts.module };
		const stamp = opts.stamp === undefined || opts.stamp === null ? null : String(opts.stamp);
		if (stamp !== null && cached(argv0, stamp)) return { path: argv0, module: stamped.get(argv0).mod };
		if (opts.bytes) return { path: argv0, bytes: opts.bytes, stamp };
		return readProgram(argv0, cwd, env);
	}

	async function resolveModule(prog) {
		if (prog.module) return prog.module;
		if (prog.stamp !== undefined) {
			const mod = await WebAssembly.compile(prog.bytes);
			if (prog.stamp !== null) stamped.set(prog.path, { stamp: prog.stamp, mod });
			return mod;
		}
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
	// wasmExec is Go's loader, for a page that has none yet; wasmExecGo, when
	// set, takes its place, and wasmExecTinyGo is TinyGo's.
	const assets = {
		fsbridge: BASE + 'fsbridge.js',
		wasmExec: BASE + 'wasm_exec.js',
		wasmExecGo: '',
		wasmExecTinyGo: BASE + 'wasm_exec_tinygo.js',
	};

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
				const kind = kindOf(mod);
				const loader = kind === 'tinygo' ? assets.wasmExecTinyGo : (assets.wasmExecGo || assets.wasmExec);
				w.postMessage({ sab, mod, argv: argv.slice(), env, cwd, assets: Object.assign({}, assets, { wasmExec: loader }) });
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
		resize, tty: ttyOf, self, environ, argv: argvOf, cached,
		registerModule, registerURL, compileURL,
		// The two page-lifetime registries. Exposed so a page can name them
		// under its own globals (a child's Go signal handler registers into
		// proc.signals by whatever alias the page gave it) and so an operator
		// or a probe can read a dead process's last words from proc.tails.
		signals, tails,
	};
})();
