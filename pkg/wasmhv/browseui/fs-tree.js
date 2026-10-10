// pkg/wasmhv/browseui/fs-tree.js
// One subtree of a jsfs tree, lent across the page/worker boundary. The page
// and the exec worker each have their own jsfs, and each mounts the subtrees
// the other one owns: the page shows the worker's /opt/skywire and /mnt, and
// the worker shows the page's /home, so a command reads and writes the files
// the desk shell sees. A jsfs mount provider may answer whenever it is ready,
// so no SharedArrayBuffer is needed.
//
//   SkywireFSTree(post) -> { mount(prefix), call(m), answer(m) }
//   post(msg, transfer) sends to the other side; call answers its {t:'fs'}
//   requests on this side's jsfs and answer takes its {t:'fsr'} replies.
(function () {
	'use strict';
	if (globalThis.SkywireFSTree) return;

	// Which arguments are paths inside the mount, per call.
	var PATHS = {
		stat: [0], lstat: [0], readdir: [0], mkdir: [0], rmdir: [0], unlink: [0],
		truncate: [0], chmod: [0], chown: [0], lchown: [0], utimes: [0], readlink: [0],
		open: [0], rename: [0, 1], link: [0, 1], symlink: [1],
		close: [], fstat: [], ftruncate: [], fchmod: [], fchown: [], fsync: [],
	};

	globalThis.SkywireFSTree = function (post) {
		var wait = {};
		var seq = 0;

		function ask(op, args, cb, transfer) {
			var id = ++seq;
			wait[id] = cb;
			post({ t: 'fs', id: id, op: op, args: args }, transfer);
		}

		function provider(prefix) {
			var abs = function (rel) { return rel === '/' ? prefix : prefix + rel; };
			var p = {};
			Object.keys(PATHS).forEach(function (op) {
				p[op] = function () {
					var args = Array.prototype.slice.call(arguments);
					var cb = args.pop();
					PATHS[op].forEach(function (i) { args[i] = abs(args[i]); });
					ask(op, args, cb);
				};
			});
			p.read = function (fd, length, position, cb) { ask('read', [fd, length, position], cb); };
			p.write = function (fd, bytes, position, cb) { ask('write', [fd, bytes, position], cb, [bytes.buffer]); };
			return p;
		}

		// call runs one request on this side's jsfs. read and write move bytes
		// rather than a caller's buffer, and a stat result loses its is*()
		// methods, which the asking side's jsfs puts back.
		function call(m) {
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

		function answer(m) {
			var cb = wait[m.id];
			if (!cb) return;
			delete wait[m.id];
			cb(m.err || null, m.res);
		}

		function mount(prefix) {
			var jsfs = globalThis.jsfs;
			if (!jsfs || typeof jsfs.mount !== 'function') return;
			try { jsfs.mount(prefix, provider(prefix)); } catch (e) {
				console.warn('[fs-tree] could not mount ' + prefix + ':', e && e.message);
			}
		}

		return { mount: mount, call: call, answer: answer };
	};
})();
