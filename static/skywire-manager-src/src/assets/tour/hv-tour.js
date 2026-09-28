/*
 * hv-tour.js — the guided walkthrough of the Skywire hypervisor UI.
 *
 * Dependency-free and self-contained: index.html loads it with a plain
 * <script defer>, and it touches no Angular code. It dims the page, spotlights
 * one element at a time (a transparent cutout made with a very large
 * box-shadow) and shows a callout with Back / Next / Skip.
 *
 * SCOPE — this tour covers the ANGULAR HYPERVISOR UI only: the visor list, a
 * visor's own tabs, and the network-wide views. The desktop that can host this
 * UI (windows, the mesh browser, the shell, files, identity, pairing, mail) is
 * a different surface with its own tour, registered by the desk as the "tour"
 * app. See docs/tours.md for the seam.
 *
 * The same UI is served two ways and the copy differs where the truth differs:
 *   native — a visor process on a host machine; TCP/QUIC carriers, real disk,
 *            a reward system and host resources to report.
 *   wasm   — the identical Angular build served by an in-tab wasm visor core;
 *            WSS/WebTransport carriers, no host to measure, Rewards and
 *            Resources hidden (see home-tabs.ts isWasmHvCore).
 * A step's copy may be a string, {wasm, native, both}, or fn(mode).
 */
