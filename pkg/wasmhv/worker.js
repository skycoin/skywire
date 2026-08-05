// worker.js — a SharedWorker that hosts ONE Go/wasm skywire-visor per origin,
// shared by every same-origin tab, OFF the browser's main thread.
//
// WHY SHARED (not one-per-tab): the visor is a stateful network node — a dmsg
// client (newest-session-wins), a router, autoconnect, an identity registered in
// discovery. Running one per tab means N clients of the SAME key fighting over
// dmsg sessions and colliding on AR/transport/route state; switching or closing a
// tab used to re-boot the whole visor (~10-20s). A SharedWorker is exactly the
// right lifecycle primitive: ONE instance, booted once on the first connect, alive
// while ANY tab is connected, terminated by the browser when the last tab closes.
// Tabs attach as thin views over MessagePorts — no second boot, no re-register, no
// "already running in another tab" notice.
//
// WHY A WORKER AT ALL: the Go/wasm runtime's occasionally-blocking work (dmsg/WS/WT
// dials, route setup, SOCKS handshakes) runs on whatever thread hosts the runtime.
// On a page's MAIN thread that starves the UI event loop (a slow skysocks route
// setup once froze the whole HV UI). A worker gives the runtime its own thread, so
// every tab's UI stays responsive no matter what the visor is doing.
//
// hv-boot.js (in each tab) connects with `new SharedWorker('worker.js')`, sends a
// one-time {t:'init'} carrying the key + seed config, and installs a main-thread
// `globalThis.skywireVisor` PROXY whose every method is a postMessage round-trip to
// here. The protocol (tab port ⇄ worker):
//   tab → worker:  {t:'init', sk, seedpk, seedws, disc}  first tab boots the visor
//                  {t:'call', id, fn, args}              invoke skywireVisor[fn](...)
//                  {t:'vis', state}                      tab visibility (agent election)
//                  {t:'bye'}                             tab is unloading; drop its port
//                  {t:'stun-ip-result', id, ip}         reply from the agent tab's STUN
//                  {t:'rtc', ...}                        reply/event from the agent tab
//   worker → tab:  {t:'up', methods, pk}                runtime up + booted; API names
//                  {t:'ret', id, val}                   call id resolved
//                  {t:'err', id, msg}                   call id rejected
//                  {t:'log', level, line}               console output (broadcast)
//                  {t:'fatal', msg}                     runtime/boot failed
//                  {t:'stun-ip', id, ice}               → AGENT tab: run STUN
//                  {t:'rtc', ...}                        → AGENT tab: run RTC op
//
// The Go side needs NO changes: syscall/js, fetch, WebSocket and WebTransport all
// work in a worker, and self.location is same-origin as the page (so the HTTPS
// mixed-content gating in bootEdge stays correct). The one browser API NOT exposed
// to any worker is RTCPeerConnection — so STUN self-IP probing and the WebRTC
// carrier are delegated to exactly ONE connected tab (the "agent"): the worker
// orchestrates (it holds the dmsg signaling + transport logic), a tab executes the
// PeerConnection. Only the agent port receives {t:'stun-ip'}/{t:'rtc'} messages, so
// there is never more than one RTCPeerConnection host regardless of tab count. If
// the agent tab closes, another is elected and the WebRTC transports re-dial; the
// dmsg session, routes and the rest of the visor state live in the worker and are
// untouched.
(function () {
  // ----- connected tabs -----
  var ports = [];        // MessagePorts of connected tabs
  var agentPort = null;  // the ONE tab elected to run STUN + WebRTC (main-thread APIs)

  function broadcast(msg) {
    for (var i = 0; i < ports.length; i++) {
      try { ports[i].postMessage(msg); } catch (e) { /* dead port — pruned on bye */ }
    }
  }
  // Post to the agent tab. Returns false if there is no agent (caller degrades).
  function agentPost(msg, transfer) {
    if (!agentPort) return false;
    try { agentPort.postMessage(msg, transfer || []); return true; }
    catch (e) { return false; }
  }

  // Forward everything the Go runtime writes to the console to EVERY tab, so each
  // HV-UI log window (which captures the PAGE console) shows visor logs even though
  // the runtime runs off-thread and shared. Override BEFORE loading wasm_exec.js so
  // its stdout wiring is captured too; each override still calls the real console fn
  // (worker devtools).
  var real = { log: console.log.bind(console), error: console.error.bind(console), warn: console.warn.bind(console) };
  function forward(level, args) {
    try {
      var line = Array.prototype.map.call(args, function (a) {
        if (typeof a === 'string') { return a; }
        try { return JSON.stringify(a); } catch (e) { return String(a); }
      }).join(' ');
      broadcast({ t: 'log', level: level, line: line });
    } catch (e) { /* ignore */ }
  }
  console.log = function () { forward('log', arguments); real.log.apply(null, arguments); };
  console.error = function () { forward('error', arguments); real.error.apply(null, arguments); };
  console.warn = function () { forward('warn', arguments); real.warn.apply(null, arguments); };

  // Variant (go|tinygo) arrives on this worker's URL (hv-boot.js appends it —
  // workers can't read localStorage). It selects BOTH the loader and the blob,
  // which must match toolchains. Empty = the server default.
  var __variant = '';
  try { __variant = (new URLSearchParams(self.location.search)).get('variant') || ''; } catch (e) {}
  var __vqs = __variant ? ('?variant=' + encodeURIComponent(__variant)) : '';
  importScripts('wasm_exec.js' + __vqs);
  var go = new Go();
  // TinyGo's wasm_exec.js omits the gojs getRandomData import the crypto-using Go
  // runtime needs to seed itself; inject it only when absent (mirrors hv-boot.js).
  if (go.importObject.gojs && !go.importObject.gojs['runtime.getRandomData']) {
    go.importObject.gojs['runtime.getRandomData'] = function (ptr, len) {
      crypto.getRandomValues(new Uint8Array(go._inst.exports.memory.buffer, ptr >>> 0, len >>> 0));
    };
  }

  var api = null;        // globalThis.skywireVisor once the runtime installs it
  var bootParams = null; // {sk, seedpk, seedws, disc} from the FIRST tab's init
  var bootPromise = null;// the single boot() call
  var bootedPK = null;   // resolved visor PK (null until booted)
  var queued = [];       // {port, m} calls that arrived before the runtime booted

  function dispatch(port, id, fn, args) {
    var f = api && api[fn];
    if (typeof f !== 'function') {
      try { port.postMessage({ t: 'err', id: id, msg: 'skywireVisor.' + fn + ' is not a function' }); } catch (e) {}
      return;
    }
    var out;
    try { out = f.apply(api, args || []); }
    catch (e) { try { port.postMessage({ t: 'err', id: id, msg: String((e && e.message) || e) }); } catch (e2) {} return; }
    // Every visor method is either sync (value/undefined) or returns a Promise;
    // Promise.resolve normalizes both. undefined → null so the wire stays valid.
    Promise.resolve(out).then(function (val) {
      try { port.postMessage({ t: 'ret', id: id, val: val === undefined ? null : val }); } catch (e) {}
    }, function (err) {
      try { port.postMessage({ t: 'err', id: id, msg: String((err && err.message) || err) }); } catch (e) {}
    });
  }

  // Boot the visor exactly once, using the params the first tab supplied. Broadcast
  // {t:'up'} (with the booted PK) to every connected tab and flush queued calls.
  function tryBoot() {
    if (!api || !bootParams || bootPromise) { return; }
    bootPromise = Promise.resolve(
      api.boot(bootParams.sk, bootParams.seedpk || '', bootParams.seedws || '', bootParams.disc || '', bootParams.cfg || '')
    );
    bootPromise.then(function (pk) {
      bootedPK = (pk === undefined || pk === null) ? '' : pk;
      broadcast({ t: 'up', methods: Object.keys(api), pk: bootedPK });
      var q = queued; queued = [];
      for (var i = 0; i < q.length; i++) { dispatch(q[i].port, q[i].m.id, q[i].m.fn, q[i].m.args); }
    }, function (err) {
      broadcast({ t: 'fatal', msg: String((err && err.message) || err) });
    });
  }

  // ----- STUN self-IP bridge (delegated to the agent tab) -----
  // The Go runtime calls self.__skywireStunIP(iceServers); we ask the agent tab to
  // run STUN (RTCPeerConnection) and resolve with the discovered public IP.
  var stunSeq = 1, stunPending = {};
  self.__skywireStunIP = function (iceServers) {
    return new Promise(function (resolve) {
      var id = stunSeq++;
      stunPending[id] = resolve;
      if (!agentPost({ t: 'stun-ip', id: id, ice: iceServers })) { delete stunPending[id]; resolve(''); }
    });
  };

  // The Go runtime calls self.__skywireSaveConfig(overrideJSON) when the UI's
  // runtime-config editor Saves. localStorage + reload are the page's job (a
  // worker can't touch either), so broadcast the override to every connected
  // tab; hv-boot.js persists it and reloads the visor with it applied.
  self.__skywireSaveConfig = function (cfgJSON) {
    broadcast({ t: 'cfg-save', cfg: cfgJSON });
  };

  // ----- Voice audio bridge (delegated to the agent tab) -----
  // getUserMedia/AudioContext are main-thread-only. The Go voice Sink calls
  // self.__skyvoiceEmit(bytes) with each decoded frame; we forward it to the agent
  // tab (voice-audio.js) for playback. Captured mic frames come back as {t:'voiceMic'}
  // and are handed to the Go Source via self.__skyvoiceMic (installed by the wasm).
  self.__skyvoiceEmit = function (bytes) { agentPost({ t: 'voicePlay', b: bytes }); };

  // ----- WebRTC bridge (delegated to the agent tab) -----
  // RTCPeerConnection/RTCDataChannel don't exist in a worker, so
  // globalThis.__skywireRTC.newPC(iceServers) returns a PROXY PeerConnection whose
  // methods/events forward to a real one on the AGENT tab. The Go WebRTC carrier
  // (pkg/transport/network/webrtc_browser.go) uses it unchanged.
  var rtcPcSeq = 1, rtcDcSeq = 1, rtcCallSeq = 1;
  var rtcPCs = {};        // pcId -> { obj, dcs: {dcId -> dcObj} }
  var rtcCalls = {};      // callId -> {resolve, reject}
  function rtcPost(msg, transfer) { agentPost(msg, transfer); }
  function rtcPcCall(pcId, method, arg) {
    return new Promise(function (resolve, reject) {
      var callId = rtcCallSeq++;
      rtcCalls[callId] = { resolve: resolve, reject: reject };
      if (!agentPost({ t: 'rtc', op: 'pcCall', pcId: pcId, callId: callId, method: method, arg: arg })) {
        delete rtcCalls[callId]; reject(new Error('no webrtc agent tab'));
      }
    });
  }
  function rtcMakeDC(pcId, dcId) {
    return {
      binaryType: 'arraybuffer', readyState: 'connecting',
      onopen: null, onmessage: null, onclose: null, onerror: null,
      send: function (data) { rtcPost({ t: 'rtc', op: 'dcSend', pcId: pcId, dcId: dcId, data: data }, (data instanceof ArrayBuffer) ? [data] : []); },
      close: function () { rtcPost({ t: 'rtc', op: 'dcClose', pcId: pcId, dcId: dcId }); }
    };
  }
  self.__skywireRTC = {
    newPC: function (iceServers) {
      var pcId = rtcPcSeq++;
      var rec = { dcs: {} };
      var pc = {
        onicecandidate: null, ondatachannel: null,
        createDataChannel: function (label, opts) {
          var dcId = rtcDcSeq++;
          var dc = rtcMakeDC(pcId, dcId);
          rec.dcs[dcId] = dc;
          rtcPost({ t: 'rtc', op: 'createDC', pcId: pcId, dcId: dcId, label: label, opts: opts });
          return dc;
        },
        createOffer: function () { return rtcPcCall(pcId, 'createOffer', null); },
        createAnswer: function () { return rtcPcCall(pcId, 'createAnswer', null); },
        setLocalDescription: function (d) { return rtcPcCall(pcId, 'setLocalDescription', d); },
        setRemoteDescription: function (d) { return rtcPcCall(pcId, 'setRemoteDescription', d); },
        addIceCandidate: function (c) { return rtcPcCall(pcId, 'addIceCandidate', c); },
        close: function () { rtcPost({ t: 'rtc', op: 'pcClose', pcId: pcId }); }
      };
      rec.obj = pc;
      rtcPCs[pcId] = rec;
      var ice = [];
      try { for (var i = 0; iceServers && i < iceServers.length; i++) { ice.push({ urls: iceServers[i].urls }); } } catch (e) {}
      rtcPost({ t: 'rtc', op: 'newPC', pcId: pcId, iceServers: ice });
      return pc;
    }
  };
  // Apply an agent→worker RTC event to the proxy object (invoking the Go-set handler).
  function rtcHandleEvent(m) {
    var rec = rtcPCs[m.pcId];
    switch (m.op) {
      case 'ret': {
        var c = rtcCalls[m.callId];
        if (c) { delete rtcCalls[m.callId]; if (m.ok) { c.resolve(m.val); } else { c.reject(new Error(m.msg || 'rtc failed')); } }
        break;
      }
      case 'icecandidate':
        if (rec && typeof rec.obj.onicecandidate === 'function') { rec.obj.onicecandidate({ candidate: m.candidate }); }
        break;
      case 'datachannel':
        if (rec) { var dc = rtcMakeDC(m.pcId, m.dcId); rec.dcs[m.dcId] = dc; if (typeof rec.obj.ondatachannel === 'function') { rec.obj.ondatachannel({ channel: dc }); } }
        break;
      case 'dcOpen': { var d = rec && rec.dcs[m.dcId]; if (d) { d.readyState = 'open'; if (typeof d.onopen === 'function') { d.onopen({}); } } break; }
      case 'dcMessage': { var d2 = rec && rec.dcs[m.dcId]; if (d2 && typeof d2.onmessage === 'function') { d2.onmessage({ data: m.data }); } break; }
      case 'dcClose': { var d3 = rec && rec.dcs[m.dcId]; if (d3) { d3.readyState = 'closed'; if (typeof d3.onclose === 'function') { d3.onclose({}); } } break; }
      case 'dcError': { var d4 = rec && rec.dcs[m.dcId]; if (d4 && typeof d4.onerror === 'function') { d4.onerror({}); } break; }
      case 'pcGone': delete rtcPCs[m.pcId]; break;
    }
  }
  // The agent tab just changed. Every RTCPeerConnection lived in the OLD agent tab
  // and is gone, so every WebRTC transport is dead — but the Go carrier still holds
  // the proxy objects and doesn't know. Fire `onclose` on each proxy DataChannel
  // (webrtc_browser.go dcConn.onClose) and mark it closed, so the carrier tears the
  // transport down and autoconnect re-dials through the NEW agent (the peer
  // re-offers over dmsg, producing a fresh pcId the new agent hosts cleanly).
  // Reject in-flight pcCalls and fail pending STUN probes so nothing hangs.
  function resetRTCForNewAgent() {
    for (var cid in rtcCalls) { try { rtcCalls[cid].reject(new Error('webrtc agent tab changed')); } catch (e) {} }
    rtcCalls = {};
    for (var pid in rtcPCs) {
      var rec = rtcPCs[pid];
      if (!rec) { continue; }
      for (var did in rec.dcs) {
        var dc = rec.dcs[did];
        if (!dc) { continue; }
        try { dc.readyState = 'closed'; if (typeof dc.onclose === 'function') { dc.onclose({}); } } catch (e) {}
      }
    }
    rtcPCs = {};
    for (var sid in stunPending) { try { stunPending[sid](''); } catch (e) {} }
    stunPending = {};
  }

  // ----- port lifecycle + agent election -----
  function electAgent() {
    // Prefer a visible tab (browsers throttle background tabs, slowing the relay).
    var pick = null;
    for (var i = 0; i < ports.length; i++) {
      if (ports[i]._vis === 'visible') { pick = ports[i]; break; }
      if (!pick) { pick = ports[i]; }
    }
    if (pick !== agentPort) {
      agentPort = pick;
      resetRTCForNewAgent();
    }
  }

  function removePort(port) {
    var i = ports.indexOf(port);
    if (i >= 0) { ports.splice(i, 1); }
    if (port === agentPort) { agentPort = null; electAgent(); }
    // When the last tab goes, the browser tears the SharedWorker down (visor stops).
  }

  function handleMessage(port, m) {
    if (!m) { return; }
    switch (m.t) {
      case 'init':
        if (!bootParams) { bootParams = { sk: m.sk, seedpk: m.seedpk, seedws: m.seedws, disc: m.disc, cfg: m.cfg }; tryBoot(); }
        // If already booted, this tab was told 'up' on connect; nothing more to do.
        return;
      case 'shutdown':
        // Auto-update path: the SharedWorker holds the booted wasm and survives a
        // tab's location.reload(), so a plain reload would reconnect to THIS stale
        // runtime. self.close() terminates the worker; the reloading tab then boots
        // a fresh SharedWorker that fetches the new wasm-visor.wasm. Kills the visor
        // for every connected tab by design — they're all updating to the new build.
        try { self.close(); } catch (e) { /* not a SharedWorker (dedicated fallback) */ }
        return;
      case 'vis':
        port._vis = m.state;
        // We do NOT hand off the agent role on visibility change alone: the live
        // WebRTC transports live in the current agent tab and can't migrate, so a
        // handoff tears them all down — doing that on every tab switch would thrash.
        // A backgrounded agent tab is throttled far less for data channels than for
        // timers (acceptable per design), so the agent keeps its role until it
        // actually closes. Visibility is used only as a PREFERENCE when a fresh
        // agent must be elected (electAgent, on agent close) and there's a choice.
        // If there is no agent at all (shouldn't happen while tabs exist), adopt
        // this one so STUN/WebRTC have somewhere to run.
        if (!agentPort) { agentPort = port; }
        return;
      case 'bye':
        removePort(port);
        return;
      case 'stun-ip-result': {
        var r = stunPending[m.id];
        if (r) { delete stunPending[m.id]; r(m.ip || ''); }
        return;
      }
      case 'rtc':
        rtcHandleEvent(m);
        return;
      case 'voiceMic':
        if (typeof self.__skyvoiceMic === 'function') { self.__skyvoiceMic(m.b); }
        return;
      case 'call':
        if (!api || bootedPK === null) { queued.push({ port: port, m: m }); return; }
        dispatch(port, m.id, m.fn, m.args);
        return;
    }
  }

  function registerPort(port) {
    port._vis = 'visible';
    ports.push(port);
    if (!agentPort) { agentPort = port; }
    port.onmessage = function (ev) { handleMessage(port, ev.data); };
    if (typeof port.start === 'function') { port.start(); }
    // If the visor is already up, tell this tab immediately so it can install its
    // proxy and resolve without waiting for a broadcast.
    if (api && bootedPK !== null) {
      try { port.postMessage({ t: 'up', methods: Object.keys(api), pk: bootedPK }); } catch (e2) {}
    }
  }

  // Dual mode. As a SharedWorker (the preferred host) each connecting tab gets its
  // own MessagePort — the first connect kicks off the single boot, later connects
  // attach to the running visor. As a plain dedicated Worker (fallback when
  // SharedWorker is unavailable) `self` IS the single implicit port: STUN/RTC then
  // forward to the page's own main thread (its lone tab), which is exactly that
  // tab's agent. Both modes speak the identical {t:'init'|'call'|...} protocol.
  if (typeof SharedWorkerGlobalScope !== 'undefined' && self instanceof SharedWorkerGlobalScope) {
    self.onconnect = function (e) { registerPort(e.ports[0]); };
  } else {
    var selfPort = { postMessage: function (m, t) { self.postMessage(m, t || []); }, _vis: 'visible' };
    self.onmessage = function (ev) { handleMessage(selfPort, ev.data); };
    registerPort(selfPort);
  }

  fetch('wasm-visor.wasm' + __vqs).then(function (r) { return r.arrayBuffer(); }).then(function (buf) {
    return WebAssembly.instantiate(buf, go.importObject);
  }).then(function (res) {
    go.run(res.instance); // installs self.skywireVisor, then parks on select{}
    (function waitReady() {
      if (self.skywireVisor && self.skywireVisor.boot) {
        api = self.skywireVisor;
        tryBoot(); // boots now if the first tab's init already arrived; else on init
      } else {
        setTimeout(waitReady, 10);
      }
    })();
  }).catch(function (e) {
    broadcast({ t: 'fatal', msg: String((e && e.message) || e) });
  });
})();
