// gobrowser-loader.js — opens the Go/wasm browser (github.com/0magnet/shipyard
// cmd/browser) in a winbox window, wired to the visor's transports. This is an
// EXPERIMENTAL alternative to netscrape's browse.js: the browser's chrome, page
// transcoding (sandboxed srcdoc, inlined stylesheets/images) and navigation are
// Go/syscall/js; only the network is delegated here, through the same
// skywireVisor.fetchDmsg / fetchClearnet the JS engine uses.
//
// Usage from the visor page (or its console): SkywireGoBrowser.open().
// It reads globalThis.WinBox (window manager), globalThis.Go (the wasm_exec
// runtime) and globalThis.skywireVisor (transports) at call time, so it is safe
// to define at bundle load before those exist.
(function () {
	var WASM_URL = "/gobrowser.wasm";

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

	// The transport the Go browser calls (globalThis.__shipyardBrowserFetch):
	// mesh hosts go over dmsg, everything else over clearnet, with a plain
	// same-origin fetch as the last resort.
	function transport(url) {
		var sv = globalThis.skywireVisor || {};
		var u;
		try { u = new URL(url); } catch (e) { return fetch(url); }
		var path = (u.pathname || "/") + (u.search || "");
		if (sv.fetchDmsg && meshHost(u.hostname)) {
			return Promise.resolve(sv.fetchDmsg(u.hostname, "GET", path, null)).then(respond);
		}
		if (sv.fetchClearnet) {
			return Promise.resolve(sv.fetchClearnet(url, "GET", null)).then(respond);
		}
		return fetch(url);
	}

	globalThis.SkywireGoBrowser = {
		// open mounts the Go browser in a new winbox window and returns it.
		open: function (title) {
			if (typeof globalThis.WinBox !== "function") {
				console.error("SkywireGoBrowser: window manager not ready");
				return null;
			}
			if (typeof globalThis.Go !== "function") {
				console.error("SkywireGoBrowser: Go runtime (wasm_exec) not present");
				return null;
			}
			globalThis.__shipyardBrowserFetch = transport;
			var wb = new globalThis.WinBox({ title: title || "Go Browser (beta)", width: "72%", height: "72%" });
			var mount = document.createElement("div");
			mount.style.cssText = "position:absolute;inset:0";
			wb.body.appendChild(mount);
			globalThis.__shipyardMount = mount;
			fetch(WASM_URL)
				.then(function (r) { return r.arrayBuffer(); })
				.then(function (buf) {
					var go = new globalThis.Go();
					return WebAssembly.instantiate(buf, go.importObject).then(function (res) { go.run(res.instance); });
				})
				.catch(function (e) { mount.textContent = "Go browser failed to load: " + e; });
			return wb;
		},
	};
})();
