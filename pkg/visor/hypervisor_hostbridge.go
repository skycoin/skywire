// Package visor pkg/visor/hypervisor_hostbridge.go c3-vis-wasm
package visor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/skycoin/skywire/pkg/hostbridge/dist"
	"github.com/skycoin/skywire/pkg/httputil"
)

var errNoLocalVisor = errors.New("no local visor")

// hostBridgeReq is the body of the POST /api/hostbridge/{action} calls.
type hostBridgeReq struct {
	URL   string `json:"url"`
	Host  string `json:"host"`
	Local string `json:"local"`
}

// hostBridgePage serves the page that hands out the bridge builds and
// connects the visor to a bridge.
func (hv *Hypervisor) hostBridgePage() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(hostBridgeHTML)) //nolint:errcheck
	}
}

// hostBridgeDownload serves one embedded bridge build as a file to save.
func (hv *Hypervisor) hostBridgeDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		b, err := dist.Open(file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file))
		_, _ = w.Write(b) //nolint:errcheck
	}
}

// hostBridgeBuilds lists the embedded bridge builds.
func (hv *Hypervisor) hostBridgeBuilds() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := dist.Platforms()
		if p == nil {
			p = []dist.Platform{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, p)
	}
}

// hostBridgeStatusHandler reports the visor's session to a bridge.
func (hv *Hypervisor) hostBridgeStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, errNoLocalVisor)
			return
		}
		st, err := hv.visor.HostBridgeStatus()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, st)
	}
}

// hostBridgeAction runs connect, disconnect, forward or unforward.
func (hv *Hypervisor) hostBridgeAction() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, errNoLocalVisor)
			return
		}
		if hv.useCsrf() {
			if err := verifyCSRFToken(r.Header.Get(CSRFHeaderName)); err != nil {
				httputil.WriteJSON(w, r, http.StatusForbidden, err)
				return
			}
		}
		var req hostBridgeReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		var err error
		switch r.PathValue("action") {
		case "connect":
			err = hv.visor.HostBridgeConnect(req.URL)
		case "disconnect":
			err = hv.visor.HostBridgeDisconnect()
		case "forward":
			_, err = hv.visor.HostBridgeForward(req.Host, req.Local)
		case "unforward":
			err = hv.visor.HostBridgeUnforward(req.Host)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		st, _ := hv.visor.HostBridgeStatus() //nolint:errcheck
		httputil.WriteJSON(w, r, http.StatusOK, st)
	}
}