(function () {
  "use strict";

  var SEEN_KEY = "skywire-hv-tour-seen";
  var HL_ID = "skywire-hv-tour-hl";
  var running = false;

  // ---------------------------------------------------------------- platform

  // hv-boot.js sets window.__SKYWIRE_HV__ before Angular boots when the page is
  // served by a browser wasm core. A native hypervisor never sets it. Mirrors
  // isWasmHvCore() in app/utils/home-tabs.ts — .visor / .standalone are the two
  // in-tab modes; .pk is a remote viewer of a NATIVE hypervisor, so it is not
  // a wasm core and must read as native here too.
  function detectMode() {
    try {
      var cfg = window.__SKYWIRE_HV__ || {};
      if (cfg.visor || cfg.standalone) { return "wasm"; }
      if (window.SKYWIRE_HV_MODE === "wasm") { return "wasm"; }
    } catch (e) { /* a sandboxed frame can throw on window access */ }
    return "native";
  }

  // ------------------------------------------------------------------- steps

  function buildSteps(mode, nodePath, visorList, localVisor) {
    return [
      // --- the front page: the visor list, read left to right ---------------
      {
        route: visorList,
        title: "This is not a normal web page",
        body: {
          wasm: "You're running a full <b>Skywire visor</b> — a live routing peer on an encrypted, peer-to-peer mesh — <b>inside this browser tab</b>. No install, no account, no server, no one in the middle. It lives only here: close the tab and it's gone unless you export your key. We'll start on the <b>visor list</b> in front of you and read it left to right.",
          native: "You're running a full <b>Skywire visor</b> on this machine — a persistent routing peer on an encrypted, peer-to-peer mesh. No account, no central server, no one in the middle. We'll start on your hypervisor's <b>visor list</b> and read it left to right."
        },
        disc: {
          summary: "Open-source, no warranty — your keys are yours",
          details: "Skywire and the Skycoin wallet are experimental, open-source software, provided as-is and without warranty. You run this visor yourself and hold your own keys and coins; no one else can access or recover them. Understand the risks before relying on it or storing value."
        }
      },

      {
        route: visorList, sel: "td.key-cell",
        title: "You are your public key",
        body: {
          wasm: "Every visor is named by a <b>public key</b> — this 66-character hex string. There are no usernames and no accounts on the mesh: the key <i>is</i> the identity and the address. It's one half of a keypair held in <b>this browser</b>; the secret half never leaves it, and every link your visor makes is encrypted end-to-end with it.",
          native: "Every visor is named by a <b>public key</b> — this 66-character hex string. No usernames, no accounts: the key <i>is</i> the identity and the address. It's one half of a keypair on <b>this machine</b>; the secret half never leaves it, and every link is encrypted end-to-end with it."
        },
        more: {
          summary: "Why a key instead of a name or IP",
          panel: "On the clearnet you're found by IP and trusted through a certificate authority vouching for a name. On Skywire the <b>public key</b> is both at once: it's the address other visors route to, and the thing they encrypt to — so there's nothing to spoof and no authority to trust. Whoever holds the matching secret key <i>is</i> this visor."
        }
      },

      // Anchored to the live count cell first, falling back to the always-present
      // column header: a browser visor's dmsg sessions can flap to zero, and the
      // cell then renders "-" with no .dmsg-counts, which would skip the step.
      {
        route: visorList, sel: ".dmsg-counts, th.dmsg-column",
        title: "How you're connected: dmsg",
        body: {
          wasm: "This is your <b>live count</b> of <b>dmsg servers</b>, broken down by <b>carrier</b>. dmsg is an encrypted relay network: any two visors reach each other through these servers without connecting directly. A browser can't open a raw TCP socket, so it joins over <b>WSS</b> (WebSocket-over-TLS) and <b>WebTransport</b> — the carriers counted here.",
          native: "This is your <b>live count</b> of <b>dmsg servers</b>, broken down by <b>carrier</b>. dmsg is an encrypted relay network: any two visors reach each other through these servers without connecting directly. A native visor joins over <b>TCP</b>, and QUIC where a server offers it."
        },
        more: {
          summary: "The four carriers",
          panel: "dmsg carries the control plane and a fallback data path. The <b>carrier</b> — how a visor reaches a dmsg server — depends on the host:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>tcp</b> — native visors; a raw TCP socket.</li>' +
            '<li style="margin:.3em 0"><b>ws</b> / <b>wss</b> — WebSocket, the browser\'s only option. <code>wss</code> is required on an https page: browsers block mixed-content <code>ws</code>.</li>' +
            '<li style="margin:.3em 0"><b>webtransport</b> — HTTP/3 datagrams; browser-dialable.</li>' +
            '<li style="margin:.3em 0"><b>quic</b> — QUIC over UDP; native. The one carrier that also passes unreliable <b>datagrams</b>, not just reliable streams.</li>' +
            "</ul>" +
            "These are the <i>same four protocols</i> Skywire uses for direct transports, in the next column. dmsg is simply the <b>relayed</b> version: same wire, reached through a server instead of directly."
        }
      },

      {
        route: visorList, sel: ".tp-counts, th.transports-column",
        title: "Direct links: transports",
        body: "Right beside it: your visor's live <b>transports</b> count, by type. Where dmsg <i>relays</i> through a server, a transport is point-to-point. Your visor is already dialing peers around the world; the total climbs as it settles in.",
        more: {
          summary: "Transport types, and how they mirror dmsg",
          panel: "A transport is a direct link between two visors. <b>Four mirror the dmsg carriers exactly</b> — same wire, dialed peer-to-peer instead of to a server:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>stcpr</b> — direct TCP, found via the address resolver.</li>' +
            '<li style="margin:.3em 0"><b>ws</b> — direct WebSocket.</li>' +
            '<li style="margin:.3em 0"><b>webtransport</b> — direct WebTransport.</li>' +
            '<li style="margin:.3em 0"><b>quic</b> — QUIC over UDP; also carries true UDP datagrams end-to-end.</li>' +
            "</ul>" +
            "<b>Two only make sense peer-to-peer</b>, because a dmsg server is a fixed public endpoint with nothing to holepunch to:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>sudph</b> — direct UDP, NAT-holepunched, carrying a reliable stream via KCP.</li>' +
            '<li style="margin:.3em 0"><b>webrtc</b> — browser-to-browser DataChannel, NAT-traversed via ICE; dmsg carries only the signaling.</li>' +
            "</ul>" +
            "And <b>dmsg</b> itself is a transport — the relayed one. More direct transports means less relaying, and lower latency."
        }
      },

      {
        route: visorList, sel: "td.label-cell",
        title: "Label & location",
        body: "Last on the row: a human <b>label</b> you can set to recognize a visor at a glance, and its detected <b>IP and location</b>. The label is local and cosmetic; the identity that matters is the public key. That's the whole front page — <i>who</i> the visor is, <i>how</i> it's connected, and <i>where</i>. Now let's open the visor itself."
      },

      // --- inside this visor ------------------------------------------------
      // Entered through #/nodes/local rather than a PK, so that on a hypervisor
      // managing several visors the tour opens the one it keeps calling "your
      // visor". The redirect leaves that PK in the hash and every step after
      // this one reads it from there.
      {
        route: localVisor,
        sel: "app-node-info-content .info-line", has: "DMSG servers",
        title: "Inside your visor: Info",
        body: {
          wasm: "Opening your visor lands on its <b>Info</b> tab. The <b>DMSG servers</b> line spells out the same connection the list summarized, next to version, uptime and identity — the relay layer up close, reached over WSS and WebTransport from the browser.",
          native: "Opening your visor lands on its <b>Info</b> tab — <b>DMSG servers</b>, version, uptime and identity in one place. This is the relay layer up close: the control plane and a fallback data path, reached over TCP."
        }
      },

      {
        route: function () { return nodePath("transports"); }, sel: "app-transport-list",
        title: "Inside your visor: Transports",
        body: {
          wasm: "This visor's <b>Transports</b>: each row is one direct link to a peer. In a browser that's mostly WebTransport and WebRTC, plus dmsg; a native visor also builds stcpr, sudph and quic.",
          native: "This visor's <b>Transports</b>: each row is a direct link — stcpr, sudph, dmsg, quic — punching through NATs where needed, so traffic takes the shortest path instead of always relaying."
        }
      },

      {
        route: function () { return nodePath("routing"); }, sel: "app-route-list",
        title: "Inside your visor: Routing",
        body: "The <b>Routing</b> tab shows the paths traffic takes across those transports. A route can be <b>DIRECT</b> (one hop straight to the peer), <b>MULTIHOP</b> (through several visors, so no single hop sees both ends), or <b>MULTIPLEXED</b> (spread across parallel paths for resilience and throughput). The route-finder builds them on demand.",
        more: {
          summary: "Hops and the privacy trade-off",
          panel: "A route is an ordered path of transports. One hop is fastest; more hops mean no single intermediary knows both source and destination, at the cost of latency. A multiplexed route splits one stream over several paths at once — if a leg dies the rest carry on, and the aggregate is faster than any single leg."
        }
      },

      {
        route: function () { return nodePath("bandwidth"); }, sel: "app-bandwidth",
        title: "Inside your visor: Bandwidth",
        body: "<b>Bandwidth</b> charts what those routes actually carried — sent and received over time. It's the quickest way to see whether a transport is doing real work or merely connected."
      },

      {
        route: function () { return nodePath("apps"); }, sel: "app-node-app-list",
        title: "Inside your visor: Apps",
        body: {
          wasm: "This visor's <b>Apps</b>. In a browser tab the useful one is <b>skysocks-client-lite</b> — a proxy client that routes clearnet fetches out through an <b>exit</b> visor, IP-anonymously. Unlike the native skysocks-client it serves <i>no local port</i>: it works only inside this tab.",
          native: "This visor's <b>Apps</b> — start, stop and configure them here: the <b>skysocks</b> proxy client and server, the <b>VPN</b> client and server, and <b>skychat</b>. On a native visor these bind real local ports and can serve other machines on your network."
        },
        more: {
          summary: "skysocks-client vs. skysocks-client-lite",
          panel: "<b>skysocks-client</b> (native) runs as an app process and serves a local <b>SOCKS5 port</b> other programs point at. <b>skysocks-client-lite</b> (browser) has no process and no port — it lives in the tab and proxies only this visor's own browse windows and wallet. Both dial an <b>exit</b> visor that does the clearnet egress, so a site sees the exit's IP, not yours."
        }
      },

      {
        route: function () { return nodePath("uptime"); }, sel: "app-uptime",
        title: "Inside your visor: Uptime",
        body: "<b>Uptime</b> is this visor's own record of staying online and reachable, day by day. On the mesh that record is worth something: it's what the reward system pays against."
      },

      {
        route: function () { return nodePath("logs"); }, sel: "app-node-logs, app-logs",
        title: "Inside your visor: Logs",
        body: {
          wasm: "<b>Logs</b> is a live tail of this visor's own runtime — dmsg, transports, routing and apps — straight from the wasm core running in this tab.",
          native: "<b>Logs</b> is a live tail of this visor's runtime — dmsg, transports, routing and apps — read from the visor process on this machine."
        }
      },

      {
        route: function () { return nodePath("settings"); }, sel: "app-node-settings",
        title: "Inside your visor: Settings",
        body: "Per-visor <b>Settings</b> — its label, its reward address, and the knobs that decide how it behaves on the mesh. Everything here applies to this one visor; the hypervisor-wide settings live on the last tab of the tour."
      },

      // --- back out: this hypervisor's cluster ------------------------------
      {
        route: visorList, sel: "app-node-list",
        title: "Your cluster: the hypervisor UI",
        body: {
          wasm: "Back at the top level. This visor list <b>is the hypervisor UI</b> — every visor connected to <i>this</i> hypervisor appears here. Right now that's one visor, in this browser tab. To <b>manage a remote visor</b> from here, add its public key; to have this visor <b>managed by a remote hypervisor</b>, set that hypervisor's key in this visor's config.",
          native: "This visor list <b>is the hypervisor UI</b> — every visor connected to this hypervisor appears here. To <b>manage a remote visor</b>, add its public key; to have this visor <b>managed by a remote hypervisor</b>, set that hypervisor's key in this visor's config. Connected visors appear here, each fully controllable over the mesh."
        },
        more: {
          summary: "Hypervisor and visor: who manages whom",
          panel: "A <b>hypervisor</b> is just a visor that also serves this management UI and holds the keys of the visors it manages. The relationship is set in config: give this hypervisor a remote visor's key to <i>manage</i> it, or set a remote hypervisor's key on this visor to be <i>managed</i> by it. Management runs over the same encrypted mesh — a hypervisor in a browser tab can drive a native visor on the other side of the world, and the reverse."
        }
      },

      // --- the whole mesh ---------------------------------------------------
      {
        route: "#/nodes/transports",
        title: "The mesh: all transports",
        body: "<b>Transports</b> — every direct link across the <i>whole</i> mesh, with per-type bandwidth: the live edge list the route-finder draws on. Fetched <b>peer-to-peer over dmsg</b>, never from a web server."
      },

      {
        route: "#/nodes/network", sel: "app-network-view",
        title: "The mesh: every visor",
        body: "<b>Network</b> — a searchable directory of <b>every visor on the mesh</b>, with the running count top-left. Filter by country, version or transport type, and read each visor's transport mix and uptime at a glance. The next tab draws the same set as a live graph."
      },

      {
        route: "#/nodes/visualizer", sel: "app-network-visualizer",
        title: "The mesh: visualizer",
        body: "<b>Network Visualizer</b> — an interactive, geographic render of that same graph, in flat, globe and WebGL views."
      },

      {
        route: "#/nodes/services-health", sel: "app-services-health",
        title: "The mesh: services",
        body: {
          wasm: "<b>Services</b> — the health of the shared services that keep the mesh working: config, dmsg discovery, transport discovery, the route-finder, the address resolver, service discovery. <b>This browser tab probes each one live over dmsg</b> — status, version and latency — the same reach a native visor has.",
          native: "<b>Services</b> — the health of the shared services that keep the mesh working: config, dmsg discovery, transport discovery, the route-finder, the address resolver, service discovery. All of it fetched over dmsg, a visor talking directly to visors around the world."
        }
      },

      {
        route: "#/nodes/uptime", sel: "app-multi-visor-uptime",
        title: "The mesh: uptime",
        body: "<b>Uptime</b> — how consistently every visor has stayed online over 1d, 7d and 30d. This is the mesh-wide version of your own visor's uptime tab, and the basis for rewards."
      },

      {
        nativeOnly: true, route: "#/nodes/rewards", sel: "app-node-list",
        title: "The mesh: rewards",
        body: "<b>Rewards</b> — the Skycoin paid out to visors that stay online and reachable. A visor appears here once it qualifies. The tab is hidden on a browser visor: a tab that vanishes when you close it can't hold up its end of an uptime record."
      },

      {
        nativeOnly: true, route: "#/nodes/resources", sel: "app-multi-visor-resources",
        title: "The mesh: resources",
        body: "<b>Resources</b> — CPU, memory and disk of the machines hosting the visors this hypervisor manages. Also hidden on a browser visor, which has no host to measure."
      },

      {
        route: "#/settings", sel: "app-settings",
        title: "Settings",
        body: "Hypervisor-wide <b>Settings</b> — the services this deployment points at, the update channel, and how the UI behaves. Changes here affect every visor this hypervisor manages, not just one."
      },

      // --- close ------------------------------------------------------------
      {
        title: { wasm: "A self-hosting internet, in a tab", native: "A self-hosting internet" },
        body: {
          wasm: "No server. No account. No IP handed out. Just your browser, cryptographic keys, and a global peer-to-peer mesh — reachable from anywhere, run by nobody, and gone when you close the tab unless you export your key.<br><br>This tour covered the <b>hypervisor UI</b>. The desktop around it — the mesh browser, the shell, files, your identity, pairing and mail — has <b>its own tour</b>, in the desk's launcher. Reopen this one any time with the <b>?</b> button.",
          native: "No server. No account. Just cryptographic keys and a global peer-to-peer mesh — reachable from anywhere, run by nobody.<br><br>This tour covered the <b>hypervisor UI</b>: the visor list, one visor up close, and the mesh-wide views. Reopen it any time with the <b>?</b> button."
        }
      }
    ];
  }

  // ------------------------------------------------------------------ engine

  function startTour(doc) {
    doc = doc || document;
    if (running || doc.getElementById(HL_ID)) { return; }
    running = true;

    var win = doc.defaultView || window;
    var mode = detectMode();

    // pick resolves per-platform copy: a plain string, {wasm,native,both}, or fn(mode).
    function pick(v) {
      if (typeof v === "function") { return v(mode); }
      if (v && typeof v === "object" && !v.nodeType) {
        return v[mode] != null ? v[mode] : (v.both != null ? v.both : "");
      }
      return v == null ? "" : v;
    }

    // The PK the "inside your visor" steps address.
    //
    // The URL wins, and is re-read every time rather than cached: the walk into
    // the visor goes through #/nodes/local, the route that resolves THIS
    // hypervisor's own visor and redirects to it, so after that step the hash
    // holds the PK the copy actually means by "your visor".
    //
    // A /nodes/<pk>/ link in the DOM is only a fallback, and a poor one on a
    // hypervisor managing more than one visor: it is whichever row happens to
    // sort first, which is how the first draft of this tour ended up saying
    // "your visor" over somebody else's. It is kept for the case where the
    // local route is unavailable, and never cached over a URL answer.
    var visorList = "#/nodes/list/1";
    var localVisor = "#/nodes/local";
    var selfPK = "";
    function resolveSelfPK() {
      try {
        var m = (win.location.hash || "").match(/nodes\/([0-9a-fA-F]{66})/);
        if (m) { selfPK = m[1]; return selfPK; }
      } catch (e) { /* ignore */ }
      if (selfPK) { return selfPK; }
      try {
        var links = doc.querySelectorAll('a[href*="/nodes/"]');
        for (var k = 0; k < links.length; k++) {
          var mm = (links[k].getAttribute("href") || "").match(/nodes\/([0-9a-fA-F]{66})/);
          if (mm) { selfPK = mm[1]; return selfPK; }
        }
      } catch (e) { /* ignore */ }
      return selfPK;
    }

    // Falls back to the local route rather than the list: a step that cannot
    // name the PK should still land on the right visor, and #/nodes/local is
    // the router's own answer to "which one is mine".
    function nodePath(tab) {
      var pk = resolveSelfPK();
      return pk ? "#/nodes/" + pk + "/" + tab : localVisor;
    }

    var steps = buildSteps(mode, nodePath, visorList, localVisor).filter(function (s) {
      return !(s.nativeOnly && mode !== "native") && !(s.wasmOnly && mode !== "wasm");
    });

    injectStyles(doc);

    var hl = doc.createElement("div");
    hl.id = HL_ID;
    hl.className = "skywire-tour-hl";
    doc.body.appendChild(hl);

    var call = doc.createElement("div");
    call.className = "skywire-tour-call";
    doc.body.appendChild(call);

    var i = 0;
    var pending = null; // timer id of an in-flight "wait for the target" poll

    function cleanup() {
      if (pending) { clearTimeout(pending); pending = null; }
      if (hl.parentNode) { hl.parentNode.removeChild(hl); }
      if (call.parentNode) { call.parentNode.removeChild(call); }
      win.removeEventListener("resize", reposition);
      win.removeEventListener("scroll", reposition, true);
      running = false;
      try { localStorage.setItem(SEEN_KEY, "1"); } catch (e) { /* private mode */ }
    }

    // pickTarget takes a comma list in PRIORITY order rather than letting
    // querySelector pick by document order: the live count cells come first,
    // the always-present column headers are the fallback.
    function pickTarget(sel, has) {
      if (!sel) { return null; }
      var parts = sel.split(",");
      for (var p = 0; p < parts.length; p++) {
        var nodes = doc.querySelectorAll(parts[p].trim());
        for (var n = 0; n < nodes.length; n++) {
          if (!has || (nodes[n].textContent || "").indexOf(has) >= 0) { return nodes[n]; }
        }
      }
      return null;
    }

    var current = null;

    function reposition() {
      if (!current) {
        hl.style.opacity = "0";
        call.style.top = "50%";
        call.style.left = "50%";
        call.style.transform = "translate(-50%,-50%)";
        return;
      }
      var r = current.getBoundingClientRect();
      var pad = 6;
      hl.style.opacity = "1";
      hl.style.top = (r.top - pad) + "px";
      hl.style.left = (r.left - pad) + "px";
      hl.style.width = (r.width + pad * 2) + "px";
      hl.style.height = (r.height + pad * 2) + "px";

      // Put the callout under the target, or above it when there is no room.
      var below = r.bottom + 14;
      var ch = call.offsetHeight || 220;
      call.style.transform = "none";
      if (below + ch < win.innerHeight - 10) {
        call.style.top = below + "px";
      } else if (r.top - ch - 14 > 10) {
        call.style.top = (r.top - ch - 14) + "px";
      } else {
        call.style.top = Math.max(10, (win.innerHeight - ch) / 2) + "px";
      }
      var cw = call.offsetWidth || 420;
      var left = Math.min(Math.max(10, r.left), win.innerWidth - cw - 10);
      call.style.left = left + "px";
    }

    win.addEventListener("resize", reposition);
    win.addEventListener("scroll", reposition, true);

    function render(step) {
      var n = i + 1, total = steps.length;
      var parts = [];
      parts.push('<div class="skywire-tour-count">' + n + " / " + total + "</div>");
      parts.push("<h3>" + pick(step.title) + "</h3>");
      parts.push("<div class=\"skywire-tour-body\">" + pick(step.body) + "</div>");
      if (step.more) {
        parts.push('<details class="skywire-tour-more"><summary>' + step.more.summary +
          "</summary><div>" + pick(step.more.panel) + "</div></details>");
      }
      if (step.disc) {
        parts.push('<details class="skywire-tour-disc"><summary>' + step.disc.summary +
          "</summary><div>" + pick(step.disc.details) + "</div></details>");
      }
      parts.push('<div class="skywire-tour-btns">' +
        '<button data-act="skip" class="skywire-tour-skip">Skip</button>' +
        '<span class="skywire-tour-spacer"></span>' +
        '<button data-act="back"' + (i === 0 ? " disabled" : "") + ">Back</button>" +
        '<button data-act="next" class="skywire-tour-next">' +
        (i === total - 1 ? "Done" : "Next") + "</button></div>");
      call.innerHTML = parts.join("");

      call.querySelector('[data-act="skip"]').onclick = cleanup;
      call.querySelector('[data-act="back"]').onclick = function () { go(i - 1); };
      call.querySelector('[data-act="next"]').onclick = function () {
        if (i === steps.length - 1) { cleanup(); } else { go(i + 1); }
      };
    }

    // go navigates if the step asks for a route, then waits for the target to
    // appear before rendering. A target that never turns up is not an error —
    // tabs differ between a native and a browser-served UI — so after the
    // deadline the step shows centred, with no spotlight, rather than stalling.
    function go(n) {
      if (n < 0 || n >= steps.length) { return; }
      if (pending) { clearTimeout(pending); pending = null; }
      i = n;
      var step = steps[i];
      current = null;

      var route = typeof step.route === "function" ? step.route() : step.route;
      if (route && win.location.hash !== route) { win.location.hash = route; }

      render(step);

      if (!step.sel) { reposition(); return; }

      var deadline = Date.now() + 8000;
      (function wait() {
        var el = pickTarget(step.sel, step.has);
        if (el) {
          current = el;
          try { el.scrollIntoView({ block: "center", behavior: "smooth" }); } catch (e) { /* ignore */ }
          // One more frame so the smooth scroll has moved before we measure.
          pending = setTimeout(function () { pending = null; reposition(); }, 260);
          return;
        }
        if (Date.now() > deadline) { reposition(); return; }
        pending = setTimeout(wait, 160);
      })();
    }

    go(0);
  }

  // ------------------------------------------------------------------ styles

  function injectStyles(doc) {
    if (doc.getElementById("skywire-tour-css")) { return; }
    var css = [
      // The dim is the spotlight's own shadow, so there is exactly one element
      // over the page and the cutout can never drift out of sync with it.
      ".skywire-tour-hl{position:fixed;z-index:2147483646;border-radius:6px;",
      "box-shadow:0 0 0 9999px rgba(0,0,0,.62),0 0 0 2px #0072FF inset;",
      "pointer-events:none;transition:top .18s,left .18s,width .18s,height .18s,opacity .18s;opacity:0}",
      ".skywire-tour-call{position:fixed;z-index:2147483647;width:min(440px,calc(100vw - 20px));",
      "background:#12161c;color:#e8eef7;border:1px solid #2a3442;border-radius:10px;",
      "padding:16px 18px;box-shadow:0 12px 40px rgba(0,0,0,.55);",
      "font:14px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;",
      // The dashboard sets letter-spacing on headings and body copy. Inheriting
      // it pushes the glyphs of an apostrophe apart — "you're" renders as
      // "you ' re" — so the callout states its own spacing rather than taking
      // whatever the page around it happens to use.
      "letter-spacing:normal;word-spacing:normal;text-transform:none;",
      "transition:top .18s,left .18s}",
      ".skywire-tour-call h3,.skywire-tour-call div,.skywire-tour-call summary,",
      ".skywire-tour-call button{letter-spacing:normal;text-transform:none}",
      ".skywire-tour-call h3{margin:.1em 0 .5em;font-size:17px;color:#fff}",
      ".skywire-tour-call code{background:#1e2530;padding:.08em .35em;border-radius:3px;font-size:.92em}",
      ".skywire-tour-count{float:right;opacity:.5;font-size:12px;letter-spacing:.04em}",
      ".skywire-tour-body{opacity:.94}",
      ".skywire-tour-more,.skywire-tour-disc{margin-top:.8em;border-top:1px solid #222b36;padding-top:.6em}",
      ".skywire-tour-more summary,.skywire-tour-disc summary{cursor:pointer;opacity:.72;font-size:13px}",
      ".skywire-tour-more div,.skywire-tour-disc div{margin-top:.55em;opacity:.85;font-size:13px}",
      ".skywire-tour-btns{display:flex;align-items:center;gap:8px;margin-top:1em}",
      ".skywire-tour-spacer{flex:1}",
      ".skywire-tour-call button{background:#1d2632;color:#dbe6f3;border:1px solid #33404f;",
      "border-radius:6px;padding:.42em .95em;cursor:pointer;font-size:13px}",
      ".skywire-tour-call button:hover:not(:disabled){background:#27323f}",
      ".skywire-tour-call button:disabled{opacity:.38;cursor:default}",
      ".skywire-tour-next{background:#0072FF !important;border-color:#0072FF !important;color:#fff !important}",
      ".skywire-tour-skip{opacity:.6}",
      // The launcher: small, out of the way, and the only thing the tour adds
      // to the page when it is not running.
      "#skywire-tour-launch{position:fixed;right:14px;bottom:14px;z-index:2147483645;",
      "width:34px;height:34px;border-radius:50%;border:1px solid #33404f;background:#1d2632;",
      "color:#dbe6f3;font:16px/1 system-ui,sans-serif;cursor:pointer;opacity:.55;",
      "transition:opacity .15s}",
      "#skywire-tour-launch:hover{opacity:1}"
    ].join("");
    var el = doc.createElement("style");
    el.id = "skywire-tour-css";
    el.textContent = css;
    doc.head.appendChild(el);
  }

  // ---------------------------------------------------------------- launcher

  function addLauncher(doc) {
    if (doc.getElementById("skywire-tour-launch")) { return; }
    injectStyles(doc);
    var b = doc.createElement("button");
    b.id = "skywire-tour-launch";
    b.type = "button";
    b.textContent = "?";
    b.title = "Take the hypervisor UI tour";
    b.onclick = function () { startTour(doc); };
    doc.body.appendChild(b);
  }

  globalThis.skywireStartHvTour = function () { startTour(document); };

  // insideApp is false on the login page, and anywhere else AuthGuardService
  // has not let us through. Every step navigates, so a tour started outside the
  // app would just be bounced back to /login by the guard — worse on a native
  // hypervisor with auth enabled, where that is the FIRST page a new user sees
  // and the autostart would fire straight into it.
  function insideApp() {
    try {
      var h = window.location.hash || "";
      return h.indexOf("#/nodes") === 0 || h.indexOf("#/settings") === 0;
    } catch (e) {
      return false;
    }
  }

  function syncLauncher(doc) {
    var b = doc.getElementById("skywire-tour-launch");
    if (b) { b.style.display = insideApp() ? "" : "none"; }
  }

  // Angular's router navigates with history.pushState, and pushState does NOT
  // fire hashchange — so listening for that event alone catches a hand-edited
  // address bar and nothing the app itself does. Logging in is a router
  // navigation, which is precisely the transition the launcher has to notice.
  // A once-a-second poll is the whole of the fix; the events just make it feel
  // instant when they do fire.
  function watchRoute(onChange) {
    var last = insideApp();
    function check() {
      var now = insideApp();
      if (now !== last) { last = now; onChange(); }
    }
    window.addEventListener("hashchange", check);
    window.addEventListener("popstate", check);
    setInterval(check, 1000);
  }

  function boot() {
    addLauncher(document);
    syncLauncher(document);
    watchRoute(function () { syncLauncher(document); });

    var seen = true;
    try { seen = !!localStorage.getItem(SEEN_KEY); } catch (e) { /* private mode: do not nag */ }
    if (seen) { return; }

    // Offered once, and only once we are past the login guard and Angular has
    // had time to draw the list the first step points at. On the login page we
    // wait for the hash to say we are in rather than giving up: logging in is
    // exactly when a first-time user should be offered the tour.
    var tries = 0;
    (function offer() {
      if (insideApp()) { setTimeout(function () { startTour(document); }, 1200); return; }
      if (++tries > 600) { return; } // ~5 minutes, then stop watching
      setTimeout(offer, 500);
    })();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
