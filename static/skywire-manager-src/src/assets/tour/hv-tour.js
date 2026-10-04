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
 * The words are in hv-tour.md beside this file (see parseTour); a field may
 * differ between the two with "Title (wasm):" / "Body (native):" and so on.
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

  // The words live in hv-tour.md beside this script, so they can be edited
  // without touching code. What stays here is the wiring: the page each step
  // opens and the element it lights up, keyed by the markdown's "## <id>".
  // The markdown also decides the order of the steps.
  var TEXT_URL = (function () {
    var s = document.currentScript;
    return s && s.src ? new URL("hv-tour.md", s.src).href : "assets/tour/hv-tour.md";
  })();

  // sel is a comma list in priority order (see pickTarget); has narrows it to
  // an element whose text contains that string. nativeOnly steps are skipped on
  // a browser visor, which hides those pages.
  function wiring(nodePath, visorList, localVisor) {
    function tab(name) { return function () { return nodePath(name); }; }
    return {
      "visor-list": { route: visorList },
      "key": { route: visorList, sel: "td.key-cell" },
      // The live count cell first, then the always-present header: a browser
      // visor's dmsg sessions can flap to zero, and the cell then renders "-"
      // with no .dmsg-counts, which would skip the step.
      "dmsg": { route: visorList, sel: ".dmsg-counts, th.dmsg-column" },
      "transport-counts": { route: visorList, sel: ".tp-counts, th.transports-column" },
      "label": { route: visorList, sel: "td.label-cell" },
      // Entered through #/nodes/local rather than a PK, so that on a hypervisor
      // managing several visors the tour opens the local one. The redirect
      // leaves that PK in the hash and every step after this reads it there.
      "info": { route: localVisor, sel: "app-node-info-content .info-line", has: "DMSG servers" },
      "transports": { route: tab("transports"), sel: "app-transport-list" },
      "routing": { route: tab("routing"), sel: "app-route-list" },
      "bandwidth": { route: tab("bandwidth"), sel: "app-bandwidth" },
      "apps": { route: tab("apps"), sel: "app-node-app-list" },
      "uptime": { route: tab("uptime"), sel: "app-uptime" },
      "logs": { route: tab("logs"), sel: "app-node-logs, app-logs" },
      "visor-settings": { route: tab("settings"), sel: "app-node-settings" },
      "cluster": { route: visorList, sel: "app-node-list" },
      "mesh-transports": { route: "#/nodes/transports" },
      "mesh-visors": { route: "#/nodes/network", sel: "app-network-view" },
      "visualizer": { route: "#/nodes/visualizer", sel: "app-network-visualizer" },
      "services": { route: "#/nodes/services-health", sel: "app-services-health" },
      "mesh-uptime": { route: "#/nodes/uptime", sel: "app-multi-visor-uptime" },
      "rewards": { nativeOnly: true, route: "#/nodes/rewards", sel: "app-node-list" },
      "resources": { nativeOnly: true, route: "#/nodes/resources", sel: "app-multi-visor-resources" },
      "hv-settings": { route: "#/settings", sel: "app-settings" },
      "close": {}
    };
  }

  // mdInline renders `code`, **bold** and *italic*. Code spans are escaped and
  // set aside first so their contents are left alone; everything else may also
  // carry plain HTML.
  function mdInline(s) {
    var codes = [];
    s = s.replace(/`([^`]+)`/g, function (_, c) {
      codes.push("<code>" + c.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;") + "</code>");
      return "\u0000" + (codes.length - 1) + "\u0000";
    });
    s = s.replace(/\*\*([^*]+)\*\*/g, "<b>$1</b>").replace(/\*([^*]+)\*/g, "<i>$1</i>");
    return s.replace(/\u0000(\d+)\u0000/g, function (_, n) { return codes[+n]; });
  }

  // mdBlock renders a field: blank lines separate paragraphs, and a run of
  // lines starting with "- " is a list. Lines inside a paragraph join with a space.
  var UL = '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">';
  var LI = '<li style="margin:.3em 0">';
  function mdBlock(text) {
    var out = "", para = [], items = [], prev = "";
    function flush() {
      if (para.length) {
        if (prev === "p") { out += "<br><br>"; }
        out += mdInline(para.join(" "));
        para = []; prev = "p";
      }
      if (items.length) {
        out += UL + items.map(function (i) { return LI + mdInline(i) + "</li>"; }).join("") + "</ul>";
        items = []; prev = "ul";
      }
    }
    text.split("\n").forEach(function (line) {
      var t = line.trim();
      if (!t) { flush(); return; }
      if (t.indexOf("- ") === 0) {
        if (para.length) { flush(); }
        items.push(t.slice(2));
        return;
      }
      if (items.length) { flush(); }
      para.push(t);
    });
    flush();
    return out;
  }

  // parseTour reads hv-tour.md into steps of the shape the engine renders:
  // title and body as {wasm, native, both}, more as {summary, panel}, disc as
  // {summary, details}. Anything before the first "## " is the file's own notes.
  function parseTour(md) {
    var steps = [], step = null, field = null;
    var fieldRE = /^(Title|Body)(?: \((wasm|native)\))?:\s*(.*)$/;
    var panelRE = /^(More|Notice):\s*(.+)$/;
    function close() {
      if (!field) { return; }
      var text = field.lines.join("\n");
      if (field.kind === "Title") { step.title[field.mode] = mdInline(text.trim()); }
      if (field.kind === "Body") { step.body[field.mode] = mdBlock(text); }
      if (field.kind === "More") { step.more = { summary: mdInline(field.label), panel: mdBlock(text) }; }
      if (field.kind === "Notice") { step.disc = { summary: mdInline(field.label), details: mdBlock(text) }; }
      field = null;
    }
    md.split("\n").forEach(function (line) {
      var h = line.match(/^## (\S+)\s*$/);
      if (h) {
        close();
        step = { id: h[1], title: {}, body: {} };
        steps.push(step);
        return;
      }
      if (!step) { return; }
      var m = line.match(fieldRE);
      if (m) {
        close();
        field = { kind: m[1], mode: m[2] || "both", lines: m[3] ? [m[3]] : [] };
        return;
      }
      var p = line.match(panelRE);
      if (p) {
        close();
        field = { kind: p[1] === "More" ? "More" : "Notice", label: p[2], lines: [] };
        return;
      }
      if (field) { field.lines.push(line); }
    });
    close();
    return steps;
  }

  // buildSteps joins the text to its wiring. A step id with no wiring is an
  // error in one file or the other, and is reported rather than shown unlit.
  function buildSteps(text, nodePath, visorList, localVisor) {
    var wire = wiring(nodePath, visorList, localVisor);
    var out = [];
    parseTour(text).forEach(function (s) {
      var w = wire[s.id];
      if (!w) { console.error("hv-tour: step '" + s.id + "' in hv-tour.md has no wiring in hv-tour.js"); return; }
      Object.keys(w).forEach(function (k) { s[k] = w[k]; });
      out.push(s);
    });
    return out;
  }

  var tourText = null;
  function loadText() {
    if (tourText !== null) { return Promise.resolve(tourText); }
    return fetch(TEXT_URL).then(function (r) {
      if (!r.ok) { throw new Error("HTTP " + r.status); }
      return r.text();
    }).then(function (t) { tourText = t; return t; });
  }

  // ------------------------------------------------------------------ engine

  function startTour(doc) {
    doc = doc || document;
    if (running || doc.getElementById(HL_ID)) { return; }
    running = true;
    loadText().then(function (text) { runTour(doc, text); }, function (err) {
      running = false;
      console.error("hv-tour: could not load " + TEXT_URL + ": " + err.message);
    });
  }

  function runTour(doc, text) {
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

    var steps = buildSteps(text, nodePath, visorList, localVisor).filter(function (s) {
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
