package browseui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0magnet/bottle"
)

// fsTreeScript runs jsfs twice in separate realms, as the page and the exec
// worker do, and joins them with fs-tree.js over an asynchronous channel.
const fsTreeScript = `
const vm = require('vm');
const src = require('fs').readFileSync(process.argv[2], 'utf8');
function realm() {
	const c = vm.createContext({ console, queueMicrotask, setTimeout, clearTimeout, TextEncoder, TextDecoder, Uint8Array, Error });
	c.globalThis = c;
	vm.runInContext(src, c);
	return c;
}
const page = realm(), worker = realm();
const send = (to) => (m) => setTimeout(() => (m.t === 'fs' ? to.tree.call(m) : to.tree.answer(m)), 1);
page.tree = page.SkywireFSTree(send(worker));
worker.tree = worker.SkywireFSTree(send(page));
page.tree.mount('/opt/skywire');
worker.tree.mount('/home');
const P = (r, f, ...a) => new Promise((res, rej) => r.fs[f](...a, (err, v) => err ? rej(err) : res(v)));
const enc = (s) => new TextEncoder().encode(s), dec = (b) => new TextDecoder().decode(b);
async function read(r, path) {
	const fd = await P(r, 'open', path, 0, 0);
	const buf = new Uint8Array(64);
	const n = await P(r, 'read', fd, buf, 0, 64, null);
	await P(r, 'close', fd);
	return dec(buf.subarray(0, n));
}
async function write(r, path, s) {
	const fd = await P(r, 'open', path, 0o1101, 0o640);
	const b = enc(s);
	await P(r, 'write', fd, b, 0, b.length, null);
	await P(r, 'close', fd);
}
(async () => {
	const out = [];
	await P(page, 'mkdir', '/home/user/proj', 0o755);
	await write(page, '/home/user/proj/note.txt', 'from shell');
	worker.process.chdir('/home/user/proj');
	out.push('cwd=' + worker.process.cwd());
	out.push('worker=' + await read(worker, '/home/user/proj/note.txt'));
	await write(worker, '/home/user/proj/note.txt', 'saved');
	out.push('page=' + await read(page, '/home/user/proj/note.txt'));
	out.push('list=' + (await P(worker, 'readdir', '/home/user/proj')).join(','));
	await P(worker, 'mkdir', '/opt', 0o755).catch(() => {});
	await P(worker, 'mkdir', '/opt/skywire', 0o755).catch(() => {});
	await write(worker, '/opt/skywire/k', 'visor');
	out.push('opt=' + await read(page, '/opt/skywire/k'));
	try { await P(worker, 'stat', '/home/user/none'); } catch (e) { out.push('missing=' + e.code); }
	console.log(out.join(' '));
})().catch((e) => console.log('FAIL ' + e.code + ' ' + e.message));
`

func TestFSTreeBothWays(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.js")
	if err := os.WriteFile(lib, concat([][]byte{bottle.JSFS(), fsTreeJS}), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fstree.js")
	if err := os.WriteFile(script, []byte(fsTreeScript), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script, lib).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "cwd=/home/user/proj worker=from shell page=saved list=note.txt opt=visor missing=ENOENT"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
