// jsfs.js — an in-memory POSIX-ish filesystem installed
// as globalThis.fs / globalThis.process BEFORE any Go wasm instance starts.
//
// Go's js/wasm runtime routes the ENTIRE os package through this shim
// (syscall/fs_js.go calls the node-style callback API; wasm_exec.js only
// stubs it with ENOSYS). Installing a real implementation means every Go
// instance on the page shares ONE filesystem, laid out like a Linux root —
// one program writes /etc or /opt files, another (a shell's cat/jq, a second
// wasm instance) reads them back, exactly as processes on a host would.
//
// A generic FHS skeleton is seeded here; application-specific layout (an
// installed package tree, config files) is the PAGE's job, via the exposed
// jsfs.mkdirp / jsfs.writeFile helpers, in a script loaded after this one.
//
// Contract implemented (what syscall/fs_js.go actually calls):
//   fs.constants, fs.writeSync(fd,buf) and callback-style open close read
//   write stat lstat fstat mkdir rmdir readdir rename unlink truncate
//   ftruncate chmod fchmod chown fchown lchown lchmod? no — utimes readlink
//   link symlink fsync; process.cwd chdir getuid getgid geteuid getegid
//   getgroups umask pid ppid.
// Errors are objects with a .code string ("ENOENT", ...) as Go expects.
//
// Stdout/stderr: fds 1 and 2 route through jsfs.stdio, a swappable sink so
// the terminal can capture the CURRENT command's output; instances run
// sequentially in the shell, so a single active sink suffices. fd 0 reads
// return EOF by default.
//
// In-memory only: page lifetime, no quota, no persistence (an IndexedDB
// snapshot can layer on later without changing this contract).
(function () {
	'use strict';
	if (globalThis.jsfs && globalThis.jsfs.installed) return;

	const enosys = (sc) => { const e = new Error(sc + ': not implemented'); e.code = 'ENOSYS'; return e; };
	const mkerr = (code, msg) => { const e = new Error(msg || code); e.code = code; return e; };

	// ---- inode layer -------------------------------------------------------
	const S_IFDIR = 0o040000, S_IFREG = 0o100000, S_IFLNK = 0o120000, S_IFCHR = 0o020000;
	let nextIno = 2;
	const now = () => Date.now();

	function mknode(type, mode) {
		return {
			ino: nextIno++,
			mode: (type | (mode & 0o7777)) >>> 0,
			uid: 0, gid: 0, nlink: 1,
			atimeMs: now(), mtimeMs: now(), ctimeMs: now(),
			// dirs: Map(name -> node); files: Uint8Array; symlinks: target string
			entries: type === S_IFDIR ? new Map() : null,
			data: type === S_IFREG ? new Uint8Array(0) : null,
			target: type === S_IFLNK ? '' : null,
			dev: type === S_IFCHR ? true : false,
		};
	}

	const root = mknode(S_IFDIR, 0o755);
	root.ino = 1;

	// ---- path resolution ---------------------------------------------------
	let cwd = '/home/user';

	function normalize(path) {
		if (typeof path !== 'string' || path === '') return null;
		let p = path.startsWith('/') ? path : cwd + '/' + path;
		const out = [];
		for (const part of p.split('/')) {
			if (part === '' || part === '.') continue;
			if (part === '..') { out.pop(); continue; }
			out.push(part);
		}
		return '/' + out.join('/');
	}

	// resolve returns {node, parent, name} or throws. followLinks resolves a
	// trailing symlink; intermediate symlinks are always resolved.
	function resolve(path, followLinks, depth) {
		depth = depth || 0;
		if (depth > 40) throw mkerr('ELOOP', 'too many symlinks');
		const norm = normalize(path);
		if (norm === null) throw mkerr('ENOENT', 'bad path');
		if (norm === '/') return { node: root, parent: null, name: '/' };
		const parts = norm.slice(1).split('/');
		let cur = root;
		for (let i = 0; i < parts.length; i++) {
			if (!cur.entries) throw mkerr('ENOTDIR', norm);
			let child = cur.entries.get(parts[i]);
			if (!child) {
				if (i === parts.length - 1) return { node: null, parent: cur, name: parts[i] };
				throw mkerr('ENOENT', norm);
			}
			if (child.target !== null && (i < parts.length - 1 || followLinks)) {
				const rest = parts.slice(i + 1).join('/');
				const tgt = child.target.startsWith('/') ? child.target : '/' + parts.slice(0, i).join('/') + '/' + child.target;
				return resolve(rest ? tgt + '/' + rest : tgt, followLinks, depth + 1);
			}
			cur = child;
		}
		// find the parent again for {parent,name}
		let parent = root;
		for (let i = 0; i < parts.length - 1; i++) parent = parent.entries.get(parts[i]);
		return { node: cur, parent, name: parts[parts.length - 1] };
	}

	function statOf(node) {
		const isFile = node.data !== null;
		// A lazy file reports its real size before its bytes are fetched, so a
		// caller that stats-then-reads (os.ReadFile) asks for the whole file.
		const size = isFile ? (node.lazy ? node.lazy.size : node.data.length) : (node.target !== null ? node.target.length : 64);
		return {
			dev: 1, ino: node.ino, mode: node.mode, nlink: node.nlink,
			uid: node.uid, gid: node.gid, rdev: 0, size,
			blksize: 4096, blocks: Math.max(1, Math.ceil(size / 512)),
			atimeMs: node.atimeMs, mtimeMs: node.mtimeMs, ctimeMs: node.ctimeMs,
			isBlockDevice: () => false, isCharacterDevice: () => (node.mode & 0o170000) === S_IFCHR,
			isDirectory: () => (node.mode & 0o170000) === S_IFDIR,
			isFIFO: () => false, isFile: () => (node.mode & 0o170000) === S_IFREG,
			isSocket: () => false, isSymbolicLink: () => (node.mode & 0o170000) === S_IFLNK,
		};
	}

	// ---- seed a Linux-ish layout ------------------------------------------
	function mkdirp(path, mode) {
		const parts = normalize(path).slice(1).split('/').filter(Boolean);
		let cur = root;
		for (const part of parts) {
			let child = cur.entries.get(part);
			if (!child) { child = mknode(S_IFDIR, mode === undefined ? 0o755 : mode); cur.entries.set(part, child); }
			cur = child;
		}
		return cur;
	}
	function writeFileSeed(path, content, mode) {
		const norm = normalize(path);
		const dir = mkdirp(norm.slice(0, norm.lastIndexOf('/')) || '/');
		const f = mknode(S_IFREG, mode === undefined ? 0o644 : mode);
		f.data = typeof content === 'string' ? new TextEncoder().encode(content) : content;
		dir.entries.set(norm.slice(norm.lastIndexOf('/') + 1), f);
		return f;
	}
	// writeLazy seeds a file whose bytes are fetched from url on first read. The
	// tree, names and sizes are present up front (cheap); content arrives only
	// when something opens the file — so a caller seeds a whole source tree and
	// pays network only for the files a build actually touches. Called after a
	// mutation hook is installed, a lazy read's populate marks the fs dirty, so
	// the fetched bytes persist like any write.
	let lazyDirtyHook = null;
	function writeLazy(path, size, url) {
		const norm = normalize(path);
		const dir = mkdirp(norm.slice(0, norm.lastIndexOf('/')) || '/');
		const f = mknode(S_IFREG, 0o644);
		f.data = new Uint8Array(0);
		f.lazy = { size: size, url: url };
		dir.entries.set(norm.slice(norm.lastIndexOf('/') + 1), f);
		return f;
	}
	// readData serves a read from a file's resident bytes (the non-lazy path,
	// and what a lazy read runs once its bytes have landed).
	function readData(e, buf, offset, length, position, cb) {
		const pos = (position === null || position === undefined) ? e.pos : position;
		const avail = e.node.data.length - pos;
		const n = Math.max(0, Math.min(length, avail));
		if (n > 0) buf.set(e.node.data.subarray(pos, pos + n), offset);
		if (position === null || position === undefined) e.pos += n;
		queueMicrotask(() => cb(null, n));
	}

	['/bin', '/dev', '/etc', '/home/user', '/opt', '/proc', '/root', '/run', '/sys',
		'/usr/bin', '/var/log',
	].forEach((d) => mkdirp(d));
	mkdirp('/tmp', 0o777);
	const devNull = mknode(S_IFCHR, 0o666);
	resolve('/dev').node.entries.set('null', devNull);
	writeFileSeed('/etc/hostname', 'bottle\n');
	writeFileSeed('/etc/os-release', 'PRETTY_NAME="Bottle (wasm)"\nID=bottle\n');

	// ---- stdio sinks -------------------------------------------------------
	const td = new TextDecoder();
	const stdio = {
		stdout: (buf) => console.log(td.decode(buf)),
		stderr: (buf) => console.error(td.decode(buf)),
		stdin: () => null, // return Uint8Array or null for EOF
	};

	// ---- fd table ----------------------------------------------------------
	// 0,1,2 reserved.
	const fds = new Map();
	let nextFd = 3;

	function fdEntry(fd) {
		const e = fds.get(fd);
		if (!e) throw mkerr('EBADF', 'fd ' + fd);
		return e;
	}

	// ---- pipes -------------------------------------------------------------
	// os.Pipe / os/exec need pipes, which a wasm tab has no OS analog for.
	// A pipe is a shared byte queue with a read end and a write end fd. Both
	// ends live here in JS so a writer in one wasm instance and a reader in
	// another meet in the page — never re-entering each other's Go runtime.
	// A read on an empty pipe defers its callback until a write or a close
	// arrives, so syscall.Read blocks through the same callback mechanism the
	// filesystem calls already use.
	const pipeEnds = new Map(); // fd -> {pipe, write}
	function makePipe() {
		const p = { chunks: [], wRefs: 1, rRefs: 1, readers: [] };
		const r = nextFd++;
		const w = nextFd++;
		pipeEnds.set(r, { pipe: p, write: false });
		pipeEnds.set(w, { pipe: p, write: true });
		return [r, w];
	}
	// Retain another reference to fd's pipe end, so a later close of the caller's
	// fd does not tear the pipe down while the retainer still holds it. Used by
	// StartProcess: os/exec closes the parent's copy of a child's stdio fd right
	// after spawn, but the child still needs it (as a Unix child would hold its
	// own dup). Returns the same fd for convenience.
	function pipeRetain(fd) {
		const e = pipeEnds.get(fd);
		if (e) { if (e.write) e.pipe.wRefs++; else e.pipe.rRefs++; }
		return fd;
	}
	function pipeDeliver(p) {
		// Fulfill as many waiting readers as the queued bytes (and EOF) allow.
		while (p.readers.length) {
			const total = p.chunks.reduce((n, c) => n + c.length, 0);
			if (total === 0) {
				if (p.wRefs <= 0) {
					const w = p.readers.shift();
					queueMicrotask(() => w.cb(null, 0)); // EOF
					continue;
				}
				break; // nothing to give, writer still open: keep waiting
			}
			const w = p.readers.shift();
			let need = w.length, got = 0;
			while (need > 0 && p.chunks.length) {
				const c = p.chunks[0];
				const take = Math.min(need, c.length);
				w.buf.set(c.subarray(0, take), w.offset + got);
				got += take; need -= take;
				if (take === c.length) p.chunks.shift();
				else p.chunks[0] = c.subarray(take);
			}
			queueMicrotask(() => w.cb(null, got));
		}
	}
	function pipeReadInto(fd, buf, offset, length, cb) {
		const e = pipeEnds.get(fd);
		if (!e || e.write) { queueMicrotask(() => cb(mkerr('EBADF', 'pipe read fd ' + fd))); return; }
		e.pipe.readers.push({ buf, offset, length, cb });
		pipeDeliver(e.pipe);
	}
	function pipeWriteFrom(fd, sub) {
		const e = pipeEnds.get(fd);
		if (!e || !e.write) throw mkerr('EBADF', 'pipe write fd ' + fd);
		if (e.pipe.rRefs <= 0) throw mkerr('EPIPE', 'pipe read end closed');
		e.pipe.chunks.push(sub.slice()); // copy: caller may reuse the buffer
		pipeDeliver(e.pipe);
		return sub.length;
	}
	function pipeClose(fd) {
		const e = pipeEnds.get(fd);
		if (!e) return false;
		// The SAME fd number can hold more than one reference — the creator's,
		// plus one retained for a spawned child (see pipeRetain). Decrement,
		// and only drop the fd entry once the last reference is gone, so a
		// later close of the retained reference can still find it. Deleting on
		// the first close would strand the retained reference and the reader
		// would never see EOF.
		if (e.write) {
			if (--e.pipe.wRefs <= 0) pipeEnds.delete(fd);
		} else {
			if (--e.pipe.rRefs <= 0) pipeEnds.delete(fd);
		}
		pipeDeliver(e.pipe); // a now-zero writer count lets readers see EOF
		return true;
	}
	function isPipe(fd) { return pipeEnds.has(fd); }

	// ---- constants (node names; values mirror Linux) -----------------------
	const constants = {
		O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2,
		O_CREAT: 0o100, O_EXCL: 0o200, O_TRUNC: 0o1000,
		O_APPEND: 0o2000, O_DIRECTORY: 0o200000, O_NONBLOCK: 0o4000, O_SYNC: 0o4010000,
	};

	// ---- the fs object -----------------------------------------------------
	function wrap(fn) {
		// turn a sync impl into the node callback convention with proper
		// asynchrony: the callback is delivered on a MICROTASK, after the
		// calling wasm's stack has unwound. Synchronous delivery would
		// re-enter the wasm mid-syscall (nested _resume) and a long run of
		// fs ops then overflows Go's fixed g0 stack. The deferral means the
		// syscall goroutine parks — so the PROGRAM must always hold at least
		// one pending Go timer (an idle loop that sleeps, not select{}), or
		// the Go scheduler declares "all goroutines are asleep" before the
		// microtask can run. Every skywire wasm entrypoint does.
		return function (...args) {
			const cb = args.pop();
			let res;
			try { res = fn(...args); } catch (e) { queueMicrotask(() => cb(e)); return; }
			queueMicrotask(() => cb(null, res));
		};
	}

	const fsImpl = {
		constants,

		pipe() { return makePipe(); },     // [readFd, writeFd] — used by syscall.Pipe
		pipeRetain(fd) { return pipeRetain(fd); },
		pipeRelease(fd) { return pipeClose(fd); }, // synchronous close of a pipe fd
		isPipe(fd) { return isPipe(fd); },

		writeSync(fd, buf) {
			if (fd === 1) { stdio.stdout(buf); return buf.length; }
			if (fd === 2) { stdio.stderr(buf); return buf.length; }
			if (isPipe(fd)) return pipeWriteFrom(fd, buf);
			const e = fdEntry(fd);
			return writeAt(e, buf, null);
		},

		write(fd, buf, offset, length, position, cb) {
			try {
				const sub = buf.subarray(offset, offset + length);
				if (fd === 1 || fd === 2) {
					if (position !== null && position !== undefined) throw mkerr('ESPIPE', 'seek on tty');
					const n = fsImpl.writeSync(fd, sub);
					queueMicrotask(() => cb(null, n)); return;
				}
				if (isPipe(fd)) { const n = pipeWriteFrom(fd, sub); queueMicrotask(() => cb(null, n)); return; }
				const e = fdEntry(fd);
				const n = writeAt(e, sub, position === undefined ? null : position);
				queueMicrotask(() => cb(null, n));
			} catch (err) { queueMicrotask(() => cb(err)); }
		},

		read(fd, buf, offset, length, position, cb) {
			try {
				if (fd === 0) {
					const chunk = stdio.stdin();
					if (!chunk || chunk.length === 0) { queueMicrotask(() => cb(null, 0)); return; }
					const n = Math.min(length, chunk.length);
					buf.set(chunk.subarray(0, n), offset);
					queueMicrotask(() => cb(null, n)); return;
				}
				if (isPipe(fd)) { pipeReadInto(fd, buf, offset, length, cb); return; }
				const e = fdEntry(fd);
				if (e.node.dev) { queueMicrotask(() => cb(null, 0)); return; } // /dev/null
				if (e.node.data === null) throw mkerr('EISDIR', 'read dir');
				// A lazy file's bytes arrive on first read: fetch, populate, mark
				// dirty so they persist, then serve this read and every later one
				// from the now-resident data.
				if (e.node.lazy) {
					const lz = e.node.lazy;
					fetch(lz.url).then((r) => {
						if (!r.ok) throw new Error('lazy fetch ' + lz.url + ': ' + r.status);
						return r.arrayBuffer();
					}).then((ab) => {
						e.node.data = new Uint8Array(ab);
						e.node.lazy = null;
						if (lazyDirtyHook) { try { lazyDirtyHook(); } catch (e2) { /* best-effort */ } }
						readData(e, buf, offset, length, position, cb);
					}).catch((err) => queueMicrotask(() => cb(err)));
					return;
				}
				readData(e, buf, offset, length, position, cb);
			} catch (err) { queueMicrotask(() => cb(err)); }
		},

		open: wrap((path, flags, mode) => {
			const acc = flags & 3;
			let r;
			try {
				r = resolve(path, true);
			} catch (e) { throw e; }
			let node = r.node;
			if (!node) {
				if (!(flags & constants.O_CREAT)) throw mkerr('ENOENT', path);
				if (!r.parent) throw mkerr('ENOENT', path);
				node = mknode(S_IFREG, mode);
				r.parent.entries.set(r.name, node);
				r.parent.mtimeMs = now();
			} else {
				if ((flags & constants.O_CREAT) && (flags & constants.O_EXCL)) throw mkerr('EEXIST', path);
				if ((flags & constants.O_DIRECTORY) && node.entries === null) throw mkerr('ENOTDIR', path);
				if (node.entries !== null && acc !== constants.O_RDONLY) throw mkerr('EISDIR', path);
				if ((flags & constants.O_TRUNC) && node.data !== null) { node.data = new Uint8Array(0); node.mtimeMs = now(); }
			}
			const fd = nextFd++;
			// path is kept so persistence can tell whether a WRITE to this fd could
			// change what a snapshot contains; see hookMutators.
			fds.set(fd, { node, pos: (flags & constants.O_APPEND) && node.data ? node.data.length : 0, flags, path: normalize(path) });
			return fd;
		}),

		close: wrap((fd) => { if (pipeClose(fd)) return undefined; fdEntry(fd); fds.delete(fd); return undefined; }),

		stat: wrap((path) => { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); return statOf(r.node); }),
		lstat: wrap((path) => { const r = resolve(path, false); if (!r.node) throw mkerr('ENOENT', path); return statOf(r.node); }),
		fstat: wrap((fd) => statOf(fdEntry(fd).node)),

		mkdir: wrap((path, perm) => {
			const r = resolve(path, true);
			if (r.node) throw mkerr('EEXIST', path);
			if (!r.parent) throw mkerr('ENOENT', path);
			r.parent.entries.set(r.name, mknode(S_IFDIR, perm));
			r.parent.mtimeMs = now();
			return undefined;
		}),

		rmdir: wrap((path) => {
			const r = resolve(path, false);
			if (!r.node) throw mkerr('ENOENT', path);
			if (r.node.entries === null) throw mkerr('ENOTDIR', path);
			if (r.node.entries.size > 0) throw mkerr('ENOTEMPTY', path);
			r.parent.entries.delete(r.name);
			return undefined;
		}),

		readdir: wrap((path) => {
			const r = resolve(path, true);
			if (!r.node) throw mkerr('ENOENT', path);
			if (r.node.entries === null) throw mkerr('ENOTDIR', path);
			return Array.from(r.node.entries.keys());
		}),

		rename: wrap((from, to) => {
			const rf = resolve(from, false);
			if (!rf.node) throw mkerr('ENOENT', from);
			const rt = resolve(to, false);
			if (!rt.parent) throw mkerr('ENOENT', to);
			rf.parent.entries.delete(rf.name);
			rt.parent.entries.set(rt.name, rf.node);
			rf.node.ctimeMs = now();
			return undefined;
		}),

		unlink: wrap((path) => {
			const r = resolve(path, false);
			if (!r.node) throw mkerr('ENOENT', path);
			if (r.node.entries !== null) throw mkerr('EISDIR', path);
			r.parent.entries.delete(r.name);
			return undefined;
		}),

		truncate: wrap((path, length) => {
			const r = resolve(path, true);
			if (!r.node || r.node.data === null) throw mkerr('ENOENT', path);
			r.node.data = resized(r.node.data, length);
			r.node.mtimeMs = now();
			return undefined;
		}),
		ftruncate: wrap((fd, length) => {
			const e = fdEntry(fd);
			if (e.node.data === null) throw mkerr('EINVAL', 'not a file');
			e.node.data = resized(e.node.data, length);
			e.node.mtimeMs = now();
			return undefined;
		}),

		chmod: wrap((path, mode) => { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); r.node.mode = ((r.node.mode & 0o170000) | (mode & 0o7777)) >>> 0; return undefined; }),
		fchmod: wrap((fd, mode) => { const e = fdEntry(fd); e.node.mode = ((e.node.mode & 0o170000) | (mode & 0o7777)) >>> 0; return undefined; }),
		chown: wrap((path, uid, gid) => { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); r.node.uid = uid; r.node.gid = gid; return undefined; }),
		fchown: wrap((fd, uid, gid) => { const e = fdEntry(fd); e.node.uid = uid; e.node.gid = gid; return undefined; }),
		lchown: wrap((path, uid, gid) => { const r = resolve(path, false); if (!r.node) throw mkerr('ENOENT', path); r.node.uid = uid; r.node.gid = gid; return undefined; }),
		utimes: wrap((path, atime, mtime) => { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); r.node.atimeMs = atime * 1000; r.node.mtimeMs = mtime * 1000; return undefined; }),

		readlink: wrap((path) => { const r = resolve(path, false); if (!r.node) throw mkerr('ENOENT', path); if (r.node.target === null) throw mkerr('EINVAL', path); return r.node.target; }),
		link: wrap((from, to) => {
			const rf = resolve(from, true);
			if (!rf.node) throw mkerr('ENOENT', from);
			const rt = resolve(to, false);
			if (rt.node) throw mkerr('EEXIST', to);
			rt.parent.entries.set(rt.name, rf.node);
			rf.node.nlink++;
			return undefined;
		}),
		symlink: wrap((target, path) => {
			const r = resolve(path, false);
			if (r.node) throw mkerr('EEXIST', path);
			const ln = mknode(S_IFLNK, 0o777);
			ln.target = target;
			r.parent.entries.set(r.name, ln);
			return undefined;
		}),
		fsync: wrap((fd) => { fdEntry(fd); return undefined; }),
	};

	function resized(data, length) {
		if (length === data.length) return data;
		const nd = new Uint8Array(length);
		nd.set(data.subarray(0, Math.min(length, data.length)));
		return nd;
	}

	function writeAt(e, sub, position) {
		if (e.node.dev) return sub.length; // /dev/null
		if (e.node.data === null) throw mkerr('EISDIR', 'write dir');
		let pos = (position === null) ? ((e.flags & constants.O_APPEND) ? e.node.data.length : e.pos) : position;
		const end = pos + sub.length;
		if (end > e.node.data.length) e.node.data = resized(e.node.data, end);
		e.node.data.set(sub, pos);
		if (position === null) e.pos = pos + sub.length;
		e.node.mtimeMs = now();
		return sub.length;
	}

	// ---- process shim ------------------------------------------------------
	const processImpl = {
		getuid() { return 0; }, getgid() { return 0; },
		geteuid() { return 0; }, getegid() { return 0; },
		getgroups() { return [0]; },
		pid: 1, ppid: 0,
		umask() { return 0o22; },
		cwd() { return cwd; },
		chdir(dir) {
			const r = resolve(dir, true);
			if (!r.node) throw mkerr('ENOENT', dir);
			if (r.node.entries === null) throw mkerr('ENOTDIR', dir);
			cwd = normalize(dir);
		},
	};

	// ---- persistence (IndexedDB) ------------------------------------------
	// Optional whole-tree snapshots so the filesystem survives page reloads.
	// jsfs.persist.enable(dbName) restores the last snapshot (REPLACING the
	// seeded tree — deletions persist too) and then auto-saves: mutating
	// syscalls mark the tree dirty, a debounce+floor batches the writes, and
	// pagehide flushes best-effort. Typed arrays structured-clone into
	// IndexedDB directly, so file data round-trips byte-exact. No-op (resolves
	// {restored:false}) where IndexedDB is unavailable (node, workers without
	// IDB) — the contract stays purely in-memory there.
	const persist = (() => {
		const STORE = 'tree';
		const KEY = 'root';
		let db = null;
		let enabled = false;
		let dirty = false;
		let timer = null;
		let lastSave = 0;
		let saveChain = Promise.resolve();
		// exclude(path) → true skips the file (and, for a dir, its whole
		// subtree) from snapshots. The host uses it to keep DATABASES out:
		// a store snapshotted mid-write restores corrupt and can hang its
		// consumer on the next boot — runtime caches must be rebuilt, not
		// carried. Configs, keys and user files persist; caches don't.
		let excludeFn = null;

		function serialize() {
			const out = [];
			(function walk(node, path) {
				for (const [name, child] of node.entries) {
					const p = path + '/' + name;
					if (excludeFn && excludeFn(p)) continue;
					const t = child.mode & 0o170000;
					if (t === S_IFDIR) {
						out.push({ p, t: 'd', m: child.mode & 0o7777, mt: child.mtimeMs });
						walk(child, p);
					} else if (t === S_IFREG) {
						out.push({ p, t: 'f', m: child.mode & 0o7777, mt: child.mtimeMs, d: child.data.slice() });
					} else if (t === S_IFLNK) {
						out.push({ p, t: 'l', m: child.mode & 0o7777, mt: child.mtimeMs, tgt: child.target });
					} else if (t === S_IFCHR) {
						out.push({ p, t: 'c', m: child.mode & 0o7777, mt: child.mtimeMs });
					}
				}
			})(root, '');
			return out;
		}

		function applySnapshot(entries) {
			root.entries.clear();
			for (const e of entries) {
				// serialize() emits parents before children, so the parent dir
				// always exists by the time its entries arrive.
				const slash = e.p.lastIndexOf('/');
				const parent = slash === 0 ? root : resolve(e.p.slice(0, slash), true).node;
				const name = e.p.slice(slash + 1);
				let node;
				switch (e.t) {
				case 'd': node = mknode(S_IFDIR, e.m); break;
				case 'f':
					node = mknode(S_IFREG, e.m);
					node.data = e.d instanceof Uint8Array ? e.d : new Uint8Array(e.d);
					break;
				case 'l': node = mknode(S_IFLNK, e.m); node.target = e.tgt || ''; break;
				case 'c': node = mknode(S_IFCHR, e.m); break;
				default: continue;
				}
				node.mtimeMs = e.mt || now();
				parent.entries.set(name, node);
			}
		}

		function idbOpen(name) {
			return new Promise((res, rej) => {
				const rq = indexedDB.open(name, 1);
				rq.onupgradeneeded = () => { rq.result.createObjectStore(STORE); };
				rq.onsuccess = () => res(rq.result);
				rq.onerror = () => rej(rq.error);
			});
		}
		function idbPut(val) {
			return new Promise((res, rej) => {
				const tx = db.transaction(STORE, 'readwrite');
				tx.objectStore(STORE).put(val, KEY);
				tx.oncomplete = () => res();
				tx.onerror = () => rej(tx.error);
			});
		}
		function idbGet() {
			return new Promise((res, rej) => {
				const tx = db.transaction(STORE, 'readonly');
				const rq = tx.objectStore(STORE).get(KEY);
				rq.onsuccess = () => res(rq.result);
				rq.onerror = () => rej(rq.error);
			});
		}

		function save() {
			if (!enabled || !dirty) return saveChain;
			dirty = false;
			lastSave = Date.now();
			const snap = serialize(); // synchronous copy — consistent by construction
			saveChain = saveChain
				.then(() => idbPut({ v: 1, entries: snap, savedAt: Date.now() }))
				.catch(() => { dirty = true; }); // retry on the next mutation
			return saveChain;
		}

		function markDirty() {
			if (!enabled) return;
			dirty = true;
			if (timer) return;
			// Debounce, with a floor so a chatty writer (the visor's bbolt
			// stores) batches into one snapshot every few seconds at most.
			const wait = Math.max(1500, 3000 - (Date.now() - lastSave));
			timer = setTimeout(() => { timer = null; save(); }, wait);
		}

		// Which argument of each mutator names the path a snapshot would record.
		// rename and link touch both ends, so either end being included is enough
		// to dirty the tree; symlink's first argument is the link target, which is
		// content rather than a location.
		const PATH_ARGS = {
			open: [0], mkdir: [0], rmdir: [0], unlink: [0], truncate: [0],
			chmod: [0], chown: [0], lchown: [0], utimes: [0],
			rename: [0, 1], link: [0, 1], symlink: [1],
		};
		const FD_ARGS = { ftruncate: [0], fchmod: [0], fchown: [0] };

		function pathOfFd(fd) { const e = fds.get(fd); return e ? e.path : null; }

		function isExcluded(p) {
			if (!p || !excludeFn) return false;
			try { return !!excludeFn(p); } catch (e) { return false; }
		}

		// snapshotUnaffected is true only when EVERY path a call touches is
		// excluded. Anything unknown — a pipe fd, an unresolvable path, an
		// exclude that throws — falls through to scheduling a snapshot, so the
		// failure mode is a wasted copy rather than a lost write.
		function snapshotUnaffected(paths) {
			if (!paths || !paths.length) return false;
			for (const p of paths) if (!isExcluded(p)) return false;
			return true;
		}

		// Wrap the mutating syscalls once, at enable() time. write/writeSync
		// only count for real files (fd > 2) — stdout/stderr traffic must not
		// trigger snapshots.
		//
		// A write to an excluded path must not schedule one either. Excluding a
		// path already keeps it OUT of the snapshot, so a write there cannot
		// change what a snapshot contains — but without this check the exclude
		// list only shrinks each snapshot, never reduces how many are taken, and
		// a build writing thousands of cache files still queues a full-tree copy
		// every couple of seconds.
		function hookMutators() {
			const names = ['open', 'mkdir', 'rmdir', 'rename', 'unlink', 'truncate',
				'ftruncate', 'chmod', 'fchmod', 'chown', 'fchown', 'lchown', 'utimes',
				'link', 'symlink'];
			for (const n of names) {
				const orig = fsImpl[n];
				if (typeof orig !== 'function') continue;
				const pa = PATH_ARGS[n], fa = FD_ARGS[n];
				fsImpl[n] = function (...args) {
					let paths = null;
					if (pa) paths = pa.map((i) => normalize(args[i]));
					else if (fa) paths = fa.map((i) => pathOfFd(args[i]));
					if (!snapshotUnaffected(paths)) markDirty();
					return orig.apply(this, args);
				};
			}
			const w = fsImpl.write;
			fsImpl.write = function (fd, ...rest) {
				if (fd > 2 && !isExcluded(pathOfFd(fd))) markDirty();
				return w.call(this, fd, ...rest);
			};
			const ws = fsImpl.writeSync;
			fsImpl.writeSync = function (fd, ...rest) {
				if (fd > 2 && !isExcluded(pathOfFd(fd))) markDirty();
				return ws.call(this, fd, ...rest);
			};
		}

		return {
			enable(dbName, opts) {
				if (enabled) return Promise.resolve({ restored: false });
				if (typeof indexedDB === 'undefined') return Promise.resolve({ restored: false });
				if (opts && typeof opts.exclude === 'function') excludeFn = opts.exclude;
				return idbOpen(dbName || 'jsfs').then((d) => {
					db = d;
					return idbGet();
				}).then((snap) => {
					let restored = false;
					if (snap && Array.isArray(snap.entries)) {
						applySnapshot(snap.entries);
						restored = true;
					}
					hookMutators();
					lazyDirtyHook = markDirty; // a lazy read's populate persists
					enabled = true;
					if (typeof addEventListener === 'function') {
						addEventListener('pagehide', () => { try { save(); } catch (e) { /* best-effort */ } });
						addEventListener('visibilitychange', () => {
							try { if (document.visibilityState === 'hidden') save(); } catch (e) { /* best-effort */ }
						});
					}
					return { restored };
				});
			},
			flush() { dirty = true; return save(); },
			clear() {
				if (!db) return Promise.resolve();
				enabled = false;
				return new Promise((res, rej) => {
					const tx = db.transaction(STORE, 'readwrite');
					tx.objectStore(STORE).delete(KEY);
					tx.oncomplete = () => res();
					tx.onerror = () => rej(tx.error);
				});
			},
		};
	})();

	globalThis.fs = fsImpl;
	globalThis.process = processImpl;
	globalThis.jsfs = {
		installed: true,
		stdio,           // swap .stdout/.stderr/.stdin to capture a command
		mkdirp,          // host-side seeding helpers
		writeFile: writeFileSeed,
		writeLazy,       // seed a file fetched from a url on first read
		readFile(path) { const r = resolve(path, true); if (!r.node || r.node.data === null) return null; return r.node.data; },
		setCwd(d) { processImpl.chdir(d); },
		pipe() { return makePipe(); },        // [readFd, writeFd]
		isPipe(fd) { return isPipe(fd); },
		getCwd() { return cwd; },
		persist,         // IndexedDB snapshots: enable(db) → Promise<{restored}>
	};
})();