// hostBridgeHTML is the host bridge page. It uses paths relative to itself,
// so it works wherever the dashboard is mounted.
const hostBridgeHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Host bridge</title>
<style>
:root{--bg:#fff;--fg:#1d2330;--muted:#5b6475;--line:#d9dee7;--accent:#0b6bcb;--ok:#18794e;--bad:#b42318;--code:#f3f5f8}
@media (prefers-color-scheme:dark){:root{--bg:#11151c;--fg:#e6e9ef;--muted:#9aa3b2;--line:#2a313d;--accent:#5aa9ff;--ok:#4cc38a;--bad:#ff6b5e;--code:#1a202a}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,sans-serif}
main{max-width:760px;margin:0 auto;padding:24px 16px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 8px}
p{margin:6px 0;color:var(--muted)}
code,pre{background:var(--code);border-radius:6px;font:13px ui-monospace,monospace}
pre{padding:10px 12px;overflow-x:auto;white-space:pre-wrap;word-break:break-all}
table{border-collapse:collapse;width:100%}td{padding:6px 4px;border-bottom:1px solid var(--line)}
a{color:var(--accent)}.rec{font-weight:600}
input{width:100%;box-sizing:border-box;padding:8px;border:1px solid var(--line);border-radius:6px;background:var(--bg);color:var(--fg);font:inherit}
button{padding:8px 14px;border:0;border-radius:6px;background:var(--accent);color:#fff;font:inherit;cursor:pointer}
button.secondary{background:transparent;color:var(--accent);border:1px solid var(--accent)}
.row{display:flex;gap:8px;margin:8px 0;flex-wrap:wrap}.row>input{flex:1;min-width:160px}
#status{padding:10px 12px;border:1px solid var(--line);border-radius:6px}
.ok{color:var(--ok)}.bad{color:var(--bad)}
</style></head><body><main>
<h1>Host bridge</h1>
<p>This visor runs in a browser tab, which cannot listen on a port or open a raw socket. The host bridge is a small program on this computer that does that for it, so the visor can take transports and serve ports of this computer, such as a SOCKS proxy.</p>

<h2>1. Save the bridge for this computer</h2>
<table id="builds"><tr><td>Loading…</td></tr></table>

<h2>2. Run it</h2>
<pre id="cmd"></pre>
<p>It prints a URL with a secret token. The token is kept in your user config directory, so the URL stays the same when the bridge restarts. Only this page's origin may use it, and the first visor to connect pairs with it: after that the bridge serves only that visor, until it is run with <code>-unpair</code>.</p>

<h2>3. Connect this visor to it</h2>
<div class="row"><input id="url" placeholder="ws://127.0.0.1:3438/..." autocomplete="off" spellcheck="false"><button id="connect">Connect</button></div>
<p>The browser may ask to allow access to devices on your local network. Allow it, or the visor cannot reach the bridge.</p>
<div id="status">Loading…</div>

<h2>Forward a port of this computer</h2>
<p>Connections to the host address go to the local address in the visor. For example 1080 to 1080 lets programs on this computer use the visor's SOCKS proxy. An address without a host is on 127.0.0.1; give 0.0.0.0:1080 to reach it from other computers.</p>
<div class="row"><input id="fhost" placeholder="host 127.0.0.1:1080"><input id="flocal" placeholder="local 127.0.0.1:1080"><button id="forward">Forward</button></div>
</main>
<script>
"use strict";
const $ = id => document.getElementById(id);
const api = p => "../api/hostbridge/" + p;

async function csrf() {
  try { const r = await fetch("../api/csrf", {credentials: "same-origin"}); if (r.ok) return (await r.json()).csrf_token || ""; } catch (e) {}
  return "";
}
async function post(action, body) {
  const headers = {"Content-Type": "application/json"};
  const t = await csrf(); if (t) headers["X-CSRF-Token"] = t;
  const r = await fetch(api(action), {method: "POST", credentials: "same-origin", headers, body: JSON.stringify(body || {})});
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j;
}
function text(el, s, cls) { el.textContent = s; el.className = cls || ""; }

function render(st) {
  const box = $("status"); box.textContent = "";
  const head = document.createElement("div");
  if (!st.url) text(head, "Not connected to a bridge.");
  else if (st.connected) text(head, "Connected to " + st.url.replace(/\/[0-9a-f]{32}$/, "/…"), "ok");
  else text(head, "Reconnecting to " + st.url.replace(/\/[0-9a-f]{32}$/, "/…") + (st.error ? ": " + st.error : ""), "bad");
  box.appendChild(head);
  for (const f of st.forwards || []) {
    const row = document.createElement("div"); row.className = "row";
    const s = document.createElement("span"); s.style.flex = "1";
    s.textContent = (f.bound || f.host) + " → " + f.local + (f.error ? "  (" + f.error + ")" : "");
    const b = document.createElement("button"); b.className = "secondary"; b.textContent = "Stop";
    b.onclick = () => post("unforward", {host: f.host}).then(render).catch(e => alert(e.message));
    row.append(s, b); box.appendChild(row);
  }
  if (st.url) {
    const b = document.createElement("button"); b.className = "secondary"; b.textContent = "Disconnect";
    b.onclick = () => post("disconnect").then(render).catch(e => alert(e.message));
    box.appendChild(b);
  }
}
async function refresh() {
  try { const r = await fetch(api("status"), {credentials: "same-origin"}); render(await r.json()); }
  catch (e) { text($("status"), "Could not read the visor: " + e.message, "bad"); }
}

async function hostPlatform() {
  let os = "", arch = "";
  const ua = navigator.userAgent;
  if (/Windows/.test(ua)) os = "windows"; else if (/Mac OS X|Macintosh/.test(ua)) os = "darwin"; else if (/Linux|X11/.test(ua)) os = "linux";
  try {
    const d = navigator.userAgentData && await navigator.userAgentData.getHighEntropyValues(["architecture", "bitness"]);
    if (d && d.architecture) arch = d.architecture === "arm" ? (d.bitness === "64" ? "arm64" : "arm") : (d.bitness === "64" ? "amd64" : "386");
  } catch (e) {}
  if (!arch) arch = /aarch64|arm64/i.test(ua) ? "arm64" : /armv7|armv6/i.test(ua) ? "arm" : "amd64";
  return {os, arch};
}

async function builds() {
  const tbl = $("builds");
  let list = [];
  try { list = await (await fetch(api("builds"), {credentials: "same-origin"})).json(); } catch (e) {}
  tbl.textContent = "";
  if (!list.length) { tbl.innerHTML = "<tr><td>This visor has no bridge builds. Install skywire and run <code>skywire hostbridge</code>.</td></tr>"; return; }
  const me = await hostPlatform();
  let rec = list.find(p => p.os === me.os && p.arch === me.arch);
  for (const p of list) {
    const tr = document.createElement("tr");
    const a = document.createElement("a"); a.href = "../hostbridge/download/" + encodeURIComponent(p.file); a.download = p.file; a.textContent = p.file;
    const td1 = document.createElement("td"); td1.appendChild(a);
    const td2 = document.createElement("td"); td2.textContent = (p.size / 1024).toFixed(0) + " KiB" + (p === rec ? "  (this computer)" : "");
    if (p === rec) tr.className = "rec";
    tr.append(td1, td2); tbl.appendChild(tr);
  }
  const file = rec ? rec.file : list[0].file;
  const origin = location.origin;
  $("cmd").textContent = me.os === "windows"
    ? ".\\" + file + " -origin " + origin
    : "chmod +x " + file + " && ./" + file + " -origin " + origin;
}

$("connect").onclick = async () => {
  const url = $("url").value.trim(); if (!url) return;
  text($("status"), "Asking the browser for access to the bridge…");
  // Opening the WebSocket from the page lets the browser ask about local
  // network access here, where a prompt can show, before the visor dials it.
  await new Promise(res => { let ws; try { ws = new WebSocket(url); } catch (e) { return res(); }
    const t = setTimeout(() => { try { ws.close(); } catch (e) {} res(); }, 60000);
    ws.onopen = ws.onerror = () => { clearTimeout(t); try { ws.close(); } catch (e) {} res(); }; });
  try { render(await post("connect", {url})); } catch (e) { text($("status"), "Could not connect: " + e.message, "bad"); }
};
$("forward").onclick = () => {
  const host = $("fhost").value.trim(), local = $("flocal").value.trim(); if (!host || !local) return;
  post("forward", {host, local}).then(render).catch(e => alert(e.message));
};
builds(); refresh(); setInterval(refresh, 5000);
</script></body></html>`
