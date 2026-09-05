// fsbridge.js — jsfs, callable synchronously from a worker.
//
// proc.js runs every child on the one JS thread: its _resume wrapper swaps the
// active stdio set and jsfs's cwd around each synchronous execution slice, and
// that is only correct because nothing else can run. It also means a long
// compile freezes the page — `go install` of a real module graph pins the main
// thread for minutes.
//
// Moving a child to a Worker breaks the sharing proc.js depends on: a worker
// has its own global scope, so the parent's writes to $WORK are invisible to a
// compiler child. Rebuilding jsfs inside a SharedArrayBuffer would fix that and
// rewrite 700 lines of working filesystem.
//
// This does neither. jsfs stays exactly where it is, on the thread that already
// owns it, and workers reach it through a blocking channel:
//
//   owner (main thread)          worker (a child process)
//   ------------------           ------------------------
//   serve(sab)                   fs.readFile(path)
//     waitAsync on REQ             write args into sab
//     run the real jsfs call       Atomics.notify(REQ)
//     await its callback           Atomics.wait(RES)   <- blocks, sync to Go
//     write the reply              read reply, return
//     Atomics.notify(RES)
//
// Atomics.wait is forbidden on the main thread and allowed in workers, which is
// exactly the right way round: the owner never blocks, and the caller — whose
// Go runtime demands synchronous syscalls — does.
//
// Four things about jsfs shape this, and getting any of them wrong looks like a
// hang rather than an error:
//
//   1. Callbacks are delivered on a MICROTASK, on purpose (jsfs.js ~line 290):
//      synchronous delivery re-enters the wasm mid-syscall and overflows Go's
//      fixed g0 stack. So the owner AWAITS the callback, and the client hands
//      its own callback back on queueMicrotask — the deferral is preserved
//      end to end, and the worker's program must still hold a pending Go timer.
//   2. fs.read(fd, buf, ...) fills a caller-supplied buffer. The buffer cannot
//      cross as an argument; the owner reads into scratch and returns the bytes,
//      and the client copies them into the caller's buf at offset.
//   3. TextDecoder.decode() REFUSES a view onto a SharedArrayBuffer, so every
//      read out of the shared region copies first (copyOut below).
//   4. stat() returns is*() methods that JSON drops. They are pure functions of
//      mode, so the client rebuilds them exactly.
//
// One SAB serves one worker: the control slots are a single ping-pong slot, not
// a queue. Call serve() once per worker with its own buffer.
//
// Requires cross-origin isolation (COOP/COEP) for SharedArrayBuffer.
(function () {
	'use strict';
	if (globalThis.fsbridge) return;

	// Control slots (Int32Array over the head of the buffer).
	const REQ = 0;   // bumped by the caller when a request is ready
	const RES = 1;   // bumped by the owner when the reply is ready
	const JLEN = 2;  // byte length of the JSON header
	const ERR = 3;   // non-zero when the reply is an error
	const BLEN = 4;  // byte length of the binary tail that follows the header
	const HEAD = 32; // bytes reserved for control

	const enc = new TextEncoder();
	const dec = new TextDecoder();

	// Reads out of the shared region always copy: TextDecoder and several other
	// built-ins reject views backed by a SharedArrayBuffer.
	function copyOut(src, off, n) {
		const b = new Uint8Array(n);
		b.set(src.subarray(off, off + n));
		return b;
	}
	function decodeShared(src, off, n) { return dec.decode(copyOut(src, off, n)); }

	// Methods whose last argument is a Node-style callback.
	const CALLBACK_METHODS = [
		'open', 'close', 'read', 'write', 'fsync', 'stat', 'lstat', 'fstat',
		'mkdir', 'rmdir', 'unlink', 'rename', 'readdir', 'readlink', 'symlink',
		'link', 'truncate', 'ftruncate', 'chmod', 'fchmod', 'chown', 'fchown',
		'lchown', 'utimes',
	];
	// Methods that return their value directly rather than through a callback.
	// The client defines each one explicitly, since cwd is tracked per client.
	const DIRECT_METHODS = ['getCwd', 'setCwd', 'chdir', 'mkdirp'];

	const S_IFMT = 0o170000, S_IFDIR = 0o040000, S_IFREG = 0o100000;
	const S_IFLNK = 0o120000, S_IFCHR = 0o020000;

	// Marks the one argument carried in the binary tail instead of the JSON.
	const BIN = { __bin: true };

	// Which arguments of each method are paths, and so need absolutizing before
	// they leave the client. symlink's first argument is the link TARGET, which
	// is allowed to be relative and must be stored as written.
	const PATH_ARGS = {
		open: [0], stat: [0], lstat: [0], mkdir: [0], rmdir: [0], unlink: [0],
		rename: [0, 1], readdir: [0], readlink: [0], symlink: [1], link: [0, 1],
		truncate: [0], chmod: [0], chown: [0], lchown: [0], utimes: [0],
	};

	globalThis.fsbridge = {
		installed: true,
		HEAD: HEAD,

		// serve installs the owner side on the realm that holds the real jsfs.
		// Returns a stop function. The owner never blocks.
		serve(sab, targets) {
			// jsfs is split across three globals and the split is not cosmetic:
			// globalThis.fs holds the Node-style callback API, globalThis.jsfs the
			// seeding helpers (readFile/writeFile/mkdirp/getCwd/setCwd), and
			// globalThis.process holds chdir. Dispatch searches all three and
			// invokes each method on the object that actually owns it.
			const owners = (targets && targets.fs)
				? [targets.fs, targets.jsfs, targets.process]
				: [globalThis.fs, globalThis.jsfs, globalThis.process];
			const lookup = (m) => {
				for (const o of owners) if (o && typeof o[m] === 'function') return o;
				return null;
			};
			const ctl = new Int32Array(sab, 0, 8);
			const bytes = new Uint8Array(sab, HEAD);
			let stopped = false;
			let pendingOut = null;  // a reply tail too big for one message
			let pendingIn = [];     // request tail chunks pushed ahead of the call

			const reply = (out, tail) => {
				// A throw in here strands the client in Atomics.wait forever, so
				// every failure becomes an error reply instead.
				let j;
				try {
					j = enc.encode(JSON.stringify(out));
				} catch (e) {
					j = enc.encode(JSON.stringify({ err: 'fsbridge: unserializable reply: ' + ((e && e.message) || e) }));
				}
				if (j.length > bytes.length) {
					j = enc.encode(JSON.stringify({ err: 'fsbridge: reply header too large (' + j.length + ' bytes)' }));
					tail = null;
				}
				bytes.set(j, 0);
				let bn = 0;
				if (tail && tail.length) {
					bn = Math.min(bytes.length - j.length, tail.length);
					bytes.set(tail.subarray(0, bn), j.length);
				}
				Atomics.store(ctl, JLEN, j.length);
				Atomics.store(ctl, BLEN, bn);
				Atomics.store(ctl, ERR, out && out.err ? 1 : 0);
				Atomics.add(ctl, RES, 1);
				Atomics.notify(ctl, RES);
			};

			// served counts requests this owner has HANDLED. Re-reading REQ here
			// instead would lose a wakeup: between reply() and the next load, the
			// client can wake, send, and bump REQ, and the owner would then wait
			// for a bump that already happened while the client waits for a reply
			// that never comes. Single calls hide this — the owner almost always
			// wins the race — so it only appears under back-to-back traffic, which
			// is exactly what a compile produces.
			let served = Atomics.load(ctl, REQ);

			const pump = async () => {
				while (!stopped) {
					if (Atomics.load(ctl, REQ) === served) {
						const w = Atomics.waitAsync(ctl, REQ, served);
						if (w.async) {
							const r = await w.value;
							if (r === 'timed-out') continue;
						}
					}
					if (stopped) return;
					served++;

					let out, tail = null;
					try {
						const jn = Atomics.load(ctl, JLEN);
						const bn = Atomics.load(ctl, BLEN);
						const req = JSON.parse(decodeShared(bytes, 0, jn));
						if (globalThis.fsbridge.debug) globalThis.fsbridge.debug('req', req.m, jn, bn);
						const inTail = bn ? copyOut(bytes, jn, bn) : null;
						const r = await handle(req, inTail);
						out = r.out;
						tail = r.tail || null;
					} catch (e) {
						out = { err: String((e && e.message) || e), code: e && e.code };
					}

					if (globalThis.fsbridge.debug) globalThis.fsbridge.debug('reply', out && out.err ? out.err : 'ok', tail ? tail.length : 0);
					reply(out, tail);
				}
			};

			// big marks a reply whose tail does not fit in one message. pendingOut
			// holds the whole blob and is never rebased, so __pull offsets remain
			// absolute until the client has drained it.
			const big = (out, tail) => {
				if (tail && tail.length > bytes.length - 4096) {
					pendingOut = tail;
					out.total = tail.length;
				}
				return { out: out, tail: tail };
			};

			const handle = async (req, inTail) => {
				const m = req.m;
				if (m === '__init') {
					const g = lookup('getCwd');
					return { out: { v: { constants: owners[0].constants, cwd: g ? g.getCwd() : '/' } } };
				}
				if (m === '__ping') return { out: { v: 1 } };
				if (m === '__pull') {
					const off = req.a[0];
					return { out: { v: 1 }, tail: pendingOut.subarray(off) };
				}
				if (m === '__push') {
					pendingIn.push(inTail);
					return { out: { v: 1 } };
				}

				// Resolve the binary argument: either staged by earlier __push
				// calls, or riding along in this message's tail.
				const args = (req.a || []).map((a) => {
					if (a && a.__b) {
						if (a.staged) { const b = concat(pendingIn); pendingIn = []; return b; }
						return inTail || new Uint8Array(0);
					}
					return a;
				});

				// read() fills a caller-supplied buffer, which cannot cross as an
				// argument. Read into scratch and send the bytes back instead.
				if (m === 'read') {
					const [fd, , , length, position] = args;
					const scratch = new Uint8Array(length);
					const n = await cbCall(lookup('read'), 'read', [fd, scratch, 0, length, position]);
					return big({ v: { n: n } }, n > 0 ? scratch.subarray(0, n) : null);
				}
				// write() takes its bytes already sliced to [offset, offset+length).
				if (m === 'write') {
					const [fd, buf, , , position] = args;
					const n = await cbCall(lookup('write'), 'write', [fd, buf, 0, buf.length, position]);
					return { out: { v: { n: n } } };
				}

				if (CALLBACK_METHODS.indexOf(m) !== -1) {
					const v = await cbCall(lookup(m), m, args);
					return v instanceof Uint8Array
						? big({ v: { __b: true } }, v)
						: { out: { v: safeValue(v) } };
				}

				const owner = lookup(m);
				if (!owner) throw new Error('fsbridge: no method ' + m);
				const v = owner[m].apply(owner, args);
				return v instanceof Uint8Array
					? big({ v: { __b: true } }, v)
					: { out: { v: safeValue(v) } };
			};

			pump();
			return () => { stopped = true; Atomics.add(ctl, REQ, 1); Atomics.notify(ctl, REQ); };
		},

		// client returns an fs-shaped object for a worker. Every call blocks
		// until the owner answers, which is what makes it usable from a Go
		// runtime that expects synchronous syscalls.
		client(sab, opts) {
			opts = opts || {};
			const ctl = new Int32Array(sab, 0, 8);
			const bytes = new Uint8Array(sab, HEAD);
			const CHUNK = bytes.length - 4096; // headroom for the JSON header

			// One blocking round trip.
			const raw = (m, a, tail) => {
				const j = enc.encode(JSON.stringify({ m: m, a: a }));
				if (j.length > bytes.length) throw new Error('fsbridge: request header too large');
				bytes.set(j, 0);
				let bn = 0;
				if (tail && tail.length) {
					bn = Math.min(bytes.length - j.length, tail.length);
					bytes.set(tail.subarray(0, bn), j.length);
				}
				Atomics.store(ctl, JLEN, j.length);
				Atomics.store(ctl, BLEN, bn);
				const before = Atomics.load(ctl, RES);
				Atomics.add(ctl, REQ, 1);
				Atomics.notify(ctl, REQ);
				// Blocking is the point. The owner is on the main thread and never
				// blocks back, so this cannot deadlock on it.
				while (Atomics.load(ctl, RES) === before) {
					Atomics.wait(ctl, RES, before, 30000);
				}
				const jn = Atomics.load(ctl, JLEN);
				const tn = Atomics.load(ctl, BLEN);
				const out = JSON.parse(decodeShared(bytes, 0, jn));
				const rtail = tn ? copyOut(bytes, jn, tn) : null;
				if (out && out.err) throw Object.assign(new Error(out.err), { code: out.code });
				return { v: out.v, tail: rtail, total: out.total };
			};

			// send stages an oversized binary argument ahead of the call.
			const send = (m, a, tail) => {
				if (tail && tail.length > CHUNK) {
					for (let off = 0; off < tail.length; off += CHUNK) {
						raw('__push', [], tail.subarray(off, Math.min(off + CHUNK, tail.length)));
					}
					return raw(m, a.map((x) => (x === BIN ? { __b: true, staged: true } : x)), null);
				}
				return raw(m, a.map((x) => (x === BIN ? { __b: true } : x)), tail);
			};

			// recvTail reassembles a reply too big for one message.
			const recvTail = (r) => {
				if (r.total == null) return r.tail || new Uint8Array(0);
				const out = new Uint8Array(r.total);
				let off = 0;
				if (r.tail) { out.set(r.tail, 0); off = r.tail.length; }
				while (off < r.total) {
					const p = raw('__pull', [off], null);
					if (!p.tail || !p.tail.length) throw new Error('fsbridge: short pull at ' + off + '/' + r.total);
					out.set(p.tail, off);
					off += p.tail.length;
				}
				return out;
			};

			const init = raw('__init', [], null).v;

			// Every path argument is made absolute HERE, on the client, because
			// jsfs resolves relative paths against one module-global cwd. proc.js
			// gets away with that on the main thread by swapping the cwd around
			// each synchronous slice — correct only because nothing else can run.
			// A worker runs concurrently, so a shared cwd would let one process's
			// chdir silently change another's path resolution mid-call. Each
			// client carries its own cwd and sends absolute paths, and the owner's
			// cwd stops mattering.
			let cwd = opts.cwd || init.cwd || '/';

			const absPath = (p) => {
				if (typeof p !== 'string') return p;
				const joined = p.charAt(0) === '/' ? p : cwd + '/' + p;
				const out = [];
				for (const part of joined.split('/')) {
					if (!part || part === '.') continue;
					if (part === '..') { out.pop(); continue; }
					out.push(part);
				}
				return '/' + out.join('/');
			};
			const fix = (m, args) => {
				const idx = PATH_ARGS[m];
				if (!idx) return args;
				const a = args.slice();
				for (const i of idx) a[i] = absPath(a[i]);
				return a;
			};

			const shim = { __bridged: true, constants: init.constants };

			shim.getCwd = function () { return cwd; };
			// chdir validates before committing, so a failed chdir leaves the
			// process where it was rather than silently pointing at nothing.
			const goTo = function (dir) {
				const p = absPath(dir);
				const st = send('stat', [p], null).v;
				if (!st || (st.mode & 0o170000) !== 0o040000) {
					throw Object.assign(new Error('ENOTDIR: ' + p), { code: 'ENOTDIR' });
				}
				cwd = p;
			};
			shim.chdir = goTo;
			shim.setCwd = goTo;
			shim.mkdirp = function (path, mode) { return send('mkdirp', [absPath(path), mode], null).v; };
			shim.writeSync = function (fd, buf) { return send('writeSync', [fd, BIN], buf).v; };
			shim.writeFile = function (path, data) { return send('writeFile', [absPath(path), BIN], data).v; };
			shim.readFile = function (path) {
				const r = send('readFile', [absPath(path)], null);
				if (!r.v) return null;
				return recvTail(r);
			};

			for (const m of CALLBACK_METHODS) {
				shim[m] = function (...args) {
					const cb = args.pop();
					let v;
					try {
						const r = send(m, fix(m, args), null);
						v = r.v && r.v.__b ? recvTail(r) : reviveStat(r.v);
					} catch (e) { queueMicrotask(() => cb(e)); return; }
					// The microtask deferral is jsfs's contract, not an accident:
					// calling back inline would re-enter the wasm mid-syscall.
					queueMicrotask(() => cb(null, v));
				};
			}

			shim.read = function (fd, buf, offset, length, position, cb) {
				let n;
				try {
					const r = send('read', [fd, null, 0, length, position], null);
					n = r.v.n;
					if (n > 0) buf.set(recvTail(r).subarray(0, n), offset);
				} catch (e) { queueMicrotask(() => cb(e)); return; }
				queueMicrotask(() => cb(null, n));
			};
			shim.write = function (fd, buf, offset, length, position, cb) {
				let n;
				try {
					const sub = buf.subarray(offset, offset + length);
					n = send('write', [fd, BIN, 0, length, position], sub).v.n;
				} catch (e) { queueMicrotask(() => cb(e)); return; }
				queueMicrotask(() => cb(null, n));
			};

			return shim;
		},

		// install wires a worker's globals to the owner's filesystem, matching the
		// three-object layout jsfs publishes on the main thread. A child running
		// here sees the same paths, the same cwd and the same bytes as its parent.
		// stdio is deliberately absent: a worker child's streams are routed by
		// proc.js, not by the filesystem bridge.
		install(sab, opts) {
			const shim = globalThis.fsbridge.client(sab, opts);
			globalThis.fs = shim;
			globalThis.jsfs = {
				installed: true,
				bridged: true,
				readFile: shim.readFile,
				writeFile: shim.writeFile,
				mkdirp: shim.mkdirp,
				getCwd: shim.getCwd,
				setCwd: shim.setCwd,
			};
			globalThis.process = Object.assign(globalThis.process || {}, {
				chdir: shim.chdir,
				cwd: () => shim.getCwd(),
			});
			return shim;
		},
	};

	function concat(chunks) {
		let n = 0;
		for (const c of chunks) n += c.length;
		const out = new Uint8Array(n);
		let off = 0;
		for (const c of chunks) { out.set(c, off); off += c.length; }
		return out;
	}

	function cbCall(owner, m, args) {
		return new Promise((res, rej) => {
			if (!owner || typeof owner[m] !== 'function') { rej(new Error('fsbridge: no method ' + m)); return; }
			owner[m].apply(owner, args.concat([(err, v) => (err ? rej(err) : res(v))]));
		});
	}

	// stat objects carry is*() methods that JSON drops; strip them on the way
	// out and rebuild them from mode on the way in. They are pure functions of
	// mode, so the reconstruction is exact.
	function safeValue(v) {
		if (v === undefined || v === null) return null;
		const t = typeof v;
		if (t === 'string' || t === 'number' || t === 'boolean') return v;
		if (Array.isArray(v)) return v.map((x) => (x && typeof x === 'object' ? safeValue(x) : x));
		if (t === 'object') {
			if (typeof v.isDirectory === 'function') return plainStat(v);
			// A jsfs node or anything else internal: not meaningful here.
			return null;
		}
		return null;
	}

	function plainStat(v) {
		if (!v || typeof v !== 'object' || typeof v.isDirectory !== 'function') return v === undefined ? null : v;
		const o = {};
		for (const k of Object.keys(v)) if (typeof v[k] !== 'function') o[k] = v[k];
		o.__stat = true;
		return o;
	}
	function reviveStat(v) {
		if (!v || typeof v !== 'object' || !v.__stat) return v;
		const mode = v.mode;
		v.isBlockDevice = () => false;
		v.isCharacterDevice = () => (mode & S_IFMT) === S_IFCHR;
		v.isDirectory = () => (mode & S_IFMT) === S_IFDIR;
		v.isFIFO = () => false;
		v.isFile = () => (mode & S_IFMT) === S_IFREG;
		v.isSocket = () => false;
		v.isSymbolicLink = () => (mode & S_IFMT) === S_IFLNK;
		return v;
	}
})();