;
// pkg/wasmhv/browseui/seed-skywire.js c3-vis-wasm
// Seeds the skywire package layout into the generic Linux root that bottle's
// jsfs.js installs — the application half of the split: bottle owns the FHS
// skeleton, this file makes the tab look like a host with the skywire
// package installed. Runs right after jsfs.js in the BrowseJS bundle;
// idempotent so a bundle loaded twice doesn't clobber operator edits.
(function () {
	'use strict';
	var j = globalThis.jsfs;
	if (!j || !j.installed || j.skywireSeeded) return;
	j.skywireSeeded = true;

	['/etc/skywire', '/opt/skywire/apps', '/opt/skywire/bin', '/opt/skywire/local',
		'/var/log/skywire',
	].forEach(function (d) { j.mkdirp(d); });

	j.writeFile('/etc/hostname', 'skywire-playground\n');
	j.writeFile('/etc/os-release', 'PRETTY_NAME="Skywire Playground (wasm)"\nID=skywire-playground\n');
	// The SKYENV file, exactly as the Linux packages ship it: PKGENV=true
	// makes `skywire autoconfig` / `skywire cli config gen` resolve the
	// package paths (/opt/skywire/skywire.json). Edit it with the shell
	// the same way you would on Linux.
	j.writeFile('/etc/skywire.conf',
		'#/etc/skywire.conf\n' +
		'#sourced by `skywire autoconfig` and `skywire cli config gen`\n' +
		'PKGENV=true\n');
	j.writeFile('/home/user/README',
		'This is an in-memory filesystem shared by the shell and the skywire binary.\n' +
		'skywire is "installed" under /opt/skywire — try:\n' +
		'    skywire autoconfig\n' +
		'    skywire cli config gen -rp\n' +
		'    cat /opt/skywire/skywire.json | jq .pk\n');
})();

