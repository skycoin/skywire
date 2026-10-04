// Package wasmhv pkg/wasmhv/vnet_fallback.go c3-vis-wasm
package wasmhv

import "net/http"

// vnetFallbackHTML answers a /vnet/<port>/ request that reached the server.
// The vnet service worker normally answers those from the desk page, but a
// hard reload bypasses service workers for that one navigation, and so does
// the first visit before the worker controls the page. Rather than a bare
// 404, this reloads once normally, which goes through the worker, and says
// what is missing if there is no worker to go through.
var vnetFallbackHTML = []byte(`<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>skywire</title>
<body style="background:#111;color:#ddd;font:14px system-ui,sans-serif;padding:16px">
<p id="m">Reaching the page that serves this address…</p>
<script>
(async function () {
	var m = document.getElementById('m');
	if (!('serviceWorker' in navigator)) {
		m.textContent = 'This address is served by the desk through a service worker, which this browser does not offer here.';
		return;
	}
	var reg = null;
	try { reg = await navigator.serviceWorker.getRegistration(location.pathname); } catch (e) {}
	if (!reg) {
		m.innerHTML = 'Nothing serves this address yet. Open the <a style="color:#8ab4f8" href="/">desk</a> first; its visor answers these pages.';
		return;
	}
	// One normal reload: it goes through the worker. A second visit here
	// within a few seconds means the worker did not take it, so stop.
	var key = 'vnet-fallback:' + location.href, last = 0;
	try { last = Number(sessionStorage.getItem(key)) || 0; } catch (e) {}
	if (Date.now() - last < 5000) {
		m.textContent = 'The desk did not answer for this address. Is the desk tab open?';
		return;
	}
	try { sessionStorage.setItem(key, String(Date.now())); } catch (e) {}
	location.reload();
})();
</script>
`)

// ServeVNetFallback serves vnetFallbackHTML for a /vnet/ path.
func ServeVNetFallback(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(vnetFallbackHTML) //nolint:errcheck
}
