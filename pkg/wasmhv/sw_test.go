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

// pushHarness fires a push and a click at the worker and prints what it
// showed and opened.
const pushHarness = `
const listeners = {};
const shown = [];
globalThis.self = {
	location: { origin: 'https://hv.test' },
	addEventListener: (t, f) => { listeners[t] = f; },
	registration: { scope: 'https://hv.test/', showNotification: (title, o) => { shown.push(title + '|' + o.body + '|' + o.tag); return Promise.resolve(); } },
	clients: { matchAll: () => Promise.resolve([]), openWindow: (u) => { shown.push('open ' + u); return Promise.resolve(); } },
};
` + "%SW%" + `
(async () => {
	const waits = [];
	const data = { json: () => ({ title: 'visor a is offline', body: 'A visor you watch went offline.', tag: 'skywire-02aa' }) };
	listeners.push({ data, waitUntil: (p) => waits.push(p) });
	listeners.notificationclick({ notification: { close: () => {} }, waitUntil: (p) => waits.push(p) });
	await Promise.all(waits);
	console.log(shown.join('\n'));
})();
`

// TestServiceWorkerShowsPushedAlerts: the root worker carries the push
// handler, so a page under a desk gets alert notifications from it.
func TestServiceWorkerShowsPushedAlerts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := filepath.Join(t.TempDir(), "sw.js")
	sw := string(ServiceWorkerFor("test", nil))
	if err := os.WriteFile(script, []byte(strings.Replace(pushHarness, "%SW%", sw, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := "visor a is offline|A visor you watch went offline.|skywire-02aa\nopen https://hv.test/"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
