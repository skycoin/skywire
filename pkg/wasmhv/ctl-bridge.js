// ctl-bridge.js — connects this serverless-HV tab to the ctlbridge control
// surface (SSE /ctl/events) so a shell can inspect/drive the in-tab visor
// (status, checkRegistered, hvApi, …) and see boot progress in /ctl/log. Served
// by the cmd/dmsg-wasm harness and by `cli hv serve --harness`. Debug aid for
// the serverless-UI harness; NOT part of the shipped standalone.
(function () {
  // This file is served ONLY when the control surface is mounted, so its
  // presence IS the harness. Say so explicitly rather than leaving the UI to
  // infer it from a side effect like __skylog, which the visor also probes for.
  window.__SKYWIRE_HARNESS__ = true;

  var tabId = sessionStorage.getItem('ctlTabId');
  if (!tabId) { tabId = 'hv-' + Math.random().toString(36).slice(2, 8); sessionStorage.setItem('ctlTabId', tabId); }
  var post = function (path, body) {
    return fetch(path, { method: 'POST', body: typeof body === 'string' ? body : JSON.stringify(body) }).catch(function () {});
  };
  // wasm-visor vlog() forwards boot progress here → /ctl/log.
  window.__skylog = function (m) { post('/ctl/log?tab=' + tabId, '[hv] ' + m); };

  async function exec(c) {
    var a = c.args || [];
    var v = window.skywireVisor || {};
    switch (c.action) {
      case 'status': return v.status ? v.status() : { error: 'no skywireVisor yet' };
      case 'checkRegistered': return v.checkRegistered ? await v.checkRegistered() : { error: 'not ready' };
      case 'hvApi': {
        var r = await v.hvApi(a[0] || 'GET', a[1] || '/api/visors-summary', a[2] || null);
        return { status: r.status, body: new TextDecoder().decode(r.body).slice(0, 1200) };
      }
      case 'bootResult': return window.__SKYWIRE_HV__ && window.__SKYWIRE_HV__.ready
        ? await window.__SKYWIRE_HV__.ready.then(function (pk) { return { booted: pk }; }).catch(function (e) { return { bootError: String(e && e.message || e) }; })
        : { error: 'no ready promise' };
      // reload the page (re-fetches freshly served assets). The tab keeps its
      // ctlTabId (sessionStorage), so the bridge reconnects under the same id.
      case 'reload': setTimeout(function () { location.reload(); }, 50); return { reloading: true };
      // generic driver: eval a[0] in the page and return the (awaited) result —
      // lets the shell click the browse/host panel, fill inputs, query the DOM,
      // etc. Harness-only debug aid; NEVER shipped in the standalone.
      case 'js': { var out = await (0, eval)(a[0]); return { value: out === undefined ? null : out }; }
      default: throw new Error('unknown action: ' + c.action);
    }
  }

  var es = new EventSource('/ctl/events?tab=' + tabId);
  es.onmessage = async function (ev) {
    var c = JSON.parse(ev.data);
    try { var v = await exec(c); post('/ctl/result', { id: c.id, ok: true, value: v }); }
    catch (e) { post('/ctl/result', { id: c.id, ok: false, error: e.message || String(e) }); }
  };
  es.onerror = function () {};

  // RPC bridge: dial /ctl/rpc so a shell can drive THIS tab's visor with the
  // ordinary CLI (skywire cli --rpc <addr> visor info). skywireVisor.serveRPC
  // opens the WebSocket, wraps it as a yamux session and serves the tab's
  // net/rpc surface over it; the host fronts it with a local 127.0.0.1:<port>
  // that shows up as the tab's "rpc" field in /ctl/tabs. Retry until the visor
  // exposes serveRPC (worker proxy installs it on boot).
  var rpcBridged = false;
  function dialRPCBridge() {
    if (rpcBridged) { return; }
    var v = window.skywireVisor;
    if (!v || typeof v.serveRPC !== 'function') { setTimeout(dialRPCBridge, 300); return; }
    rpcBridged = true;
    var url = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ctl/rpc?tab=' + tabId;
    Promise.resolve(v.serveRPC(url))
      .then(function () { post('/ctl/log?tab=' + tabId, 'rpc bridge up: ' + url); })
      .catch(function (e) { rpcBridged = false; post('/ctl/log?tab=' + tabId, 'rpc bridge error: ' + (e && e.message || e)); setTimeout(dialRPCBridge, 2000); });
  }
  dialRPCBridge();

  post('/ctl/log?tab=' + tabId, 'ctl-bridge ready ' + tabId);
})();
