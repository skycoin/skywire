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
			// A mount answers later, so a directory inside one is taken on trust;
			// the first call made there reports it if it is not one.
			if (mountOf(dir)) { cwd = normalize(dir); return; }
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
