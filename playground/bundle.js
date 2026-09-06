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
// Code generated by 'make dist' from TinyGo's targets/wasm_exec.js. DO NOT EDIT.
//
// Wrapped so the loader class lands on __winboxGo instead of globalThis.Go.
// browse.js's shell path treats an existing globalThis.Go as 'the right
// wasm_exec for the visor blob is already loaded' and skips loading its own,
// so leaking TinyGo's here would break the shell whenever the visor is the
// standard-Go blob. Nothing else about the file is changed.
globalThis.__winboxGo = (function () {
  var had = Object.prototype.hasOwnProperty.call(globalThis, 'Go');
  var prev = globalThis.Go;
// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// This file has been modified for use by the TinyGo compiler.
"use strict";

(() => {
	const enosys = () => {
		const err = new Error("not implemented");
		err.code = "ENOSYS";
		return err;
	};

	if (!globalThis.fs) {
		let outputBuf = "";
		globalThis.fs = {
			constants: { O_WRONLY: -1, O_RDWR: -1, O_CREAT: -1, O_TRUNC: -1, O_APPEND: -1, O_EXCL: -1, O_DIRECTORY: -1 }, // unused
			writeSync(fd, buf) {
				outputBuf += decoder.decode(buf);
				const nl = outputBuf.lastIndexOf("\n");
				if (nl != -1) {
					console.log(outputBuf.substring(0, nl));
					outputBuf = outputBuf.substring(nl + 1);
				}
				return buf.length;
			},
			write(fd, buf, offset, length, position, callback) {
				if (offset !== 0 || length !== buf.length || position !== null) {
					callback(enosys());
					return;
				}
				const n = this.writeSync(fd, buf);
				callback(null, n);
			},
			chmod(path, mode, callback) { callback(enosys()); },
			chown(path, uid, gid, callback) { callback(enosys()); },
			close(fd, callback) { callback(enosys()); },
			fchmod(fd, mode, callback) { callback(enosys()); },
			fchown(fd, uid, gid, callback) { callback(enosys()); },
			fstat(fd, callback) { callback(enosys()); },
			fsync(fd, callback) { callback(null); },
			ftruncate(fd, length, callback) { callback(enosys()); },
			lchown(path, uid, gid, callback) { callback(enosys()); },
			link(path, link, callback) { callback(enosys()); },
			lstat(path, callback) { callback(enosys()); },
			mkdir(path, perm, callback) { callback(enosys()); },
			open(path, flags, mode, callback) { callback(enosys()); },
			read(fd, buffer, offset, length, position, callback) { callback(enosys()); },
			readdir(path, callback) { callback(enosys()); },
			readlink(path, callback) { callback(enosys()); },
			rename(from, to, callback) { callback(enosys()); },
			rmdir(path, callback) { callback(enosys()); },
			stat(path, callback) { callback(enosys()); },
			symlink(path, link, callback) { callback(enosys()); },
			truncate(path, length, callback) { callback(enosys()); },
			unlink(path, callback) { callback(enosys()); },
			utimes(path, atime, mtime, callback) { callback(enosys()); },
		};
	}

	if (!globalThis.process) {
		globalThis.process = {
			getuid() { return -1; },
			getgid() { return -1; },
			geteuid() { return -1; },
			getegid() { return -1; },
			getgroups() { throw enosys(); },
			pid: -1,
			ppid: -1,
			umask() { throw enosys(); },
			cwd() { throw enosys(); },
			chdir() { throw enosys(); },
		}
	}

	if (!globalThis.crypto) {
		throw new Error("globalThis.crypto is not available, polyfill required (crypto.getRandomValues only)");
	}

	if (!globalThis.performance) {
		throw new Error("globalThis.performance is not available, polyfill required (performance.now only)");
	}

	if (!globalThis.TextEncoder) {
		throw new Error("globalThis.TextEncoder is not available, polyfill required");
	}

	if (!globalThis.TextDecoder) {
		throw new Error("globalThis.TextDecoder is not available, polyfill required");
	}

	const encoder = new TextEncoder("utf-8");
	const decoder = new TextDecoder("utf-8");
	let reinterpretBuf = new DataView(new ArrayBuffer(8));
	var logLine = [];
	const wasmExit = {}; // thrown to exit via proc_exit (not an error)

	globalThis.Go = class {
		constructor() {
			this._callbackTimeouts = new Map();
			this._nextCallbackTimeoutID = 1;

			const mem = () => {
				// The buffer may change when requesting more memory.
				return new DataView(this._inst.exports.memory.buffer);
			}

			const unboxValue = (v_ref) => {
				reinterpretBuf.setBigInt64(0, v_ref, true);
				const f = reinterpretBuf.getFloat64(0, true);
				if (f === 0) {
					return undefined;
				}
				if (!isNaN(f)) {
					return f;
				}

				const id = v_ref & 0xffffffffn;
				return this._values[id];
			}


			const loadValue = (addr) => {
				let v_ref = mem().getBigUint64(addr, true);
				return unboxValue(v_ref);
			}

			const boxValue = (v) => {
				const nanHead = 0x7FF80000n;

				if (typeof v === "number") {
					if (isNaN(v)) {
						return nanHead << 32n;
					}
					if (v === 0) {
						return (nanHead << 32n) | 1n;
					}
					reinterpretBuf.setFloat64(0, v, true);
					return reinterpretBuf.getBigInt64(0, true);
				}

				switch (v) {
					case undefined:
						return 0n;
					case null:
						return (nanHead << 32n) | 2n;
					case true:
						return (nanHead << 32n) | 3n;
					case false:
						return (nanHead << 32n) | 4n;
				}

				let id = this._ids.get(v);
				if (id === undefined) {
					id = this._idPool.pop();
					if (id === undefined) {
						id = BigInt(this._values.length);
					}
					this._values[id] = v;
					this._goRefCounts[id] = 0;
					this._ids.set(v, id);
				}
				this._goRefCounts[id]++;
				let typeFlag = 1n;
				switch (typeof v) {
					case "string":
						typeFlag = 2n;
						break;
					case "symbol":
						typeFlag = 3n;
						break;
					case "function":
						typeFlag = 4n;
						break;
				}
				return id | ((nanHead | typeFlag) << 32n);
			}

			const storeValue = (addr, v) => {
				let v_ref = boxValue(v);
				mem().setBigUint64(addr, v_ref, true);
			}

			const loadSlice = (array, len, cap) => {
				return new Uint8Array(this._inst.exports.memory.buffer, array, len);
			}

			const loadSliceOfValues = (array, len, cap) => {
				const a = new Array(len);
				for (let i = 0; i < len; i++) {
					a[i] = loadValue(array + i * 8);
				}
				return a;
			}

			const loadString = (ptr, len) => {
				return decoder.decode(new DataView(this._inst.exports.memory.buffer, ptr, len));
			}

			const timeOrigin = Date.now() - performance.now();
			const wasi_EBADF = 8;
			const wasi_ENOSYS = 52;
			this.importObject = {
				wasi_snapshot_preview1: {
					// https://github.com/WebAssembly/WASI/blob/snapshot-01/phases/snapshot/docs.md
					fd_write: function(fd, iovs_ptr, iovs_len, nwritten_ptr) {
						iovs_ptr >>>= 0;
						iovs_len >>>= 0;
						nwritten_ptr >>>= 0;
						let nwritten = 0;
						if (fd == 1) {
							for (let iovs_i = 0; iovs_i < iovs_len; iovs_i++) {
								let iov_ptr = iovs_ptr + iovs_i * 8; // assuming wasm32
								let ptr = mem().getUint32(iov_ptr + 0, true);
								let len = mem().getUint32(iov_ptr + 4, true);
								nwritten += len;
								for (let i = 0; i < len; i++) {
									let c = mem().getUint8(ptr + i);
									if (c == 13) { // CR
										// ignore
									} else if (c == 10) { // LF
										// write line
										let line = decoder.decode(new Uint8Array(logLine));
										logLine = [];
										console.log(line);
									} else {
										logLine.push(c);
									}
								}
							}
						} else {
							console.error('invalid file descriptor:', fd);
						}
						mem().setUint32(nwritten_ptr, nwritten, true);
						return 0;
					},
					fd_read: () => wasi_ENOSYS,
					fd_close: () => wasi_ENOSYS,
					fd_fdstat_get: () => wasi_ENOSYS,
					fd_prestat_get: () => wasi_EBADF, // wasi-libc relies on this errno value
					fd_prestat_dir_name: () => wasi_ENOSYS,
					fd_seek: () => wasi_ENOSYS,
					path_open: () => wasi_ENOSYS,
					proc_exit: (code) => {
						this.exited = true;
						this.exitCode = code;
						this._resolveExitPromise();
						throw wasmExit;
					},
					random_get: (bufPtr, bufLen) => {
						bufPtr >>>= 0;
						bufLen >>>= 0;
						crypto.getRandomValues(loadSlice(bufPtr, bufLen));
						return 0;
					},
				},
				gojs: {
					// func ticks() int64
					"runtime.ticks": () => {
						return BigInt((timeOrigin + performance.now()) * 1e6);
					},

					// func getRandomData(r []byte)
					"runtime.getRandomData": (slice_ptr, slice_len, slice_cap) => {
						slice_ptr >>>= 0;
						slice_len >>>= 0;
						crypto.getRandomValues(loadSlice(slice_ptr, slice_len, slice_cap));
					},

					// func sleepTicks(timeout int64)
					"runtime.sleepTicks": (timeout) => {
						// Do not sleep, only reactivate scheduler after the given timeout.
						setTimeout(() => {
							if (this.exited) return;
							try {
								this._inst.exports.go_scheduler();
							} catch (e) {
								if (e !== wasmExit) throw e;
							}
						}, Number(timeout) / 1e6);
					},

					// func finalizeRef(v ref)
					"syscall/js.finalizeRef": (v_ref) => {
						// Note: TinyGo does not support finalizers so this is only called
						// for one specific case, by js.go:jsString. and can/might leak memory.
						const id = v_ref & 0xffffffffn;
						if (this._goRefCounts?.[id] !== undefined) {
							this._goRefCounts[id]--;
							if (this._goRefCounts[id] === 0) {
								const v = this._values[id];
								this._values[id] = null;
								this._ids.delete(v);
								this._idPool.push(id);
							}
						} else {
							console.error("syscall/js.finalizeRef: unknown id", id);
						}
					},

					// func stringVal(value string) ref
					"syscall/js.stringVal": (value_ptr, value_len) => {
						value_ptr >>>= 0;
						value_len >>>= 0;
						const s = loadString(value_ptr, value_len);
						return boxValue(s);
					},

					// func valueGet(v ref, p string) ref
					"syscall/js.valueGet": (v_ref, p_ptr, p_len) => {
						p_ptr >>>= 0;
						p_len >>>= 0;
						let prop = loadString(p_ptr, p_len);
						let v = unboxValue(v_ref);
						let result = Reflect.get(v, prop);
						return boxValue(result);
					},

					// func valueSet(v ref, p string, x ref)
					"syscall/js.valueSet": (v_ref, p_ptr, p_len, x_ref) => {
						p_ptr >>>= 0;
						p_len >>>= 0;
						const v = unboxValue(v_ref);
						const p = loadString(p_ptr, p_len);
						const x = unboxValue(x_ref);
						Reflect.set(v, p, x);
					},

					// func valueDelete(v ref, p string)
					"syscall/js.valueDelete": (v_ref, p_ptr, p_len) => {
						p_ptr >>>= 0;
						p_len >>>= 0;
						const v = unboxValue(v_ref);
						const p = loadString(p_ptr, p_len);
						Reflect.deleteProperty(v, p);
					},

					// func valueIndex(v ref, i int) ref
					"syscall/js.valueIndex": (v_ref, i) => {
						return boxValue(Reflect.get(unboxValue(v_ref), i));
					},

					// valueSetIndex(v ref, i int, x ref)
					"syscall/js.valueSetIndex": (v_ref, i, x_ref) => {
						Reflect.set(unboxValue(v_ref), i, unboxValue(x_ref));
					},

					// func valueCall(v ref, m string, args []ref) (ref, bool)
					"syscall/js.valueCall": (ret_addr, v_ref, m_ptr, m_len, args_ptr, args_len, args_cap) => {
						ret_addr >>>= 0;
						m_ptr >>>= 0;
						m_len >>>= 0;
						args_ptr >>>= 0;
						args_len >>>= 0;
						const v = unboxValue(v_ref);
						const name = loadString(m_ptr, m_len);
						const args = loadSliceOfValues(args_ptr, args_len, args_cap);
						try {
							const m = Reflect.get(v, name);
							storeValue(ret_addr, Reflect.apply(m, v, args));
							mem().setUint8(ret_addr + 8, 1);
						} catch (err) {
							storeValue(ret_addr, err);
							mem().setUint8(ret_addr + 8, 0);
						}
					},

					// func valueInvoke(v ref, args []ref) (ref, bool)
					"syscall/js.valueInvoke": (ret_addr, v_ref, args_ptr, args_len, args_cap) => {
						ret_addr >>>= 0;
						args_ptr >>>= 0;
						args_len >>>= 0;
						try {
							const v = unboxValue(v_ref);
							const args = loadSliceOfValues(args_ptr, args_len, args_cap);
							storeValue(ret_addr, Reflect.apply(v, undefined, args));
							mem().setUint8(ret_addr + 8, 1);
						} catch (err) {
							storeValue(ret_addr, err);
							mem().setUint8(ret_addr + 8, 0);
						}
					},

					// func valueNew(v ref, args []ref) (ref, bool)
					"syscall/js.valueNew": (ret_addr, v_ref, args_ptr, args_len, args_cap) => {
						ret_addr >>>= 0;
						args_ptr >>>= 0;
						args_len >>>= 0;
						const v = unboxValue(v_ref);
						const args = loadSliceOfValues(args_ptr, args_len, args_cap);
						try {
							storeValue(ret_addr, Reflect.construct(v, args));
							mem().setUint8(ret_addr + 8, 1);
						} catch (err) {
							storeValue(ret_addr, err);
							mem().setUint8(ret_addr + 8, 0);
						}
					},

					// func valueLength(v ref) int
					"syscall/js.valueLength": (v_ref) => {
						return unboxValue(v_ref).length;
					},

					// valuePrepareString(v ref) (ref, int)
					"syscall/js.valuePrepareString": (ret_addr, v_ref) => {
						ret_addr >>>= 0;
						const s = String(unboxValue(v_ref));
						const str = encoder.encode(s);
						storeValue(ret_addr, str);
						mem().setInt32(ret_addr + 8, str.length, true);
					},

					// valueLoadString(v ref, b []byte)
					"syscall/js.valueLoadString": (v_ref, slice_ptr, slice_len, slice_cap) => {
						slice_ptr >>>= 0;
						slice_len >>>= 0;
						const str = unboxValue(v_ref);
						loadSlice(slice_ptr, slice_len, slice_cap).set(str);
					},

					// func valueInstanceOf(v ref, t ref) bool
					"syscall/js.valueInstanceOf": (v_ref, t_ref) => {
						return unboxValue(v_ref) instanceof unboxValue(t_ref);
					},

					// func copyBytesToGo(dst []byte, src ref) (int, bool)
					"syscall/js.copyBytesToGo": (ret_addr, dest_addr, dest_len, dest_cap, src_ref) => {
						ret_addr >>>= 0;
						dest_addr >>>= 0;
						dest_len >>>= 0;
						let num_bytes_copied_addr = ret_addr;
						let returned_status_addr = ret_addr + 4; // Address of returned boolean status variable

						const dst = loadSlice(dest_addr, dest_len);
						const src = unboxValue(src_ref);
						if (!(src instanceof Uint8Array || src instanceof Uint8ClampedArray)) {
							mem().setUint8(returned_status_addr, 0); // Return "not ok" status
							return;
						}
						const toCopy = src.subarray(0, dst.length);
						dst.set(toCopy);
						mem().setUint32(num_bytes_copied_addr, toCopy.length, true);
						mem().setUint8(returned_status_addr, 1); // Return "ok" status
					},

					// copyBytesToJS(dst ref, src []byte) (int, bool)
					// Originally copied from upstream Go project, then modified:
					//   https://github.com/golang/go/blob/3f995c3f3b43033013013e6c7ccc93a9b1411ca9/misc/wasm/wasm_exec.js#L404-L416
					"syscall/js.copyBytesToJS": (ret_addr, dst_ref, src_addr, src_len, src_cap) => {
						ret_addr >>>= 0;
						src_addr >>>= 0;
						src_len >>>= 0;
						let num_bytes_copied_addr = ret_addr;
						let returned_status_addr = ret_addr + 4; // Address of returned boolean status variable

						const dst = unboxValue(dst_ref);
						const src = loadSlice(src_addr, src_len);
						if (!(dst instanceof Uint8Array || dst instanceof Uint8ClampedArray)) {
							mem().setUint8(returned_status_addr, 0); // Return "not ok" status
							return;
						}
						const toCopy = src.subarray(0, dst.length);
						dst.set(toCopy);
						mem().setUint32(num_bytes_copied_addr, toCopy.length, true);
						mem().setUint8(returned_status_addr, 1); // Return "ok" status
					},
				}
			};

			// Go 1.20 uses 'env'. Go 1.21 uses 'gojs'.
			// For compatibility, we use both as long as Go 1.20 is supported.
			this.importObject.env = this.importObject.gojs;
		}

		async run(instance) {
			this._inst = instance;
			this._values = [ // JS values that Go currently has references to, indexed by reference id
				NaN,
				0,
				null,
				true,
				false,
				globalThis,
				this,
			];
			this._goRefCounts = []; // number of references that Go has to a JS value, indexed by reference id
			this._ids = new Map();  // mapping from JS values to reference ids
			this._idPool = [];      // unused ids that have been garbage collected
			this.exited = false;    // whether the Go program has exited
			this.exitCode = 0;
			// syscall/js.handleEvent reads _pendingEvent and returns early only when
			// it IsNull(). Leaving it `undefined` is not null, so handleEvent falls
			// through to cb.Get("id") and panics with "call of Value.Get on
			// undefined", which surfaces as RuntimeError: unreachable. Upstream Go's
			// wasm_exec.js initializes this in its constructor; this line restores
			// parity. Any resume() that runs without a pending event -- and resume()
			// always spawns a handleEvent goroutine -- depends on it.
			this._pendingEvent = null; // event awaiting dispatch by syscall/js.handleEvent

			if (this._inst.exports._start) {
				let exitPromise = new Promise((resolve, reject) => {
					this._resolveExitPromise = resolve;
				});

				// Run program, but catch the wasmExit exception that's thrown
				// to return back here.
				try {
					this._inst.exports._start();
				} catch (e) {
					if (e !== wasmExit) throw e;
				}

				await exitPromise;
				return this.exitCode;
			} else {
				this._inst.exports._initialize();
			}
		}

		_resume() {
			if (this.exited) {
				throw new Error("Go program has already exited");
			}
			try {
				this._inst.exports.resume();
			} catch (e) {
				if (e !== wasmExit) throw e;
			}
			if (this.exited) {
				this._resolveExitPromise();
			}
		}

		_makeFuncWrapper(id) {
			const go = this;
			return function() {
				const event = { id: id, this: this, args: arguments };
				go._pendingEvent = event;
				go._resume();
				return event.result;
			};
		}
	}
})();

  var mine = globalThis.Go;
  if (had) { globalThis.Go = prev; } else { try { delete globalThis.Go; } catch (e) { globalThis.Go = undefined; } }
  return mine;
})();

