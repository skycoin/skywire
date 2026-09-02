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
		const size = isFile ? node.data.length : (node.target !== null ? node.target.length : 64);
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

	// ---- constants (node names; values mirror Linux) -----------------------
	const constants = {
		O_RDONLY: 0, O_WRONLY: 1, O_RDWR: 2,
		O_CREAT: 0o100, O_EXCL: 0o200, O_TRUNC: 0o1000,
		O_APPEND: 0o2000, O_DIRECTORY: 0o200000, O_NONBLOCK: 0o4000, O_SYNC: 0o4010000,
	};

	// ---- the fs object -----------------------------------------------------
	function wrap(fn) {
		// turn a sync impl into the node callback convention with proper
		// asynchrony (Go's fsCall await expects the callback, and re-entrant
		// synchronous callbacks into wasm are not allowed mid-syscall).
		return function (...args) {
			const cb = args.pop();
			let res;
			try { res = fn(...args); } catch (e) { queueMicrotask(() => cb(e)); return; }
			queueMicrotask(() => cb(null, res));
		};
	}

	const fsImpl = {
		constants,

		writeSync(fd, buf) {
			if (fd === 1) { stdio.stdout(buf); return buf.length; }
			if (fd === 2) { stdio.stderr(buf); return buf.length; }
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
				const e = fdEntry(fd);
				if (e.node.dev) { queueMicrotask(() => cb(null, 0)); return; } // /dev/null
				if (e.node.data === null) throw mkerr('EISDIR', 'read dir');
				const pos = (position === null || position === undefined) ? e.pos : position;
				const avail = e.node.data.length - pos;
				const n = Math.max(0, Math.min(length, avail));
				if (n > 0) buf.set(e.node.data.subarray(pos, pos + n), offset);
				if (position === null || position === undefined) e.pos += n;
				queueMicrotask(() => cb(null, n));
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
			fds.set(fd, { node, pos: (flags & constants.O_APPEND) && node.data ? node.data.length : 0, flags });
			return fd;
		}),

		close: wrap((fd) => { fdEntry(fd); fds.delete(fd); return undefined; }),

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

	globalThis.fs = fsImpl;
	globalThis.process = processImpl;
	globalThis.jsfs = {
		installed: true,
		stdio,           // swap .stdout/.stderr/.stdin to capture a command
		mkdirp,          // host-side seeding helpers
		writeFile: writeFileSeed,
		readFile(path) { const r = resolve(path, true); if (!r.node || r.node.data === null) return null; return r.node.data; },
		setCwd(d) { processImpl.chdir(d); },
		getCwd() { return cwd; },
	};
})();