;
// vnet.js — a virtual loopback network for the page.
//
// The missing OS layer for running localhost-shaped software in the browser:
// on a host, one process LISTENS on a loopback port and another DIALS it.
// Wasm instances have no shared network — Go's js runtime simulates loopback
// only WITHIN one instance — so this provides the between-instances piece: a
// page-global port table with in-memory duplex byte pipes. The Go side (the
// vnet subpackage) adapts these to net.Listener / net.Conn, so REAL server
// and client code — an RPC server in one instance, its CLI in another, an
// http.Server a page script fetches from — works unmodified across
// instances. Page JS can resolve http://127.0.0.1:<port> against the same
// table via httpFetch below.
//
// Same-realm v1: all endpoints live in one JS realm (the page). The API is
// callback-based so a MessagePort bridge can extend it across Workers later.
//
// Sides: the dialer is side 'a', the accepter side 'b'. q.a holds bytes
// readable BY side a (written by b), and vice versa.
(function () {
	'use strict';
	if (globalThis.vnet) return;

	let nextID = 1;
	const ports = new Map(); // port -> { onconn(connId) }
	const conns = new Map(); // id -> { q:{a:[],b:[]}, wake:{a:null,b:null}, closed:{a:false,b:false} }

	function peer(side) { return side === 'a' ? 'b' : 'a'; }

	function wake(c, side) {
		const cb = c.wake[side];
		if (cb) { c.wake[side] = null; queueMicrotask(cb); }
	}

	// Service-worker bridge state (see enableSW below).
	let swPrefix = null;

	// httpExchange runs ONE HTTP/1.0 request/response over an already-open
	// pipe (side 'a' of conn `id`) and settles the given resolve/reject with
	// {status, body:Uint8Array, headers:{lowercased:value}}. Shared by
	// httpFetch (plain pipe) and socksHttpFetch (pipe with a SOCKS5 CONNECT
	// prelude). HTTP/1.0 + Connection: close — no chunked encoding, EOF
	// delimits when Content-Length is absent; Content-Length short-circuits
	// servers that hold the connection open.
	function httpExchange(v, id, hostLabel, method, path, body, headers, resolve, reject, timeoutMs, preBytes) {
		let done = false;
		const timer = setTimeout(() => {
			if (done) return;
			done = true;
			v.close(id, 'a');
			reject(new Error('timeout: ' + hostLabel));
		}, timeoutMs || 30000);
		const te = new TextEncoder();
		let req = (method || 'GET') + ' ' + (path || '/') + ' HTTP/1.0\r\nHost: ' + hostLabel + '\r\n';
		const h = headers || {};
		for (const k in h) { if (Object.prototype.hasOwnProperty.call(h, k)) req += k + ': ' + h[k] + '\r\n'; }
		let bodyBytes = null;
		if (body != null) {
			bodyBytes = (body instanceof Uint8Array) ? body : te.encode(String(body));
			req += 'Content-Length: ' + bodyBytes.length + '\r\n';
		}
		req += 'Connection: close\r\n\r\n';
		v.send(id, 'a', te.encode(req));
		if (bodyBytes && bodyBytes.length) v.send(id, 'a', bodyBytes);
		const chunks = [];
		let total = 0;
		// preBytes: response bytes a caller's prelude reader (the SOCKS
		// handshake) already pulled off the pipe before handing it over.
		if (preBytes && preBytes.length) { chunks.push(preBytes); total += preBytes.length; }
		let parsed = null; // {status, headers, bodyStart, contentLength}
		const concat = () => {
			const all = new Uint8Array(total);
			let off = 0;
			for (const c of chunks) { all.set(c, off); off += c.length; }
			return all;
		};
		const tryParseHead = (all) => {
			let sep = -1; // header/body split at CRLFCRLF
			for (let i = 0; i + 3 < all.length; i++) {
				if (all[i] === 13 && all[i + 1] === 10 && all[i + 2] === 13 && all[i + 3] === 10) { sep = i; break; }
			}
			if (sep < 0) return null;
			const head = new TextDecoder().decode(all.subarray(0, sep));
			const lines = head.split('\r\n');
			const status = parseInt(lines[0].split(' ')[1] || '0', 10) || 0;
			const hs = {};
			for (let i = 1; i < lines.length; i++) {
				const ci = lines[i].indexOf(':');
				if (ci > 0) hs[lines[i].slice(0, ci).trim().toLowerCase()] = lines[i].slice(ci + 1).trim();
			}
			const cl = /^\d+$/.test(hs['content-length'] || '') ? parseInt(hs['content-length'], 10) : -1;
			return { status: status, headers: hs, bodyStart: sep + 4, contentLength: cl };
		};
		const finish = (all) => {
			if (done) return;
			done = true;
			clearTimeout(timer);
			v.close(id, 'a');
			if (!all) all = concat();
			if (!parsed) parsed = tryParseHead(all);
			if (!parsed) { reject(new Error('malformed HTTP response from ' + hostLabel)); return; }
			let respBody = all.subarray(parsed.bodyStart);
			if (parsed.contentLength >= 0 && respBody.length > parsed.contentLength) respBody = respBody.subarray(0, parsed.contentLength);
			resolve({ status: parsed.status, body: respBody, headers: parsed.headers });
		};
		const pump = () => {
			if (done) return;
			for (;;) {
				const b = v.recv(id, 'a');
				if (b) {
					chunks.push(b);
					total += b.length;
					if (!parsed || parsed.contentLength >= 0) {
						const all = concat();
						if (!parsed) parsed = tryParseHead(all);
						if (parsed && parsed.contentLength >= 0 && total - parsed.bodyStart >= parsed.contentLength) { finish(all); return; }
					}
					continue;
				}
				if (v.eof(id, 'a')) { finish(null); return; }
				v.onReadable(id, 'a', pump);
				return;
			}
		};
		pump();
	}

	globalThis.vnet = {
		// listen claims a port; onconn(connId) fires per inbound dial (the
		// accepter is side 'b' of that conn). Returns false if taken.
		// owner (optional) tags the claim with the OWNING INSTANCE (the Go
		// adapter passes its SKYWIRE_EXEC_ID) so releaseOwner can clear a
		// dead program's claims — a wasm instance that exits cannot unlisten
		// itself, and zombie entries otherwise fake liveness forever.
		listen(port, onconn, owner) {
			if (ports.has(port)) return false;
			ports.set(port, { onconn, owner: owner || '' });
			return true;
		},

		unlisten(port) { ports.delete(port); },

		listening(port) { return ports.has(port); },

		// dial connects to a listening port; returns the conn id (dialer is
		// side 'a') or -1 (connection refused). owner (optional) tags the
		// dialer side for releaseOwner; the accepter side inherits the
		// listener's owner.
		dial(port, owner) {
			const l = ports.get(port);
			if (!l) return -1;
			const id = nextID++;
			conns.set(id, { q: { a: [], b: [] }, wake: { a: null, b: null }, closed: { a: false, b: false }, aOwner: owner || '', bOwner: l.owner || '' });
			queueMicrotask(() => l.onconn(id));
			return id;
		},

		// releaseOwner clears every claim a dead instance left behind: its
		// listeners are unbound (the port becomes claimable again — or falls
		// through to the host loopback in the nested browser) and both sides
		// of its conns are closed so peers read EOF instead of blocking on a
		// program that will never write. Called by the exec harness when a
		// wasm instance's run() settles.
		releaseOwner(owner) {
			if (!owner) return 0;
			let n = 0;
			for (const [port, l] of Array.from(ports.entries())) {
				if (l.owner === owner) { ports.delete(port); n++; }
			}
			for (const [id, c] of Array.from(conns.entries())) {
				if (c.aOwner === owner && !c.closed.a) { this.close(id, 'a'); n++; }
				if (c.bOwner === owner && !c.closed.b) { this.close(id, 'b'); n++; }
			}
			return n;
		},

		// send appends bytes for the peer. Returns false when the peer end is
		// closed (EPIPE) or the conn is gone.
		send(id, side, bytes) {
			const c = conns.get(id);
			if (!c || c.closed[peer(side)]) return false;
			c.q[peer(side)].push(bytes.slice());
			wake(c, peer(side));
			return true;
		},

		// recv pops one readable chunk for side, or returns null: would-block
		// when the conn is open, EOF when the peer closed and the queue is dry.
		recv(id, side) {
			const c = conns.get(id);
			if (!c) return null;
			const q = c.q[side];
			if (q.length > 0) return q.shift();
			return null;
		},

		// eof reports "peer closed and nothing left to read".
		eof(id, side) {
			const c = conns.get(id);
			if (!c) return true;
			return c.closed[peer(side)] && c.q[side].length === 0;
		},

		// onReadable registers a ONE-SHOT wakeup for when side has data (or
		// EOF). Fires immediately (async) if already readable.
		onReadable(id, side, cb) {
			const c = conns.get(id);
			if (!c) { queueMicrotask(cb); return; }
			if (c.q[side].length > 0 || c.closed[peer(side)]) { queueMicrotask(cb); return; }
			c.wake[side] = cb;
		},

		// close shuts this side; the peer reads EOF after draining. When both
		// sides are closed the conn is dropped.
		close(id, side) {
			const c = conns.get(id);
			if (!c) return;
			c.closed[side] = true;
			wake(c, peer(side));
			if (c.closed.a && c.closed.b) conns.delete(id);
		},

		// httpFetch performs ONE HTTP request against a virtual-loopback port
		// and resolves {status, body:Uint8Array, headers:{lowercased:value}} —
		// a fetch-like result shape, so a page-side virtual browser can
		// treat http://127.0.0.1:<port> as just another channel. Speaks
		// HTTP/1.0 with Connection: close (no chunked encoding — EOF delimits
		// the body), which Go's http.Server answers natively.
		httpFetch(port, method, path, body, headers) {
			return new Promise((resolve, reject) => {
				const id = this.dial(port);
				if (id < 0) { reject(new Error('connection refused: 127.0.0.1:' + port)); return; }
				httpExchange(this, id, '127.0.0.1:' + port, method, path, body, headers, resolve, reject, 30000);
			});
		},

		// socksHttpFetch performs ONE HTTP request THROUGH a SOCKS5 proxy
		// listening on a virtual-loopback port (no auth): CONNECT to
		// targetHostPort ("home.dmsg:80", "<pk>.dmsg:80", …), then the same
		// HTTP/1.0 exchange httpFetch speaks. This is how page JS reaches the
		// in-page visor's RESOLVING PROXIES — the desk's nested browser
		// fetches dmsg/skynet sites via the visor running in a terminal
		// (dmsgweb on vnet:4445), which the page cannot address any other way
		// (the visor is a separate wasm instance with no page API).
		socksHttpFetch(port, targetHostPort, method, path, body, headers) {
			const v = this;
			return new Promise((resolve, reject) => {
				const id = v.dial(port);
				if (id < 0) { reject(new Error('connection refused: 127.0.0.1:' + port)); return; }
				let settled = false;
				const fail = (msg) => {
					if (settled) return;
					settled = true;
					clearTimeout(hsTimer);
					v.close(id, 'a');
					reject(new Error(msg));
				};
				const hsTimer = setTimeout(() => fail('timeout: SOCKS5 handshake with 127.0.0.1:' + port), 20000);
				// Buffered reader over recv for the fixed-size handshake replies.
				let buf = new Uint8Array(0);
				let need = 0, onBytes = null;
				const pump = () => {
					if (settled) return;
					for (;;) {
						const b = v.recv(id, 'a');
						if (b) {
							const nb = new Uint8Array(buf.length + b.length);
							nb.set(buf, 0); nb.set(b, buf.length);
							buf = nb;
							if (onBytes && buf.length >= need) { const cb = onBytes; onBytes = null; cb(); }
							// STOP once the handshake settled (afterBind ran inside
							// that callback): looping on would re-register this pump
							// as the pipe's one-shot wake callback, stealing every
							// response byte from the HTTP exchange that took over.
							if (settled) return;
							continue;
						}
						if (v.eof(id, 'a')) { fail('SOCKS5 proxy closed during handshake'); return; }
						v.onReadable(id, 'a', pump);
						return;
					}
				};
				const read = (n, cb) => {
					need = n; onBytes = () => {
						const out = buf.subarray(0, n);
						buf = buf.subarray(n);
						cb(out);
					};
					if (buf.length >= n) { const cb2 = onBytes; onBytes = null; cb2(); } else { pump(); }
				};
				const te = new TextEncoder();
				const hp = String(targetHostPort);
				const ci = hp.lastIndexOf(':');
				const host = ci > 0 ? hp.slice(0, ci) : hp;
				const tport = ci > 0 ? (parseInt(hp.slice(ci + 1), 10) || 80) : 80;
				const hostBytes = te.encode(host);
				if (hostBytes.length > 255) { fail('SOCKS5 host too long'); return; }
				// greeting: VER=5, one method: no-auth
				v.send(id, 'a', new Uint8Array([5, 1, 0]));
				read(2, (g) => {
					if (g[0] !== 5 || g[1] !== 0) { fail('SOCKS5 method negotiation failed'); return; }
					// CONNECT: VER CMD RSV ATYP=domain len host port
					const reqB = new Uint8Array(7 + hostBytes.length);
					reqB[0] = 5; reqB[1] = 1; reqB[2] = 0; reqB[3] = 3; reqB[4] = hostBytes.length;
					reqB.set(hostBytes, 5);
					reqB[5 + hostBytes.length] = (tport >> 8) & 0xff;
					reqB[6 + hostBytes.length] = tport & 0xff;
					v.send(id, 'a', reqB);
					read(4, (r) => {
						if (r[0] !== 5 || r[1] !== 0) { fail('SOCKS5 CONNECT refused (rep=' + r[1] + ') for ' + hp); return; }
						// consume the bound address: ATYP decides its length
						const atyp = r[3];
						const rest = atyp === 1 ? 4 + 2 : atyp === 4 ? 16 + 2 : -1;
						const afterBind = () => {
							if (settled) return;
							settled = true;
							clearTimeout(hsTimer);
							// Hand the pipe to the shared HTTP exchange, along with
							// any bytes the handshake reader already pulled past the
							// bind address (a fast server's response can share a
							// chunk with the reply).
							const leftover = buf.length ? buf : null;
							buf = new Uint8Array(0);
							httpExchange(v, id, hp, method, path, body, headers, resolve, reject, 45000, leftover);
						};
						if (rest > 0) { read(rest, afterBind); } else if (atyp === 3) { read(1, (l) => read(l[0] + 2, afterBind)); } else { fail('SOCKS5 bad ATYP ' + atyp); }
					});
				});
			});
		},

		// enableSW registers the vnet service worker (vnet-sw.js) and installs
		// the responder that answers its forwarded requests from this page's
		// port table. Once resolved, swURL() returns REAL same-origin URLs for
		// virtual ports — an <iframe src=vnet.swURL(8001)> loads a server
		// running inside this page with fully native resolution (module
		// graphs, XHR, history), no transcoding. Resolves to true when the
		// bridge is live, false when service workers are unavailable (no
		// secure context, file://, browser policy) — callers fall back to
		// whatever they did before.
		enableSW(swPath, prefix) {
			// Default the scope to a vnet/ directory BESIDE the page, so the
			// bridge works for pages deployed under a subdirectory (GitHub
			// Pages) exactly as at a server root.
			if (!prefix) {
				try { prefix = new URL('vnet/', location.href).pathname; } catch (e) { prefix = '/vnet/'; }
			}
			if (swPrefix) return Promise.resolve(true);
			if (!('serviceWorker' in navigator)) return Promise.resolve(false);
			navigator.serviceWorker.addEventListener('message', (ev) => {
				const m = ev.data || {};
				if (m.type !== 'vnet-fetch' || !ev.ports || !ev.ports[0]) return;
				const reply = ev.ports[0];
				if (!this.listening(m.port)) { reply.postMessage({ refused: true }); return; }
				this.httpFetch(m.port, m.method, m.path, m.body, m.headers)
					.then((r) => {
						// Uint8Array bodies structured-clone fine; pass headers as
						// a plain object (already lowercased by httpFetch).
						reply.postMessage({ status: r.status, headers: r.headers, body: r.body });
					})
					.catch(() => { reply.postMessage({ status: 502, headers: { 'content-type': 'text/plain' }, body: new TextEncoder().encode('vnet: fetch failed') }); });
			});
			const url = (swPath || 'vnet-sw.js') + '?prefix=' + encodeURIComponent(prefix);
			return navigator.serviceWorker.register(url, { scope: prefix })
				.then((reg) => new Promise((resolve) => {
					// Wait on THIS registration's worker reaching 'activated'.
					// (navigator.serviceWorker.ready is the wrong wait here: it
					// tracks the registration matching the PAGE's URL, and the
					// registering page normally lives outside the vnet scope —
					// it would never resolve.)
					const settle = (w) => {
						if (!w) { resolve(false); return; }
						if (w.state === 'activated') { resolve(true); return; }
						w.addEventListener('statechange', () => {
							if (w.state === 'activated') resolve(true);
							else if (w.state === 'redundant') resolve(false);
						});
					};
					settle(reg.active || reg.waiting || reg.installing);
				}))
				.then((ok) => { if (ok) swPrefix = prefix; return ok; })
				.catch(() => false);
		},

		// swURL returns the real same-origin URL prefix for a virtual port
		// ('/vnet/8001/'), or null when the service-worker bridge is not live.
		swURL(port, path) {
			if (!swPrefix) return null;
			return swPrefix + port + (path || '/');
		},
	};
})();