;
// dist/winbox-loader.js
// Starts the window-manager wasm module (cmd/winbox-js) and publishes the
// global `WinBox` constructor pages build their windows on.
//
// The constructor exists only once the module's main has run, which a <script>
// tag did not require of the JS library this replaces. Nothing may open a
// window before then, so this publishes globalThis.__winboxReady for page code
// to await (or poll `typeof WinBox === "function"`).
//
// Module bytes come from, in order:
//   globalThis.__WINBOX_WASM_B64__ — gzipped module inlined as base64
//     (single-file pages with no server to fetch from);
//   globalThis.__WINBOX_WASM_URL__ — explicit URL;
//   "winbox.wasm" resolved against document.baseURI.
(function () {
  // No document means no window manager to install (e.g. the bundle is
  // precached by a service worker, which never executes it).
  if (typeof document === "undefined") { return; }
  if (typeof globalThis.WinBox === "function") {
    globalThis.__winboxReady = Promise.resolve(globalThis.WinBox);
    return;
  }

  var resolve, reject;
  globalThis.__winboxReady = new Promise(function (a, b) { resolve = a; reject = b; });
  // cmd/winbox-js invokes __winboxResolve once the constructor is installed —
  // the direct path; the settle() poll below stays as a fallback.
  globalThis.__winboxResolve = function (wb) { resolve(wb || globalThis.WinBox); };

  // go.run() hands control back as soon as the module's main blocks, but the
  // constructor is installed from Go — wait for it to actually appear rather
  // than assuming it is there on the turn run() returns.
  function settle() {
    var tries = 0;
    (function poll() {
      if (typeof globalThis.WinBox === "function") { resolve(globalThis.WinBox); return; }
      if (++tries > 300) { reject(new Error("winbox.wasm ran but installed no WinBox")); return; }
      setTimeout(poll, 10);
    })();
  }

  function moduleBytes() {
    var b64 = globalThis.__WINBOX_WASM_B64__;
    if (b64) {
      var bin = Uint8Array.from(atob(b64), function (c) { return c.charCodeAt(0); });
      return new Response(new Blob([bin]).stream().pipeThrough(new DecompressionStream("gzip"))).arrayBuffer();
    }
    var url = globalThis.__WINBOX_WASM_URL__ || new URL("winbox.wasm", document.baseURI).href;
    return fetch(url).then(function (r) {
      if (!r.ok) { throw new Error("winbox.wasm: HTTP " + r.status); }
      return r.arrayBuffer();
    });
  }

  var Go = globalThis.__winboxGo;
  if (typeof Go !== "function") {
    reject(new Error("winbox loader: winbox-exec.js did not load"));
    return;
  }

  moduleBytes().then(function (buf) {
    var go = new Go();
    return WebAssembly.instantiate(buf, go.importObject).then(function (res) {
      go.run(res.instance);
      settle();
    });
  }).catch(function (e) {
    // A failure here means no windows at all, so say so rather than leaving
    // page code polling forever with no explanation.
    try { console.error("winbox: window manager (winbox.wasm) failed to start —", e); } catch (_) {}
    reject(e);
  });
})();

