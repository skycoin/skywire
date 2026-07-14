// wallet-fs-shim.js — a minimal virtual filesystem implementing the Go/wasm
// `globalThis.fs` surface (syscall/fs_js.go), backed by a key-value store. In
// node the store is an in-memory Map (proves the shim + Go `os` path); in the
// browser the same shim backs onto localStorage/OPFS → persistent wallet "dir".
// This is Gap A of the skycoin-web multicoin RFC: bridge "there are no dirs in
// the browser" so skycoin's wallet.Service persists .wlt files unchanged.
(function () {
  const S_IFDIR = 0o040000, S_IFREG = 0o100000;
  // backing store: path -> { dir:bool, data:Uint8Array, mode:int }
  const store = new Map();
  store.set('/', { dir: true, data: null, mode: S_IFDIR | 0o755 });
  const now = 1000000; // fixed (Date.now() is unavailable to wasm anyway)

  const enc = (globalThis.__wfsPersist && globalThis.__wfsPersist.load) || (() => {});
  enc(store); // browser backing may prime the store here

  let nextFd = 3;
  const fds = new Map(); // fd -> { path, pos }

  const err = (code) => { const e = new Error(code); e.code = code; return e; };
  const norm = (p) => p.replace(/\/+/g, '/').replace(/\/$/, '') || '/';
  const dirname = (p) => { p = norm(p); const i = p.lastIndexOf('/'); return i <= 0 ? '/' : p.slice(0, i); };

  function statsFor(node) {
    return {
      dev: 0, ino: 0, mode: node.mode, nlink: 1, uid: 0, gid: 0, rdev: 0,
      size: node.dir ? 0 : (node.data ? node.data.length : 0),
      blksize: 4096, blocks: 1,
      atimeMs: now, mtimeMs: now, ctimeMs: now,
      isDirectory: () => node.dir, isFile: () => !node.dir,
    };
  }
  const persist = () => { if (globalThis.__wfsPersist && globalThis.__wfsPersist.save) globalThis.__wfsPersist.save(store); };

  const fs = {
    constants: { O_WRONLY: 1, O_RDWR: 2, O_CREAT: 64, O_TRUNC: 512, O_APPEND: 1024, O_EXCL: 128, O_RDONLY: 0, O_DIRECTORY: 65536 },

    open(path, flags, mode, cb) {
      path = norm(path);
      let node = store.get(path);
      if (!node) {
        if (!(flags & this.constants.O_CREAT)) return cb(err('ENOENT'));
        if (!store.has(dirname(path))) return cb(err('ENOENT'));
        node = { dir: false, data: new Uint8Array(0), mode: S_IFREG | (mode & 0o777) };
        store.set(path, node);
      } else if (flags & this.constants.O_TRUNC) {
        node.data = new Uint8Array(0);
      }
      const fd = nextFd++;
      fds.set(fd, { path, pos: (flags & this.constants.O_APPEND) && node.data ? node.data.length : 0 });
      cb(null, fd);
    },
    close(fd, cb) { fds.delete(fd); cb(null); },
    fsync(fd, cb) { persist(); cb(null); },

    write(fd, buffer, offset, length, position, cb) {
      const f = fds.get(fd);
      if (!f) return cb(err('EBADF'));
      const node = store.get(f.path);
      const pos = position === null || position === undefined ? f.pos : position;
      const chunk = buffer.subarray(offset, offset + length);
      const end = pos + chunk.length;
      if (!node.data || end > node.data.length) {
        const grown = new Uint8Array(end);
        if (node.data) grown.set(node.data);
        node.data = grown;
      }
      node.data.set(chunk, pos);
      if (position === null || position === undefined) f.pos = end;
      cb(null, chunk.length, buffer);
    },
    read(fd, buffer, offset, length, position, cb) {
      const f = fds.get(fd);
      if (!f) return cb(err('EBADF'));
      const node = store.get(f.path);
      const data = node.data || new Uint8Array(0);
      const pos = position === null || position === undefined ? f.pos : position;
      const n = Math.max(0, Math.min(length, data.length - pos));
      if (n > 0) buffer.set(data.subarray(pos, pos + n), offset);
      if (position === null || position === undefined) f.pos += n;
      cb(null, n, buffer);
    },

    stat(path, cb) { const n = store.get(norm(path)); n ? cb(null, statsFor(n)) : cb(err('ENOENT')); },
    lstat(path, cb) { this.stat(path, cb); },
    fstat(fd, cb) { const f = fds.get(fd); f ? this.stat(f.path, cb) : cb(err('EBADF')); },

    mkdir(path, perm, cb) {
      path = norm(path);
      if (store.has(path)) return cb(err('EEXIST'));
      if (!store.has(dirname(path))) return cb(err('ENOENT'));
      store.set(path, { dir: true, data: null, mode: S_IFDIR | (perm & 0o777) });
      persist(); cb(null);
    },
    rmdir(path, cb) { store.delete(norm(path)); persist(); cb(null); },
    unlink(path, cb) { store.delete(norm(path)); persist(); cb(null); },
    rename(from, to, cb) {
      from = norm(from); to = norm(to);
      const n = store.get(from);
      if (!n) return cb(err('ENOENT'));
      store.set(to, n); store.delete(from); persist(); cb(null);
    },
    readdir(path, cb) {
      path = norm(path);
      if (!store.has(path)) return cb(err('ENOENT'));
      const pre = path === '/' ? '/' : path + '/';
      const names = [];
      for (const k of store.keys()) {
        if (k === path || !k.startsWith(pre)) continue;
        const rest = k.slice(pre.length);
        if (rest && !rest.includes('/')) names.push(rest);
      }
      cb(null, names);
    },
    ftruncate(fd, length, cb) {
      const f = fds.get(fd); if (!f) return cb(err('EBADF'));
      const node = store.get(f.path);
      const d = new Uint8Array(length);
      if (node.data) d.set(node.data.subarray(0, Math.min(length, node.data.length)));
      node.data = d; cb(null);
    },
    // no-ops that Go may call during SaveBinary / os operations
    chmod(p, m, cb) { cb(null); }, fchmod(fd, m, cb) { cb(null); },
    chown(p, u, g, cb) { cb(null); }, fchown(fd, u, g, cb) { cb(null); }, lchown(p, u, g, cb) { cb(null); },
    utimes(p, a, m, cb) { cb(null); }, truncate(p, l, cb) { cb(null); },
    symlink(t, p, cb) { cb(err('ENOSYS')); }, link(a, b, cb) { cb(err('ENOSYS')); }, readlink(p, cb) { cb(err('EINVAL')); },
  };

  globalThis.fs = fs;
  globalThis.__wfsStore = store; // for inspection
})();