;
// pkg/wasmhv/browseui/hvws-client.js c3-vis-wasm
// The page-side client for /ws — the hypervisor's own /api surface carried as
// a message transport (pkg/visor/hypervisor_ws.go).
//
// The endpoint takes a frame naming a method, a path and a body and replays it
// through the hypervisor's OWN chi router, so a response over this socket is
// byte-identical to the HTTP call it stands in for. That request-in /
// response-out shape is an exact impedance match for a virtual-loopback port,
// which is what hvws-vnet.js builds on top of this: with the two together, a
// desk page served by a NATIVE hypervisor reaches the HOST visor through the
// same vnet calls the in-tab wasm visor answers, and no panel has to know
// which kind of visor is behind them.
//
//	SkywireHVWS.probe()          -> Promise<boolean>   is there a /ws here?
//	SkywireHVWS.open(opts)       -> client
//	client.request(m, p, b, h)   -> Promise<{status, body:string, headers}>
//	client.state                 'connecting' | 'open' | 'closed'
//	client.onstate = fn(state)
//	client.close()
//
// Only ONE socket is needed per page: the server replays up to wsMaxInFlight
// frames concurrently, so requests are correlated by id rather than serialized
// or spread over several connections.
(function () {
	'use strict';
	if (globalThis.SkywireHVWS) return;

	var PATH = '/ws';
	// A replayed request still traverses /api, which carries a 30s
	// middleware.Timeout server-side; giving up a little sooner keeps a
	// timed-out call from outliving the response that is about to arrive.
	var REQ_TIMEOUT_MS = 25000;
	var BACKOFF_MIN_MS = 250;
	var BACKOFF_MAX_MS = 8000;
	// Frames written while the socket is down. Bounded: a socket that stays
	// down must not let a polling panel grow the page's heap without limit.
	var MAX_QUEUE = 64;

	function wsURL(path) {
		var l = globalThis.location;
		return (l.protocol === 'https:' ? 'wss://' : 'ws://') + l.host + (path || PATH);
	}

	// probe answers "is this page served by something that has a /ws?" in ONE
	// cheap request, without opening a socket to find out. The endpoint answers
	// a plain GET with 426 Upgrade Required (coder/websocket's rejection of a
	// non-upgrade request); an origin that has no such route answers 404, and a
	// static host — the docs site — answers 404 too. That is the whole
	// capability test behind the desk's host-vs-in-tab decision: no flag, no
	// build tag, no configuration.
	//
	// 426 specifically, not "anything but 404": with EnableAuth on, an
	// unauthenticated caller gets 401 from the same route, and a socket opened
	// on that answer would be closed again immediately. A page whose operator
	// is not logged in has no business claiming the visor's ports.
	function probe(path) {
		try {
			return fetch(path || PATH, { method: 'GET', cache: 'no-store', credentials: 'same-origin' })
				.then(function (r) { return r.status === 426; })
				.catch(function () { return false; });
		} catch (e) {
			return Promise.resolve(false);
		}
	}

	function Client(opts) {
		opts = opts || {};
		this.path = opts.path || PATH;
		this.state = 'closed';
		this.onstate = opts.onstate || function () {};
		this.ws = null;
		this.nextID = 1;
		this.pending = new Map();
		this.queue = [];
		this.backoff = BACKOFF_MIN_MS;
		this.retryTimer = null;
		this.stopped = false;
		this.connect();
	}

	Client.prototype.setState = function (s) {
		if (this.state === s) return;
		this.state = s;
		try { this.onstate(s); } catch (e) { console.error('hvws: state listener:', e); }
	};

	Client.prototype.connect = function () {
		if (this.stopped || this.ws) return;
		var self = this;
		var sock;
		try {
			sock = new WebSocket(wsURL(this.path));
		} catch (e) {
			this.retry();
			return;
		}
		sock.binaryType = 'arraybuffer';
		this.ws = sock;
		this.setState('connecting');

		sock.onopen = function () {
			if (self.ws !== sock) return;
			self.backoff = BACKOFF_MIN_MS;
			self.setState('open');
			var q = self.queue;
			self.queue = [];
			q.forEach(function (f) {
				try { sock.send(f); } catch (e) { console.warn('hvws: send:', e); }
			});
		};
		sock.onmessage = function (ev) { self.onFrame(ev.data); };
		sock.onerror = function () { /* onclose always follows; nothing to add */ };
		sock.onclose = function () {
			if (self.ws !== sock) return;
			self.ws = null;
			self.setState('closed');
			// Fail every in-flight request. A reconnected socket is a NEW
			// server-side connection with its own handler goroutines; a reply
			// to an id issued on the old one is never coming, so a caller left
			// waiting would hang until its own timeout for no reason.
			var p = self.pending;
			self.pending = new Map();
			p.forEach(function (e) {
				clearTimeout(e.timer);
				e.reject(new Error('/ws: connection closed'));
			});
			// Queued frames were rejected with the rest of `pending` above.
			self.queue = [];
			self.retry();
		};
	};

	Client.prototype.retry = function () {
		if (this.stopped || this.retryTimer) return;
		var self = this;
		// Jitter: several tabs of the same visor must not all reconnect on the
		// same tick after the visor restarts.
		var wait = this.backoff + Math.floor(Math.random() * (this.backoff / 2));
		this.backoff = Math.min(this.backoff * 2, BACKOFF_MAX_MS);
		this.retryTimer = setTimeout(function () {
			self.retryTimer = null;
			self.connect();
		}, wait);
	};

	Client.prototype.onFrame = function (data) {
		var m;
		try {
			m = JSON.parse(typeof data === 'string' ? data : new TextDecoder().decode(data));
		} catch (e) {
			return;
		}
		// "event" is reserved by the wire format for server push; there is no
		// consumer yet, and an unknown type must never be mistaken for a reply.
		if (!m || (m.type && m.type !== 'res')) return;
		var e = this.pending.get(m.id);
		// id 0 is the server's answer to a malformed envelope — it names no
		// request, so there is nothing to settle.
		if (!e) return;
		this.pending.delete(m.id);
		clearTimeout(e.timer);
		e.resolve({ status: m.status || 0, body: m.body || '', headers: m.headers || {} });
	};

	// request replays one HTTP call over the socket. Bodies are strings both
	// ways — the hypervisor API is JSON in and JSON out.
	Client.prototype.request = function (method, path, body, headers) {
		var self = this;
		return new Promise(function (resolve, reject) {
			if (self.stopped) { reject(new Error('/ws: client closed')); return; }
			var id = self.nextID++;
			var frame = JSON.stringify({
				type: 'req',
				id: id,
				method: (method || 'GET').toUpperCase(),
				path: path,
				body: (body == null ? null : String(body)),
				headers: headers || {},
			});
			var timer = setTimeout(function () {
				self.pending.delete(id);
				reject(new Error('/ws: timeout on ' + path));
			}, REQ_TIMEOUT_MS);
			self.pending.set(id, { resolve: resolve, reject: reject, timer: timer });

			if (self.ws && self.state === 'open') {
				try {
					self.ws.send(frame);
				} catch (e) {
					self.pending.delete(id);
					clearTimeout(timer);
					reject(e);
				}
				return;
			}
			if (self.queue.length >= MAX_QUEUE) {
				self.pending.delete(id);
				clearTimeout(timer);
				reject(new Error('/ws: not connected (queue full)'));
				return;
			}
			self.queue.push(frame);
		});
	};

	Client.prototype.close = function () {
		this.stopped = true;
		if (this.retryTimer) { clearTimeout(this.retryTimer); this.retryTimer = null; }
		var sock = this.ws;
		this.ws = null;
		if (sock) { try { sock.close(); } catch (e) { /* already gone */ } }
		var p = this.pending;
		this.pending = new Map();
		p.forEach(function (e) {
			clearTimeout(e.timer);
			e.reject(new Error('/ws: client closed'));
		});
		this.queue = [];
		this.setState('closed');
	};

	globalThis.SkywireHVWS = {
		probe: probe,
		url: wsURL,
		open: function (opts) { return new Client(opts); },
	};
})();