;
// panel-nowasm.js — the desk chrome as a plain-JS asset, for no-wasm pages.
//
// The persistent taskbar + window registry the desk surfaces share, in plain
// JS with no wasm dependency. It replaced the retired browse.js panel
// (SkywireBrowse.mountPanel): the browsing ENGINE moved into the wasm-visor
// binary (globalThis.skywireBrowser), but the CHROME — the always-on bar, the
// ☰ launcher menu, per-window taskbar buttons — must exist even on pages that
// load no wasm at all (the native hypervisor's desk is a shell OVER the host
// visor; nothing wasm-shaped boots there). Launcher entries appear only for
// capabilities that are actually present on the page (a terminal needs
// skywireShell, a browser window needs SkywireGoBrowser), so one panel serves
// every desk mode.
//
//	globalThis.skywireDeskPanel.mount(document, opts) → panel
//	  opts.dashboardURL — same-origin URL for the dashboard window (omit = no entry)
//	  opts.title       — taskbar label (default "skywire")
//	panel.open(winboxOpts) → WinBox registered on the taskbar
//	panel.root / panel.bar — the windows root / taskbar elements
//
// Published as globalThis.__skywireDesk by the callers (desk-boot, the native
// launcher) once mounted — the signal desk-boot's waiters key on. Element ids
// (skywire-skynet-root / skywire-skynet-taskbar) are kept from the previous
// panel so existing geometry code keeps working.
(function () {
	'use strict';
	if (globalThis.skywireDeskPanel) return;

	function mount(doc, opts) {
		opts = opts || {};
		var root = doc.getElementById('skywire-skynet-root');
		if (!root) {
			root = doc.createElement('div');
			root.id = 'skywire-skynet-root';
			root.style.cssText = 'position:fixed;inset:0;pointer-events:none;z-index:50';
			// The root spans the viewport and is pointer-events:none so clicks fall
			// through to the page wherever no window covers it. Anything mounted in
			// it must opt back in or it renders and updates but cannot be clicked --
			// the taskbar and menu set auto inline, windows had nothing, so every
			// WinBox was inert. A rule rather than a per-instance assignment: it
			// covers windows opened later and does not depend on the WinBox API
			// exposing its root element.
			var peStyle = doc.createElement('style');
			peStyle.textContent = '#skywire-skynet-root .winbox{pointer-events:auto}';
			root.appendChild(peStyle);
			doc.body.appendChild(root);
		}
		var bar = doc.getElementById('skywire-skynet-taskbar');
		if (!bar) {
			bar = doc.createElement('div');
			bar.id = 'skywire-skynet-taskbar';
			bar.style.cssText = 'position:fixed;left:0;right:0;bottom:0;height:36px;display:flex;' +
				'align-items:center;gap:6px;padding:0 8px;background:#100d18;color:#cdd2da;' +
				'border-top:1px solid #2a2342;font:12px monospace;z-index:60;pointer-events:auto';
			doc.body.appendChild(bar);
		}
		bar.innerHTML = '';

		var menuBtn = doc.createElement('button');
		menuBtn.textContent = '☰';
		menuBtn.title = 'desk menu';
		menuBtn.style.cssText = 'background:#2a2342;color:#cdd2da;border:1px solid #3a3352;' +
			'cursor:pointer;font:14px monospace;padding:2px 10px;border-radius:4px';
		bar.appendChild(menuBtn);

		var label = doc.createElement('span');
		label.textContent = opts.title || 'skywire';
		label.style.cssText = 'color:#9d7cff;font-weight:600;margin-right:6px';
		bar.appendChild(label);

		var winArea = doc.createElement('div');
		winArea.style.cssText = 'display:flex;gap:4px;overflow-x:auto;flex:1';
		bar.appendChild(winArea);

		var menu = doc.createElement('div');
		menu.style.cssText = 'position:fixed;left:8px;bottom:40px;display:none;flex-direction:column;' +
			'background:#17141f;border:1px solid #2a2342;border-radius:6px;padding:4px;z-index:61;' +
			'pointer-events:auto;font:12px monospace;min-width:160px';
		doc.body.appendChild(menu);
		menuBtn.addEventListener('click', function () {
			menu.style.display = menu.style.display === 'none' ? 'flex' : 'none';
		});

		function menuItem(text, fn) {
			var b = doc.createElement('button');
			b.textContent = text;
			b.style.cssText = 'background:none;border:none;color:#cdd2da;text-align:left;' +
				'cursor:pointer;font:inherit;padding:6px 10px';
			b.addEventListener('mouseenter', function () { b.style.background = '#2a2342'; });
			b.addEventListener('mouseleave', function () { b.style.background = 'none'; });
			b.addEventListener('click', function () { menu.style.display = 'none'; fn(); });
			menu.appendChild(b);
			return b;
		}

		var panel = {
			root: root,
			bar: bar,
			// open wraps WinBox: window mounts in the panel's root (shared
			// stacking context) above the taskbar, and gets a taskbar button
			// that focuses/minimizes it and disappears on close.
			open: function (wo) {
				wo = wo || {};
				wo.root = root;
				wo.bottom = wo.bottom === undefined ? 36 : wo.bottom;
				if (!wo['class']) wo['class'] = ['skywire-wb', 'no-full'];
				var btn = doc.createElement('button');
				btn.textContent = wo.title || 'window';
				btn.style.cssText = 'background:#1b1726;color:#cdd2da;border:1px solid #2a2342;' +
					'cursor:pointer;font:inherit;padding:2px 8px;border-radius:4px;white-space:nowrap';
				var onclose = wo.onclose;
				wo.onclose = function (force) {
					btn.remove();
					if (onclose) return onclose.call(this, force);
					return false;
				};
				var wb = new globalThis.WinBox(wo);
				btn.addEventListener('click', function () {
					if (wb.min) { wb.minimize(false); }
					wb.focus();
				});
				winArea.appendChild(btn);
				return wb;
			},
		};

		if (opts.dashboardURL) {
			menuItem('dashboard', function () {
				panel.open({ title: 'dashboard', url: opts.dashboardURL, x: 'center', y: 'center', width: '85%', height: '85%' });
			});
		}
		// Capability-gated entries: present only when the page actually carries
		// the machinery (the wasm-visor DOM instance installs these; the native
		// desk page has neither and shows neither).
		if (globalThis.skywireShell && typeof globalThis.skywireShell.open === 'function') {
			menuItem('terminal', function () {
				var wb = panel.open({ title: 'terminal', x: 'center', y: 'center', width: '70%', height: '60%' });
				globalThis.skywireShell.open(wb.body);
			});
		}
		if (globalThis.SkywireGoBrowser && typeof globalThis.SkywireGoBrowser.open === 'function') {
			menuItem('browser', function () { globalThis.SkywireGoBrowser.open(); });
		}
		return panel;
	}

	globalThis.skywireDeskPanel = { mount: mount };
})();

