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
// jsfs.sync has the same methods returning results or throwing, for loaders
// that cannot wait for a callback, such as a WASI one.
//
// Stdout/stderr: fds 1 and 2 route through jsfs.stdio, a swappable sink so
// the terminal can capture the CURRENT command's output; instances run
// sequentially in the shell, so a single active sink suffices. fd 0 reads
// return EOF by default. A process with a stdin pipe names it through
// jsfs.stdio.stdinPipe (a function returning its read fd, or -1), and a read
// with a callback — Go's — then waits on that pipe as on any other.
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
		const n = readNow(e, buf, offset, length, position);
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
		stdinPipe: () => -1, // a pipe read fd that fd 0 reads wait on, or -1
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
	// pipeDrop closes both ends of fd's pipe outright, whatever references are
	// held, and forgets its waiting readers without calling them: they belong
	// to a program that is gone, and a callback into it would fail.
	function pipeDrop(fd) {
		const e = pipeEnds.get(fd);
		if (!e) return false;
		const p = e.pipe;
		p.readers.length = 0;
		p.chunks.length = 0;
		p.wRefs = 0;
		p.rRefs = 0;
		for (const [k, v] of pipeEnds) if (v.pipe === p) pipeEnds.delete(k);
		return true;
	}

	// ---- constants (node names; values mirror Linux) -----------------------
	const constants = {
		O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2,
		O_CREAT: 0o100, O_EXCL: 0o200, O_TRUNC: 0o1000,
		O_APPEND: 0o2000, O_DIRECTORY: 0o200000, O_NONBLOCK: 0o4000, O_SYNC: 0o4010000,
	};

	// ---- synchronous implementations ---------------------------------------
	// Each returns its result or throws an error carrying .code. The node-style
	// fs object below wraps them, and jsfs.sync exposes them to loaders.
	const S = {
		open(path, flags, mode) {
			const acc = flags & 3;
			const r = resolve(path, true);
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
		},

		close(fd) { if (pipeClose(fd)) return undefined; fdEntry(fd); fds.delete(fd); return undefined; },

		stat(path) { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); return statOf(r.node); },
		lstat(path) { const r = resolve(path, false); if (!r.node) throw mkerr('ENOENT', path); return statOf(r.node); },
		fstat(fd) { return statOf(fdEntry(fd).node); },

		mkdir(path, perm) {
			const r = resolve(path, true);
			if (r.node) throw mkerr('EEXIST', path);
			if (!r.parent) throw mkerr('ENOENT', path);
			r.parent.entries.set(r.name, mknode(S_IFDIR, perm));
			r.parent.mtimeMs = now();
			return undefined;
		},

		rmdir(path) {
			const r = resolve(path, false);
			if (!r.node) throw mkerr('ENOENT', path);
			if (r.node.entries === null) throw mkerr('ENOTDIR', path);
			if (r.node.entries.size > 0) throw mkerr('ENOTEMPTY', path);
			r.parent.entries.delete(r.name);
			return undefined;
		},

		readdir(path) {
			const r = resolve(path, true);
			if (!r.node) throw mkerr('ENOENT', path);
			if (r.node.entries === null) throw mkerr('ENOTDIR', path);
			return Array.from(r.node.entries.keys());
		},

		rename(from, to) {
			const rf = resolve(from, false);
			if (!rf.node) throw mkerr('ENOENT', from);
			const rt = resolve(to, false);
			if (!rt.parent) throw mkerr('ENOENT', to);
			rf.parent.entries.delete(rf.name);
			rt.parent.entries.set(rt.name, rf.node);
			rf.node.ctimeMs = now();
			return undefined;
		},

		unlink(path) {
			const r = resolve(path, false);
			if (!r.node) throw mkerr('ENOENT', path);
			if (r.node.entries !== null) throw mkerr('EISDIR', path);
			r.parent.entries.delete(r.name);
			return undefined;
		},

		truncate(path, length) {
			const r = resolve(path, true);
			if (!r.node || r.node.data === null) throw mkerr('ENOENT', path);
			r.node.data = resized(r.node.data, length);
			r.node.mtimeMs = now();
			return undefined;
		},
		ftruncate(fd, length) {
			const e = fdEntry(fd);
			if (e.node.data === null) throw mkerr('EINVAL', 'not a file');
			e.node.data = resized(e.node.data, length);
			e.node.mtimeMs = now();
			return undefined;
		},

		chmod(path, mode) { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); r.node.mode = ((r.node.mode & 0o170000) | (mode & 0o7777)) >>> 0; return undefined; },
		fchmod(fd, mode) { const e = fdEntry(fd); e.node.mode = ((e.node.mode & 0o170000) | (mode & 0o7777)) >>> 0; return undefined; },
		chown(path, uid, gid) { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); r.node.uid = uid; r.node.gid = gid; return undefined; },
		fchown(fd, uid, gid) { const e = fdEntry(fd); e.node.uid = uid; e.node.gid = gid; return undefined; },
		lchown(path, uid, gid) { const r = resolve(path, false); if (!r.node) throw mkerr('ENOENT', path); r.node.uid = uid; r.node.gid = gid; return undefined; },
		utimes(path, atime, mtime) { const r = resolve(path, true); if (!r.node) throw mkerr('ENOENT', path); r.node.atimeMs = atime * 1000; r.node.mtimeMs = mtime * 1000; return undefined; },

		readlink(path) { const r = resolve(path, false); if (!r.node) throw mkerr('ENOENT', path); if (r.node.target === null) throw mkerr('EINVAL', path); return r.node.target; },
		link(from, to) {
			const rf = resolve(from, true);
			if (!rf.node) throw mkerr('ENOENT', from);
			const rt = resolve(to, false);
			if (rt.node) throw mkerr('EEXIST', to);
			rt.parent.entries.set(rt.name, rf.node);
			rf.node.nlink++;
			return undefined;
		},
		symlink(target, path) {
			const r = resolve(path, false);
			if (r.node) throw mkerr('EEXIST', path);
			const ln = mknode(S_IFLNK, 0o777);
			ln.target = target;
			r.parent.entries.set(r.name, ln);
			return undefined;
		},
		fsync(fd) { fdEntry(fd); return undefined; },

		// read never waits. An empty pipe with a live writer and a lazy file whose
		// bytes have not landed answer EAGAIN, and the lazy fetch is started.
		read(fd, buf, offset, length, position) {
			if (fd === 0) {
				const pfd = stdio.stdinPipe ? stdio.stdinPipe() : -1;
				if (pfd >= 0 && isPipe(pfd)) return pipeReadNow(pfd, buf, offset, length);
				const chunk = stdio.stdin();
				if (!chunk || chunk.length === 0) return 0;
				const n = Math.min(length, chunk.length);
				buf.set(chunk.subarray(0, n), offset);
				return n;
			}
			if (isPipe(fd)) return pipeReadNow(fd, buf, offset, length);
			const e = fdEntry(fd);
			if (e.node.dev) return 0;
			if (e.node.data === null) throw mkerr('EISDIR', 'read dir');
			if (e.node.lazy) { lazyFetch(e.node); throw mkerr('EAGAIN', 'lazy file still loading: ' + e.path); }
			return readNow(e, buf, offset, length, position);
		},

		write(fd, buf, offset, length, position) {
			const sub = buf.subarray(offset, offset + length);
			if (fd === 1 || fd === 2) {
				if (position !== null && position !== undefined) throw mkerr('ESPIPE', 'seek on tty');
				return fsImpl.writeSync(fd, sub);
			}
			if (isPipe(fd)) return pipeWriteFrom(fd, sub);
			return writeAt(fdEntry(fd), sub, position === undefined ? null : position);
		},
	};

	function readNow(e, buf, offset, length, position) {
		const pos = (position === null || position === undefined) ? e.pos : position;
		const avail = e.node.data.length - pos;
		const n = Math.max(0, Math.min(length, avail));
		if (n > 0) buf.set(e.node.data.subarray(pos, pos + n), offset);
		if (position === null || position === undefined) e.pos += n;
		return n;
	}

	// lazyFetch starts populating a lazy file once, for a reader that cannot
	// wait. A failed fetch is forgotten so a later read tries again.
	function lazyFetch(node) {
		const lz = node.lazy;
		if (lz.pending) return;
		lz.pending = fetch(lz.url).then((r) => {
			if (!r.ok) throw new Error('lazy fetch ' + lz.url + ': ' + r.status);
			return r.arrayBuffer();
		}).then((ab) => {
			if (node.lazy !== lz) return;
			node.data = new Uint8Array(ab);
			node.lazy = null;
			if (lazyDirtyHook) { try { lazyDirtyHook(); } catch (e2) { /* best-effort */ } }
		}).catch(() => { lz.pending = null; });
	}

	// pipeReadNow takes what a pipe already holds. Readers queued by the
	// callback API go first, so a sync read never jumps ahead of them.
	function pipeReadNow(fd, buf, offset, length) {
		const e = pipeEnds.get(fd);
		if (!e || e.write) throw mkerr('EBADF', 'pipe read fd ' + fd);
		const p = e.pipe;
		const total = p.chunks.reduce((n, c) => n + c.length, 0);
		if (p.readers.length || total === 0) {
			if (!p.readers.length && p.wRefs <= 0) return 0;
			throw mkerr('EAGAIN', 'pipe empty');
		}
		let got = 0;
		while (got < length && p.chunks.length) {
			const c = p.chunks[0];
			const take = Math.min(length - got, c.length);
			buf.set(c.subarray(0, take), offset + got);
			got += take;
			if (take === c.length) p.chunks.shift();
			else p.chunks[0] = c.subarray(take);
		}
		return got;
	}

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
					const pfd = stdio.stdinPipe ? stdio.stdinPipe() : -1;
					if (pfd >= 0 && isPipe(pfd)) { pipeReadInto(pfd, buf, offset, length, cb); return; }
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
	};
	for (const name of ['open', 'close', 'stat', 'lstat', 'fstat', 'mkdir', 'rmdir',
		'readdir', 'rename', 'unlink', 'truncate', 'ftruncate', 'chmod', 'fchmod',
		'chown', 'fchown', 'lchown', 'utimes', 'readlink', 'link', 'symlink', 'fsync']) {
		fsImpl[name] = wrap(S[name]);
	}


	// ---- mounts -----------------------------------------------------------
	// A mount hands every path under a prefix, and every fd opened there, to a
	// provider: an object with node-style callback methods named like the fs
	// ones (open close read write stat lstat fstat readdir mkdir rmdir unlink
	// rename truncate ftruncate chmod fchmod chown fchown lchown utimes
	// readlink symlink link fsync). Paths reach it relative to the mount, "/"
	// being its root, and fds as the handles its open returned. read is
	// read(h, length, position, cb(err, bytes)) and write is
	// write(h, bytes, position, cb(err, n)); position is null for the
	// handle's own offset. A missing method answers ENOSYS.
	//
	// A provider may answer at any later time, and is usually another wasm
	// instance (a remote filesystem). Its answer reaches the caller on a
	// microtask, so the provider is never on the stack when the caller
	// resumes, and must never block in a method.
	const mounts = []; // { prefix, provider }, longest prefix first
	const mountFds = new Map(); // fd -> { m, h }

	function mountOf(path) {
		const p = normalize(path);
		if (p === null) return null;
		for (const m of mounts) {
			if (p === m.prefix) return { m, rel: '/' };
			if (p.startsWith(m.prefix + '/')) return { m, rel: p.slice(m.prefix.length) };
		}
		return null;
	}

	function withModeTests(st) {
		const t = (st.mode & 0o170000) >>> 0;
		st.isBlockDevice = () => false;
		st.isCharacterDevice = () => t === S_IFCHR;
		st.isDirectory = () => t === S_IFDIR;
		st.isFIFO = () => false;
		st.isFile = () => t === S_IFREG;
		st.isSocket = () => false;
		st.isSymbolicLink = () => t === S_IFLNK;
		return st;
	}

	function callMount(m, name, args, cb) {
		if (m.dead) { queueMicrotask(() => cb(mkerr('EIO', 'unmounted'))); return; }
		const fn = m.provider[name];
		if (typeof fn !== 'function') { queueMicrotask(() => cb(enosys(name))); return; }
		let answered = false;
		const reply = (err, res) => {
			if (answered) return;
			answered = true;
			if (err && !(err instanceof Error)) err = mkerr(err.code || 'EIO', err.message || String(err.code || err));
			queueMicrotask(() => cb(err || null, res));
		};
		try { fn.apply(m.provider, args.concat([reply])); } catch (e) { reply(e); }
	}

	for (const name of ['stat', 'lstat']) {
		const orig = fsImpl[name];
		fsImpl[name] = function (path, cb) {
			const r = mountOf(path);
			if (!r) return orig.call(this, path, cb);
			callMount(r.m, name, [r.rel], (err, st) => cb(err, err ? undefined : withModeTests(st)));
		};
	}
	for (const name of ['readdir', 'mkdir', 'rmdir', 'unlink', 'truncate', 'chmod',
		'chown', 'lchown', 'utimes', 'readlink']) {
		const orig = fsImpl[name];
		fsImpl[name] = function (path, ...rest) {
			const r = mountOf(path);
			if (!r) return orig.call(this, path, ...rest);
			const cb = rest.pop();
			callMount(r.m, name, [r.rel, ...rest], cb);
		};
	}
	for (const name of ['rename', 'link']) {
		const orig = fsImpl[name];
		fsImpl[name] = function (from, to, cb) {
			const a = mountOf(from), b = mountOf(to);
			if (!a && !b) return orig.call(this, from, to, cb);
			if (!a || !b || a.m !== b.m) { queueMicrotask(() => cb(mkerr('EXDEV', from + ' -> ' + to))); return; }
			callMount(a.m, name, [a.rel, b.rel], cb);
		};
	}
	{
		const orig = fsImpl.symlink;
		fsImpl.symlink = function (target, path, cb) {
			const r = mountOf(path);
			if (!r) return orig.call(this, target, path, cb);
			callMount(r.m, 'symlink', [target, r.rel], cb);
		};
	}
	{
		const orig = fsImpl.open;
		fsImpl.open = function (path, flags, mode, cb) {
			const r = mountOf(path);
			if (!r) return orig.call(this, path, flags, mode, cb);
			callMount(r.m, 'open', [r.rel, flags, mode], (err, h) => {
				if (err) { cb(err); return; }
				const fd = nextFd++;
				mountFds.set(fd, { m: r.m, h });
				cb(null, fd);
			});
		};
	}
	{
		const origRead = fsImpl.read, origWrite = fsImpl.write, origWriteSync = fsImpl.writeSync;
		fsImpl.read = function (fd, buf, offset, length, position, cb) {
			const e = mountFds.get(fd);
			if (!e) return origRead.call(this, fd, buf, offset, length, position, cb);
			callMount(e.m, 'read', [e.h, length, position === undefined ? null : position], (err, bytes) => {
				if (err) { cb(err); return; }
				const n = bytes ? Math.min(bytes.length, length) : 0;
				if (n) buf.set(bytes.subarray(0, n), offset);
				cb(null, n);
			});
		};
		fsImpl.write = function (fd, buf, offset, length, position, cb) {
			const e = mountFds.get(fd);
			if (!e) return origWrite.call(this, fd, buf, offset, length, position, cb);
			callMount(e.m, 'write', [e.h, buf.slice(offset, offset + length), position === undefined ? null : position], cb);
		};
		fsImpl.writeSync = function (fd, buf) {
			if (mountFds.has(fd)) throw mkerr('EAGAIN', 'writeSync on a mounted file');
			return origWriteSync.call(this, fd, buf);
		};
	}
	for (const name of ['fstat', 'close', 'ftruncate', 'fchmod', 'fchown', 'fsync']) {
		const orig = fsImpl[name];
		fsImpl[name] = function (fd, ...rest) {
			const e = mountFds.get(fd);
			if (!e) return orig.call(this, fd, ...rest);
			const cb = rest.pop();
			if (name === 'close') mountFds.delete(fd);
			callMount(e.m, name, [e.h, ...rest], name === 'fstat'
				? (err, st) => cb(err, err ? undefined : withModeTests(st))
				: cb);
		};
	}

	// ---- the synchronous API -----------------------------------------------
	// jsfs.sync has the fs methods without callbacks, for a caller that cannot
	// park, such as a WASI loader. Mount providers answer later, so their paths
	// and fds answer ENOTSUP here.
	const SYNC_PATH_ARGS = {
		open: [0], stat: [0], lstat: [0], mkdir: [0], rmdir: [0], readdir: [0],
		rename: [0, 1], unlink: [0], truncate: [0], chmod: [0], chown: [0],
		lchown: [0], utimes: [0], readlink: [0], link: [0, 1], symlink: [1],
	};
	const SYNC_FD_ARGS = {
		close: [0], fstat: [0], ftruncate: [0], fchmod: [0], fchown: [0],
		fsync: [0], read: [0], write: [0],
	};
	const syncImpl = { constants };
	for (const name of Object.keys(S)) {
		const pa = SYNC_PATH_ARGS[name] || [], fa = SYNC_FD_ARGS[name] || [];
		syncImpl[name] = function (...args) {
			for (const i of pa) if (mountOf(args[i])) throw mkerr('ENOTSUP', name + ' ' + args[i] + ': mounted, async only');
			for (const i of fa) if (mountFds.has(args[i])) throw mkerr('ENOTSUP', name + ' fd ' + args[i] + ': mounted, async only');
			return S[name](...args);
		};
	}

	// mount attaches provider at prefix, which becomes a directory in the tree
	// so its parent lists it. unmount detaches it; fds still open there answer
	// EIO from then on.
	function mount(prefix, provider) {
		const p = normalize(prefix);
		if (p === null || p === '/') throw mkerr('EINVAL', 'mount at ' + prefix);
		if (!provider || typeof provider !== 'object') throw mkerr('EINVAL', 'no provider');
		if (mounts.some((m) => m.prefix === p)) throw mkerr('EBUSY', p);
		mkdirp(p, 0o755);
		mounts.push({ prefix: p, provider });
		mounts.sort((a, b) => b.prefix.length - a.prefix.length);
	}

	function unmount(prefix) {
		const p = normalize(prefix);
		const i = mounts.findIndex((m) => m.prefix === p);
		if (i < 0) throw mkerr('EINVAL', 'not mounted: ' + prefix);
		const m = mounts[i];
		mounts.splice(i, 1);
		m.dead = true;
		if (typeof m.provider.unmounted === 'function') {
			try { m.provider.unmounted(); } catch (e) { /* the provider's own affair */ }
		}
	}
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

		// applySnapshot replaces what the snapshot covers. What it does not
		// cover — excluded paths, mounts — is left as THIS load made it: a
		// fresh, empty /tmp, new /dev devices, the page's own seeded files.
		// Nothing of the last session comes back outside the snapshot. Clearing
		// the whole root instead left a page that persisted only part of the
		// tree with no /tmp and no /dev at all. (A page that excludes nothing
		// snapshots /tmp and /dev too, and gets the old ones back: exclude
		// them unless that is what it wants.)
		function applySnapshot(entries) {
			(function prune(node, path) {
				for (const [name, child] of [...node.entries]) {
					const p = path + '/' + name;
					if (isExcluded(p)) continue;
					if ((child.mode & 0o170000) === S_IFDIR) {
						prune(child, p);
						if (child.entries.size === 0) node.entries.delete(name);
					} else {
						node.entries.delete(name);
					}
				}
			})(root, '');
			for (const e of entries) {
				// serialize() emits parents before children, so the parent dir
				// always exists by the time its entries arrive.
				const slash = e.p.lastIndexOf('/');
				const parent = slash === 0 ? root : resolve(e.p.slice(0, slash), true).node;
				const name = e.p.slice(slash + 1);
				const have = parent.entries.get(name);
				if (e.t === 'd' && have && (have.mode & 0o170000) === S_IFDIR) {
					// kept by prune for what is excluded inside it
					have.mode = S_IFDIR | (e.m & 0o7777);
					have.mtimeMs = e.mt || now();
					continue;
				}
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
			if (p && mountOf(p)) return true;
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
			// jsfs.sync mutates the same tree, so it schedules snapshots the same way.
			for (const obj of [fsImpl, syncImpl]) {
				for (const n of names) {
					const orig = obj[n];
					if (typeof orig !== 'function') continue;
					const pa = PATH_ARGS[n], fa = FD_ARGS[n];
					obj[n] = function (...args) {
						let paths = null;
						if (pa) paths = pa.map((i) => normalize(args[i]));
						else if (fa) paths = fa.map((i) => pathOfFd(args[i]));
						if (!snapshotUnaffected(paths)) markDirty();
						return orig.apply(this, args);
					};
				}
				const w = obj.write;
				obj.write = function (fd, ...rest) {
					if (fd > 2 && !isExcluded(pathOfFd(fd))) markDirty();
					return w.call(this, fd, ...rest);
				};
			}
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
		sync: syncImpl,  // the fs methods without callbacks; mounts answer ENOTSUP
		mkdirp,          // host-side seeding helpers
		writeFile: writeFileSeed,
		writeLazy,       // seed a file fetched from a url on first read
		readFile(path) { const r = resolve(path, true); if (!r.node || r.node.data === null) return null; return r.node.data; },
		setCwd(d) { processImpl.chdir(d); },
		pipe() { return makePipe(); },        // [readFd, writeFd]
		isPipe(fd) { return isPipe(fd); },
		pipeDrop(fd) { return pipeDrop(fd); }, // close a pipe for good, silently
		getCwd() { return cwd; },
		persist,         // IndexedDB snapshots: enable(db) → Promise<{restored}>
		mount,           // mount(prefix, provider): hand a subtree to a provider
		unmount,         // unmount(prefix)
		mounts() { return mounts.map((m) => m.prefix); },
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
	// /etc/skywire.conf is deliberately NOT seeded, for the same reason the
	// deb/arch packages do not ship it: `skywire autoconfig` writes it from
	// the annotated template on its first run and never overwrites it after,
	// so the tab gets the SAME file a packaged host gets — every knob present
	// and commented at its default, editable with the shell exactly as on
	// Linux. A three-line stub here used to pre-empt that, which left the tab
	// with no knobs to edit (ISHYPERVISOR among them) and made the browser
	// visor diverge from a native one for no reason.
	//
	// The directory is still ours to make: autoconfig creates it too, but a
	// shell that wants to drop a conf in before the first run should find it.
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

	// writeRequest sends one HTTP/1.0 request with Connection: close down side
	// 'a' of conn id.
	function writeRequest(v, id, hostLabel, method, path, body, headers) {
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
	}

	// parseHead reads a response head from the start of all, or returns null
	// until the blank line that ends it has arrived.
	function parseHead(all) {
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
	}

	// httpStream is httpExchange for a body that may never end, such as an
	// event stream. onHead(status, headers) fires once the head is in, then
	// onChunk(bytes) per piece of body, then onEnd(err) once. The timeout covers
	// only the wait for the head. Returns a function that stops it early.
	function httpStream(v, id, hostLabel, method, path, body, headers, onHead, onChunk, onEnd, timeoutMs) {
		let done = false;
		let head = null;
		let pending = new Uint8Array(0);
		let remaining = -1;
		const end = (err) => {
			if (done) return;
			done = true;
			clearTimeout(timer);
			v.close(id, 'a');
			onEnd(err || null);
		};
		const timer = setTimeout(() => { if (!head) end(new Error('timeout: ' + hostLabel)); }, timeoutMs || 30000);
		const deliver = (b) => {
			if (remaining >= 0 && b.length > remaining) b = b.subarray(0, remaining);
			if (remaining >= 0) remaining -= b.length;
			if (b.length) onChunk(b);
			if (remaining === 0) end();
		};
		writeRequest(v, id, hostLabel, method, path, body, headers);
		const pump = () => {
			while (!done) {
				const b = v.recv(id, 'a');
				if (b) {
					if (head) { deliver(b); continue; }
					const all = new Uint8Array(pending.length + b.length);
					all.set(pending, 0);
					all.set(b, pending.length);
					head = parseHead(all);
					if (!head) { pending = all; continue; }
					pending = null;
					clearTimeout(timer);
					remaining = head.contentLength;
					onHead(head.status, head.headers);
					deliver(all.subarray(head.bodyStart));
					continue;
				}
				if (v.eof(id, 'a')) { end(head ? null : new Error('malformed HTTP response from ' + hostLabel)); return; }
				v.onReadable(id, 'a', pump);
				return;
			}
		};
		pump();
		return () => end();
	}

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
		writeRequest(v, id, hostLabel, method, path, body, headers);
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
		const tryParseHead = parseHead;
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
		// timeoutMs, when set, is how long the worker waits for this page to answer
		// one request; it must exceed the slowest endpoint the page serves.
		enableSW(swPath, prefix, timeoutMs) {
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
				if (m.stream) {
					// A worker that streams gets the head, then each piece of the
					// body, then the end, so an event stream reaches the page live.
					const id = this.dial(m.port);
					if (id < 0) { reply.postMessage({ refused: true }); return; }
					let headSent = false;
					const stop = httpStream(this, id, '127.0.0.1:' + m.port, m.method, m.path, m.body, m.headers,
						(status, headers) => { headSent = true; reply.postMessage({ head: true, status: status, headers: headers }); },
						(b) => { const c = b.slice(); reply.postMessage({ chunk: c }, [c.buffer]); },
						(err) => {
							if (!headSent) {
								reply.postMessage({ head: true, status: 502, headers: { 'content-type': 'text/plain' } });
								reply.postMessage({ chunk: new TextEncoder().encode('vnet: fetch failed') });
							}
							reply.postMessage({ end: true, error: err ? String(err.message || err) : '' });
							reply.close();
						});
					reply.onmessage = (e) => { if (e.data && e.data.cancel) { stop(); reply.close(); } };
					return;
				}
				this.httpFetch(m.port, m.method, m.path, m.body, m.headers)
					.then((r) => {
						// Uint8Array bodies structured-clone fine; pass headers as
						// a plain object (already lowercased by httpFetch).
						reply.postMessage({ status: r.status, headers: r.headers, body: r.body });
					})
					.catch(() => { reply.postMessage({ status: 502, headers: { 'content-type': 'text/plain' }, body: new TextEncoder().encode('vnet: fetch failed') }); });
			});
			// coi=1 when this page is cross-origin isolated: an isolated parent
			// refuses an embedded document carrying no COEP of its own, and
			// everything this worker serves into an iframe is such a document.
			// Passed at register time because a service worker cannot see its
			// client's isolation.
			const url = (swPath || 'vnet-sw.js') + '?prefix=' + encodeURIComponent(prefix)
				+ (globalThis.crossOriginIsolated ? '&coi=1' : '')
				+ (timeoutMs > 0 ? '&timeout=' + Math.round(timeoutMs) : '');
			return navigator.serviceWorker.register(url, { scope: prefix })
				// Registering an unchanged URL does not check for a new worker, so a
				// deployed vnet-sw.js would wait for the browser's own schedule.
				.then((reg) => { reg.update().catch(() => {}); return reg; })
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

		// webSocket opens a WebSocket to a server listening on a virtual port,
		// RFC 6455 over a vnet conn, and returns an object with the browser
		// WebSocket's interface. A service worker cannot carry WebSockets, so
		// pages served under the vnet scope reach their server through this
		// (vnet-sw.js injects a WebSocket shim that calls it).
		webSocket(port, path, protocols, url, realm) {
			const v = this;
			// Events and message data are made in the caller's realm (its window),
			// so an iframe's `data instanceof ArrayBuffer` holds.
			const R = realm || globalThis;
			const te = new TextEncoder(), td = new TextDecoder();
			const ws = new R.EventTarget();
			const origin = typeof location !== 'undefined' ? location.origin : 'null';
			ws.url = url || ('ws://127.0.0.1:' + port + (path || '/'));
			ws.readyState = 0;
			ws.protocol = '';
			ws.extensions = '';
			ws.bufferedAmount = 0;
			ws.binaryType = 'blob';
			ws.CONNECTING = 0; ws.OPEN = 1; ws.CLOSING = 2; ws.CLOSED = 3;
			ws.onopen = null; ws.onmessage = null; ws.onerror = null; ws.onclose = null;
			const fire = (type, init) => {
				let ev;
				if (type === 'message') ev = new R.MessageEvent('message', init);
				else if (type === 'close' && typeof R.CloseEvent === 'function') ev = new R.CloseEvent('close', init);
				else if (type === 'close') {
					// No CloseEvent before Node 23: an Event with its fields.
					ev = new R.Event('close');
					for (const k of ['code', 'reason', 'wasClean']) Object.defineProperty(ev, k, { value: init[k] });
				}
				else ev = new R.Event(type);
				const h = ws['on' + type];
				if (typeof h === 'function') { try { h.call(ws, ev); } catch (e) { setTimeout(() => { throw e; }); } }
				ws.dispatchEvent(ev);
			};
			const id = v.dial(port);
			let closedSent = false, finished = false, closeTimer = null;
			const finish = (code, reason, clean) => {
				if (finished) return;
				finished = true;
				if (closeTimer) clearTimeout(closeTimer);
				ws.readyState = 3;
				v.close(id, 'a');
				fire('close', { code: code, reason: reason || '', wasClean: !!clean });
			};
			if (id < 0) {
				queueMicrotask(() => { fire('error'); finish(1006, '', false); });
				return ws;
			}
			// Client frames are masked (RFC 6455 §5.3).
			const frame = (op, payload) => {
				const p = payload || new Uint8Array(0);
				const n = p.length;
				const head = n < 126 ? 2 : (n < 65536 ? 4 : 10);
				const out = new Uint8Array(head + 4 + n);
				out[0] = 0x80 | op;
				if (n < 126) { out[1] = 0x80 | n; }
				else if (n < 65536) { out[1] = 0x80 | 126; out[2] = n >> 8; out[3] = n & 255; }
				else { out[1] = 0x80 | 127; new DataView(out.buffer).setUint32(6, n); }
				const mask = crypto.getRandomValues(new Uint8Array(4));
				out.set(mask, head);
				for (let i = 0; i < n; i++) out[head + 4 + i] = p[i] ^ mask[i & 3];
				return v.send(id, 'a', out);
			};
			ws.send = (data) => {
				if (ws.readyState === 0) throw new DOMException('WebSocket is still connecting', 'InvalidStateError');
				if (ws.readyState !== 1) return;
				if (typeof data === 'string') { frame(1, te.encode(data)); return; }
				if (data instanceof Blob) { data.arrayBuffer().then((b) => frame(2, new Uint8Array(b))); return; }
				if (ArrayBuffer.isView(data)) { frame(2, new Uint8Array(data.buffer, data.byteOffset, data.byteLength)); return; }
				frame(2, new Uint8Array(data));
			};
			ws.close = (code, reason) => {
				if (ws.readyState >= 2) return;
				ws.readyState = 2;
				const r = te.encode(reason || '');
				const p = new Uint8Array(2 + r.length);
				p[0] = (code || 1000) >> 8; p[1] = (code || 1000) & 255; p.set(r, 2);
				closedSent = true;
				frame(8, p);
				closeTimer = setTimeout(() => finish(code || 1000, reason, true), 3000);
			};
			const key = btoa(String.fromCharCode.apply(null, crypto.getRandomValues(new Uint8Array(16))));
			let req = 'GET ' + (path || '/') + ' HTTP/1.1\r\nHost: 127.0.0.1:' + port +
				'\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ' + key +
				'\r\nSec-WebSocket-Version: 13\r\nOrigin: ' + origin + '\r\n';
			const protos = protocols == null ? [] : (Array.isArray(protocols) ? protocols : [protocols]);
			if (protos.length) req += 'Sec-WebSocket-Protocol: ' + protos.join(', ') + '\r\n';
			v.send(id, 'a', te.encode(req + '\r\n'));
			let buf = new Uint8Array(0), parts = [], partOp = 0;
			const append = (b) => { const n = new Uint8Array(buf.length + b.length); n.set(buf); n.set(b, buf.length); buf = n; };
			const deliver = (op, payload) => {
				if (op === 1) { fire('message', { data: td.decode(payload), origin: origin }); return; }
				const u = new R.Uint8Array(payload.length);
				u.set(payload);
				fire('message', { data: ws.binaryType === 'arraybuffer' ? u.buffer : new R.Blob([u]), origin: origin });
			};
			const drain = () => {
				if (ws.readyState === 0) {
					let sep = -1;
					for (let i = 0; i + 3 < buf.length; i++) {
						if (buf[i] === 13 && buf[i + 1] === 10 && buf[i + 2] === 13 && buf[i + 3] === 10) { sep = i; break; }
					}
					if (sep < 0) return;
					const head = td.decode(buf.subarray(0, sep));
					buf = buf.slice(sep + 4);
					if (!/^HTTP\/1\.[01] 101/.test(head)) { fire('error'); finish(1006, '', false); return; }
					const pm = head.match(/\r\nsec-websocket-protocol:\s*([^\r\n]+)/i);
					ws.protocol = pm ? pm[1].trim() : '';
					ws.readyState = 1;
					fire('open');
				}
				while (buf.length >= 2 && !finished) {
					const fin = (buf[0] & 0x80) !== 0, op = buf[0] & 15;
					let n = buf[1] & 127, off = 2;
					if (n === 126) { if (buf.length < 4) return; n = (buf[2] << 8) | buf[3]; off = 4; }
					else if (n === 127) { if (buf.length < 10) return; n = new DataView(buf.buffer, buf.byteOffset).getUint32(6); off = 10; }
					const masked = (buf[1] & 0x80) !== 0;
					const mk = masked ? buf.slice(off, off + 4) : null;
					if (masked) off += 4;
					if (buf.length < off + n) return;
					const payload = buf.slice(off, off + n);
					if (mk) for (let i = 0; i < n; i++) payload[i] ^= mk[i & 3];
					buf = buf.slice(off + n);
					if (op === 8) {
						const code = n >= 2 ? (payload[0] << 8) | payload[1] : 1005;
						if (!closedSent) { closedSent = true; frame(8, payload.slice(0, 2)); }
						finish(code, td.decode(payload.subarray(2)), true);
						return;
					}
					if (op === 9) { frame(10, payload); continue; }
					if (op === 10) continue;
					if (op === 0) { parts.push(payload); if (!fin) continue; }
					else if (!fin) { partOp = op; parts = [payload]; continue; }
					else { deliver(op, payload); continue; }
					let len = 0; for (const p of parts) len += p.length;
					const all = new Uint8Array(len); let o = 0;
					for (const p of parts) { all.set(p, o); o += p.length; }
					parts = [];
					deliver(partOp, all);
				}
			};
			const pump = () => {
				let b;
				while ((b = v.recv(id, 'a')) !== null) append(b);
				drain();
				if (finished) return;
				if (v.eof(id, 'a')) { finish(1006, '', false); return; }
				v.onReadable(id, 'a', pump);
			};
			v.onReadable(id, 'a', pump);
			return ws;
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
			// hooks.tty: a terminal of its own (cols, rows, onRaw), when the shell
			// runs it on one, so a full-screen program gets keys, raw mode and size.
			tty: hooks.tty || null,
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
			hooks.instance({ interrupt: p.kill, stdin: p.stdin, resize: p.resize });
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
// pkg/wasmhv/browseui/exec-worker.js c3-vis-wasm
// The worker half of "skywire commands run OFF the main thread". Tail of the
// worker bundle (jsfs + the skywire seeding + vnet + proc + skywire-exec are
// concatenated ahead of it by browseui/embed.go, so this thread owns a whole
// bottle of its own), and the far end of exec-remote.js's protocol.
//
// A worker is the right home for a wasm visor and a poor home for a shell, so
// the split is: the page keeps the UI — the desk, the terminals, the nested
// browser, the desk host — and this thread keeps every Go runtime that
// is a skywire COMMAND. The page's own jsfs stays where it is, seeded and
// in-memory; this worker's jsfs is the one the visor writes and the one that
// holds the IndexedDB snapshot, so the identity in /opt/skywire survives a
// reload exactly as before, just from over here.
//
// The filesystems are therefore SEPARATE, and that is a real consequence, not
// an oversight: `cat /opt/skywire/skywire.json` typed at the desk's shell reads
// the page's tree, which the visor no longer writes to. Sharing one jsfs across
// the boundary is what bottle's fsbridge.js does, and it needs SharedArrayBuffer
// (Atomics.wait, forbidden on the main thread and required by a Go runtime's
// synchronous syscalls) — which needs cross-origin isolation, which the desk
// does not have and will not take on for this. Every skywire command runs HERE,
// so the tree the binary reads and the tree it writes are always the same one;
// only the shell's own builtins see a different one.
//
// Protocol (page ⇄ worker), page first:
//   → {t:'init', persistDB, wasmURL, wasmExecURL}   restore the FS, bind the module
//   ← {t:'ready', restored}
//   → {t:'spawn', id, args, env}                    run `skywire <args...>`
//   ← {t:'out'|'err', id, b}                        stdout/stderr chunks
//   ← {t:'exit', id, code} | {t:'fail', id, msg}
//   → {t:'kill', id}                                the instance's own interrupt
//   ← {t:'vlisten'|'vunlisten', port}               this thread's vnet claims
//   → {t:'vopen', cid, port} ⇄ {t:'vdata', cid, b} ⇄ {t:'vclose', cid}
//   ← {t:'log', level, line}                        console output
(function () {
	'use strict';
	// Page-side load of the bundle is a no-op: everything below is worker-only.
	if (typeof importScripts !== 'function' || typeof self === 'undefined' || typeof self.postMessage !== 'function') return;
	if (!globalThis.jsfs || !globalThis.proc || !globalThis.vnet || !globalThis.skywireExec) return;

	var v = globalThis.vnet;

	function post(m, transfer) {
		try { self.postMessage(m, transfer || []); } catch (e) { /* page gone */ }
	}

	// sendable hands a chunk's buffer over instead of copying it — only when
	// the view owns the whole buffer, or its siblings would travel with it.
	function sendable(b) {
		return (b && b.buffer && b.byteOffset === 0 && b.byteLength === b.buffer.byteLength)
			? [b.buffer] : [];
	}

	// ---- console forwarding ------------------------------------------------
	// The visor's JS-side errors (a failed WebSocket dial, a rejected fetch)
	// would otherwise land only in a worker inspector nobody has open. Mirror
	// them to the page so the desk's log window and a CDP probe see them, and
	// still call the real console for worker devtools.
	var real = { log: console.log, warn: console.warn, error: console.error };
	function forward(level, args) {
		try {
			var line = Array.prototype.map.call(args, function (a) {
				if (typeof a === 'string') return a;
				try { return JSON.stringify(a); } catch (e) { return String(a); }
			}).join(' ');
			post({ t: 'log', level: level, line: line });
		} catch (e) { /* never let logging break the program */ }
	}
	console.log = function () { forward('log', arguments); real.log.apply(console, arguments); };
	console.warn = function () { forward('warn', arguments); real.warn.apply(console, arguments); };
	console.error = function () { forward('error', arguments); real.error.apply(console, arguments); };

	// ---- vnet claim advertisement -----------------------------------------
	// A listener bound in this thread is invisible to the page, and the page is
	// where every desk consumer asks (vnet.httpFetch, the vnet service worker,
	// desk-boot's vnet.listening(3435) gate). So every claim made here is
	// announced, and exec-remote.js mirrors it onto the page's port table with
	// a forwarding handler.
	//
	// Wrapping the three mutators is exact rather than approximate: vnet.js
	// deletes from its port map in unlisten() and releaseOwner() and nowhere
	// else, so no poll is needed to notice a claim going away. releaseOwner is
	// the one that matters in practice — a wasm instance that exits cannot
	// unlisten itself, and proc calls it on every reap.
	var advertised = {};
	var rawListen = v.listen.bind(v);
	var rawUnlisten = v.unlisten.bind(v);
	var rawRelease = v.releaseOwner ? v.releaseOwner.bind(v) : null;

	v.listen = function (port, onconn, owner) {
		var ok = rawListen(port, onconn, owner);
		if (ok && !advertised[port]) { advertised[port] = true; post({ t: 'vlisten', port: port }); }
		return ok;
	};
	v.unlisten = function (port) {
		rawUnlisten(port);
		if (advertised[port]) { delete advertised[port]; post({ t: 'vunlisten', port: port }); }
	};
	if (rawRelease) {
		v.releaseOwner = function (owner) {
			var n = rawRelease(owner);
			// vnet has no port enumeration, so reconcile what we announced
			// against what is still bound.
			Object.keys(advertised).forEach(function (p) {
				var port = parseInt(p, 10);
				if (!v.listening(port)) { delete advertised[p]; post({ t: 'vunlisten', port: port }); }
			});
			return n;
		};
	}

	// ---- bridged conns -----------------------------------------------------
	// conns: bridge conn id -> this thread's vnet conn id. The worker is always
	// the DIALER (side 'a'): the page accepts, we dial the local listener.
	var conns = {};

	function pumpWorker(cid) {
		var id = conns[cid];
		if (id === undefined) return;
		for (;;) {
			var b = v.recv(id, 'a');
			if (b) { post({ t: 'vdata', cid: cid, b: b }, sendable(b)); continue; }
			// EOF: the listener closed its end, so close ours as well — vnet
			// drops a conn only when both sides are closed, and a half-closed
			// one is a map entry that never goes away.
			if (v.eof(id, 'a')) {
				post({ t: 'vclose', cid: cid });
				delete conns[cid];
				try { v.close(id, 'a'); } catch (e) { /* already gone */ }
				return;
			}
			v.onReadable(id, 'a', function () { pumpWorker(cid); });
			return;
		}
	}

	function openConn(cid, port) {
		var id = v.dial(port, 'exec-worker-bridge');
		if (id < 0) {
			// Refused: the claim was released between the page's accept and
			// this dial. Closing is the whole answer — the page's peer reads
			// EOF, which is what a connection to a dead listener should look
			// like.
			post({ t: 'vclose', cid: cid });
			return;
		}
		conns[cid] = id;
		pumpWorker(cid);
	}

	// ---- exec --------------------------------------------------------------
	// instances: exec id -> { interrupt }, the handle skywireExec hands over
	// synchronously so a page-side Ctrl+C reaches the running command's own
	// registered interrupt (pkg/cmdutil/signal_js.go) — a foreground visor
	// shuts down exactly as it would on SIGINT.
	var instances = {};
	// pendingKill: a Ctrl+C that beat its command's registration. The page hands
	// the interrupt to its caller SYNCHRONOUSLY (the contract
	// skywirecmd_js.go relies on), so a kill can be posted before the spawn it
	// belongs to has been processed over here — and dropping it would leave a
	// visor running that the operator has already stopped.
	var pendingKill = {};

	function interrupt(id) {
		var inst = instances[id];
		if (!inst || typeof inst.interrupt !== 'function') return false;
		try { inst.interrupt(); } catch (e) { /* already gone */ }
		return true;
	}

	function spawn(m) {
		var id = m.id;
		var hooks = {
			stdout: function (b) { post({ t: 'out', id: id, b: b }, sendable(b)); },
			stderr: function (b) { post({ t: 'err', id: id, b: b }, sendable(b)); },
			instance: function (inst) {
				instances[id] = inst;
				if (pendingKill[id]) { delete pendingKill[id]; interrupt(id); }
			},
		};
		if (m.env) hooks.env = m.env;
		if (m.tty) {
			hooks.stdin = 'pipe';
			hooks.tty = { cols: m.tty.cols, rows: m.tty.rows, onRaw: function (on) { post({ t: 'raw', id: id, on: !!on }); } };
		}
		try {
			globalThis.skywireExec(m.args || [], hooks).then(function (code) {
				delete instances[id]; delete pendingKill[id];
				post({ t: 'exit', id: id, code: code });
			}, function (e) {
				delete instances[id]; delete pendingKill[id];
				post({ t: 'fail', id: id, msg: (e && e.message) || String(e) });
			});
		} catch (e) {
			delete instances[id]; delete pendingKill[id];
			post({ t: 'fail', id: id, msg: (e && e.message) || String(e) });
		}
	}

	// ---- persistence -------------------------------------------------------
	// The same exclusion the desk has always used, moved to the side that now
	// owns the snapshot: identity and config persist, runtime stores do not. A
	// bbolt database snapshotted mid-write restores corrupt and hangs its
	// consumer on the next boot (observed: the hypervisor module stalling on a
	// restored users.db). Caches rebuild; keys don't.
	function excluded(p) {
		return /\.db$/.test(p) || p.indexOf('/opt/skywire/local/') === 0 || p === '/opt/skywire/local';
	}

	function init(m) {
		if (m.wasmURL) globalThis.skywireExec.wasmURL = m.wasmURL;
		if (m.wasmExecURL) globalThis.skywireExec.wasmExecURL = m.wasmExecURL;
		if (!m.persistDB) { post({ t: 'ready', restored: false }); return; }
		globalThis.jsfs.persist.enable(m.persistDB, { exclude: excluded }).then(function (p) {
			post({ t: 'ready', restored: !!(p && p.restored) });
		}, function (e) {
			// A denied or corrupt IndexedDB is not a reason to have no visor:
			// the filesystem stays in memory for this session.
			console.warn('exec-worker: persistence unavailable:', (e && e.message) || e);
			post({ t: 'ready', restored: false });
		});
	}

	// ---- WebRTC through the page -------------------------------------------
	// A Web Worker has no RTCPeerConnection, so the visor here could not make
	// a webrtc transport at all ("unknown network type"). __skywireRTC.newPC
	// returns a proxy peer connection whose methods and events travel to a real
	// one on the page (exec-remote.js); the Go carrier
	// (pkg/transport/network/webrtc_browser.go) uses it as it would the real
	// thing. Installed before any command runs, so the transport layer's
	// capability probe sees it.
	var rtcPcSeq = 1, rtcDcSeq = 1, rtcCallSeq = 1;
	var rtcPCs = {};   // pcId -> { obj, dcs: { dcId -> proxy channel } }
	var rtcCalls = {}; // callId -> { resolve, reject }
	function rtcPcCall(pcId, method, arg) {
		return new Promise(function (resolve, reject) {
			var callId = rtcCallSeq++;
			rtcCalls[callId] = { resolve: resolve, reject: reject };
			post({ t: 'rtc', op: 'pcCall', pcId: pcId, callId: callId, method: method, arg: arg });
		});
	}
	function rtcMakeDC(pcId, dcId) {
		return {
			binaryType: 'arraybuffer', readyState: 'connecting', bufferedHigh: false, onbufferedamountlow: null,
			onopen: null, onmessage: null, onclose: null, onerror: null,
			send: function (data) {
				var buf = data;
				if (ArrayBuffer.isView(data)) { buf = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength); }
				post({ t: 'rtc', op: 'dcSend', pcId: pcId, dcId: dcId, data: buf }, (buf instanceof ArrayBuffer) ? [buf] : []);
			},
			close: function () { post({ t: 'rtc', op: 'dcClose', pcId: pcId, dcId: dcId }); }
		};
	}
	self.__skywireRTC = {
		newPC: function (iceServers) {
			var pcId = rtcPcSeq++;
			var rec = { dcs: {} };
			var pc = {
				onicecandidate: null, ondatachannel: null,
				createDataChannel: function (label, opts) {
					var dcId = rtcDcSeq++;
					var dc = rtcMakeDC(pcId, dcId);
					rec.dcs[dcId] = dc;
					post({ t: 'rtc', op: 'createDC', pcId: pcId, dcId: dcId, label: label, opts: opts });
					return dc;
				},
				createOffer: function () { return rtcPcCall(pcId, 'createOffer', null); },
				createAnswer: function () { return rtcPcCall(pcId, 'createAnswer', null); },
				setLocalDescription: function (d) { return rtcPcCall(pcId, 'setLocalDescription', d); },
				setRemoteDescription: function (d) { return rtcPcCall(pcId, 'setRemoteDescription', d); },
				addIceCandidate: function (c) { return rtcPcCall(pcId, 'addIceCandidate', c); },
				close: function () { post({ t: 'rtc', op: 'pcClose', pcId: pcId }); }
			};
			rec.obj = pc;
			rtcPCs[pcId] = rec;
			var ice = [];
			try { for (var i = 0; iceServers && i < iceServers.length; i++) { ice.push({ urls: iceServers[i].urls }); } } catch (e) { /* none */ }
			post({ t: 'rtc', op: 'newPC', pcId: pcId, iceServers: ice });
			return pc;
		}
	};
	// rtcEvent applies an event from the page's real peer connection to its
	// proxy, calling the handler Go set on it.
	function rtcEvent(m) {
		var rec = rtcPCs[m.pcId], dc;
		switch (m.op) {
		case 'ret': {
			var c = rtcCalls[m.callId];
			if (c) { delete rtcCalls[m.callId]; if (m.ok) { c.resolve(m.val); } else { c.reject(new Error(m.msg || 'rtc failed')); } }
			return;
		}
		case 'icecandidate':
			if (rec && typeof rec.obj.onicecandidate === 'function') { rec.obj.onicecandidate({ candidate: m.candidate }); }
			return;
		case 'datachannel':
			if (rec) {
				dc = rtcMakeDC(m.pcId, m.dcId);
				rec.dcs[m.dcId] = dc;
				if (typeof rec.obj.ondatachannel === 'function') { rec.obj.ondatachannel({ channel: dc }); }
			}
			return;
		case 'dcOpen': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.readyState = 'open'; if (typeof dc.onopen === 'function') { dc.onopen({}); } } return;
		case 'dcMessage': dc = rec && rec.dcs[m.dcId]; if (dc && typeof dc.onmessage === 'function') { dc.onmessage({ data: m.data }); } return;
		case 'dcClose': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.readyState = 'closed'; if (typeof dc.onclose === 'function') { dc.onclose({}); } } return;
		case 'dcError': dc = rec && rec.dcs[m.dcId]; if (dc && typeof dc.onerror === 'function') { dc.onerror({}); } return;
		// The page's channel queue passed its high-water mark, or drained below the
		// low one; Write waits in between (webrtc_browser.go).
		case 'dcHigh': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.bufferedHigh = true; } return;
		case 'dcLow': dc = rec && rec.dcs[m.dcId]; if (dc) { dc.bufferedHigh = false; if (typeof dc.onbufferedamountlow === 'function') { dc.onbufferedamountlow({}); } } return;
		case 'pcGone': delete rtcPCs[m.pcId]; return;
		}
	}

	// fsCall runs one call of the page's mount of this thread's tree
	// (exec-remote.js, mountWorkerTree) on this thread's jsfs and answers it.
	// read and write move bytes rather than a caller's buffer, and a stat
	// result loses its is*() methods, which the page's jsfs puts back.
	function fsCall(m) {
		function reply(err, res) {
			if (err) {
				post({ t: 'fsr', id: m.id, err: { code: err.code || 'EIO', message: String(err.message || err) } });
				return;
			}
			if (res instanceof Uint8Array) { post({ t: 'fsr', id: m.id, res: res }, [res.buffer]); return; }
			if (res && typeof res === 'object' && !Array.isArray(res)) {
				var plain = {};
				for (var k in res) if (typeof res[k] !== 'function') plain[k] = res[k];
				res = plain;
			}
			post({ t: 'fsr', id: m.id, res: res });
		}
		var fs = globalThis.fs, a = m.args || [];
		try {
			if (m.op === 'read') {
				var buf = new Uint8Array(a[1]);
				fs.read(a[0], buf, 0, a[1], a[2], function (err, n) { reply(err, err ? null : buf.slice(0, n)); });
				return;
			}
			if (m.op === 'write') {
				fs.write(a[0], a[1], 0, a[1].length, a[2], reply);
				return;
			}
			if (typeof fs[m.op] !== 'function') { reply({ code: 'ENOSYS', message: m.op }); return; }
			fs[m.op].apply(fs, a.concat([reply]));
		} catch (e) { reply(e); }
	}

	self.onmessage = function (ev) {
		var m = ev.data || {};
		switch (m.t) {
		case 'init': init(m); return;
		case 'fs': fsCall(m); return;
		case 'spawn': spawn(m); return;
		case 'stdin': {
			var si = instances[m.id];
			if (si && si.stdin) { try { si.stdin.write(m.b); } catch (e) { /* gone */ } }
			return;
		}
		case 'stdinclose': {
			var sc = instances[m.id];
			if (sc && sc.stdin) { try { sc.stdin.close(); } catch (e) { /* gone */ } }
			return;
		}
		case 'resize': {
			var rz = instances[m.id];
			if (rz && typeof rz.resize === 'function') { try { rz.resize(m.c, m.r); } catch (e) { /* gone */ } }
			return;
		}
		case 'kill':
			if (!interrupt(m.id)) pendingKill[m.id] = true;
			return;
		case 'rtc': rtcEvent(m); return;
		case 'vopen': openConn(m.cid, m.port); return;
		case 'vdata': {
			var idv = conns[m.cid];
			if (idv === undefined) return;
			if (!v.send(idv, 'a', m.b)) { post({ t: 'vclose', cid: m.cid }); delete conns[m.cid]; }
			return;
		}
		case 'vclose': {
			var idc = conns[m.cid];
			if (idc === undefined) return;
			delete conns[m.cid];
			try { v.close(idc, 'a'); } catch (e) { /* already gone */ }
			return;
		}
		default: return;
		}
	};
})();