;
// pkg/wasmhv/browseui/hvws-vnet.js c3-vis-wasm
// The vnet adapter for the HOST visor: claims the visor's virtual-loopback
// ports and serves them from the /ws socket (hvws-client.js).
//
// The desk never talks to a visor directly — everything goes through vnet, the
// page-side namespace both sides of the wasm/native split already agree on.
// Panels call vnet.httpFetch(<port>, …); the nested browser and the vnet
// service worker resolve /vnet/<port>/… against the same port table. So the
// whole of "make the desk work against the host visor" reduces to: put a
// listener on those ports whose handler is the host visor.
//
// That is what this does. Nothing above it changes — not one panel, not the
// service worker, not the browser's DirectLoader — because from the page's
// side a bridged port is indistinguishable from a wasm visor's.
//
// Two request classes, and they are routed differently on purpose:
//
//   /api/…      over /ws. The endpoint replays the frame through the
//               hypervisor's own router, so permissions, middleware and the
//               response body are the real thing.
//   everything  one same-origin fetch. /ws deliberately refuses any path
//   else        outside /api (wsAllowedPath), and it does not need to carry
//               them: this bridge only ever installs on a page the hypervisor
//               ITSELF served, so its assets — the Angular bundle, its fonts,
//               index.html — are already same-origin. That is what makes
//               /vnet/<hvPort>/ a complete, natively-resolving mirror of the
//               dashboard rather than an API-only port.
//
//	SkywireHVBridge.install({ports:[3435, 8001]}) -> Promise<bridge|null>
//
// The claim tracks the socket: ports are bound when /ws is open and released
// when it drops, so vnet.listening(<port>) keeps meaning what the desk reads
// it as — "a visor is up and reachable from this page" — instead of becoming a
// permanent lie the moment the host visor restarts.
(function () {
	'use strict';
	if (globalThis.SkywireHVBridge) return;

	var te = new TextEncoder();
	var td = new TextDecoder();

	// A reason phrase is decorative here (vnet's HTTP reader takes the status
	// from the second field and ignores the rest), but a readable one shows up
	// in a devtools network panel and in the browser's own error pages.
	var REASON = {
		200: 'OK', 204: 'No Content', 301: 'Moved Permanently', 302: 'Found',
		304: 'Not Modified', 400: 'Bad Request', 401: 'Unauthorized',
		403: 'Forbidden', 404: 'Not Found', 405: 'Method Not Allowed',
		500: 'Internal Server Error', 502: 'Bad Gateway', 503: 'Service Unavailable',
	};

	// Headers that belong to the real HTTP hop and would be nonsense — or
	// actively wrong — on the far side of a virtual pipe.
	var HOP_HEADERS = /^(host|connection|content-length|transfer-encoding|upgrade|via|accept-encoding|keep-alive|proxy-.*)$/i;

	// A request head larger than this is not one this bridge will ever serve;
	// the cap stops a peer that never sends CRLFCRLF from growing the buffer.
	var MAX_HEAD = 65536;

	function joinChunks(chunks, total) {
		var out = new Uint8Array(total);
		var off = 0;
		for (var i = 0; i < chunks.length; i++) {
			out.set(chunks[i], off);
			off += chunks[i].length;
		}
		return out;
	}

	function sieve(headers) {
		var out = {};
		for (var k in headers) {
			if (!Object.prototype.hasOwnProperty.call(headers, k)) continue;
			if (HOP_HEADERS.test(k)) continue;
			out[k] = headers[k];
		}
		return out;
	}

	// readRequest pulls ONE HTTP/1.x request off the accepter side of a vnet
	// conn and hands it to cb, or cb(null) when the peer is not speaking HTTP.
	//
	// The shape check matters beyond hygiene: 3435 is the visor's RPC port, and
	// on a wasm desk what listens there speaks Go's net/rpc GOB stream, not
	// HTTP. This bridge cannot carry gob — /ws is request/response, with no
	// streaming — so a gob dialer is refused at the first bytes and fails fast
	// with a closed pipe, instead of being answered with an HTTP response it
	// would try to decode as a gob header.
	function readRequest(v, id, cb) {
		var chunks = [];
		var total = 0;
		var head = null;
		var settled = false;

		function done(req) {
			if (settled) return;
			settled = true;
			cb(req);
		}

		function parseHead(buf) {
			var sep = -1;
			for (var i = 0; i + 3 < buf.length; i++) {
				if (buf[i] === 13 && buf[i + 1] === 10 && buf[i + 2] === 13 && buf[i + 3] === 10) { sep = i; break; }
			}
			if (sep < 0) {
				if (buf.length > MAX_HEAD) done(null);
				return null;
			}
			var lines = td.decode(buf.subarray(0, sep)).split('\r\n');
			var rl = /^([A-Z]{3,10}) (\S+) HTTP\/1\.[01]$/.exec(lines[0] || '');
			if (!rl) { done(null); return null; }
			var hs = {};
			for (var j = 1; j < lines.length; j++) {
				var ci = lines[j].indexOf(':');
				if (ci > 0) hs[lines[j].slice(0, ci).trim().toLowerCase()] = lines[j].slice(ci + 1).trim();
			}
			var cl = /^\d+$/.test(hs['content-length'] || '') ? parseInt(hs['content-length'], 10) : 0;
			return { method: rl[1], path: rl[2], headers: hs, bodyStart: sep + 4, contentLength: cl };
		}

		function emit(buf, upTo) {
			done({
				method: head.method,
				path: head.path,
				headers: head.headers,
				body: buf.subarray(head.bodyStart, upTo),
			});
		}

		function pump() {
			if (settled) return;
			for (;;) {
				var b = v.recv(id, 'b');
				if (b) {
					chunks.push(b);
					total += b.length;
					var buf = joinChunks(chunks, total);
					if (!head) head = parseHead(buf);
					if (settled) return;
					if (head && total - head.bodyStart >= head.contentLength) {
						emit(buf, head.bodyStart + head.contentLength);
						return;
					}
					continue;
				}
				if (v.eof(id, 'b')) {
					// EOF with a complete head: httpFetch writes
					// Content-Length, but a caller that closed early still gets
					// whatever body did arrive rather than a dropped request.
					if (head) { emit(joinChunks(chunks, total), total); } else { done(null); }
					return;
				}
				v.onReadable(id, 'b', pump);
				return;
			}
		}
		pump();
	}

	// writeResponse serializes one HTTP/1.0 response onto the pipe and closes
	// it. Connection: close with an explicit Content-Length is exactly what
	// vnet's reader expects; no chunked encoding exists on this wire.
	function writeResponse(v, id, status, headers, body) {
		var out = 'HTTP/1.0 ' + status + ' ' + (REASON[status] || 'Status') + '\r\n';
		var hs = headers || {};
		for (var k in hs) {
			if (!Object.prototype.hasOwnProperty.call(hs, k)) continue;
			if (HOP_HEADERS.test(k)) continue;
			// A header value carrying CR/LF would split the response into two.
			out += k + ': ' + String(hs[k]).replace(/[\r\n]+/g, ' ') + '\r\n';
		}
		out += 'Content-Length: ' + body.length + '\r\nConnection: close\r\n\r\n';
		v.send(id, 'b', te.encode(out));
		if (body.length) v.send(id, 'b', body);
		v.close(id, 'b');
	}

	function isAPI(path) {
		var p = String(path).split('?')[0].split('#')[0];
		return p === '/api' || p.indexOf('/api/') === 0;
	}

	// route answers one parsed request from the host visor.
	function route(client, req) {
		if (isAPI(req.path)) {
			var body = (req.body && req.body.length) ? td.decode(req.body) : null;
			return client.request(req.method, req.path, body, sieve(req.headers)).then(function (r) {
				return { status: r.status, headers: r.headers, body: te.encode(r.body || '') };
			});
		}
		var init = {
			method: req.method,
			credentials: 'same-origin',
			cache: 'no-store',
			headers: sieve(req.headers),
			// Follow redirects here rather than relaying a 3xx: the Location
			// the hypervisor writes is relative to ITS root, and the page that
			// receives it sits under a /vnet/<port>/ prefix the server never
			// saw, so a relayed redirect walks straight out of the prefix.
			redirect: 'follow',
		};
		if (req.method !== 'GET' && req.method !== 'HEAD' && req.body && req.body.length) {
			init.body = req.body;
		}
		return fetch(req.path, init).then(function (r) {
			return r.arrayBuffer().then(function (buf) {
				var hs = {};
				r.headers.forEach(function (val, key) { hs[key] = val; });
				return { status: r.status, headers: hs, body: new Uint8Array(buf) };
			});
		});
	}

	function serve(v, id, client) {
		readRequest(v, id, function (req) {
			if (!req) { v.close(id, 'b'); return; }
			route(client, req).then(function (r) {
				writeResponse(v, id, r.status, r.headers, r.body);
			}).catch(function (e) {
				writeResponse(v, id, 502, { 'content-type': 'text/plain' },
					te.encode('vnet→/ws bridge: ' + ((e && e.message) || e)));
			});
		});
	}

	// install opens the socket and, once it is up, claims the given virtual
	// ports for the host visor. Resolves the bridge, or null when there is no
	// /ws to bridge to (the standalone desk, the docs playground) or when every
	// requested port is already claimed by something in this page — an in-tab
	// wasm visor keeps its own ports; the host bridge never evicts it.
	function install(opts) {
		opts = opts || {};
		var v = globalThis.vnet;
		if (!v || !globalThis.SkywireHVWS) return Promise.resolve(null);
		var ports = opts.ports || [];
		var claimed = [];

		var client = globalThis.SkywireHVWS.open({ path: opts.path });

		function claim() {
			ports.forEach(function (port) {
				if (claimed.indexOf(port) >= 0) return;
				var ok = v.listen(port, function (id) { serve(v, id, client); });
				if (ok) { claimed.push(port); }
			});
		}
		function release() {
			claimed.forEach(function (port) { v.unlisten(port); });
			claimed = [];
		}

		client.onstate = function (s) {
			if (s === 'open') { claim(); } else { release(); }
		};

		var bridge = {
			client: client,
			ports: ports,
			claimed: function () { return claimed.slice(); },
			// fetch is the /api call as the desk's own code would make it —
			// handy for a caller that has a bridge in hand and does not want to
			// go back out through the vnet pipe to reach the same socket.
			fetch: function (m, p, b, h) { return client.request(m, p, b, h); },
			close: function () { release(); client.close(); },
		};

		return new Promise(function (resolve) {
			var settled = false;
			var timer = setTimeout(function () {
				if (settled) return;
				settled = true;
				// The probe said /ws was there, so keep the client (it will
				// reconnect and claim on its own); just do not make the boot
				// wait any longer for it.
				resolve(claimed.length ? bridge : null);
			}, opts.timeoutMs || 8000);
			var prev = client.onstate;
			client.onstate = function (s) {
				prev(s);
				if (s !== 'open' || settled) return;
				settled = true;
				clearTimeout(timer);
				resolve(bridge);
			};
			if (client.state === 'open') { client.onstate('open'); }
		});
	}

	globalThis.SkywireHVBridge = { install: install };
})();