;
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
		// routerTail: a SELECTIVE ring of route-establishment lines. The full
		// tail churns through its window in seconds under dmsg DEBUG spam, so
		// the one error that explains a failed dial is gone before anyone
		// looks; these lines are rare and survive.
		let routerTail = '';
		let lineBuf = '';
		const ROUTERISH = /(router|route_setup|RouteGroup|routegroup|setupclient|rule|cascade|rsn)/i;
		const tailDec = new TextDecoder();
		const keepTail = (buf) => {
			try {
				const s = tailDec.decode(buf, { stream: true });
				stderrTail = (stderrTail + s).slice(-16384);
				lineBuf += s;
				let nl;
				while ((nl = lineBuf.indexOf('\n')) >= 0) {
					const line = lineBuf.slice(0, nl);
					lineBuf = lineBuf.slice(nl + 1);
					if (ROUTERISH.test(line)) { routerTail = (routerTail + line + '\n').slice(-8192); }
				}
			} catch (e) { /* ignore */ }
		};
		// Live observability: __skywireExecTails[iid]() returns the tail at any
		// moment (DevTools, CDP probes, the operator) — the only other copy of
		// a long-running instance's log lives inside an xterm nobody can read
		// programmatically. Kept after exit (the crash's last words); replaced
		// naturally as new instances reuse the registry.
		try {
			(globalThis.__skywireExecTails = globalThis.__skywireExecTails || {})[iid] =
				function () { return stderrTail; };
			globalThis.__skywireExecTails[iid].argv = args.slice();
			globalThis.__skywireExecTails[iid].router = function () { return routerTail; };
		} catch (e) { /* ignore */ }
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
			// Record how this instance ended, for consumers that must tell a
			// CRASH from a deliberate stop (the desk session: a crashed visor
			// restarts on the next load; only a clean exit stays stopped).
			try {
				const reg2 = globalThis.__skywireExecTails;
				if (reg2 && reg2[iid]) reg2[iid].exitInfo = { code: code, crashed: !!runErr };
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

;
// gobrowser-loader.js — opens the netscrape Go/wasm browser
// (github.com/0magnet/netscrape) in a winbox window, wired to the visor's
// transports.
//
// The browser is NO LONGER a separate wasm module. It is compiled into the
// wasm-visor binary and exposed as globalThis.skywireBrowser.open(el) by that
// binary's DOM-side instance (the same one that carries the terminal —
// cmd/wasm-visor/browser_js.go). So this launcher does not fetch or instantiate
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
