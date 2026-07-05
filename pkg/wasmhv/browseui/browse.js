// browse.js — the dmsg virtual-browser engine, shared by the wasm-visor dev
// harness (cmd/wasm-visor/index.html loads a copy of this) and the standalone
// hypervisor overlay (embedded + injected by the generator in visor mode).
//
// It renders a page fetched over dmsg into a SANDBOXED iframe and routes the
// whole site over dmsg with no DNS, no IP, no CA:
//   - same-site subresources (stylesheets/images/scripts) are fetched over dmsg
//     and inlined (stylesheets as <style>, the rest as base64 data: URIs);
//   - url() assets inside CSS (background-images/fonts/@import) are rewritten to
//     data: URIs fetched over dmsg;
//   - same-site link clicks navigate over dmsg (a shim relays them to the parent);
//   - the page's OWN window.fetch is overridden so its same-site requests are
//     relayed to the parent and fetched over dmsg (the app-level equivalent of a
//     SOCKS5 proxy for the iframe).
// External http(s)/scheme URLs are left to the browser untouched.
//
// All fetching goes through an injected fetchDmsg(pkHost, method, path, body) that
// returns {status, body:Uint8Array, headers} — i.e. globalThis.skywireVisor.fetchDmsg.
(function () {
  // --- log capture ----------------------------------------------------------
  // Mirror console output into a ring buffer so the in-page LOG WINDOW can show
  // the visor's live log (a wasm visor logs to console) WITHOUT browser devtools
  // — the "logging accessibility" gap, especially in standalone PWA mode. Wrapped
  // once (guarded); the originals still fire, so devtools is unaffected. Exposed
  // as window.skywireLog for other scripts / the console.
  var LOGBUF = window.skywireLog || (function () {
    var MAX = 5000, buf = [], subs = [];
    function fmt(a) {
      if (typeof a === "string") return a;
      try { return JSON.stringify(a); } catch (_) { return String(a); }
    }
    return {
      all: function () { return buf; },
      clear: function () { buf.length = 0; },
      subscribe: function (fn) { subs.push(fn); return function () { var i = subs.indexOf(fn); if (i >= 0) subs.splice(i, 1); }; },
      emit: function (level, args) {
        var text; try { text = Array.prototype.map.call(args, fmt).join(" "); } catch (_) { text = "[unprintable]"; }
        var line = { t: Date.now(), level: level, text: text };
        buf.push(line); if (buf.length > MAX) buf.splice(0, buf.length - MAX);
        for (var i = 0; i < subs.length; i++) { try { subs[i](line); } catch (_) {} }
      }
    };
  })();
  if (!window.__skywireLogCaptured) {
    window.__skywireLogCaptured = true;
    window.skywireLog = LOGBUF;
    ["log", "info", "warn", "error", "debug"].forEach(function (lvl) {
      var orig = console[lvl] ? console[lvl].bind(console) : function () {};
      console[lvl] = function () { LOGBUF.emit(lvl, arguments); orig.apply(null, arguments); };
    });
    window.addEventListener("error", function (e) { LOGBUF.emit("error", ["[window.error] " + (e.message || e.error || e)]); });
    window.addEventListener("unhandledrejection", function (e) { LOGBUF.emit("error", ["[unhandledrejection] " + ((e.reason && (e.reason.stack || e.reason.message)) || e.reason)]); });
  }

  function sameSite(u) {
    return !!u && u.charAt(0) !== "#" && !/^https?:\/\//i.test(u) && !/^\/\//.test(u) && !/^[a-z][a-z0-9+.-]*:/i.test(u);
  }
  function resolvePath(href, base) {
    try { var u = new URL(href, "http://dmsg" + base); return u.pathname + u.search; } catch (e) { return href; }
  }
  // Guess a MIME from a path extension (fallback when dmsg omits Content-Type).
  function mimeOf(path) {
    var m = (path.split("?")[0].match(/\.([a-z0-9]+)$/i) || [, ""])[1].toLowerCase();
    return { css: "text/css", js: "text/javascript", mjs: "text/javascript", json: "application/json",
      png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", svg: "image/svg+xml",
      webp: "image/webp", ico: "image/x-icon", woff: "font/woff", woff2: "font/woff2", ttf: "font/ttf",
      wasm: "application/wasm", // WebAssembly.instantiateStreaming requires this exact type
      html: "text/html" }[m] || "application/octet-stream";
  }
  function bytesToB64(bytes) {
    var s = "", C = 0x8000;
    for (var i = 0; i < bytes.length; i += C) s += String.fromCharCode.apply(null, bytes.subarray(i, i + C));
    return btoa(s);
  }
  // Module-scope HTML escaper (createBrowser has its own local copy; this one is
  // for tool windows like createHostWindow that live outside createBrowser).
  function esc(s) { return String(s == null ? "" : s).replace(/[<>&"]/g, function (c) { return { "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;" }[c]; }); }
  function ctOf(headers, path) {
    var ct = headers && (headers["Content-Type"] || headers["content-type"]);
    return (ct || mimeOf(path)).split(";")[0].trim();
  }

  // ── visor identity ────────────────────────────────────────────────────────
  // The served wasm-visor persists its 32-byte secret key (and therefore its PK)
  // in localStorage under this key (hv-boot.js SK_KEY). We read/write the SAME
  // slot so the identity dialog can export it (backup / move the visor) or import
  // another — without coupling to the boot path. ID_KEY MUST match hv-boot.js.
  var ID_KEY = "skywire-visor-sk";
  function idLoad() { try { return localStorage.getItem(ID_KEY) || ""; } catch (e) { return ""; } }
  function idStore(hex) { try { localStorage.setItem(ID_KEY, hex); } catch (e) {} }
  function idClear() { try { localStorage.removeItem(ID_KEY); } catch (e) {} }
  // parseSK accepts a bare 64-hex secret key OR an exported {"sk":"…"} bundle.
  function parseSK(input) {
    var s = (input || "").trim();
    if (/^[0-9a-fA-F]{64}$/.test(s)) return s.toLowerCase();
    try { var o = JSON.parse(s); if (o && typeof o.sk === "string" && /^[0-9a-fA-F]{64}$/.test(o.sk.trim())) return o.sk.trim().toLowerCase(); } catch (e) {}
    return "";
  }

  // navShimSrc is JS injected (as a <script> built via DOM, so no </script> in a
  // string) at the top of the browsed iframe's <head>: (1) same-site link clicks
  // → postMessage a nav request to the parent; (2) window.fetch override → relay
  // the page's own requests to the parent — same-site over dmsg, cross-origin
  // (clearnet) through the skysocks-lite upstream proxy (gated by cnMode). cnMode
  // is the clearnet policy at render time ("block"|"direct"|"proxy").
  function navShimSrc(path, cnMode) {
    return (
      'var cur=' + JSON.stringify(path) + ';' +
      'function pathOf(h){try{var u=new URL(h,"http://dmsg"+cur);return u.pathname+u.search;}catch(e){return h;}}' +
      'function same(u){return !!u&&u.charAt(0)!=="#"&&!/^https?:\\/\\//i.test(u)&&!/^\\/\\//.test(u)&&!/^[a-z][a-z0-9+.-]*:/i.test(u);}' +
      'document.addEventListener("click",function(e){' +
      'var a=e.target.closest?e.target.closest("a[href]"):null;if(!a)return;' +
      'var h=a.getAttribute("href")||"";if(!same(h))return;' +
      'e.preventDefault();parent.postMessage({type:"dmsgnav",path:pathOf(h)},"*");' +
      '},true);' +
      'var _rq=0,_pend={};' +
      'window.addEventListener("message",function(e){var d=e.data||{};if(d.type!=="dmsgreply")return;var p=_pend[d.id];if(!p)return;delete _pend[d.id];p(d);});' +
      'function relay(t,m,b,cn){return new Promise(function(res){var id=++_rq;_pend[id]=res;parent.postMessage({type:"dmsgreq",id:id,path:t,method:m||"GET",body:b||null,clearnet:!!cn},"*");});}' +
      'function _toResp(r){var body=r.body?Uint8Array.from(atob(r.body),function(c){return c.charCodeAt(0);}):new Uint8Array();return new Response(body,{status:r.status||200,headers:{"Content-Type":r.ct||"application/octet-stream"}});}' +
      'var CNMODE=' + JSON.stringify(cnMode || "block") + ';' +
      'var _f=window.fetch;window.fetch=function(input,init){var u=typeof input==="string"?input:(input&&input.url);var m=(init&&init.method)||"GET",b=(init&&init.body)||null;' +
      'if(same(u))return relay(pathOf(u),m,b,false).then(_toResp);' +           // same-site → dmsg
      'if(!/^https?:\\/\\//i.test(u||""))return _f.apply(this,arguments);' +    // data:/blob:/etc → real
      'if(CNMODE==="direct")return _f.apply(this,arguments);' +                 // direct mode → real (CSP off)
      'if(CNMODE!=="proxy")return Promise.resolve(new Response("clearnet blocked: set an upstream proxy",{status:403}));' +
      'return relay(u,m,b,true).then(_toResp);};' +                            // proxy → skysocks-lite via parent
      // (3) lazy image loader: images are NOT inlined into the srcdoc (that bloats
      // a catalog page to tens of MB). Each carries a data-dmsg-src; fetch it over
      // dmsg via the relay only when it scrolls near the viewport, then swap in a
      // data: URL. A MutationObserver picks up images the page adds dynamically.
      'function _limg(el){var p=el.getAttribute("data-dmsg-src");if(!p)return;el.removeAttribute("data-dmsg-src");' +
      'relay(pathOf(p),"GET",null).then(function(r){if(r&&r.body&&(r.status||200)<400)el.src="data:"+(r.ct||"application/octet-stream")+";base64,"+r.body;}).catch(function(){});}' +
      'var _lio=("IntersectionObserver"in window)?new IntersectionObserver(function(es){es.forEach(function(e){if(e.isIntersecting){_lio.unobserve(e.target);_limg(e.target);}});},{rootMargin:"400px"}):null;' +
      'function _lobs(el){if(_lio)_lio.observe(el);else _limg(el);}' +
      'function _lscan(root){var ns=(root&&root.querySelectorAll)?root.querySelectorAll("img[data-dmsg-src]"):[];for(var i=0;i<ns.length;i++)_lobs(ns[i]);}' +
      'document.addEventListener("DOMContentLoaded",function(){_lscan(document);' +
      'new MutationObserver(function(ms){ms.forEach(function(m){if(!m.addedNodes)return;for(var i=0;i<m.addedNodes.length;i++){var n=m.addedNodes[i];if(n.nodeType!==1)continue;if(n.matches&&n.matches("img[data-dmsg-src]"))_lobs(n);_lscan(n);}});}).observe(document.documentElement,{childList:true,subtree:true});});'
    );
  }

  // createBrowser drives one iframe against one "current site". opts:
  //   frame      — the <iframe> element to render into (sandbox allow-scripts).
  //   fetchDmsg  — (pkHost, method, path, body) => Promise<{status,body,headers}>.
  //   log        — optional (msg) => void.
  //   setPK/setPath — optional callbacks to reflect the current pk/path into a UI.
  // Returns { renderSite, browseTo, currentPK() }.
  function createBrowser(opts) {
    var frame = opts.frame;
    var fetchDmsg = opts.fetchDmsg || function () { return globalThis.skywireVisor.fetchDmsg.apply(null, arguments); };
    // Per-window id so this window's clearnet requests get their OWN
    // skysocks-lite session/route (the Go side keys sessions by winId+exit).
    var winId = opts.winId || ("w" + (globalThis.__skywireBrowserSeq = (globalThis.__skywireBrowserSeq || 0) + 1));
    // fetchClearnet(exitPK, method, url, body) → {status, body, headers}: a CLEARNET
    // fetch tunneled through a skysocks exit over a skywire route (IP-anonymous).
    // We wrap it to append winId as the 5th arg for every call site.
    var rawFetchClearnet = opts.fetchClearnet || function () { return globalThis.skywireVisor.fetchClearnet.apply(null, arguments); };
    var fetchClearnet = function (exit, m, u, b) { return rawFetchClearnet(exit, m, u, b, winId); };
    var log = opts.log || function () {};
    var currentSitePK = "";

    // 1x1 transparent GIF — placeholder src for a deferred (lazy) image so it
    // occupies layout without a broken-image flash until the real bytes arrive.
    var BLANK_IMG = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";
    var CSS_URL = /url\(\s*(['"]?)([^'")]+)\1\s*\)/gi;
    function inlineCss(pk, base, css) {
      var uniq = [...new Set([...css.matchAll(CSS_URL)].map(function (m) { return m[2]; }).filter(sameSite))];
      if (!uniq.length) return Promise.resolve(css);
      var map = {};
      return Promise.all(uniq.map(function (u) {
        return fetchDmsg(pk, "GET", resolvePath(u, base), null)
          .then(function (r) { map[u] = "data:" + ctOf(r.headers, u) + ";base64," + bytesToB64(r.body); })
          .catch(function () {});
      })).then(function () {
        return css.replace(CSS_URL, function (m, q, u) { return map[u] ? 'url("' + map[u] + '")' : m; });
      });
    }

    async function renderSite(pk, path, html, scheme) {
      currentSitePK = pk;
      // Preserve the scheme the user navigated with (http/https) — the fetch is
      // over dmsg either way (Noise-encrypted, no in-tab TLS), so the scheme is
      // cosmetic, but echoing back what was entered avoids a surprising rewrite.
      if (opts.setAddr) opts.setAddr((scheme || "http") + "://" + pk + (path || "/"));
      var docHtml;
      try {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var head = doc.head || doc.documentElement;
        var base = doc.createElement("base"); base.setAttribute("target", "_self");
        var sc = doc.createElement("script"); sc.textContent = navShimSrc(path, clearnetPolicy().mode);
        head.insertBefore(sc, head.firstChild);
        head.insertBefore(base, head.firstChild);
        // Strict CSP catch-all unless the window is in DIRECT clearnet mode (where
        // loading clearnet resources directly is the explicit intent).
        if (clearnetPolicy().mode !== "direct") applyCSP(doc);

        var jobs = [];
        doc.querySelectorAll("link[rel~='stylesheet'][href]").forEach(function (el) {
          var href = el.getAttribute("href");
          if (!sameSite(href)) return;
          var cssPath = resolvePath(href, path);
          jobs.push(fetchDmsg(pk, "GET", cssPath, null)
            .then(function (r) { return inlineCss(pk, cssPath, new TextDecoder().decode(r.body)); })
            .then(function (css) { var style = doc.createElement("style"); style.textContent = css; el.replaceWith(style); })
            .catch(function () {}));
        });
        doc.querySelectorAll("style").forEach(function (el) {
          if (!/url\(/i.test(el.textContent || "")) return;
          jobs.push(inlineCss(pk, path, el.textContent).then(function (css) { el.textContent = css; }).catch(function () {}));
        });
        // Scripts + <source> are inlined eagerly (a script must exist before it
        // runs; a <picture>/<video> source is chosen at parse time and can't be
        // swapped afterward).
        doc.querySelectorAll("script[src],source[src]").forEach(function (el) {
          var src = el.getAttribute("src");
          if (!sameSite(src)) return;
          jobs.push(fetchDmsg(pk, "GET", resolvePath(src, path), null)
            .then(function (r) { el.setAttribute("src", "data:" + ctOf(r.headers, src) + ";base64," + bytesToB64(r.body)); })
            .catch(function () {}));
        });
        // Images are deferred, NOT inlined — a media-heavy catalog would bloat the
        // srcdoc to tens of MB and stall the render. Rewrite each to a data-dmsg-src
        // the injected lazy-loader fetches over dmsg on scroll; a transparent
        // placeholder keeps layout stable, and srcset is dropped so the browser
        // can't try to load a non-rewritten (CSP-blocked) candidate.
        doc.querySelectorAll("img[src]").forEach(function (el) {
          var src = el.getAttribute("src");
          if (!sameSite(src)) return;
          el.setAttribute("data-dmsg-src", resolvePath(src, path));
          el.removeAttribute("srcset");
          el.setAttribute("src", BLANK_IMG);
        });
        // A dmsg site may reference CLEARNET (http/https) sub-resources — gate them
        // by the upstream-proxy policy so they can't silently leak the user's IP
        // (block: stripped; direct: left for the iframe; proxy: fetched + inlined).
        jobs = jobs.concat(gateAllClearnet(doc, "http://dmsg" + path, clearnetPolicy(), false));
        await Promise.all(jobs);
        docHtml = "<!doctype html>" + doc.documentElement.outerHTML;
      } catch (e) {
        docHtml = html; // parse failed → render the raw HTML
      }
      frame.srcdoc = docHtml;
    }

    // --- navigation history (back / forward / reload / cancel) ---
    // hist is a stack of entries: {kind:'dmsg', pk, path} | {kind:'clearnet', url}.
    // histIdx points at the current entry. loadGen is bumped on every navigation
    // and on cancel, so a slow fetch that resolves after the user navigated away
    // (or cancelled) is discarded instead of clobbering the view ("cancel load").
    var hist = [], histIdx = -1, loadGen = 0;
    function setLoading(on) { if (opts.onLoading) try { opts.onLoading(on); } catch (e) {} }
    function setNavState() { if (opts.onNavState) try { opts.onNavState(histIdx > 0, histIdx < hist.length - 1); } catch (e) {} }

    // render performs the fetch + render for one history entry, tagged with the
    // current loadGen; a stale result (gen advanced) is dropped.
    async function render(entry) {
      var gen = ++loadGen;
      setLoading(true);
      try {
        if (entry.kind === "clearnet") {
          var rc = await fetchClearnetEntry(entry.url, gen);
          return rc;
        }
        var r = await fetchDmsg(entry.pk, "GET", entry.path, null);
        if (gen !== loadGen) return { status: 0, cancelled: true };
        // HTTP error with no body → a browser-style status page (with a body, the
        // site's own error page renders, exactly like a normal browser).
        if (r.status >= 400 && (!r.body || r.body.length === 0)) {
          showError("HTTP " + r.status, "dmsg://" + entry.pk + entry.path, "");
          return { status: r.status };
        }
        var html = new TextDecoder().decode(r.body);
        await renderSite(entry.pk, entry.path, html, entry.scheme);
        if (gen !== loadGen) return { status: 0, cancelled: true };
        log("browsed dmsg://" + entry.pk + entry.path + " → " + r.status + " (" + r.body.length + " bytes)");
        return { status: r.status, bytes: r.body.length, html: html };
      } catch (e) {
        if (gen !== loadGen) return { status: 0, cancelled: true };
        var msg = String((e && e.message) || e);
        log("browse error: " + msg);
        // network error / timeout / no response → a browser-style error page.
        showError("Couldn't reach this site", entry.kind === "clearnet" ? entry.url : ("dmsg://" + entry.pk + (entry.path || "")), msg);
        return { status: 0, error: msg };
      } finally {
        if (gen === loadGen) setLoading(false);
      }
    }

    async function fetchClearnetEntry(url, gen) {
      var pol = clearnetPolicy();
      if (pol.mode === "block") {
        if (gen !== loadGen) return { status: 0, cancelled: true };
        currentSitePK = ""; if (opts.setAddr) opts.setAddr(url);
        frame.srcdoc = blockedPage(url);
        log("clearnet BLOCKED (no upstream proxy): " + url);
        return { status: 0, blocked: true };
      }
      if (pol.mode === "direct") {
        if (gen !== loadGen) return { status: 0, cancelled: true };
        currentSitePK = ""; if (opts.setAddr) opts.setAddr(url);
        frame.removeAttribute("srcdoc");
        frame.src = url; // browser/visor loads it directly (non-anonymous, no skysocks hop)
        log("clearnet DIRECT (upstream = local visor): " + url);
        return { status: 0, direct: true };
      }
      var r = await fetchClearnet(pol.exit, "GET", url, null);
      if (gen !== loadGen) return { status: 0, cancelled: true };
      if (r.status >= 400 && (!r.body || r.body.length === 0)) {
        showError("HTTP " + r.status, url, "via skysocks " + pol.exit.slice(0, 8));
        return { status: r.status };
      }
      var html = new TextDecoder().decode(r.body);
      await renderClearnet(pol.exit, url, html);
      if (gen !== loadGen) return { status: 0, cancelled: true };
      log("browsed " + url + " via skysocks " + pol.exit.slice(0, 8) + " → " + r.status + " (" + r.body.length + " bytes)");
      return { status: r.status, bytes: r.body.length };
    }

    // navigate pushes a new entry (truncating any forward history) and renders it.
    function navigate(entry) {
      hist = hist.slice(0, histIdx + 1);
      hist.push(entry);
      histIdx = hist.length - 1;
      setNavState();
      return render(entry);
    }
    function back() { if (histIdx > 0) { histIdx--; setNavState(); return render(hist[histIdx]); } }
    function forward() { if (histIdx < hist.length - 1) { histIdx++; setNavState(); return render(hist[histIdx]); } }
    function reload() { if (histIdx >= 0) return render(hist[histIdx]); }
    // cancel: advance loadGen so the in-flight render is discarded, and clear the
    // loading state. The underlying fetch may still complete but its result is
    // dropped (skywireVisor fetches aren't AbortController-wired).
    function cancel() { loadGen++; setLoading(false); }

    async function browseTo(pk, path, scheme) {
      pk = (pk || "").trim();
      path = path || "/";
      if (!pk) { log("browse: enter a site PK"); return { status: 0 }; }
      // Inherit the current site's scheme for in-site link clicks (which don't
      // carry one), so navigating within an https:// site stays https://.
      if (!scheme) { var cur = hist[histIdx]; scheme = (cur && cur.kind === "dmsg" && cur.scheme) || "http"; }
      return navigate({ kind: "dmsg", pk: pk, path: path, scheme: scheme });
    }

    // --- CLEARNET upstream-proxy policy ---
    //
    // Clearnet egress is GATED behind an explicit upstream proxy, so a dmsg/skynet
    // site (or a clearnet page) can never silently pull a clearnet resource and
    // leak the user's IP. The upstream is a visor PK (per-window, defaulting to a
    // persisted global), interpreted as:
    //   ""            → BLOCK: no clearnet egress at all (the safe default).
    //   <local-PK>    → DIRECT: short-circuit straight to clearnet (no skysocks/
    //                    self-transport hop) — the browser/visor does the egress.
    //   <other-PK>    → PROXY: tunnel through that visor's skysocks exit
    //                    (fetchClearnet), IP-anonymous.
    var winUpstream = null; // per-window override; null → fall back to the global
    function globalUpstream() { try { return localStorage.getItem("skywire-upstream-proxy") || ""; } catch (_) { return ""; } }
    function upstream() { return (winUpstream !== null ? winUpstream : globalUpstream()).trim(); }
    function setUpstream(pk) { winUpstream = (pk || "").trim(); try { localStorage.setItem("skywire-upstream-proxy", winUpstream); } catch (_) {} }
    function localPK() { try { return ((opts.selfPK && opts.selfPK()) || "").trim(); } catch (_) { return ""; } }
    // clearnetPolicy: {mode:'block'} | {mode:'direct'} | {mode:'proxy', exit}.
    function clearnetPolicy() {
      var up = upstream();
      if (!up) return { mode: "block" };
      if (up === localPK()) {
        // Local-visor upstream. With a backend that can egress clearnet (the native
        // HV UI, where the visor http.Gets directly), route through it as a
        // proxy-with-self-exit so the visor fetches and we INLINE the result — which
        // (unlike a browser-direct iframe load) isn't blocked by the target site's
        // X-Frame-Options. Without such a backend (a pure browser/wasm tab, which
        // can't read cross-origin), fall back to a direct iframe load.
        return opts.directViaBackend ? { mode: "proxy", exit: up } : { mode: "direct" };
      }
      return { mode: "proxy", exit: up };
    }

    // gateClearnetResource applies the policy to ONE element referencing a clearnet
    // (http/https) URL inside a rendered doc. block → strip it (so the iframe can't
    // load it); direct → leave it (the iframe loads it directly); proxy → re-fetch
    // through the exit and inline as a data: URI. Returns a job promise (or null).
    function gateClearnetResource(doc, el, attr, absURL, policy) {
      if (policy.mode === "direct") return null; // leave for the iframe to load
      if (policy.mode === "block") {
        if (el.tagName === "SCRIPT" || el.tagName === "LINK") el.remove(); else el.removeAttribute(attr);
        return null;
      }
      // proxy: clearnet scripts are dropped (static render); css/img/media inlined.
      if (el.tagName === "SCRIPT") { el.remove(); return null; }
      return fetchClearnet(policy.exit, "GET", absURL, null).then(function (r) {
        if (el.tagName === "LINK") { var s = doc.createElement("style"); s.textContent = new TextDecoder().decode(r.body); el.replaceWith(s); }
        else el.setAttribute(attr, "data:" + ctOf(r.headers, absURL) + ";base64," + bytesToB64(r.body));
      }).catch(function () { if (el.tagName === "LINK") el.remove(); else el.removeAttribute(attr); });
    }

    // gateAllClearnet walks every resource element and applies the policy to those
    // whose URL is clearnet (http/https). resolveRelative=true (a clearnet page)
    // resolves relative URLs against baseURL; false (a dmsg site) gates ONLY hrefs
    // that are themselves absolute http(s):// — relative URLs there are same-site
    // dmsg, left to the caller's own inliner. Returns the jobs to await.
    function gateAllClearnet(doc, baseURL, policy, resolveRelative) {
      function absC(href) {
        if (!href) return null; href = href.trim();
        if (resolveRelative) { try { var u = new URL(href, baseURL); return /^https?:$/i.test(u.protocol) ? u.href : null; } catch (e) { return null; } }
        if (!/^https?:\/\//i.test(href)) return null;
        try { return new URL(href).href; } catch (e) { return null; }
      }
      var jobs = [];
      doc.querySelectorAll("link[rel~='stylesheet'][href]").forEach(function (el) { var a = absC(el.getAttribute("href")); if (a) { var j = gateClearnetResource(doc, el, "href", a, policy); if (j) jobs.push(j); } });
      doc.querySelectorAll("img[src],source[src],script[src],audio[src],video[src]").forEach(function (el) { var a = absC(el.getAttribute("src")); if (a) { var j = gateClearnetResource(doc, el, "src", a, policy); if (j) jobs.push(j); } });
      return jobs;
    }

    // renderClearnet renders a clearnet page fetched in PROXY mode. Every clearnet
    // resource (relative or absolute) is re-fetched through the SAME exit and
    // inlined as a data: URI; scripts are stripped. The sandboxed iframe therefore
    // never reaches clearnet directly — a static, read-mostly, IP-anonymous render.
    async function renderClearnet(exit, url, html) {
      currentSitePK = "";
      if (opts.setAddr) opts.setAddr(url);
      var docHtml;
      try {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var head = doc.head || doc.documentElement;
        var base = doc.createElement("base"); base.setAttribute("href", url); base.setAttribute("target", "_self");
        head.insertBefore(base, head.firstChild);
        applyCSP(doc); // proxy mode: everything is inlined; block any direct egress
        await Promise.all(gateAllClearnet(doc, url, { mode: "proxy", exit: exit }, true));
        docHtml = "<!doctype html>" + doc.documentElement.outerHTML;
      } catch (e) { docHtml = html; }
      frame.srcdoc = docHtml;
    }

    function esc(s) { return String(s == null ? "" : s).replace(/[<>&"]/g, function (c) { return { "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;" }[c]; }); }

    // applyCSP injects a strict Content-Security-Policy that BLOCKS every external
    // (http/https) resource load — only inlined data: URIs (and inline style/script)
    // are allowed, and connect-src 'none' kills fetch/XHR/WebSocket. This is the
    // catch-all behind the per-element gating: even CSS url(...)/@import, inline
    // background-image, <link rel=preload>, beacons, etc. that the element walk
    // doesn't rewrite simply cannot reach clearnet. NOT applied in DIRECT mode
    // (where loading clearnet directly is the explicit intent).
    function applyCSP(doc) {
      var head = doc.head || doc.documentElement;
      var m = doc.createElement("meta");
      m.setAttribute("http-equiv", "Content-Security-Policy");
      m.setAttribute("content",
        "default-src 'none'; img-src data:; media-src data:; font-src data:; " +
        // 'wasm-unsafe-eval' lets a fetched site compile/instantiate its own
        // WebAssembly (many static sites ship a wasm blob) WITHOUT enabling
        // general JS eval(). WASM is sandboxed — it reaches the DOM only through
        // JS glue (already permitted by 'unsafe-inline'), and connect-src 'none'
        // still blocks any network egress, so this opens no new exfil channel.
        "style-src data: 'unsafe-inline'; script-src data: 'unsafe-inline' 'wasm-unsafe-eval'; " +
        "connect-src 'none'; frame-src 'none'; form-action 'none'");
      head.insertBefore(m, head.firstChild);
    }

    // errorPage renders a browser-style failure page into the iframe (network
    // error / timeout / no response / HTTP error with no body), with a retry hint.
    function showError(title, where, detail) {
      frame.removeAttribute("src");
      frame.srcdoc = '<!doctype html><meta charset=utf-8><body style="font:14px/1.6 system-ui,sans-serif;background:#1b1b22;color:#cdd2da;padding:2rem">' +
        '<h2 style="color:#ff8f8f">' + esc(title) + '</h2>' +
        '<p style="opacity:.75;word-break:break-all">' + esc(where) + '</p>' +
        (detail ? '<pre style="color:#e0af68;white-space:pre-wrap;margin:.5em 0">' + esc(detail) + '</pre>' : '') +
        '<p style="opacity:.55">Press ⟳ to retry.</p></body>';
    }

    function blockedPage(url) {
      return '<!doctype html><meta charset=utf-8><body style="font:14px/1.6 system-ui,sans-serif;background:#15131c;color:#cdd2da;padding:2rem">' +
        '<h2 style="color:#ff8f8f">Clearnet blocked</h2>' +
        '<p>No upstream proxy is set, so this window makes no clearnet requests (prevents IP leaks).</p>' +
        '<p style="opacity:.7;word-break:break-all">' + String(url).replace(/[<>&"]/g, "") + '</p>' +
        '<p>Open <b>⚙ proxy</b> and set an upstream: a skysocks server PK (anonymous), or your own visor PK (direct, non-anonymous).</p></body>';
    }

    function browseToClearnet(url) { return navigate({ kind: "clearnet", url: url }); }

    // Relayed from inside the browsed iframe: link clicks (dmsgnav) re-fetch a
    // page; the site's own fetch (dmsgreq) is served over dmsg, bytes posted back.
    window.addEventListener("message", async function (e) {
      var d = e.data || {};
      if (d.type === "dmsgnav" && currentSitePK) { browseTo(currentSitePK, d.path); return; }
      if (d.type === "dmsgreq") {
        try {
          var r;
          if (d.clearnet) {
            // The site fetched a cross-origin (clearnet) URL. Route it through the
            // skysocks-lite upstream exit (IP-anonymous) when one is set; refuse
            // otherwise so a dmsg page can't silently egress to clearnet.
            var pol = clearnetPolicy();
            if (pol.mode !== "proxy") {
              log("clearnet " + (d.method || "GET") + " " + d.path + " → BLOCKED (no upstream)");
              e.source.postMessage({ type: "dmsgreply", id: d.id, status: 403, ct: "text/plain", body: btoa("clearnet blocked: no upstream proxy") }, "*");
              return;
            }
            var ct0 = Date.now();
            r = await fetchClearnet(pol.exit, d.method || "GET", d.path, d.body || null);
            log("clearnet " + (d.method || "GET") + " " + d.path + " via " + pol.exit.slice(0, 8) + "… → " + r.status + " (" + (r.body ? r.body.length : 0) + "B, " + (Date.now() - ct0) + "ms)");
          } else {
            if (!currentSitePK) return;
            var dt0 = Date.now();
            r = await fetchDmsg(currentSitePK, d.method || "GET", d.path, d.body || null);
            log("dmsg " + (d.method || "GET") + " " + d.path + " → " + r.status + " (" + (r.body ? r.body.length : 0) + "B, " + (Date.now() - dt0) + "ms)");
          }
          e.source.postMessage({ type: "dmsgreply", id: d.id, status: r.status, ct: ctOf(r.headers, d.path), body: bytesToB64(r.body) }, "*");
        } catch (err) {
          log("fetch error " + (d.clearnet ? "clearnet " : "dmsg ") + d.path + ": " + String((err && err.message) || err));
          e.source.postMessage({ type: "dmsgreply", id: d.id, status: 502, ct: "text/plain", body: btoa("fetch error") }, "*");
        }
      }
    });

    return {
      renderSite: renderSite, browseTo: browseTo, browseToClearnet: browseToClearnet,
      back: back, forward: forward, reload: reload, cancel: cancel,
      upstream: upstream, setUpstream: setUpstream, winId: winId,
      currentPK: function () { return currentSitePK; }
    };
  }

  // makeWin wraps WinBox (winbox.min.js, vendored) with the mini-desktop
  // defaults: dark skynet chrome, mounted into the panel's root container so the
  // whole desktop can be hidden/shown at once, and a high z-base so windows sit
  // over the dashboard. WinBox supplies all window chrome — drag, resize,
  // minimize, maximize, close, and (for url:) the iframe — so the create*Window
  // helpers only build a body. opts: {title, root, width, height, x, y,
  // mount|url, onclose}.
  // Each new window gets a higher z-index than the last so it opens IN FRONT and
  // is focused — a fixed shared index (the old behaviour) left a freshly-opened
  // window stacked BEHIND the currently-focused one, obstructing it. Base sits
  // above the HV UI but below the always-on taskbar (z 2147483646).
  var _winZ = 2147483000;
  function nextWinZ() {
    _winZ += 1;
    if (_winZ > 2147483640) _winZ = 2147483000; // cap well under the taskbar
    return _winZ;
  }

  function makeWin(doc, opts) {
    var cfg = {
      title: opts.title || "window",
      root: opts.root || (doc.body || doc.documentElement),
      width: opts.width || "70%",
      height: opts.height || "70%",
      background: "#1b1726",
      border: "1",
      index: nextWinZ(),
      // no-full hides WinBox's Fullscreen-API button: "maximize" should fill the
      // area IN-TAB (over the dashboard, below the panel) — not take over the
      // whole screen. The remaining max button stays within the top/bottom
      // boundaries below, so it maximizes in front of the HV UI in the same tab.
      "class": ["skywire-wb", "no-full"]
    };
    // Open centered by default (WinBox otherwise pins new windows at 0,0).
    cfg.x = (opts.x != null) ? opts.x : "center";
    cfg.y = (opts.y != null) ? opts.y : "center";
    // Viewport boundaries: keep the window (drag AND maximize) clear of the
    // panel, so its title bar can never slide behind the bar and become
    // ungrabbable. top/bottom come from the panel's current dock edge.
    if (opts.top != null) cfg.top = opts.top;
    if (opts.bottom != null) cfg.bottom = opts.bottom;
    if (opts.mount) cfg.mount = opts.mount;
    if (opts.url) cfg.url = opts.url;
    if (opts.onclose) cfg.onclose = opts.onclose;
    var wb = new WinBox(cfg);
    // Bring the new window to the front + focus it (WinBox raises the focused
    // window's z within its own stack; combined with the incrementing base above
    // this guarantees a new window is never obscured by an older one).
    try { wb.focus(); } catch (e) {}
    return wb;
  }

  // createWindow builds ONE browse window — a dmsg virtual browser + host/proxy
  // panels — as a WinBox. WinBox draws the title bar, window buttons and resize
  // borders; we supply only the body (nav bar + panels + page iframe). opts.root
  // is the WinBox mount container (so the desktop can be hidden as a unit);
  // onClose runs when the window is closed. Returns {wb, browser, landHome}.
  function createWindow(doc, opts, onClose) {
    var fetchDmsg = opts.fetchDmsg, serveContent = opts.serveContent;
    // The WinBox body: nav bar + collapsible host/proxy panels + the page iframe.
    // No window controls or resize grip here — WinBox draws those.
    var wrap = doc.createElement("div");
    wrap.className = "skywire-browse-window";
    wrap.style.cssText = "position:absolute;inset:0;background:#15131c;color:#cdd2da;font:12px/1.4 monospace;display:flex;flex-direction:column;overflow:hidden";
    wrap.innerHTML =
      '<div class="sbw-bar" style="display:flex;gap:.4em;align-items:center;padding:.5em;background:#1b1726;border-bottom:1px solid #2a2342">' +
      '<button id="sb-back" title="back" disabled style="cursor:pointer">◀</button>' +
      '<button id="sb-fwd" title="forward" disabled style="cursor:pointer">▶</button>' +
      '<button id="sb-reload" title="reload" style="cursor:pointer">⟳</button>' +
      '<button id="sb-home" title="home (home.dmsg)" style="cursor:pointer">⌂</button>' +
      '<input id="sb-addr" placeholder="pk · pk.dmsg · home.dmsg · alias.dmsg · https://site (clearnet via proxy)" autocapitalize="off" autocomplete="off" autocorrect="off" spellcheck="false" style="flex:1;min-width:0;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.4em">' +
      '<button id="sb-go" style="cursor:pointer">go</button>' +
      // Content hosting moved to its own 'host' tool window (top-left ☰ menu).
      '<button id="sb-proxy-t" title="skysocks proxy + request log" style="cursor:pointer">⚙</button>' +
      '<button id="sb-info-t" title="about this browser + its limitations" style="cursor:pointer">ⓘ</button>' +
      '</div>' +
      '<div id="sb-proxy" style="display:none;flex-direction:column;gap:.4em;padding:.5em;background:#1a1726;border-bottom:1px solid #2a2342">' +
      '<div style="display:flex;gap:.4em;align-items:center;flex-wrap:wrap">' +
      '<span title="blank = clearnet blocked; this visor PK = direct (non-anonymous); another visor PK = via its skysocks server (IP-anonymous exit)">skysocks proxy:</span>' +
      '<input id="sb-proxy-pk" placeholder="skysocks PK · own PK (direct) · blank (blocked)" style="flex:1;min-width:140px;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.25em">' +
      '<button id="sb-proxy-self" title="use this visor (direct, non-anonymous)" style="cursor:pointer">self</button>' +
      '<button id="sb-proxy-list-btn" title="pick a public skysocks server from service discovery" style="cursor:pointer">⌄ servers</button>' +
      '<button id="sb-proxy-save" style="cursor:pointer">set</button>' +
      '<button id="sb-proxy-stop" title="stop this window\'s skysocks-lite: release its route + session (re-establishes on the next clearnet request)" style="cursor:pointer">■ stop</button>' +
      '<button id="sb-proxy-dbg" title="stream the wasm visor\'s own detailed [skysocks-lite]/[resolve-proxy] lines to the visor-log window too" style="cursor:pointer">🐞 verbose: off</button>' +
      '<button id="sb-proxy-clear" title="clear this window\'s request log" style="cursor:pointer">clear</button>' +
      '</div>' +
      '<select id="sb-proxy-list" style="display:none;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.3em;font:11px monospace"></select>' +
      // Terminal-like per-window request log: every fetch this browser window makes
      // over the resolving proxy (dmsg) or skysocks-lite (clearnet), + config events.
      '<pre id="sb-proxy-log" title="requests through this window — resolving proxy (dmsg) + skysocks-lite (clearnet)" style="margin:0;height:160px;overflow:auto;background:#0e0c14;color:#a9b1d6;border:1px solid #2a2342;padding:.45em;font:11px/1.45 monospace;white-space:pre-wrap;word-break:break-all"></pre>' +
      '</div>' +
      // About/limitations panel (toggled by ⓘ). The skynet browser is deliberately
      // sandboxed; surface WHY so "my login didn't stick" reads as by-design, not a
      // bug. See docs/skynet-browser.md for the full rationale + the native
      // per-site-origin design that would lift the storage limit.
      '<div id="sb-info-panel" style="display:none;flex-direction:column;gap:.5em;padding:.7em .8em;background:#1a1726;border-bottom:1px solid #2a2342;font:12px/1.5 monospace;color:#cdd2da;max-height:40%;overflow:auto">' +
      '<div style="display:flex;align-items:center;gap:.5em"><b style="color:#9d7cff;font-size:13px">about the skynet browser</b><span style="flex:1"></span><button id="sb-info-x" style="cursor:pointer">×</button></div>' +
      '<div>Fetches sites over <b>dmsg</b> (skynet) — no DNS, no certificate authorities, IP-anonymous. Address bar accepts a visor <b>PK</b>, <b>pk.dmsg</b>, an <b>alias.dmsg</b> (e.g. <b>home.dmsg</b>), or an <b>https://</b> clearnet site (routed through a skysocks exit — set in ⚙).</div>' +
      '<div style="border-top:1px solid #2a2342;padding-top:.5em"><b style="color:#e0af68">Limitations (by design):</b></div>' +
      '<ul style="margin:.1em 0 0;padding-left:1.2em;display:flex;flex-direction:column;gap:.25em">' +
      '<li><b>No persistent storage.</b> Every page runs in a sandboxed frame with an opaque origin — <b>cookies, localStorage and logins do not persist</b>, even across a reload. Each visit is effectively fresh/incognito.</li>' +
      '<li><b>Isolated.</b> Sites cannot read each other\'s data, and cannot read this visor\'s keys/storage.</li>' +
      '<li><b>Scripts limited</b> (sandbox: allow-scripts allow-forms); no plugins, popups, or top-level navigation. Some clearnet sites that require cookies/service-workers may misbehave.</li>' +
      '<li>Per-site persistent storage like a normal browser would need the <b>native desktop</b> app (each site on its own local origin) — not possible in a keyless browser tab.</li>' +
      '</ul>' +
      '</div>' +
      '<iframe id="sb-frame" sandbox="allow-scripts allow-forms" style="flex:1;width:100%;border:0;background:#fff"></iframe>';

    function $(id) { return wrap.querySelector("#" + id); }
    var wb = makeWin(doc, {
      title: "skynet", root: opts.root, top: opts.top, bottom: opts.bottom, width: "74%", height: "80%", mount: wrap,
      onclose: function () {
        // Release this window's skysocks-lite sessions/routes (per-window). browser
        // is hoisted; it exists by the time onclose fires.
        try { if (browser && browser.winId && globalThis.skywireVisor && globalThis.skywireVisor.closeWindow) { globalThis.skywireVisor.closeWindow(browser.winId); } } catch (e) {}
        try { if (browser && browser.winId && globalThis.__skywireBrowserPanes) { delete globalThis.__skywireBrowserPanes[browser.winId]; } } catch (e) {}
        if (onClose) onClose();
      }
    });
    var win = { wb: wb, el: wrap };
    var loading = false;
    // Per-window request log: a small ring buffer rendered as a terminal-like pane
    // in the ⚙ panel, so each browser window shows exactly what went through its
    // resolving proxy / skysocks-lite — instead of a cramped one-line status that
    // forces a trip to the main visor-log window.
    var proxyLog = [];
    var PROXY_LOG_MAX = 400;
    function renderProxyLog() {
      var el = $("sb-proxy-log");
      if (!el) return;
      el.textContent = proxyLog.join("\n");
      el.scrollTop = el.scrollHeight;
    }
    function plog(line) {
      var t = "";
      try { t = new Date().toTimeString().slice(0, 8) + "  "; } catch (e) {}
      proxyLog.push(t + line);
      if (proxyLog.length > PROXY_LOG_MAX) proxyLog.shift();
      renderProxyLog();
    }
    var browser = createBrowser({
      frame: $("sb-frame"), fetchDmsg: fetchDmsg,
      // Thread the clearnet + self-PK providers from the panel opts so the engine
      // is host-agnostic: the wasm visor passes none (they fall back to the
      // skywireVisor.* globals), the native HV UI passes /api/browse-backed ones.
      fetchClearnet: opts.fetchClearnet, selfPK: opts.selfPK, directViaBackend: opts.directViaBackend,
      log: function (m) { try { console.log("[skynet] " + m); } catch (e) {} plog(m); },
      // Reflect the current site into the WinBox title bar.
      setAddr: function (u) { $("sb-addr").value = u; var t = u.replace(/^https?:\/\//, "").slice(0, 18); try { wb.setTitle(t || "skynet"); } catch (e) {} },
      // reflect load state into the reload/cancel button (⟳ idle, ✕ while loading)
      onLoading: function (on) { loading = on; var b = $("sb-reload"); b.textContent = on ? "✕" : "⟳"; b.title = on ? "cancel load" : "reload"; },
      // enable/disable back/forward to match history position
      onNavState: function (canBack, canFwd) { $("sb-back").disabled = !canBack; $("sb-fwd").disabled = !canFwd; }
    });
    win.browser = browser;
    // Register this window's log sink so the wasm visor's skysocks-lite path can
    // push its own connect/route-setup lines (keyed by winId) into THIS window's
    // pane — see emitProxyLog / __skywireProxyLog in cmd/wasm-visor/skysocks_js.go.
    try {
      var paneReg = (globalThis.__skywireBrowserPanes = globalThis.__skywireBrowserPanes || {});
      paneReg[browser.winId] = plog;
      if (!globalThis.__skywireProxyLog) {
        globalThis.__skywireProxyLog = function (winId, line) {
          var p = (globalThis.__skywireBrowserPanes || {})[winId];
          if (p) { try { p(line); } catch (e) {} }
        };
      }
    } catch (e) {}
    // A clearnet http(s):// URL routes through a skysocks exit (IP-anonymous); a
    // bare PK / pk:port is a dmsg/skynet site fetched over dmsg.
    function go() {
      var v = ($("sb-addr").value || "").trim();
      if (!v) return;
      var hadScheme = /^https?:\/\//i.test(v), u;
      try { u = new URL(hadScheme ? v : "http://" + v); } catch (e) { browser.browseTo(v, "/"); return; }
      var host = u.hostname, path = (u.pathname || "/") + (u.search || "");
      // .dmsg/.skynet host, or a bare 66-hex PK → dmsg/skynet site; else clearnet.
      if (/\.(dmsg|skynet)$/i.test(host) || /^[0-9a-f]{66}$/i.test(host)) {
        browser.browseTo(host + (u.port ? ":" + u.port : ""), path, (u.protocol || "http:").replace(":", ""));
      } else {
        browser.browseToClearnet(hadScheme ? v : "https://" + v);
      }
    }
    $("sb-go").onclick = go;
    $("sb-back").onclick = function () { browser.back(); };
    $("sb-fwd").onclick = function () { browser.forward(); };
    // ⟳ reloads the current page; while a load is in flight it becomes ✕ (cancel).
    $("sb-reload").onclick = function () { if (loading) browser.cancel(); else browser.reload(); };
    $("sb-home").onclick = function () { browser.browseTo("home.dmsg", "/"); };
    $("sb-info-t").onclick = function () { var h = $("sb-info-panel"); h.style.display = h.style.display === "none" ? "flex" : "none"; };
    $("sb-info-x").onclick = function () { $("sb-info-panel").style.display = "none"; };
    $("sb-addr").addEventListener("keydown", function (e) { if (e.key === "Enter") go(); });
    // clearnet upstream-proxy settings (per window; persists as the global default).
    $("sb-proxy-pk").value = browser.upstream();
    $("sb-proxy-t").onclick = function () { var h = $("sb-proxy"); h.style.display = h.style.display === "none" ? "flex" : "none"; $("sb-proxy-pk").value = browser.upstream(); };
    $("sb-proxy-self").onclick = function () { var pk = ""; try { pk = (opts.selfPK && opts.selfPK()) || ""; } catch (e) {} $("sb-proxy-pk").value = pk; };
    function saveProxy() {
      browser.setUpstream($("sb-proxy-pk").value);
      var up = browser.upstream(), self = ""; try { self = (opts.selfPK && opts.selfPK()) || ""; } catch (e) {}
      var mode = !up ? "clearnet BLOCKED (no upstream set)"
        : (up === self ? "clearnet DIRECT via self " + up.slice(0, 8) + "… (non-anonymous)"
          : "clearnet via skysocks " + up.slice(0, 8) + "… (IP-anonymous exit)");
      plog("● upstream set → " + mode);
    }
    $("sb-proxy-save").onclick = saveProxy;
    $("sb-proxy-clear").onclick = function () { proxyLog = []; renderProxyLog(); };
    // Populate the skysocks-server dropdown from service discovery (type=proxy),
    // lazily on click (avoids an SD fetch for windows that never open the panel).
    var fdmsg = opts.fetchDmsg || function () { return globalThis.skywireVisor.fetchDmsg.apply(null, arguments); };
    $("sb-proxy-list-btn").onclick = function () {
      var sel = $("sb-proxy-list");
      plog("● fetching skysocks servers from service discovery…");
      Promise.resolve(fdmsg("sd.dmsg", "GET", "/api/services?type=proxy", null)).then(function (r) {
        var list = [];
        try { list = JSON.parse(new TextDecoder().decode(r.body)) || []; } catch (e) {}
        sel.innerHTML = '<option value="">— ' + list.length + ' skysocks servers — pick one —</option>';
        list.forEach(function (s) {
          var pk = String(s.address || "").split(":")[0];
          if (!/^[0-9a-f]{66}$/i.test(pk)) return;
          var geo = (s.geo && s.geo.country) ? " · " + s.geo.country : "";
          var o = doc.createElement("option");
          o.value = pk; o.textContent = pk.slice(0, 8) + "…" + geo + (s.version ? " · " + s.version : "");
          sel.appendChild(o);
        });
        sel.style.display = "";
        plog("● " + list.length + " skysocks server(s) from SD — pick one to set it as the exit");
      }).catch(function (e) { plog("● SD fetch failed: " + String((e && e.message) || e)); });
    };
    $("sb-proxy-list").onchange = function () { if (this.value) { $("sb-proxy-pk").value = this.value; saveProxy(); } };
    // Stop this window's skysocks-lite: release its route + session. The wasm emits
    // a "stopped — released N route/session(s)" line via the per-window hook when a
    // session was active; this immediate line covers the no-active-session case.
    $("sb-proxy-stop").onclick = function () {
      plog("■ stop requested — releasing skysocks-lite route/session for this window");
      try { if (globalThis.skywireVisor && globalThis.skywireVisor.closeWindow) { globalThis.skywireVisor.closeWindow(browser.winId); } } catch (e) {}
    };
    $("sb-proxy-pk").addEventListener("keydown", function (e) { if (e.key === "Enter") saveProxy(); });
    // Verbose request logging for the skysocks-lite + resolving-proxy paths. The
    // flag is currently global to the visor (Phase 1: one log stream in the
    // "visor log" window); per-window logging is a later phase.
    var dbgOn = false;
    $("sb-proxy-dbg").onclick = function () {
      dbgOn = !dbgOn;
      try { if (globalThis.skywireVisor && globalThis.skywireVisor.proxyVerbose) { globalThis.skywireVisor.proxyVerbose(dbgOn); } } catch (e) {}
      this.textContent = "🐞 verbose: " + (dbgOn ? "on" : "off");
      this.style.color = dbgOn ? "#9ece6a" : "";
    };

    // home.dmsg (resolver alias for the deployment landing page), matching the
    // socks5 resolving proxy's default — landed once per window.
    win.landHome = function () {
      if (!wrap.dataset.landed) { wrap.dataset.landed = "1"; browser.browseTo("home.dmsg", "/"); }
    };
    // On a narrow (mobile) viewport, open maximized — a floating window is fiddly
    // to move/resize on a phone; full-screen is the usable default.
    if (((doc.defaultView || window).innerWidth || 9999) < 640) { try { wb.maximize(true); } catch (e) {} }

    return win;
  }

  // openIdentityDialog shows a modal to export / import / reset this visor's
  // identity (the 32-byte secret key in localStorage). opts.selfPK() supplies the
  // PK for display. Self-contained; reads/writes the same slot hv-boot.js uses.
  function openIdentityDialog(doc, opts) {
    var existing = doc.getElementById("skywire-identity-dialog");
    if (existing) { existing.style.display = "flex"; return; }
    var sk = idLoad();
    var pk = ""; try { pk = (opts.selfPK && opts.selfPK()) || ""; } catch (e) {}
    var ov = doc.createElement("div");
    ov.id = "skywire-identity-dialog";
    ov.style.cssText = "position:fixed;inset:0;z-index:2147483647;display:flex;align-items:center;justify-content:center;background:rgba(8,6,16,.62)";
    var box = doc.createElement("div");
    box.style.cssText = "width:min(560px,92vw);max-height:90vh;overflow:auto;background:#15131c;color:#cdd2da;border:1px solid #2a2342;border-radius:10px;box-shadow:0 10px 40px rgba(0,0,0,.6);font:12px/1.5 monospace;padding:1em;box-sizing:border-box";
    box.innerHTML =
      '<div style="display:flex;align-items:center;gap:.5em;margin-bottom:.6em">' +
      '<b style="color:#9d7cff;font-size:14px">visor identity</b><span style="flex:1"></span>' +
      '<button id="id-x" style="cursor:pointer">×</button></div>' +
      '<div style="opacity:.8;margin-bottom:.6em">This visor\'s identity is a 32-byte secret key held in your browser (localStorage). Export it to back up or move this visor; import one to adopt an existing identity. Keep it secret — whoever holds this key <i>is</i> this visor.</div>' +
      '<div style="margin:.3em 0">public key</div>' +
      '<input id="id-pk" readonly value="' + pk + '" style="width:100%;box-sizing:border-box;background:#0e0c14;color:#9ece6a;border:1px solid #2a2342;padding:.35em">' +
      '<div style="margin:.6em 0 .3em">secret key</div>' +
      '<div style="display:flex;gap:.4em"><input id="id-sk" readonly type="password" value="' + sk + '" style="flex:1;min-width:0;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.35em">' +
      '<button id="id-reveal" style="cursor:pointer">reveal</button>' +
      '<button id="id-copy" style="cursor:pointer">copy</button>' +
      '<button id="id-dl" style="cursor:pointer">download</button></div>' +
      (sk ? '' : '<div style="color:#e0af68;margin-top:.4em">No key in localStorage — this visor may use a configured key, so export is unavailable here.</div>') +
      '<hr style="border:0;border-top:1px solid #2a2342;margin:.9em 0">' +
      '<div style="margin:.3em 0">import a 64-hex key or an exported .json (paste or pick a file)</div>' +
      '<textarea id="id-in" rows="3" placeholder=\'paste secret key or {"sk":"…"}\' style="width:100%;box-sizing:border-box;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;font:12px monospace"></textarea>' +
      '<div style="display:flex;gap:.4em;align-items:center;margin-top:.4em"><input id="id-file" type="file" accept=".json,.txt,application/json" style="flex:1;min-width:0;color:#cdd2da;font:11px monospace">' +
      '<button id="id-apply" style="cursor:pointer">import + reload</button></div>' +
      '<div id="id-msg" style="min-height:1.2em;margin-top:.5em"></div>' +
      '<hr style="border:0;border-top:1px solid #2a2342;margin:.9em 0">' +
      '<button id="id-reset" style="cursor:pointer;color:#f7768e;background:transparent;border:1px solid #f7768e;border-radius:5px;padding:.35em .6em">forget this identity (mint a new key on reload)</button>';
    ov.appendChild(box);
    (doc.body || doc.documentElement).appendChild(ov);
    function $(id) { return box.querySelector("#" + id); }
    function close() { ov.style.display = "none"; }
    function msg(t, ok) { var m = $("id-msg"); m.textContent = t; m.style.color = ok ? "#9ece6a" : "#f7768e"; }
    ov.addEventListener("click", function (e) { if (e.target === ov) close(); });
    $("id-x").onclick = close;
    $("id-reveal").onclick = function () { var s = $("id-sk"); var hidden = s.type === "password"; s.type = hidden ? "text" : "password"; $("id-reveal").textContent = hidden ? "hide" : "reveal"; };
    $("id-copy").onclick = function () { if (!sk) { msg("nothing to copy", false); return; } try { navigator.clipboard.writeText(sk); msg("secret key copied to clipboard", true); } catch (e) { msg("copy failed", false); } };
    $("id-dl").onclick = function () {
      if (!sk) { msg("no key to export", false); return; }
      var blob = new Blob([JSON.stringify({ "skywire-wasm-visor": 1, pk: pk, sk: sk }, null, 2)], { type: "application/json" });
      var a = doc.createElement("a"); a.href = URL.createObjectURL(blob);
      a.download = "skywire-visor-" + (pk ? pk.slice(0, 8) : "identity") + ".json";
      (doc.body || doc.documentElement).appendChild(a); a.click(); a.remove();
      setTimeout(function () { try { URL.revokeObjectURL(a.href); } catch (e) {} }, 2000);
      msg("exported identity to a downloaded file", true);
    };
    function applyImport(text) {
      var hex = parseSK(text);
      if (!hex) { msg("not a 64-hex secret key (or {sk:…} bundle)", false); return; }
      idStore(hex);
      msg("identity imported — reloading…", true);
      setTimeout(function () { try { location.reload(); } catch (e) {} }, 600);
    }
    $("id-apply").onclick = function () { applyImport($("id-in").value); };
    $("id-file").onchange = function (e) { var f = e.target.files && e.target.files[0]; if (!f) return; var rd = new FileReader(); rd.onload = function () { $("id-in").value = String(rd.result || ""); msg("file loaded — click import + reload", true); }; rd.readAsText(f); };
    $("id-reset").onclick = function () {
      if (typeof confirm === "function" && !confirm("Forget this visor's key? A new identity (new PK) is minted on reload. Export first if you want to keep it.")) return;
      idClear(); msg("identity forgotten — reloading…", true);
      setTimeout(function () { try { location.reload(); } catch (e) {} }, 600);
    };
  }

  // startTour runs a lightweight, dependency-free guided walkthrough of the HV UI.
  // It dims the page, spotlights each target element (a transparent cutout via a
  // big box-shadow) and shows a callout with Back / Next / Skip. Steps whose target
  // is absent are skipped, so the same tour works on the wasm visor and the native
  // HV UI. The copy leans into WHAT this is (a full mesh visor in a browser tab) —
  // it isn't obvious to a first-time visitor how unusual that is. Reopen any time
  // from the Apps (☰) menu → tour; also offered once on first run (localStorage).
  var TOUR_SEEN_KEY = "skywire-tour-seen";
  function startTour(doc) {
    doc = doc || document;
    if (doc.getElementById("skywire-tour")) { return; }
    var steps = [
      { title: "This is not a normal web page",
        body: "You're running a full <b>Skywire visor</b> — a routing peer on an encrypted peer-to-peer mesh — entirely inside this browser tab. No install, no account, and no central server. A 60-second tour of what that means." },
      { sel: "#tb-menu", title: "Your apps",
        body: "Everything opens from here: a browser for the mesh, encrypted 1:1 chat, a live log, a command console, and your visor's cryptographic identity." },
      { sel: "app-top-bar", title: "The network, live",
        body: "Visor list, rewards, transports, and a live map of the mesh — all fetched <b>peer-to-peer over dmsg</b>, never from a web server. This tab is talking directly to other visors around the world." },
      { sel: ".visor-switcher-row", title: "Every peer is addressable",
        body: "Each chip is a visor on the network, shown by label and public key. Click one to inspect its transports, routes, and apps. There are no usernames here — only keys." },
      { sel: "#skywire-skynet-taskbar", title: "The mesh desktop",
        body: "Tool windows float above the UI. The <b>skynet browser</b> fetches sites over dmsg — anonymous, no DNS, no certificate authorities; sites are addressed by public key (e.g. <code>&lt;pk&gt;.dmsg</code>). Pages run sandboxed and isolated from your keys (the ⓘ button explains)." },
      { sel: "#tb-menu", title: "You are your keys",
        body: "Your visor is a keypair held in this browser (Apps → identity). Export it to back up or move your visor to another device — that key <i>is</i> your identity on the mesh. Whoever holds it is this visor." },
      { title: "A self-hosting internet, in a tab",
        body: "No server. No account. No IP address handed out. Just your browser, cryptographic keys, and a global peer-to-peer mesh — reachable from anywhere, run by nobody. Reopen this tour any time from the Apps (☰) menu → tour. Welcome to Skywire." }
    ];
    var host = doc.body || doc.documentElement;
    var ov = doc.createElement("div");
    ov.id = "skywire-tour";
    ov.style.cssText = "position:fixed;inset:0;z-index:2147483647;font:13px/1.55 system-ui,sans-serif";
    var spot = doc.createElement("div");
    spot.style.cssText = "position:fixed;border-radius:8px;box-shadow:0 0 0 9999px rgba(8,6,16,.74);transition:all .2s ease;pointer-events:none;border:2px solid #9d7cff";
    var call = doc.createElement("div");
    call.style.cssText = "position:fixed;max-width:340px;background:#15131c;color:#cdd2da;border:1px solid #2a2342;border-radius:10px;box-shadow:0 10px 40px rgba(0,0,0,.6);padding:1em 1.1em;box-sizing:border-box";
    ov.appendChild(spot); ov.appendChild(call); host.appendChild(ov);
    var i = 0;
    function done() { try { localStorage.setItem(TOUR_SEEN_KEY, "1"); } catch (e) {} if (ov.parentNode) ov.parentNode.removeChild(ov); }
    // pickTarget returns the first VISIBLE, non-tiny element matching sel (there
    // can be hidden/collapsed duplicates — e.g. a loading vs loaded top-bar — and
    // spotlighting a 1px ghost looks broken).
    function pickTarget(sel) {
      if (!sel) { return null; }
      var cand = doc.querySelectorAll(sel);
      for (var j = 0; j < cand.length; j++) {
        var cr = cand[j].getBoundingClientRect();
        if (cr.width >= 24 && cr.height >= 10 && getComputedStyle(cand[j]).visibility !== "hidden") { return cand[j]; }
      }
      return null;
    }
    function render() {
      var s = steps[i];
      var el = pickTarget(s.sel);
      if (s.sel && !el && i < steps.length - 1) { i++; return render(); } // absent/hidden target → skip
      var last = (i === steps.length - 1);
      call.innerHTML =
        '<div style="display:flex;align-items:center;gap:.5em;margin-bottom:.55em">' +
        '<b style="color:#9d7cff;font-size:14px">' + s.title + '</b><span style="flex:1"></span>' +
        '<span style="opacity:.55;font-size:11px">' + (i + 1) + " / " + steps.length + '</span></div>' +
        '<div style="margin-bottom:.9em">' + s.body + '</div>' +
        '<div style="display:flex;gap:.5em;align-items:center">' +
        '<button id="tour-skip" style="cursor:pointer;background:transparent;color:#8b93a7;border:0">skip</button>' +
        '<span style="flex:1"></span>' +
        (i > 0 ? '<button id="tour-back" style="cursor:pointer;background:#241f33;color:#cdd2da;border:1px solid #2a2342;border-radius:6px;padding:.4em .85em">back</button>' : '') +
        '<button id="tour-next" style="cursor:pointer;background:#9d7cff;color:#0e0c14;border:0;border-radius:6px;padding:.4em 1em;font-weight:600">' + (last ? "done" : "next") + '</button>' +
        '</div>';
      if (el) {
        var r = el.getBoundingClientRect(), pad = 6;
        spot.style.display = "block";
        spot.style.left = (r.left - pad) + "px"; spot.style.top = (r.top - pad) + "px";
        spot.style.width = (r.width + pad * 2) + "px"; spot.style.height = (r.height + pad * 2) + "px";
        try { el.scrollIntoView({ block: "nearest" }); } catch (e) {}
        var vw = (doc.defaultView || window).innerWidth, vh = (doc.defaultView || window).innerHeight, cw = 340;
        call.style.transform = "none";
        call.style.left = Math.min(Math.max(8, r.left), vw - cw - 8) + "px";
        if (r.bottom + 170 < vh) { call.style.top = (r.bottom + 12) + "px"; call.style.bottom = "auto"; }
        else { call.style.bottom = (vh - r.top + 12) + "px"; call.style.top = "auto"; }
      } else {
        spot.style.display = "none";
        call.style.left = "50%"; call.style.top = "50%"; call.style.bottom = "auto";
        call.style.transform = "translate(-50%,-50%)";
      }
      var b;
      if ((b = call.querySelector("#tour-skip"))) b.onclick = done;
      if ((b = call.querySelector("#tour-back"))) b.onclick = function () { if (i > 0) i--; render(); };
      if ((b = call.querySelector("#tour-next"))) b.onclick = function () { if (last) { done(); } else { i++; render(); } };
    }
    render();
  }
  globalThis.skywireStartTour = function () { startTour(document); };

  // mountPanel builds a multi-window "skynet" desktop into `doc`: a bottom taskbar
  // plus any number of independent browse windows (each its own dmsg virtual
  // browser), all draggable / resizable / minimizable / maximizable. opts:
  //   fetchDmsg, serveContent — the skywireVisor primitives; selfPK() — optional.
  // Backward-compatible surface: returns { panel, browser, toggle, openWindow }
  // where toggle() shows/hides the desktop (opening a first window on demand), so
  // the existing skynet launcher button keeps working unchanged.
  // createLogWindow opens a draggable window showing the live visor log (the
  // captured console ring buffer) with a level filter + text filter — the
  // operator can watch what the visor is doing (incl. the upstream-proxy/browse
  // activity) without browser devtools or a shell. One per panel; toggled from
  // the taskbar.
  function createLogWindow(doc, opts) {
    opts = opts || {};
    var wrap = doc.createElement("div");
    wrap.style.cssText = "position:absolute;inset:0;background:#0e0c14;color:#cdd2da;font:11px/1.4 monospace;display:flex;flex-direction:column;overflow:hidden";
    wrap.innerHTML =
      '<div class="lw-bar" style="display:flex;gap:.4em;align-items:center;padding:.45em;background:#1b1726;border-bottom:1px solid #2a2342">' +
      '<select id="lw-level" title="min level" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342"><option value="all">all</option><option value="info">info+</option><option value="warn">warn+</option><option value="error">error</option></select>' +
      '<input id="lw-filter" placeholder="filter" size="8" style="flex:1;min-width:0;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.2em">' +
      '<button id="lw-follow" title="auto-scroll" style="cursor:pointer">▼</button>' +
      '<button id="lw-clear" title="clear" style="cursor:pointer">clear</button></div>' +
      '<pre id="lw-body" style="flex:1;margin:0;padding:.5em;overflow:auto;white-space:pre-wrap;word-break:break-all"></pre>';
    function $(id) { return wrap.querySelector("#" + id); }
    var body = $("lw-body"), follow = true, minLevel = "all", filter = "";
    var rank = { debug: 0, log: 1, info: 1, warn: 2, error: 3 };
    var minRank = { all: 0, info: 1, warn: 2, error: 3 };
    var color = { error: "#f7768e", warn: "#e0af68", info: "#7dcfff", log: "#cdd2da", debug: "#9aa0a6" };
    function show(line) {
      if ((rank[line.level] || 1) < minRank[minLevel]) return false;
      if (filter && line.text.toLowerCase().indexOf(filter) < 0) return false;
      return true;
    }
    function append(line) {
      if (!show(line)) return;
      var d = doc.createElement("div");
      d.style.color = color[line.level] || "#cdd2da";
      d.textContent = new Date(line.t).toTimeString().slice(0, 8) + " " + line.text;
      body.appendChild(d);
      if (body.childNodes.length > 6000) body.removeChild(body.firstChild);
      if (follow) body.scrollTop = body.scrollHeight;
    }
    function rerender() { body.textContent = ""; (window.skywireLog ? window.skywireLog.all() : []).forEach(append); }
    rerender();
    var unsub = window.skywireLog ? window.skywireLog.subscribe(append) : function () {};
    $("lw-level").onchange = function () { minLevel = this.value; rerender(); };
    $("lw-filter").oninput = function () { filter = this.value.trim().toLowerCase(); rerender(); };
    $("lw-follow").onclick = function () { follow = !follow; this.style.opacity = follow ? "1" : ".5"; if (follow) body.scrollTop = body.scrollHeight; };
    $("lw-clear").onclick = function () { if (window.skywireLog) window.skywireLog.clear(); body.textContent = ""; };
    var wb = makeWin(doc, {
      title: "visor log", root: opts.root, top: opts.top, bottom: opts.bottom, width: "46%", height: "60%",
      mount: wrap, onclose: function () { unsub(); if (opts.onClose) opts.onClose(); }
    });
    return { wb: wb, close: function () { wb.close(); } };
  }

  // createCliWindow opens a REPL that dispatches a curated command set to the
  // visor's RPC via opts.api(method, path, body) — the wasm core's hvApi() in the
  // wasm visor (function call, no shell needed — works in standalone PWA mode),
  // or /api over fetch in the native HV UI. So the operator can drive the running
  // visor from the UI without the shell + cli binary. `raw <M> <path> [body]` is
  // the escape hatch to any API route.
  function createCliWindow(doc, opts) {
    var api = opts.api;
    function self() { try { return (opts.selfPK && opts.selfPK()) || ""; } catch (_) { return ""; } }
    var wrap = doc.createElement("div");
    wrap.style.cssText = "position:absolute;inset:0;background:#0e0c14;color:#cdd2da;font:12px/1.4 monospace;display:flex;flex-direction:column;overflow:hidden";
    wrap.innerHTML =
      '<pre id="cw-out" style="flex:1;margin:0;padding:.5em;overflow:auto;white-space:pre-wrap;word-break:break-all"></pre>' +
      '<div style="display:flex;gap:.3em;padding:.4em;border-top:1px solid #2a2342;background:#15131c;align-items:center"><span style="color:#9ece6a">&gt;</span>' +
      '<input id="cw-in" placeholder="help" autocapitalize="off" autocomplete="off" autocorrect="off" spellcheck="false" style="flex:1;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.3em;font:12px monospace"></div>';
    function $(id) { return wrap.querySelector("#" + id); }
    var out = $("cw-out"), inp = $("cw-in"), hist = [], hi = 0;
    function w(text, color) { var d = doc.createElement("div"); if (color) d.style.color = color; d.textContent = text; out.appendChild(d); out.scrollTop = out.scrollHeight; }
    function pretty(s) { try { return JSON.stringify(JSON.parse(s), null, 2); } catch (_) { return s; } }
    var HELP = ["commands (a thin REPL over the visor RPC):",
      "  about | info          GET /api/about",
      "  visors | ls           GET /api/visors",
      "  net                   GET /api/network-view",
      "  app ls | tp ls        self apps / transports",
      "  route ls | health     self routes / health",
      "  raw <M> <path> [body] arbitrary call, e.g. raw GET /api/visors",
      "  clear"].join("\n");
    function run(cmd) {
      cmd = cmd.trim(); if (!cmd) return;
      w("> " + cmd, "#9ece6a");
      if (!api) { w("no api provider wired for this host", "#e0af68"); return; }
      var a = cmd.split(/\s+/), c = a[0], sp = "/api/visors/" + self();
      if (c === "help") { w(HELP); return; }
      if (c === "clear") { out.textContent = ""; return; }
      var alias = { about: ["GET", "/api/about"], info: ["GET", "/api/about"], visors: ["GET", "/api/visors"], ls: ["GET", "/api/visors"], net: ["GET", "/api/network-view"], health: ["GET", sp + "/health"] };
      var m, path, bodyArg = null;
      if (c === "raw") { m = (a[1] || "GET").toUpperCase(); path = a[2] || "/api/about"; bodyArg = a.slice(3).join(" ") || null; }
      else if (c === "app" && a[1] === "ls") { m = "GET"; path = sp + "/apps"; }
      else if (c === "tp" && a[1] === "ls") { m = "GET"; path = sp + "/transports"; }
      else if (c === "route" && a[1] === "ls") { m = "GET"; path = sp + "/routes"; }
      else if (alias[c]) { m = alias[c][0]; path = alias[c][1]; }
      else { w("unknown: " + c + "  (try help)", "#e0af68"); return; }
      Promise.resolve(api(m, path, bodyArg)).then(function (r) {
        w(r.status + " " + path, (r.status >= 200 && r.status < 300) ? "#7dcfff" : "#f7768e");
        w(pretty(r.body));
      }).catch(function (e) { w("error: " + e, "#f7768e"); });
    }
    inp.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { var v = inp.value; inp.value = ""; if (v.trim()) { hist.push(v); hi = hist.length; } run(v); }
      else if (e.key === "ArrowUp") { if (hi > 0) { hi--; inp.value = hist[hi] || ""; } e.preventDefault(); }
      else if (e.key === "ArrowDown") { if (hi < hist.length - 1) { hi++; inp.value = hist[hi] || ""; } else { hi = hist.length; inp.value = ""; } e.preventDefault(); }
    });
    w("visor cli — type 'help'. Dispatches to the running visor's RPC.", "#9aa0a6");
    var wb = makeWin(doc, {
      title: "visor cli", root: opts.root, top: opts.top, bottom: opts.bottom, width: "50%", height: "58%",
      mount: wrap, onclose: function () { if (opts.onClose) opts.onClose(); }
    });
    setTimeout(function () { inp.focus(); }, 50);
    return { wb: wb, close: function () { wb.close(); } };
  }

  // createHostWindow manages content this tab hosts over dmsg. Add a text page or
  // upload files / a whole directory; each path is served at <this-pk>.dmsg:<port>
  // while the tab is open. Lists what's hosted with per-path enable/disable +
  // remove. Wasm-visor only (uses skywireVisor.serveContent / hostedContent /
  // unserveContent / setContentEnabled).
  function createHostWindow(doc, opts) {
    var sv = globalThis.skywireVisor || {};
    var serveContent = opts.serveContent || sv.serveContent;
    function selfPK() { try { return (opts.selfPK && opts.selfPK()) || ""; } catch (_) { return ""; } }
    var wrap = doc.createElement("div");
    wrap.style.cssText = "position:absolute;inset:0;background:#15131c;color:#cdd2da;font:12px/1.45 monospace;display:flex;flex-direction:column;overflow:auto";
    var pk = selfPK();
    wrap.innerHTML =
      '<div style="padding:.5em;border-bottom:1px solid #2a2342;background:#1b1726">' +
      'Hosting from this tab over dmsg — reachable at <b style="color:#9d7cff;word-break:break-all">' + (pk ? esc(pk) + ".dmsg" : "(boot the visor first)") + '</b> while this tab stays open.</div>' +
      '<div style="padding:.5em;display:flex;flex-direction:column;gap:.4em;border-bottom:1px solid #2a2342">' +
      '<div style="display:flex;gap:.4em;align-items:center;flex-wrap:wrap">' +
      'path <input id="hw-path" value="/" size="8" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.25em">' +
      'port <input id="hw-port" value="80" size="4" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.25em">' +
      'type <input id="hw-ct" value="text/html" size="10" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.25em">' +
      '<button id="hw-serve" style="cursor:pointer">serve text</button></div>' +
      '<textarea id="hw-body" rows="3" placeholder="&lt;h1&gt;hosted from my browser, over dmsg&lt;/h1&gt;" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;font:12px monospace"></textarea>' +
      '<div style="display:flex;gap:.7em;align-items:center;flex-wrap:wrap">' +
      '<label style="cursor:pointer">file <input id="hw-file" type="file" style="color:#cdd2da;font:11px monospace"></label>' +
      '<label style="cursor:pointer" title="host every file in a folder, each at its relative path">directory <input id="hw-dir" type="file" webkitdirectory directory multiple style="color:#cdd2da;font:11px monospace"></label>' +
      '</div><span id="hw-msg" style="color:#9ece6a;word-break:break-all"></span></div>' +
      '<div style="padding:.5em;display:flex;align-items:center;gap:.5em"><b>hosted content</b><button id="hw-refresh" style="cursor:pointer">↻ refresh</button></div>' +
      '<div id="hw-list" style="padding:0 .5em .5em;display:flex;flex-direction:column;gap:.25em"></div>';
    function $(id) { return wrap.querySelector("#" + id); }
    function msg(t, ok) { var m = $("hw-msg"); m.textContent = t; m.style.color = ok === false ? "#f7768e" : "#9ece6a"; }
    function port() { return parseInt($("hw-port").value, 10) || 80; }
    function fmtB(b) { if (b < 1024) return b + " B"; if (b < 1048576) return (b / 1024).toFixed(1) + " KB"; return (b / 1048576).toFixed(1) + " MB"; }

    function renderList() {
      var el = $("hw-list"), rows = [];
      try { rows = JSON.parse((sv.hostedContent && sv.hostedContent()) || "[]") || []; } catch (_) {}
      if (!rows.length) { el.innerHTML = '<span style="color:#9aa0a6">nothing hosted yet — add text or upload files / a directory above.</span>'; return; }
      el.innerHTML = "";
      rows.forEach(function (r) {
        var row = doc.createElement("div");
        row.style.cssText = "display:flex;gap:.5em;align-items:center;background:#1b1726;border:1px solid #2a2342;border-radius:4px;padding:.3em .5em;flex-wrap:wrap";
        var cb = doc.createElement("input"); cb.type = "checkbox"; cb.checked = !!r.enabled; cb.title = "serve this path (uncheck to disable → 404, keeps the content)";
        cb.onchange = function () { try { if (sv.setContentEnabled) sv.setContentEnabled(r.path, cb.checked, r.port); } catch (_) {} renderList(); };
        var lbl = doc.createElement("span"); lbl.style.cssText = "flex:1;min-width:120px;word-break:break-all";
        lbl.innerHTML = '<b style="color:' + (r.enabled ? "#9ece6a" : "#9aa0a6") + '">' + esc(r.path) + '</b> <span style="color:#9aa0a6">:' + r.port + ' · ' + esc(r.ct) + ' · ' + fmtB(r.size) + (r.enabled ? '' : ' · disabled') + '</span>';
        var open = doc.createElement("button"); open.textContent = "open"; open.style.cursor = "pointer"; open.title = "open in a browser window";
        open.onclick = function () { if (opts.browseTo) opts.browseTo(selfPK() + (r.port !== 80 ? ":" + r.port : ""), r.path); };
        var rm = doc.createElement("button"); rm.textContent = "remove"; rm.style.cursor = "pointer";
        rm.onclick = function () { try { if (sv.unserveContent) sv.unserveContent(r.path, r.port); } catch (_) {} renderList(); };
        row.appendChild(cb); row.appendChild(lbl); row.appendChild(open); row.appendChild(rm);
        el.appendChild(row);
      });
    }
    function serveOne(path, ct, body, b64) {
      if (!serveContent) { msg("serveContent unavailable (boot the visor first)", false); return false; }
      var m = {}; m[path] = b64 ? { ct: ct, body: body, b64: true } : { ct: ct, body: body };
      try { serveContent(m, port()); } catch (e) { msg("serve failed: " + e, false); return false; }
      renderList(); return true;
    }
    function fileB64(f) { return new Promise(function (res, rej) { var fr = new FileReader(); fr.onload = function () { var b = new Uint8Array(fr.result), s = "", i; for (i = 0; i < b.length; i++) s += String.fromCharCode(b[i]); res(btoa(s)); }; fr.onerror = rej; fr.readAsArrayBuffer(f); }); }
    function ctFor(f) { return f.type || mimeOf(f.name); }
    $("hw-serve").onclick = function () {
      var p = ($("hw-path").value || "/").trim() || "/";
      if (serveOne(p, ($("hw-ct").value || "text/html").trim(), $("hw-body").value, false)) msg("serving " + p + " (text) on dmsg:" + port());
    };
    $("hw-file").onchange = function (e) {
      var f = e.target.files && e.target.files[0]; if (!f) return;
      var p = ($("hw-path").value || "/").trim(); if (!p || p === "/") p = "/" + f.name;
      fileB64(f).then(function (b64) { if (serveOne(p, ctFor(f), b64, true)) msg("serving " + p + " (" + fmtB(f.size) + ") on dmsg:" + port()); });
    };
    $("hw-dir").onchange = function (e) {
      var files = [].slice.call(e.target.files || []); if (!files.length) return;
      msg("uploading " + files.length + " file(s)…");
      var n = 0;
      files.reduce(function (chain, f) {
        return chain.then(function () {
          var rel = (f.webkitRelativePath || f.name).replace(/^\/+/, "");
          return fileB64(f).then(function (b64) { if (serveOne("/" + rel, ctFor(f), b64, true)) n++; });
        });
      }, Promise.resolve()).then(function () { msg("hosting " + n + " file(s) from the directory on dmsg:" + port()); renderList(); });
    };
    $("hw-refresh").onclick = renderList;
    renderList();
    var wb = makeWin(doc, { title: "host content", root: opts.root, top: opts.top, bottom: opts.bottom, width: "56%", height: "66%", mount: wrap, onclose: function () { if (opts.onClose) opts.onClose(); } });
    return { wb: wb, close: function () { wb.close(); } };
  }

  // createChatWindow is the WinBox desktop's 1:1 skychat client — the missing
  // desktop peer to the skynet browser. It drives the two existing wasm-visor JS
  // hooks: skychatSend(peerPkHex, text) → Promise and skychatMessages() → JSON of
  // [{from,text,ts,out}] (the in-memory ring the browser-tab visor keeps). Because
  // receiving is passive (a peer just dials us on dmsg:1), the buffer's distinct
  // `from` PKs are surfaced as clickable chips so an incoming message from a new
  // peer is discoverable without knowing their key in advance. Wasm-visor only
  // (native has its own Angular skychat tab), gated exactly like the host window.
  function createChatWindow(doc, opts) {
    var sv = globalThis.skywireVisor || {};
    var send = opts.skychatSend || sv.skychatSend;
    var fetchMsgs = opts.skychatMessages || sv.skychatMessages;
    function selfPK() { try { return (opts.selfPK && opts.selfPK()) || ""; } catch (_) { return ""; } }
    var peer = "";          // active conversation peer PK (full hex)
    var lastRender = "";    // cheap change-detection so we don't rebuild every tick

    var wrap = doc.createElement("div");
    wrap.style.cssText = "position:absolute;inset:0;background:#0e0c14;color:#cdd2da;font:12px/1.45 monospace;display:flex;flex-direction:column;overflow:hidden";
    var sp = selfPK();
    wrap.innerHTML =
      '<div style="padding:.45em;border-bottom:1px solid #2a2342;background:#1b1726;display:flex;flex-direction:column;gap:.35em">' +
      '<div style="color:#9aa0a6">you: <b style="color:#9d7cff;word-break:break-all">' + (sp ? esc(sp) : "(boot the visor first)") + '</b></div>' +
      '<div style="display:flex;gap:.4em;align-items:center">peer ' +
      '<input id="ch-peer" placeholder="paste a peer public key (66 hex)" autocapitalize="off" autocomplete="off" autocorrect="off" spellcheck="false" style="flex:1;min-width:0;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.3em;font:12px monospace">' +
      '<button id="ch-open" title="open conversation" style="cursor:pointer;background:#1b1726;color:#9d7cff;border:1px solid #2a2342;border-radius:5px;padding:.3em .55em">open</button></div>' +
      '<div style="display:flex;gap:.4em;align-items:center;font-size:11px;color:#9aa0a6">transport ' +
      '<select id="ch-net" title="send over dmsg (direct) or skynet (routed)" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.15em"><option value="dmsg">dmsg</option><option value="skynet">skynet</option></select>' +
      '<span style="flex:1"></span>' +
      '<button id="ch-log-toggle" title="show/hide skychat activity log" style="cursor:pointer;background:#15131c;color:#cdd2da;border:1px solid #2a2342;border-radius:4px;padding:.15em .5em">🐞 log</button></div>' +
      '<div id="ch-chips" style="display:flex;gap:.3em;flex-wrap:wrap"></div></div>' +
      '<div id="ch-body" style="flex:1;padding:.5em;overflow:auto;display:flex;flex-direction:column;gap:.25em"></div>' +
      '<div style="display:flex;gap:.3em;padding:.4em;border-top:1px solid #2a2342;background:#15131c;align-items:center">' +
      '<input id="ch-in" placeholder="message…" autocomplete="off" style="flex:1;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.35em;font:12px monospace" disabled>' +
      '<button id="ch-send" style="cursor:pointer;background:#1b1726;color:#9ece6a;border:1px solid #2a2342;border-radius:5px;padding:.35em .7em" disabled>send</button></div>' +
      '<div id="ch-status" style="padding:.2em .5em;min-height:1.2em;color:#9aa0a6;border-top:1px solid #15131c"></div>' +
      '<pre id="ch-log" style="display:none;margin:0;height:120px;overflow:auto;background:#0e0c14;color:#a9b1d6;border-top:1px solid #2a2342;padding:.4em;font:10px/1.4 monospace;white-space:pre-wrap;word-break:break-all"></pre>';
    function $(id) { return wrap.querySelector("#" + id); }
    var body = $("ch-body"), input = $("ch-in"), chips = $("ch-chips"), status = $("ch-status");

    function setStatus(t, color) { status.textContent = t || ""; status.style.color = color || "#9aa0a6"; }
    function messages() {
      if (!fetchMsgs) return [];
      try { return JSON.parse(fetchMsgs() || "[]") || []; } catch (_) { return []; }
    }
    function setPeer(pk) {
      peer = (pk || "").trim();
      $("ch-peer").value = peer;
      var ready = !!(send && peer);
      input.disabled = !ready; $("ch-send").disabled = !ready;
      lastRender = ""; render();
      if (ready) setTimeout(function () { input.focus(); }, 30);
    }
    function renderChips(all) {
      var seen = {}, order = [];
      for (var i = 0; i < all.length; i++) { var f = all[i].from; if (f && !seen[f]) { seen[f] = true; order.push(f); } }
      var key = order.join(",") + "|" + peer;
      if (chips.__key === key) return; chips.__key = key;
      chips.textContent = "";
      order.forEach(function (f) {
        var b = doc.createElement("button");
        b.textContent = f.slice(0, 8) + "…";
        b.title = f;
        var active = (f === peer);
        b.style.cssText = "cursor:pointer;border-radius:4px;padding:.2em .5em;font:11px monospace;border:1px solid #2a2342;" +
          (active ? "background:#2a2342;color:#9d7cff" : "background:#15131c;color:#cdd2da");
        b.onclick = function () { setPeer(f); };
        chips.appendChild(b);
      });
    }
    function render() {
      var all = messages();
      renderChips(all);
      var thread = [];
      for (var i = 0; i < all.length; i++) { if (all[i].from === peer) thread.push(all[i]); }
      var sig = peer + "#" + thread.length + (thread.length ? "#" + thread[thread.length - 1].ts + thread[thread.length - 1].text : "");
      if (sig === lastRender) return; lastRender = sig;
      body.textContent = "";
      if (!peer) { var h = doc.createElement("div"); h.style.color = "#9aa0a6"; h.textContent = "Paste a peer public key and press open — or pick a peer above once someone messages you."; body.appendChild(h); return; }
      thread.forEach(function (m) {
        var row = doc.createElement("div");
        row.style.cssText = "display:flex;flex-direction:column;max-width:82%;" + (m.out ? "align-self:flex-end;align-items:flex-end" : "align-self:flex-start;align-items:flex-start");
        var bub = doc.createElement("div");
        bub.style.cssText = "padding:.3em .55em;border-radius:8px;white-space:pre-wrap;word-break:break-word;" +
          (m.out ? "background:#1f2b1a;color:#c8e6a8" : "background:#1b1726;color:#cdd2da");
        bub.textContent = m.text;
        var meta = doc.createElement("div");
        meta.style.cssText = "font-size:10px;color:#6b7280;margin:.1em .2em 0";
        meta.textContent = (m.out ? "→ " : "← ") + new Date(m.ts).toTimeString().slice(0, 8);
        row.appendChild(bub); row.appendChild(meta); body.appendChild(row);
      });
      body.scrollTop = body.scrollHeight;
    }
    function doSend() {
      var text = input.value; if (!text.trim() || !send || !peer) return;
      input.value = ""; setStatus("sending…");
      Promise.resolve(send(peer, text, $("ch-net").value)).then(function () {
        setStatus(""); lastRender = ""; render();
      }).catch(function (e) {
        setStatus("send failed: " + (e && e.message ? e.message : e), "#f7768e");
        input.value = text; // let the operator retry without retyping
      });
    }
    $("ch-open").onclick = function () { setPeer($("ch-peer").value); };
    $("ch-peer").addEventListener("keydown", function (e) { if (e.key === "Enter") setPeer(this.value); });
    input.addEventListener("keydown", function (e) { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); doSend(); } });
    $("ch-send").onclick = doSend;

    // Activity log pane — surfaces skychat's own dial/connect/send/receive vlog
    // lines from the shared window.skywireLog ring (the same source the 'logs'
    // window reads), filtered to skychat, so the operator sees the connection /
    // route setup like the browser window's skysocks-lite log. Collapsible (🐞).
    var logEl = $("ch-log"), logOpen = false;
    function chatLogLine(line) {
      if (!line || !line.text || !/skychat/i.test(line.text)) return;
      var d = doc.createElement("div");
      d.textContent = new Date(line.t || Date.now()).toTimeString().slice(0, 8) + " " + String(line.text).replace(/^\[visor\]\s*/, "");
      logEl.appendChild(d);
      if (logEl.childNodes.length > 500) logEl.removeChild(logEl.firstChild);
      if (logOpen) logEl.scrollTop = logEl.scrollHeight;
    }
    var logUnsub = function () {};
    if (window.skywireLog) {
      try { window.skywireLog.all().forEach(chatLogLine); } catch (_) {}
      logUnsub = window.skywireLog.subscribe(chatLogLine);
    }
    $("ch-log-toggle").onclick = function () {
      logOpen = !logOpen; logEl.style.display = logOpen ? "block" : "none";
      this.style.opacity = logOpen ? "1" : ".6";
      if (logOpen) logEl.scrollTop = logEl.scrollHeight;
    };

    var timer = setInterval(render, 1500);
    render();
    if (!send) setStatus("skychat is only available in the browser-tab (wasm) visor", "#e0af68");
    var wb = makeWin(doc, {
      title: "skychat", root: opts.root, top: opts.top, bottom: opts.bottom, width: "42%", height: "62%",
      mount: wrap, onclose: function () { clearInterval(timer); logUnsub(); if (opts.onClose) opts.onClose(); }
    });
    return { wb: wb, close: function () { wb.close(); } };
  }

  // createTerminalWindow opens a real dmsgpty terminal as a WinBox iframe to
  // opts.ptyURL (the visor's /pty/<pk>, which serves the xterm + pty WebSocket).
  // Native-only — the wasm visor has no host shell and sets no ptyURL, so the
  // launcher button isn't shown there. WinBox owns the iframe and applies
  // pointer-events:none on it during drags (body.wb-lock), so the pty session
  // survives moves/resizes without the manual capture hack we used before.
  function createTerminalWindow(doc, opts) {
    var wb = makeWin(doc, {
      title: "terminal", root: opts.root, top: opts.top, bottom: opts.bottom, width: "54%", height: "64%",
      url: opts.ptyURL, onclose: function () { if (opts.onClose) opts.onClose(); }
    });
    return { wb: wb, close: function () { wb.close(); } };
  }

  function mountPanel(doc, opts) {
    var wins = [];          // {wb, chip, browser?} for every open window
    var BARH = 36;          // bottom taskbar height; windows live above it

    // shallow-merge: opts + {root, onClose, …} so each window gets the shared
    // providers (fetchDmsg / api / selfPK / ptyURL …) plus its own root + close
    // callback. (Avoids relying on Object.assign in odd embeds.)
    function withRoot(extra) {
      var o = {}, k;
      for (k in opts) { if (Object.prototype.hasOwnProperty.call(opts, k)) o[k] = opts[k]; }
      o.root = root; o.top = barTop; o.bottom = barBottom;
      if (extra) { for (k in extra) { if (Object.prototype.hasOwnProperty.call(extra, k)) o[k] = extra[k]; } }
      return o;
    }

    // Desktop root: the windows area, sized by applyDock() to fill the viewport
    // on the side AWAY from the bar, so no window can hide behind the panel
    // (WinBox centers + bounds windows against this box). pointer-events:none
    // lets clicks fall through where no window covers — windows re-enable events
    // via .skywire-wb. The panel is always on (no hide).
    var root = doc.createElement("div");
    root.id = "skywire-skynet-root";
    root.style.cssText = "position:fixed;left:0;top:0;right:0;bottom:0;pointer-events:none";
    (doc.body || doc.documentElement).appendChild(root);
    // barTop / barBottom: the WinBox viewport boundary on the panel's edge, set
    // by applyDock and applied to every window so none can drag/maximize under
    // the bar. (0 on the free edge.)
    var barTop = 0, barBottom = 0;
    if (!doc.getElementById("skywire-wb-style")) {
      var st = doc.createElement("style");
      st.id = "skywire-wb-style";
      // WinBox ships `.winbox iframe{position:absolute;width:100%;height:100%}`
      // for url:-mounted windows — but that also covers the browse window's
      // own iframe, painting over its address/nav bar. Pin the browse iframe
      // back into the flex column (below the nav bar) so the bar shows. The
      // terminal's url: iframe (no #sb-frame) keeps WinBox's fill behaviour.
      st.textContent = ".skywire-wb{pointer-events:auto}" +
        ".skywire-wb #sb-frame{position:relative!important;height:auto!important;min-height:0!important;flex:1 1 auto!important}";
      (doc.head || doc.documentElement).appendChild(st);
    }

    // Always-on taskbar: [menu] [open-window chips…] [dock]. Top by default; the
    // dock button flips it top↔bottom (remembered in localStorage). No hide.
    var bar = doc.createElement("div");
    bar.id = "skywire-skynet-taskbar";
    bar.style.cssText = "position:fixed;left:0;right:0;height:" + BARH + "px;box-sizing:border-box;z-index:2147483646;" +
      "display:flex;gap:.5em;align-items:center;padding:0 .6em;background:#0e0b16;" +
      "font:12px/1.3 monospace;color:#cdd2da";
    bar.innerHTML =
      '<button id="tb-menu" title="apps" style="cursor:pointer;font-size:15px;line-height:1;background:#1b1726;color:#9d7cff;border:1px solid #2a2342;border-radius:5px;padding:.2em .5em">☰</button>' +
      '<span id="tb-items" style="display:flex;gap:.35em;flex:1;flex-wrap:wrap;min-width:0;overflow:hidden"></span>' +
      '<button id="tb-dock" title="dock the panel to the top or bottom" style="cursor:pointer">⇅</button>';
    (doc.body || doc.documentElement).appendChild(bar);
    function bq(id) { return bar.querySelector("#" + id); }
    var items = bq("tb-items");

    // App menu (start / whisker menu) — opens from the menu button (applyDock
    // anchors it to the bar's edge).
    var menu = doc.createElement("div");
    menu.id = "skywire-appmenu";
    menu.style.cssText = "position:fixed;left:6px;z-index:2147483647;display:none;min-width:168px;" +
      "background:#15131c;border:1px solid #2a2342;border-radius:8px;box-shadow:0 10px 30px rgba(0,0,0,.55);padding:.3em;font:13px/1.4 monospace;color:#cdd2da";
    (doc.body || doc.documentElement).appendChild(menu);
    function hideMenu() { menu.style.display = "none"; }

    // Dock the panel top or bottom; size root + anchor the menu accordingly so a
    // window can never hide behind the bar. Persisted across reloads.
    var DOCKKEY = "skywire-panel-dock", dock = "top";
    try { dock = localStorage.getItem(DOCKKEY) || "top"; } catch (e) {}
    function applyDock(d) {
      dock = (d === "bottom") ? "bottom" : "top";
      try { localStorage.setItem(DOCKKEY, dock); } catch (e) {}
      if (dock === "top") {
        bar.style.top = "0"; bar.style.bottom = "auto";
        bar.style.borderTop = "0"; bar.style.borderBottom = "1px solid #2a2342";
        menu.style.top = BARH + "px"; menu.style.bottom = "auto";
        barTop = BARH; barBottom = 0;
      } else {
        bar.style.bottom = "0"; bar.style.top = "auto";
        bar.style.borderBottom = "0"; bar.style.borderTop = "1px solid #2a2342";
        menu.style.bottom = BARH + "px"; menu.style.top = "auto";
        barTop = 0; barBottom = BARH;
      }
      // Reserve the bar's strip on the HV-UI underneath so page content isn't
      // painted over by the always-on-top taskbar. Without this the fixed bar
      // (z 2147483646) covers the top ~BARH px of the Angular page — e.g. the
      // node-info page's first line, the visor public key. Padding the body pushes
      // the normal-flow content clear; the WinBox windows are unaffected (they're
      // fixed-positioned and bounded by barTop/barBottom above).
      try {
        var pg = doc.body;
        if (pg) {
          pg.style.paddingTop = (dock === "top") ? BARH + "px" : "";
          pg.style.paddingBottom = (dock === "bottom") ? BARH + "px" : "";
        }
      } catch (e) { /* body not ready — applyDock re-runs on dock toggle */ }
    }
    function addApp(label, fn) {
      var b = doc.createElement("button");
      b.textContent = label;
      b.style.cssText = "display:block;width:100%;text-align:left;cursor:pointer;background:transparent;color:#cdd2da;border:0;border-radius:5px;padding:.5em .7em;font:13px monospace";
      b.onmouseover = function () { b.style.background = "#1b1726"; };
      b.onmouseout = function () { b.style.background = "transparent"; };
      b.onclick = function () { hideMenu(); fn(); };
      menu.appendChild(b);
    }
    addApp("browser", function () { openBrowse(); });
    // 'chat' + 'host' are wasm-visor only — they use in-tab JS hooks the native
    // HV UI doesn't expose (native has its own Angular skychat tab).
    if (globalThis.skywireVisor && globalThis.skywireVisor.skychatSend) { addApp("chat", function () { openChat(); }); }
    if (globalThis.skywireVisor && globalThis.skywireVisor.serveContent) { addApp("host", function () { openHost(); }); }
    addApp("console", function () { openCli(); });
    if (opts.ptyURL) addApp("terminal", function () { openTerm(); });
    addApp("logs", function () { openLog(); });
    addApp("identity", function () { openIdentityDialog(doc, opts); });
    addApp("tour", function () { startTour(doc); });
    // Offer the tour once, shortly after first load, so newcomers get oriented.
    try {
      if (!localStorage.getItem(TOUR_SEEN_KEY)) {
        setTimeout(function () { if (!localStorage.getItem(TOUR_SEEN_KEY)) { startTour(doc); } }, 1600);
      }
    } catch (e) {}
    bq("tb-menu").onclick = function (e) { e.stopPropagation(); menu.style.display = (menu.style.display === "block") ? "none" : "block"; };
    doc.addEventListener("pointerdown", function (e) {
      if (menu.style.display === "block" && !menu.contains(e.target) && e.target !== bq("tb-menu")) hideMenu();
    }, true);

    // Window tracking: one chip per open window (focus/restore on click, × to
    // close), so multiple windows are manageable from the bar. WinBox still owns
    // the window chrome, focus, z-order and minimize.
    function track(win, title) {
      var chip = doc.createElement("span");
      chip.style.cssText = "display:inline-flex;align-items:center;max-width:13em;background:#1b1726;border:1px solid #2a2342;border-radius:4px;overflow:hidden";
      var f = doc.createElement("button");
      f.textContent = title; f.title = "focus / restore";
      f.style.cssText = "cursor:pointer;max-width:11em;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;background:transparent;color:#cdd2da;border:0;padding:.25em .55em;font:12px monospace";
      f.onclick = function () { try { win.wb.minimize(false); } catch (e) {} try { win.wb.focus(); } catch (e) {} };
      var x = doc.createElement("button");
      x.textContent = "×"; x.title = "close";
      x.style.cssText = "cursor:pointer;background:transparent;color:#9aa0a6;border:0;border-left:1px solid #2a2342;padding:.25em .45em;font:12px monospace";
      x.onclick = function () { try { win.wb.close(); } catch (e) {} };
      chip.appendChild(f); chip.appendChild(x);
      items.appendChild(chip);
      win.chip = chip; win.titleEl = f;
      wins.push(win);
      return win;
    }
    function untrack(win) {
      var i = wins.indexOf(win); if (i >= 0) wins.splice(i, 1);
      if (win.chip && win.chip.parentNode) win.chip.parentNode.removeChild(win.chip);
    }
    function focusExisting(w) { if (!w) { return false; } try { w.wb.minimize(false); w.wb.focus(); } catch (e) {} return true; }

    // App launchers. browser is multi-instance; console/terminal/logs are
    // singletons (re-clicking focuses the open one).
    function openBrowse() {
      var win = createWindow(doc, withRoot(), function () { untrack(win); });
      track(win, "browser");
      win.landHome();
      return win;
    }
    var logWin = null;
    function openLog() {
      if (focusExisting(logWin)) { return; }
      logWin = createLogWindow(doc, withRoot({ onClose: function () { untrack(logWin); logWin = null; } }));
      track(logWin, "logs");
    }
    var cliWin = null;
    function openCli() {
      if (focusExisting(cliWin)) { return; }
      cliWin = createCliWindow(doc, withRoot({ onClose: function () { untrack(cliWin); cliWin = null; } }));
      track(cliWin, "console");
    }
    var termWin = null;
    function openTerm() {
      if (!opts.ptyURL || focusExisting(termWin)) { return; }
      termWin = createTerminalWindow(doc, withRoot({ onClose: function () { untrack(termWin); termWin = null; } }));
      track(termWin, "terminal");
    }
    var chatWin = null;
    function openChat() {
      if (focusExisting(chatWin)) { return; }
      chatWin = createChatWindow(doc, withRoot({ onClose: function () { untrack(chatWin); chatWin = null; } }));
      track(chatWin, "skychat");
    }
    var hostWin = null;
    function openHost() {
      if (focusExisting(hostWin)) { return; }
      hostWin = createHostWindow(doc, withRoot({
        onClose: function () { untrack(hostWin); hostWin = null; },
        // let the host window open a hosted path in a fresh browser window
        browseTo: function (host, path) { var w = openBrowse(); try { w.browser.browseTo(host, path); } catch (e) {} }
      }));
      track(hostWin, "host");
    }

    bq("tb-dock").onclick = function () { applyDock(dock === "top" ? "bottom" : "top"); };
    applyDock(dock);   // position the always-on panel + windows area on load

    // The panel is permanent; toggle() (kept for launcher/back-compat) just opens
    // the app menu so any old caller still surfaces the launcher.
    function toggle() { menu.style.display = (menu.style.display === "block") ? "none" : "block"; }

    // Deep-link: ?skynet=<target>[&kiosk=1] (or a #skynet=<target> hash fragment)
    // opens the skynet browser straight to <target> over dmsg, optionally full-page
    // (kiosk — hides the taskbar + maximizes so the site obscures the HV UI). Lets a
    // clearnet redirect drop a visitor into a dmsg site: e.g. Caddy sends
    // theskywirenetwork.net → skywire.theskywirenetwork.net/?skynet=rewards.dmsg&kiosk=1.
    function readDeepLink() {
      // hv-boot.js captured the query param before Angular could drop it — prefer
      // that. Fall back to the live location (query / hash fragment) for callers
      // (e.g. the native HV) that don't preload it.
      try {
        var pre = self.__SKYWIRE_DEEPLINK__ || (doc.defaultView || window).__SKYWIRE_DEEPLINK__;
        if (pre && pre.target) { return { target: pre.target, kiosk: !!pre.kiosk }; }
      } catch (e) {}
      var loc = (doc.defaultView || window).location, qs = {};
      try {
        (loc.search || "").replace(/^\?/, "").split("&").forEach(function (kv) {
          if (!kv) return;
          var i = kv.indexOf("="), k = i < 0 ? kv : kv.slice(0, i), v = i < 0 ? "" : kv.slice(i + 1);
          qs[decodeURIComponent(k)] = decodeURIComponent(v);
        });
        var h = loc.hash || "", m = h.match(/[#&]skynet=([^&]+)/);
        if (m && !qs.skynet) { qs.skynet = decodeURIComponent(m[1]); }
        if (/[#&]kiosk=1\b/.test(h)) { qs.kiosk = "1"; }
      } catch (e) {}
      if (!qs.skynet) { return null; }
      return { target: qs.skynet, kiosk: qs.kiosk === "1" || qs.kiosk === "true" };
    }
    // whenVisorConnected fires cb once the wasm visor has a live dmsg session (so a
    // fetch over dmsg won't just error), bounded to ~20s so a stuck visor still lands
    // on the browser's own error page instead of hanging forever.
    function whenVisorConnected(cb) {
      var tries = 0;
      (function poll() {
        Promise.resolve().then(function () { return self.skywireVisor && self.skywireVisor.status(); })
          .then(function (st) {
            if ((st && (st.dmsg_connected || (st.dmsg_sessions | 0) > 0)) || tries > 40) { cb(); }
            else { tries++; setTimeout(poll, 500); }
          }).catch(function () { if (tries > 40) { cb(); } else { tries++; setTimeout(poll, 500); } });
      })();
    }
    // enterKiosk hides the taskbar and maximizes the window to the full viewport so
    // the browsed site fills the page, obscuring the HV UI. Exit by un-maximizing.
    function enterKiosk(win) {
      try {
        doc.body.classList.add("skywire-kiosk");
        bar.style.display = "none";
        barTop = 0; barBottom = 0;
        if (win && win.wb && win.wb.maximize) { win.wb.maximize(true); }
      } catch (e) {}
    }
    try {
      var dl = readDeepLink();
      if (dl) {
        var dlWin = openBrowse();
        if (dl.kiosk) { enterKiosk(dlWin); }
        whenVisorConnected(function () { try { dlWin.browser.browseTo(dl.target, "/"); } catch (e) {} });
      }
    } catch (e) {}

    return {
      panel: bar,
      toggle: toggle,
      openWindow: openBrowse,
      browser: function () { for (var i = wins.length - 1; i >= 0; i--) { if (wins[i].browser) return wins[i].browser; } return null; }
    };
  }

  globalThis.SkywireBrowse = { createBrowser: createBrowser, mountPanel: mountPanel };
})();