;
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

;
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

;
// pkg/wasmhv/browseui/exec-remote.js c3-vis-wasm
// The page half of "skywire commands run OFF the main thread".
//
// WHY. The desk runs its visor as a skywireExec process — the root binary's
// Go/wasm module, `skywire autoconfig`, in a terminal. Until now that ran on
// the PAGE MAIN THREAD, and a Go runtime with a live visor in it never idles:
// profiled 2026-09-07 over 16.5 minutes, 95% of one-second samples were above
// 90% CPU, and the symbolized profile contained no application frame and no GC
// frame at all — only runtime.findRunnable / runtime.stealWork /
// runtime.nanotime1, the scheduler looking for work, finding none, and reading
// the clock through a JS crossing. Nothing in skywire can be optimized to fix
// that: the cost is the runtime's, and the only cure is to put it on a thread
// that is not drawing the UI. The retired legacy page refused to boot a visor
// in-page for exactly this reason; the desk regressed the property and this
// restores it.
//
// WHAT. install() starts ONE dedicated Worker (exec-worker.js, bundled with
// jsfs + vnet + proc + skywire-exec) and REPLACES globalThis.skywireExec with a
// same-contract shim that runs each command over there. Nothing above it
// changes: the desk host's shell still calls
// skywireExec(args, hooks) and still gets hooks.instance({interrupt})
// synchronously and a Promise<exitCode> back.
//
// NO SharedArrayBuffer, no COOP/COEP. bottle's own proc.spawnWorker needs
// cross-origin isolation because its child shares the page's jsfs through
// Atomics.wait; a visor does not need the page's filesystem, so the worker
// carries its own jsfs and owns the IndexedDB snapshot outright. This is plain
// postMessage.
//
// THE PART THAT IS NOT OBVIOUS: vnet does not cross a worker boundary. The
// desk is vnet-shaped end to end — panels call vnet.httpFetch(port, …), the
// service worker resolves /vnet/<port>/…, desk-boot gates on
// vnet.listening(3435) — and a visor inside a worker binds those ports in the
// WORKER's port table, which the page cannot see. A visor that looked healthy
// while nothing in the page could reach it is the exact silent failure to
// avoid. So the worker ADVERTISES every claim it makes, the page claims the
// same port locally, and each inbound conn is forwarded byte-for-byte over
// postMessage to a dial on the far side. Bytes, not HTTP: 3435 speaks Go's
// net/rpc gob stream and 4445 speaks SOCKS5, and neither survives an
// HTTP-shaped bridge (which is why hvws-vnet.js, whose backend really is
// request/response, refuses a non-HTTP dialer instead of bridging it).
//
//	SkywireExecWorker.install({url, persistDB, wasmURL, wasmExecURL})
//	  -> Promise<{restored, ports(), close()} | null>
//
// null means "this page cannot host one" (no Worker constructor, no vnet, the
// script 404s, the worker never reported ready) — every caller then keeps the
// in-page skywireExec it already had, unchanged.
(function () {
	'use strict';
	if (globalThis.SkywireExecWorker) return;

	// The stderr ring size skywire-exec.js asks proc for, mirrored here because
	// with the process off-thread there is no proc record on this side and
	// ctl-bridge.js + desk-boot.js both read one.
	var TAIL_BYTES = 16384;
	var ROUTERISH = /(router|route_setup|RouteGroup|routegroup|setupclient|rule|cascade|rsn)/i;

	// READY_MS: how long a worker gets to compile nothing at all and answer.
	// It only has to load its own bundle and open IndexedDB — the wasm module
	// is not touched until the first spawn — so this is generous.
	var READY_MS = 20000;

	function abs(u) {
		try { return new URL(u, globalThis.location.href).href; } catch (e) { return u; }
	}

	function install(opts) {
		opts = opts || {};
		var v = globalThis.vnet;
		// Capability probe, not a flag: a realm with no Worker constructor, or
		// no vnet to forward the visor's ports into, cannot host one.
		if (typeof Worker !== 'function' || !v || !globalThis.skywireExec) {
			return Promise.resolve(null);
		}
		var url = opts.url || 'skywire-worker.js';

		// HEAD first. `new Worker()` on a 404 fails asynchronously through an
		// error event, which would leave the boot waiting out READY_MS for a
		// worker that was never served (the docs playground before its assets
		// are staged, any deployment that ships an older bundle).
		return fetch(url, { method: 'HEAD' }).then(function (r) {
			if (!r.ok) return null;
			return start(url, opts, v);
		}).catch(function () { return null; });
	}

	function start(url, opts, v) {
		var w;
		try { w = new Worker(url); } catch (e) { return Promise.resolve(null); }

		// ---- exec bookkeeping ------------------------------------------
		// live: exec id -> { out, err, resolve, reject, rec }
		var live = {};
		var seq = 0;
		// tails is published as globalThis.__skywireExecTails, the same
		// registry name (and record shape) skywire-exec.js aliases proc.tails
		// to: { argv, tail, filtered, exitInfo:{code,crashed} }. desk-boot.js
		// reads it to tell a CRASHED visor from one the operator stopped, and
		// ctl-bridge.js mirrors the ring to /ctl/log. The records are built
		// here from the stderr stream we already receive, because proc's own
		// ring now lives in the worker where neither consumer can see it.
		var tails = {};

		// ---- vnet bridging ---------------------------------------------
		// claimed: ports this bridge holds on the PAGE's port table.
		var claimed = {};
		// conns: bridge conn id -> { id: page-side vnet conn id, port }.
		var conns = {};
		var connSeq = 0;
		var dec = new TextDecoder();

		function post(m, transfer) {
			try { w.postMessage(m, transfer || []); } catch (e) { /* worker gone */ }
		}

		// sendable decides whether a chunk's buffer can be handed over rather
		// than copied. Only when the view owns the whole buffer — a subarray
		// would take its siblings with it.
		function sendable(b) {
			return (b && b.buffer && b.byteOffset === 0 && b.byteLength === b.buffer.byteLength)
				? [b.buffer] : [];
		}

		// pumpPage drains the page side of a bridged conn toward the worker.
		// Side 'b': the page is the ACCEPTER here (the visor's listener lives
		// in the worker, so on this side the page only ever accepts).
		function pumpPage(cid) {
			var c = conns[cid];
			if (!c) return;
			for (;;) {
				var b = v.recv(c.id, 'b');
				if (b) {
					// recv shifts the queue's own copy out — nothing else holds
					// it, so its buffer travels instead of being copied again.
					post({ t: 'vdata', cid: cid, b: b }, sendable(b));
					continue;
				}
				// EOF here means the page-side DIALER closed, so nothing is
				// reading this pipe any more: shut our end too. vnet drops a
				// conn only once both sides are closed, and a half-closed one
				// left behind is a permanent entry in the page's conn map —
				// one per request through /vnet/<port>/, which the dashboard
				// makes continuously.
				if (v.eof(c.id, 'b')) { post({ t: 'vclose', cid: cid }); dropConn(cid, true); return; }
				v.onReadable(c.id, 'b', function () { pumpPage(cid); });
				return;
			}
		}

		function dropConn(cid, closeLocal) {
			var c = conns[cid];
			if (!c) return;
			delete conns[cid];
			if (closeLocal) { try { v.close(c.id, 'b'); } catch (e) { /* already gone */ } }
		}

		function onAccept(port, id) {
			var cid = ++connSeq;
			conns[cid] = { id: id, port: port };
			post({ t: 'vopen', cid: cid, port: port });
			pumpPage(cid);
		}

		function claim(port) {
			if (claimed[port]) return;
			// Never evict: a host bridge (hvws-vnet.js) or an in-page server
			// that already holds the port keeps it, and the worker's listener
			// is simply unreachable from this page — which is visible rather
			// than silent, because vnet.listening() still answers for whoever
			// does hold it.
			var ok = v.listen(port, function (id) { onAccept(port, id); }, 'skywire-exec-worker');
			if (!ok) {
				console.warn('[exec-worker] vnet port ' + port + ' is already claimed in this page — the worker\'s listener is not reachable');
				return;
			}
			claimed[port] = true;
		}

		function release(port) {
			if (!claimed[port]) return;
			delete claimed[port];
			try { v.unlisten(port); } catch (e) { /* ignore */ }
			// Close the conns that were riding that port — and only those. The
			// far side is gone; their peers must read EOF rather than block on
			// a listener that will never answer again. Conns on the ports the
			// worker still holds are untouched.
			Object.keys(conns).forEach(function (cid) {
				if (conns[cid] && conns[cid].port === port) dropConn(cid, true);
			});
		}

		function releaseAll() {
			Object.keys(claimed).forEach(function (p) { release(parseInt(p, 10)); });
		}

		// ---- the stderr ring, rebuilt page-side ------------------------
		function tailPush(rec, s) {
			rec.tail = (rec.tail + s).slice(-TAIL_BYTES);
			rec._line = (rec._line + s).slice(-TAIL_BYTES);
			var nl;
			while ((nl = rec._line.indexOf('\n')) >= 0) {
				var line = rec._line.slice(0, nl);
				rec._line = rec._line.slice(nl + 1);
				if (ROUTERISH.test(line)) rec.filtered = (rec.filtered + line + '\n').slice(-TAIL_BYTES);
			}
		}

		function finish(id, code, err) {
			var e = live[id];
			if (!e) return;
			delete live[id];
			if (e.rec && !e.rec.exitInfo) e.rec.exitInfo = { code: err ? (code || 1) : code, crashed: !!err };
			if (err) {
				console.error('[exec-worker ' + id + '] ' + err +
					(e.rec && e.rec.tail ? '\n--- last stderr ---\n' + e.rec.tail : ''));
				e.reject(new Error(err));
				return;
			}
			if (code !== 0) {
				console.error('[exec-worker ' + id + '] exited code ' + code +
					(e.rec && e.rec.tail ? '\n--- last stderr ---\n' + e.rec.tail : ''));
			}
			e.resolve(code);
		}

		// ---- the replacement skywireExec -------------------------------
		function remoteExec(args, hooks) {
			hooks = hooks || {};
			var id = 'w' + (++seq);
			var argv = ['skywire'].concat(args);
			var rec = { argv: argv.slice(), tail: '', filtered: '', exitInfo: null, _line: '' };
			tails[id] = rec;
			var p = new Promise(function (res, rej) {
				live[id] = { out: hooks.stdout || null, err: hooks.stderr || null, resolve: res, reject: rej, rec: rec };
			});
			// The interrupt is handed over SYNCHRONOUSLY, before the command
			// starts — the contract cmd/wasm-visor/skywirecmd_js.go relies on
			// for Ctrl+C. Here it posts a kill the worker turns into the same
			// registered interrupt the in-page path would have invoked.
			if (typeof hooks.instance === 'function') {
				hooks.instance({ interrupt: function () { post({ t: 'kill', id: id }); return true; } });
			}
			post({ t: 'spawn', id: id, args: args.slice(), env: hooks.env || null });
			return p;
		}
		remoteExec.wasmURL = globalThis.skywireExec.wasmURL;
		remoteExec.wasmExecURL = globalThis.skywireExec.wasmExecURL;
		// The module lives on the worker's side now, but the question callers
		// ask ("is there a skywire command on this page at all?") is still
		// answered by whether the module is served.
		remoteExec.available = function () {
			return fetch(remoteExec.wasmURL, { method: 'HEAD' })
				.then(function (r) { return r.ok; }).catch(function () { return false; });
		};
		remoteExec.inWorker = true;

		// ---- message pump ----------------------------------------------
		var readyRes = null;
		var settled = false;

		w.onmessage = function (ev) {
			var m = ev.data || {};
			switch (m.t) {
			case 'ready':
				if (settled) return;
				settled = true;
				readyRes({ restored: !!m.restored });
				return;
			case 'out': {
				var eo = live[m.id];
				if (eo && eo.out) { try { eo.out(m.b); } catch (e) { /* sink gone */ } }
				return;
			}
			case 'err': {
				var ee = live[m.id];
				if (ee) {
					if (ee.rec) { try { tailPush(ee.rec, dec.decode(m.b, { stream: true })); } catch (e) { /* ignore */ } }
					if (ee.err) { try { ee.err(m.b); } catch (e) { /* sink gone */ } }
				}
				return;
			}
			case 'exit': finish(m.id, m.code, null); return;
			case 'fail': finish(m.id, 1, m.msg || 'exec failed'); return;
			case 'vlisten': claim(m.port); return;
			case 'vunlisten': release(m.port); return;
			case 'vdata': {
				var cv = conns[m.cid];
				if (!cv) return;
				// send() false means the page-side peer hung up; tell the
				// worker so its dialer sees a closed pipe instead of writing
				// into a conn nobody reads.
				if (!v.send(cv.id, 'b', m.b)) { post({ t: 'vclose', cid: m.cid }); dropConn(m.cid, true); }
				return;
			}
			case 'vclose': dropConn(m.cid, true); return;
			case 'log':
				// Worker console output, mirrored so a desk page's own log
				// window and a CDP probe see the visor's JS-side errors.
				try { (console[m.level] || console.log).call(console, '[worker]', m.line); } catch (e) { /* ignore */ }
				return;
			default: return;
			}
		};

		w.onerror = function (e) {
			var msg = (e && e.message) || 'worker error';
			console.error('[exec-worker]', msg);
			if (!settled) { settled = true; readyRes(null); return; }
			// After ready, a worker-level error is fatal to everything running
			// in it: fail the live commands rather than leaving their promises
			// pending forever, and drop the port claims so vnet.listening()
			// stops asserting a visor that is gone.
			Object.keys(live).forEach(function (id) { finish(id, 1, msg); });
			releaseAll();
		};

		var ready = new Promise(function (res) { readyRes = res; });
		post({
			t: 'init',
			persistDB: opts.persistDB || '',
			wasmURL: abs(opts.wasmURL || globalThis.skywireExec.wasmURL),
			wasmExecURL: abs(opts.wasmExecURL || globalThis.skywireExec.wasmExecURL),
		});
		setTimeout(function () {
			if (settled) return;
			settled = true;
			readyRes(null);
		}, READY_MS);

		return ready.then(function (r) {
			if (!r) {
				console.warn('[exec-worker] worker did not come up — commands stay on the page main thread');
				try { w.terminate(); } catch (e) { /* ignore */ }
				return null;
			}
			remoteExec.wasmURL = abs(opts.wasmURL || globalThis.skywireExec.wasmURL);
			remoteExec.wasmExecURL = abs(opts.wasmExecURL || globalThis.skywireExec.wasmExecURL);
			globalThis.skywireExec = remoteExec;
			// The registries under the names skywire's page code already uses.
			// __skywireSignals is deliberately NOT re-aliased: the interrupt
			// registry that matters is the worker's (that is where
			// pkg/cmdutil/signal_js.go registers), and kill() reaches it
			// through the shim's interrupt above.
			globalThis.__skywireExecTails = tails;
			// A page going away should not leave a worker holding a dmsg
			// session and a registered identity for the browser to reap
			// whenever it feels like it.
			addEventListener('pagehide', function () { try { w.terminate(); } catch (e) { /* ignore */ } });
			return {
				restored: r.restored,
				worker: w,
				ports: function () { return Object.keys(claimed).map(Number); },
				close: function () { releaseAll(); try { w.terminate(); } catch (e) { /* ignore */ } },
			};
		});
	}

	globalThis.SkywireExecWorker = { install: install };
})();

