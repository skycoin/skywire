package wasmhv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// swHarness runs sw.js's fetch handler with the network down and a cached
// shell, and prints what each request got.
const swHarness = `
const listeners = {};
globalThis.self = { location: { origin: 'https://hv.test' }, addEventListener: (t, f) => { listeners[t] = f; } };
globalThis.fetch = () => Promise.reject(new TypeError('Failed to fetch'));
globalThis.caches = { match: (r) => Promise.resolve(r === './' ? 'SHELL' : undefined), open: () => Promise.resolve({ put: () => Promise.resolve() }) };
` + "%SW%" + `
const cases = [
	['https://hv.test/api/visors', 'cors'],
	['https://hv.test/vnet/8001/api/visors', 'cors'],
	['https://hv.test/#/nodes', 'navigate'],
	['https://hv.test/assets/x.json', 'cors'],
];
(async () => {
	const out = [];
	for (const [url, mode] of cases) {
		let responded = null;
		listeners.fetch({ request: { url, method: 'GET', mode }, respondWith: (p) => { responded = p; }, waitUntil: () => {} });
		if (!responded) { out.push('network'); continue; }
		try { out.push(String(await responded)); } catch (e) { out.push('error'); }
	}
	console.log(out.join(' '));
})();
`

// TestServiceWorkerNeverAnswersDataWithTheShell: with the hypervisor gone, a
// page load may get the cached shell, but an API call must fail, not parse
// the shell's HTML as JSON.
func TestServiceWorkerNeverAnswersDataWithTheShell(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	sw := strings.NewReplacer("__PRECACHE__", "[]", "__BUILD__", "test").Replace(string(ServiceWorkerJS))
	script := filepath.Join(t.TempDir(), "sw.js")
	if err := os.WriteFile(script, []byte(strings.Replace(swHarness, "%SW%", sw, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "network network SHELL error"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
