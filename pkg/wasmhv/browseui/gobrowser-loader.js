// gobrowser-loader.js — opens the netscrape Go/wasm browser
// (github.com/0magnet/netscrape) in a winbox window, wired to the visor's
// transports.
//
// The browser is NO LONGER a separate wasm module. It is compiled into the
// wasm-visor binary and exposed as globalThis.skywireBrowser.open(el) by that
// binary's DOM-side instance (the same one that carries the terminal —
// cmd/wasm-visor/browser_js.go). So this launcher does not fetch or instantiate
// anything: it opens a window and calls skywireBrowser.open, and the browser
// runs in the already-loaded instance's Go runtime. The browser's chrome, page
// transcoding and navigation are Go/syscall/js; only the network is delegated
// here, through the same skywireVisor.fetchDmsg / fetchClearnet the rest of the
// UI uses.
//
// Usage from the visor page (or its console): SkywireGoBrowser.open().
(function () {
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

	// The transport the Go browser calls (globalThis.__netscrapeFetch): mesh
	// hosts go over dmsg, everything else over clearnet, with a plain same-origin
	// fetch as the last resort.
	function transport(url) {
		var sv = globalThis.skywireVisor || {};
		var u;
		try { u = new URL(url); } catch (e) { return fetch(url); }
		var path = (u.pathname || "/") + (u.search || "");
		if (sv.fetchDmsg && meshHost(u.hostname)) {
			return Promise.resolve(sv.fetchDmsg(u.hostname, "GET", path, null)).then(respond);
		}
		if (sv.fetchClearnet) {
			// The visor API is fetchClearnet(exitPK, method, url[, body]) — empty
			// exitPK lets the visor pick an exit. (url must not be passed first.)
			return Promise.resolve(sv.fetchClearnet("", "GET", url, null)).then(respond);
		}
		return fetch(url);
	}

	// ensureBrowser resolves once globalThis.skywireBrowser.open exists. The desk
	// loads the wasm-visor binary's DOM-side instance (which installs it); on a
	// page where that instance has not booted yet, wait briefly for it rather
	// than failing the click.
	function ensureBrowser() {
		return new Promise(function (res, rej) {
			var tries = 0;
			(function poll() {
				var b = globalThis.skywireBrowser;
				if (b && typeof b.open === "function") { res(b); return; }
				if (++tries > 100) { rej(new Error("skywireBrowser not available — the desk wasm-visor instance is not loaded")); return; }
				setTimeout(poll, 100);
			})();
		});
	}

	globalThis.SkywireGoBrowser = {
		// open mounts the Go browser in a new winbox window and returns it.
		open: function (title) {
			if (typeof globalThis.WinBox !== "function") {
				console.error("SkywireGoBrowser: window manager not ready");
				return null;
			}
			var wb = new globalThis.WinBox({ title: title || "Browser (beta)", width: "72%", height: "72%" });
			var mount = document.createElement("div");
			mount.style.cssText = "position:absolute;inset:0";
			wb.body.appendChild(mount);
			globalThis.__netscrapeFetch = transport;
			ensureBrowser()
				.then(function (b) { b.open(mount); })
				.catch(function (e) { mount.textContent = "Browser failed to open: " + e; });
			return wb;
		},
	};
})();