;
// gobrowser-loader.js — opens the netscrape Go/wasm browser
// (github.com/0magnet/netscrape) in a winbox window, wired to the visor's
// transports.
//
// The browser is NO LONGER a separate wasm module. It is compiled into the
// skywire module and exposed as globalThis.skywireBrowser.open(el) by the
// desk host instance (the same one that carries the terminal —
// pkg/wasmhv/deskhost/browser_js.go). So this launcher does not fetch or instantiate
// anything: it opens a window and calls skywireBrowser.open, and the browser
// runs in the already-loaded instance's Go runtime. The browser's chrome, page
// transcoding and navigation are Go/syscall/js; only the network is delegated
// here, through the same skywireVisor.fetchDmsg / fetchClearnet the rest of the
// UI uses.
//
// Usage from the visor page (or its console): SkywireGoBrowser.open().
(function () {
	function meshHost(h) {
		return /\.(dmsg|skysocks|skynet)$/i.test(h) || /^[0-9a-f]{66}$/i.test(h);
	}

	// respond wraps the visor's {status, body:Uint8Array, headers} into a fetch
	// Response, so the Go browser's fetchVia sees a normal Response either way.
	function respond(r) {
		var h = new Headers();
		if (r && r.headers) {
			try { for (var k in r.headers) h.set(k, r.headers[k]); } catch (e) { /* ignore */ }
		}
		return new Response((r && r.body) || new Uint8Array(0), { status: (r && r.status) || 200, headers: h });
	}

	// The transport the Go browser calls (globalThis.__netscrapeFetch): mesh
	// hosts go over dmsg, everything else over clearnet, with a plain same-origin
	// fetch as the last resort.
	function transport(url) {
		var sv = globalThis.skywireVisor || {};
		var u;
		try { u = new URL(url); } catch (e) { return fetch(url); }
		var path = (u.pathname || "/") + (u.search || "");
		if (sv.fetchDmsg && meshHost(u.hostname)) {
			return Promise.resolve(sv.fetchDmsg(u.hostname, "GET", path, null)).then(respond);
		}
		if (sv.fetchClearnet) {
			// The visor API is fetchClearnet(exitPK, method, url[, body]) — empty
			// exitPK lets the visor pick an exit. (url must not be passed first.)
			return Promise.resolve(sv.fetchClearnet("", "GET", url, null)).then(respond);
		}
		return fetch(url);
	}

	// ensureBrowser resolves once globalThis.skywireBrowser.open exists. The desk
	// loads the wasm-visor binary's DOM-side instance (which installs it); on a
	// page where that instance has not booted yet, wait briefly for it rather
	// than failing the click.
	function ensureBrowser() {
		return new Promise(function (res, rej) {
			var tries = 0;
			(function poll() {
				var b = globalThis.skywireBrowser;
				if (b && typeof b.open === "function") { res(b); return; }
				if (++tries > 100) { rej(new Error("skywireBrowser not available — the desk wasm-visor instance is not loaded")); return; }
				setTimeout(poll, 100);
			})();
		});
	}

	globalThis.SkywireGoBrowser = {
		// open mounts the Go browser in a new winbox window and returns it.
		open: function (title) {
			if (typeof globalThis.WinBox !== "function") {
				console.error("SkywireGoBrowser: window manager not ready");
				return null;
			}
			var wb = new globalThis.WinBox({ title: title || "Browser (beta)", width: "72%", height: "72%" });
			var mount = document.createElement("div");
			mount.style.cssText = "position:absolute;inset:0";
			wb.body.appendChild(mount);
			// A page may install its own transport first (the desk routes mesh
			// fetches through the running visor's resolver on the virtual
			// loopback); only fill the default when nothing did.
			if (!globalThis.__netscrapeFetch) globalThis.__netscrapeFetch = transport;
			ensureBrowser()
				.then(function (b) { b.open(mount); })
				.catch(function (e) { mount.textContent = "Browser failed to open: " + e; });
			return wb;
		},
	};
})();
