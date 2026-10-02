/*
 * hv-tour.js — the guided walkthrough of the Skywire hypervisor UI.
 *
 * Dependency-free and self-contained: index.html loads it with a plain
 * <script defer>, and it touches no Angular code. It dims the page, spotlights
 * one element at a time (a transparent cutout made with a very large
 * box-shadow) and shows a callout with Back / Next / Skip.
 *
 * SCOPE — this tour covers the ANGULAR HYPERVISOR UI only: the visor list, a
 * visor’s own tabs, and the network-wide views. The desktop that can host this
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
 * A step’s copy may be a string, {wasm, native, both}, or fn(mode).
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

  // The copy is written as NARRATION, not instruction: it describes the screen
  // rather than addressing a reader ("Each row is a visor", never "you can see
  // your visors here"). Two reasons. The dashboard is a familiar kind of thing —
  // an admin console — and talking someone through it as if it were strange is
  // condescending; and the same text is read by two quite different arrivals,
  // one who installed a daemon and one who clicked a link, so second person
  // ends up addressing the wrong person half the time.
  function buildSteps(mode, nodePath, visorList, localVisor) {
    return [
      // --- the front page: the visor list, read left to right ---------------
      //
      // NOT "this is not a normal web page". This IS a normal web page: an
      // Angular admin console, and an unremarkable one by design. What is
      // unprecedented is the visor — and in the browser build, the desk around
      // it, which is what the desk tour opens on. Overselling the dashboard
      // spends the reader's credulity on the wrong screen.
      {
        route: visorList,
        title: {
          wasm: "A hypervisor dashboard, served from a tab",
          native: "The hypervisor dashboard"
        },
        body: {
          wasm: "A <b>hypervisor</b> manages visors: it lists them, opens them and configures them. This one is ordinary web software with one oddity — the visor it manages is the <b>wasm core in this tab</b>, and the tab is also serving the page. Nothing was installed and nothing persists: closing the tab ends the visor unless its key was exported. The list below reads left to right.",
          native: "A <b>hypervisor</b> manages visors: it lists them, opens them and configures them. It is itself a visor that serves this page and holds the keys of the visors it manages. The list below reads left to right."
        },
        disc: {
          summary: "Open-source, no warranty — keys are held locally",
          details: "Skywire and the Skycoin wallet are experimental, open-source software, provided as-is and without warranty. Visors hold their own keys and coins; nobody else can access or recover them. Understand the risks before relying on it or storing value."
        }
      },

      {
        route: visorList, sel: "td.key-cell",
        title: "A visor is a public key",
        body: {
          wasm: "Each visor is named by a <b>public key</b> — 66 hex characters. There are no usernames and no accounts: the key is both identity and address. The secret half sits in <b>this browser</b> and never leaves it; every link the visor makes is encrypted to it.",
          native: "Each visor is named by a <b>public key</b> — 66 hex characters. There are no usernames and no accounts: the key is both identity and address. The secret half sits <b>on this machine</b> and never leaves it; every link is encrypted to it."
        },
        more: {
          summary: "Why a key instead of a name or an address",
          panel: "On the clearnet an address locates a host and a certificate authority vouches that a name belongs to it — two mechanisms, two trust assumptions. A public key collapses both: it is what peers route to and what they encrypt to. There is no name to spoof and no authority to trust. Whoever holds the matching secret key <i>is</i> that visor."
        }
      },

      // Anchored to the live count cell first, falling back to the always-present
      // column header: a browser visor's dmsg sessions can flap to zero, and the
      // cell then renders "-" with no .dmsg-counts, which would skip the step.
      {
        route: visorList, sel: ".dmsg-counts, th.dmsg-column",
        title: "dmsg — the relay layer",
        body: {
          wasm: "Connected <b>dmsg servers</b>, counted by carrier. dmsg is an encrypted relay network: two visors reach each other through a server instead of directly. A browser cannot open a raw socket, so it joins over <b>WSS</b> and <b>WebTransport</b>.",
          native: "Connected <b>dmsg servers</b>, counted by carrier. dmsg is an encrypted relay network: two visors reach each other through a server instead of directly. A native visor joins over <b>TCP</b>, or QUIC where a server offers it."
        },
        more: {
          summary: "The four carriers",
          panel: "dmsg carries the control plane, and a data path when no direct one exists. The <b>carrier</b> is how a visor reaches a server:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>tcp</b> — a raw socket; native only.</li>' +
            '<li style="margin:.3em 0"><b>ws</b> / <b>wss</b> — WebSocket; the only option in a browser. An https page requires <code>wss</code>.</li>' +
            '<li style="margin:.3em 0"><b>webtransport</b> — HTTP/3; dialable from a browser.</li>' +
            '<li style="margin:.3em 0"><b>quic</b> — QUIC over UDP; native. The only carrier that also passes unreliable <b>datagrams</b>.</li>' +
            "</ul>" +
            "The next column counts <b>direct transports</b>, which use the same four protocols peer-to-peer. dmsg is the relayed form of the same wire."
        }
      },

      {
        route: visorList, sel: ".tp-counts, th.transports-column",
        title: "Transports — the direct links",
        body: "Live <b>transports</b>, by type. A transport is point-to-point where dmsg relays. The count climbs as a visor settles in and finds peers; more direct links mean less relaying and lower latency.",
        more: {
          summary: "Transport types",
          panel: "Four mirror the dmsg carriers exactly — same protocol, dialed to a peer instead of a server:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>stcpr</b> — TCP, with the peer located through the address resolver.</li>' +
            '<li style="margin:.3em 0"><b>ws</b> — WebSocket.</li>' +
            '<li style="margin:.3em 0"><b>webtransport</b> — HTTP/3.</li>' +
            '<li style="margin:.3em 0"><b>quic</b> — QUIC over UDP; carries end-to-end datagrams.</li>' +
            "</ul>" +
            "Two exist only peer-to-peer, since a dmsg server is a fixed public endpoint with nothing to hole-punch to:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>sudph</b> — UDP through a NAT hole-punch, made reliable by KCP.</li>' +
            '<li style="margin:.3em 0"><b>webrtc</b> — browser-to-browser DataChannel, traversed by ICE; dmsg carries only the signaling.</li>' +
            "</ul>" +
            "dmsg is itself a transport — the relayed one."
        }
      },

      {
        route: visorList, sel: "td.label-cell",
        title: "Label and location",
        body: "A <b>label</b> for recognizing a visor at a glance, and its detected IP and country. Both are cosmetic; the public key is the identity. That is the whole row — which visor, how it connects, and where it is."
      },

      // --- inside this visor ------------------------------------------------
      // Entered through #/nodes/local rather than a PK, so that on a hypervisor
      // managing several visors the tour opens the local one. The redirect
      // leaves that PK in the hash and every step after this reads it there.
      {
        route: localVisor,
        sel: "app-node-info-content .info-line", has: "DMSG servers",
        title: "Info",
        body: {
          wasm: "Opening a visor lands on <b>Info</b>: version, uptime, identity, and the <b>DMSG servers</b> line the list summarized — here reached over WSS and WebTransport from the tab.",
          native: "Opening a visor lands on <b>Info</b>: version, uptime, identity, and the <b>DMSG servers</b> line the list summarized — the relay layer up close."
        }
      },

      {
        route: function () { return nodePath("transports"); }, sel: "app-transport-list",
        title: "Transports",
        body: {
          wasm: "One row per direct link. From a browser these are mostly WebTransport and WebRTC; a native visor also builds stcpr, sudph and quic.",
          native: "One row per direct link — stcpr, sudph, dmsg, quic — hole-punched through NATs where necessary, so traffic takes the shortest path rather than always relaying."
        }
      },

      {
        route: function () { return nodePath("routing"); }, sel: "app-route-list",
        title: "Routing",
        body: "The paths traffic takes across those transports. A route is <b>DIRECT</b> (one hop), <b>MULTIHOP</b> (several visors, so no single hop sees both ends), or <b>MULTIPLEXED</b> (parallel paths at once). The route-finder builds them on demand.",
        more: {
          summary: "Hops, and what they cost",
          panel: "A route is an ordered path of transports. One hop is fastest. More hops mean no single intermediary knows both source and destination, paid for in latency. A multiplexed route splits one stream over several paths: a dead leg does not end the stream, and the aggregate beats any single leg."
        }
      },

      {
        route: function () { return nodePath("bandwidth"); }, sel: "app-bandwidth",
        title: "Bandwidth",
        body: "What those routes actually carried, sent and received over time — the quickest way to tell a working transport from a merely connected one."
      },

      {
        route: function () { return nodePath("apps"); }, sel: "app-node-app-list",
        title: "Apps",
        body: {
          wasm: "The apps this visor can run. In a tab the relevant one is <b>skysocks-client-lite</b>, which routes clearnet fetches out through an <b>exit</b> visor. It serves no local port — it exists only inside this tab, and only this tab's own traffic goes through it.",
          native: "Apps start, stop and configure here: the <b>skysocks</b> proxy client and server, the <b>VPN</b> client and server, and <b>skychat</b>. On a native visor these bind real local ports and can serve other machines on the network."
        },
        more: {
          summary: "skysocks-client and skysocks-client-lite",
          panel: "<b>skysocks-client</b> runs as a process and serves a local <b>SOCKS5 port</b> that other programs point at. <b>skysocks-client-lite</b> has neither: it lives in the tab and proxies only what that tab fetches. Both dial an <b>exit</b> visor which performs the clearnet request, so the site sees the exit's address."
        }
      },

      {
        route: function () { return nodePath("uptime"); }, sel: "app-uptime",
        title: "Uptime",
        body: "This visor's record of staying online and reachable, day by day. The reward system pays against that record."
      },

      {
        route: function () { return nodePath("logs"); }, sel: "app-node-logs, app-logs",
        title: "Logs",
        body: {
          wasm: "A live tail of the visor's runtime — dmsg, transports, routing, apps — from the wasm core in this tab.",
          native: "A live tail of the visor's runtime — dmsg, transports, routing, apps — read from the visor process."
        }
      },

      {
        route: function () { return nodePath("settings"); }, sel: "app-node-settings",
        title: "Visor settings",
        body: "One visor's label, reward address and mesh behavior. Hypervisor-wide settings are a separate page, at the end of this tour."
      },

      // --- back out: this hypervisor's cluster ------------------------------
      {
        route: visorList, sel: "app-node-list",
        title: "The cluster",
        body: {
          wasm: "Back at the top level. Every visor attached to this hypervisor appears here — at the moment one, in this tab. A remote visor joins the list when its public key is added; this visor becomes managed elsewhere when a remote hypervisor's key is set in its config.",
          native: "Every visor attached to this hypervisor appears here. A remote visor joins the list when its public key is added; this visor becomes managed elsewhere when a remote hypervisor's key is set in its config. Each one is fully controllable over the mesh."
        },
        more: {
          summary: "Which is the hypervisor",
          panel: "A hypervisor is a visor that also serves this UI and holds the keys of the visors it manages. The relationship is one config entry, in one direction or the other. Management travels the same encrypted mesh as everything else, so a hypervisor in a browser tab can drive a native visor on the other side of the world, and the reverse."
        }
      },

      // --- the whole mesh ---------------------------------------------------
      {
        route: "#/nodes/transports",
        title: "Every transport on the mesh",
        body: "The live edge list the route-finder draws on: every direct link across the whole mesh, with per-type bandwidth. Assembled <b>peer-to-peer over dmsg</b>, not fetched from a web server. On a large deployment this page is heavy and takes a while to settle."
      },

      {
        route: "#/nodes/network", sel: "app-network-view",
        title: "Every visor on the mesh",
        body: "A searchable directory of the whole mesh, with the running count top left. Filters by country, version and transport type; each entry carries its transport mix and uptime. The next page draws the same set as a graph."
      },

      {
        route: "#/nodes/visualizer", sel: "app-network-visualizer",
        title: "The same graph, drawn",
        body: "An interactive geographic render of that directory, in flat, globe and WebGL views."
      },

      {
        route: "#/nodes/services-health", sel: "app-services-health",
        title: "Shared services",
        body: {
          wasm: "Health of the services the mesh depends on: config, dmsg discovery, transport discovery, the route-finder, the address resolver, service discovery. <b>This tab probes each one directly over dmsg</b> — status, version, latency — with the same reach a native visor has.",
          native: "Health of the services the mesh depends on: config, dmsg discovery, transport discovery, the route-finder, the address resolver, service discovery. Each is probed over dmsg, visor to visor."
        }
      },

      {
        route: "#/nodes/uptime", sel: "app-multi-visor-uptime",
        title: "Mesh uptime",
        body: "How consistently every visor has stayed online, over 1, 7 and 30 days — the mesh-wide form of a single visor's uptime tab, and the basis for rewards."
      },

      {
        nativeOnly: true, route: "#/nodes/rewards", sel: "app-node-list",
        title: "Rewards",
        body: "Skycoin paid out to visors that stay online and reachable; a visor appears once it qualifies. The page is hidden on a browser visor, which cannot hold up an uptime record across a closed tab."
      },

      {
        nativeOnly: true, route: "#/nodes/resources", sel: "app-multi-visor-resources",
        title: "Host resources",
        body: "CPU, memory and disk of the machines hosting the managed visors. Also hidden on a browser visor, which has no host to measure."
      },

      {
        route: "#/settings", sel: "app-settings",
        title: "Hypervisor settings",
        body: "The services this deployment points at, the update channel, and how the UI behaves — applying to every visor this hypervisor manages."
      },

      // --- close ------------------------------------------------------------
      {
        title: { wasm: "Run by nobody", native: "Run by nobody" },
        body: {
          wasm: "No account, no server, no address handed down — a global peer-to-peer mesh reachable from a browser tab, and gone when the tab closes unless the key was exported.<br><br>That was the <b>dashboard</b>. The desktop around it — the mesh browser, the shell, files, identity, pairing, mail — is the genuinely unusual part, and it has <b>its own tour</b> in the launcher. This one reopens from the <b>?</b> button.",
          native: "No account, no central server, no address handed down — cryptographic keys and a global peer-to-peer mesh.<br><br>That was the <b>dashboard</b>: the visor list, one visor up close, and the mesh-wide views. It reopens from the <b>?</b> button."
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
    // hypervisor’s own visor and redirects to it, so after that step the hash
    // holds the PK the copy actually means by "your visor".
    //
    // A /nodes/<pk>/ link in the DOM is only a fallback, and a poor one on a
    // hypervisor managing more than one visor: it is whichever row happens to
    // sort first, which is how the first draft of this tour ended up saying
    // "your visor" over somebody else’s. It is kept for the case where the
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
    // the router’s own answer to "which one is mine".
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
      // The dim is the spotlight’s own shadow, so there is exactly one element
      // over the page and the cutout can never drift out of sync with it.
      ".skywire-tour-hl{position:fixed;z-index:2147483646;border-radius:6px;",
      "box-shadow:0 0 0 9999px rgba(0,0,0,.62),0 0 0 2px #0072FF inset;",
      "pointer-events:none;transition:top .18s,left .18s,width .18s,height .18s,opacity .18s;opacity:0}",
      ".skywire-tour-call{position:fixed;z-index:2147483647;width:min(440px,calc(100vw - 20px));",
      "background:#12161c;color:#e8eef7;border:1px solid #2a3442;border-radius:10px;",
      "padding:16px 18px;box-shadow:0 12px 40px rgba(0,0,0,.55);",
      // Lead with the dashboard's own face so the callout looks like part of the
      // app, and keep system-ui late in the list rather than first: whatever it
      // resolves to on this Linux box gives U+0027 the advance width of an "o"
      // (30.7px against 29.9px at 52px), which is what made "you're" read as
      // "you ' re". The prose uses U+2019 now, which is correct typography and
      // measures normally in every face here, but the order is the real fix.
      "font:14px/1.5 Skycoin,Roboto,system-ui,-apple-system,sans-serif;",
      // The dashboard sets letter-spacing and text-transform on headings, so
      // the callout states its own rather than taking whatever the page around
      // it happens to use. (This is not what widened the apostrophe — see the
      // font stack above — but the callout should still not inherit type.)
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

  // Angular’s router navigates with history.pushState, and pushState does NOT
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
