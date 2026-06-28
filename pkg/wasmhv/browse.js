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
      html: "text/html" }[m] || "application/octet-stream";
  }
  function bytesToB64(bytes) {
    var s = "", C = 0x8000;
    for (var i = 0; i < bytes.length; i += C) s += String.fromCharCode.apply(null, bytes.subarray(i, i + C));
    return btoa(s);
  }
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
  // the page's own same-site requests to the parent, fetched over dmsg.
  function navShimSrc(path) {
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
      'function relay(p,m,b){return new Promise(function(res){var id=++_rq;_pend[id]=res;parent.postMessage({type:"dmsgreq",id:id,path:p,method:m||"GET",body:b||null},"*");});}' +
      'var _f=window.fetch;window.fetch=function(input,init){var u=typeof input==="string"?input:(input&&input.url);' +
      'if(!same(u))return _f.apply(this,arguments);' +
      'return relay(pathOf(u),(init&&init.method)||"GET",(init&&init.body)||null).then(function(r){' +
      'var body=r.body?Uint8Array.from(atob(r.body),function(c){return c.charCodeAt(0);}):new Uint8Array();' +
      'return new Response(body,{status:r.status||200,headers:{"Content-Type":r.ct||"application/octet-stream"}});});};'
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
    // fetchClearnet(exitPK, method, url, body) → {status, body, headers}: a CLEARNET
    // fetch tunneled through a skysocks exit over a skywire route (IP-anonymous).
    var fetchClearnet = opts.fetchClearnet || function () { return globalThis.skywireVisor.fetchClearnet.apply(null, arguments); };
    var log = opts.log || function () {};
    var currentSitePK = "";

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

    async function renderSite(pk, path, html) {
      currentSitePK = pk;
      if (opts.setPK) opts.setPK(pk);
      if (opts.setPath) opts.setPath(path);
      var docHtml;
      try {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var head = doc.head || doc.documentElement;
        var base = doc.createElement("base"); base.setAttribute("target", "_self");
        var sc = doc.createElement("script"); sc.textContent = navShimSrc(path);
        head.insertBefore(sc, head.firstChild);
        head.insertBefore(base, head.firstChild);

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
        doc.querySelectorAll("img[src],script[src],source[src]").forEach(function (el) {
          var src = el.getAttribute("src");
          if (!sameSite(src)) return;
          jobs.push(fetchDmsg(pk, "GET", resolvePath(src, path), null)
            .then(function (r) { el.setAttribute("src", "data:" + ctOf(r.headers, src) + ";base64," + bytesToB64(r.body)); })
            .catch(function () {}));
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
        var html = new TextDecoder().decode(r.body);
        await renderSite(entry.pk, entry.path, html);
        if (gen !== loadGen) return { status: 0, cancelled: true };
        log("browsed dmsg://" + entry.pk + entry.path + " → " + r.status + " (" + r.body.length + " bytes)");
        return { status: r.status, bytes: r.body.length, html: html };
      } catch (e) {
        if (gen !== loadGen) return { status: 0, cancelled: true };
        log("browse error: " + (e.message || e));
        return { status: 0, error: String(e.message || e) };
      } finally {
        if (gen === loadGen) setLoading(false);
      }
    }

    async function fetchClearnetEntry(url, gen) {
      var pol = clearnetPolicy();
      if (pol.mode === "block") {
        if (gen !== loadGen) return { status: 0, cancelled: true };
        currentSitePK = ""; if (opts.setPK) opts.setPK(url);
        frame.srcdoc = blockedPage(url);
        log("clearnet BLOCKED (no upstream proxy): " + url);
        return { status: 0, blocked: true };
      }
      if (pol.mode === "direct") {
        if (gen !== loadGen) return { status: 0, cancelled: true };
        currentSitePK = ""; if (opts.setPK) opts.setPK(url);
        frame.removeAttribute("srcdoc");
        frame.src = url; // browser/visor loads it directly (non-anonymous, no skysocks hop)
        log("clearnet DIRECT (upstream = local visor): " + url);
        return { status: 0, direct: true };
      }
      var r = await fetchClearnet(pol.exit, "GET", url, null);
      if (gen !== loadGen) return { status: 0, cancelled: true };
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

    async function browseTo(pk, path) {
      pk = (pk || "").trim();
      path = path || "/";
      if (!pk) { log("browse: enter a site PK"); return { status: 0 }; }
      return navigate({ kind: "dmsg", pk: pk, path: path });
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
      if (up === localPK()) return { mode: "direct" };
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
      if (opts.setPK) opts.setPK(url);
      if (opts.setPath) opts.setPath("");
      var docHtml;
      try {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var head = doc.head || doc.documentElement;
        var base = doc.createElement("base"); base.setAttribute("href", url); base.setAttribute("target", "_self");
        head.insertBefore(base, head.firstChild);
        await Promise.all(gateAllClearnet(doc, url, { mode: "proxy", exit: exit }, true));
        docHtml = "<!doctype html>" + doc.documentElement.outerHTML;
      } catch (e) { docHtml = html; }
      frame.srcdoc = docHtml;
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
      if (d.type === "dmsgreq" && currentSitePK) {
        try {
          var r = await fetchDmsg(currentSitePK, d.method || "GET", d.path, d.body || null);
          e.source.postMessage({ type: "dmsgreply", id: d.id, status: r.status, ct: ctOf(r.headers, d.path), body: bytesToB64(r.body) }, "*");
        } catch (err) {
          e.source.postMessage({ type: "dmsgreply", id: d.id, status: 502, ct: "text/plain", body: btoa("dmsg fetch error") }, "*");
        }
      }
    });

    return {
      renderSite: renderSite, browseTo: browseTo, browseToClearnet: browseToClearnet,
      back: back, forward: forward, reload: reload, cancel: cancel,
      upstream: upstream, setUpstream: setUpstream,
      currentPK: function () { return currentSitePK; }
    };
  }

  // createWindow builds ONE draggable / resizable / minimizable / maximizable
  // browse window (its own dmsg virtual browser + host panel) into `doc`. hooks:
  //   onFocus()       — raise this window (z-order) in the manager;
  //   onClose()       — the manager should drop + remove this window;
  //   onTitle(text)   — reflect the current site into the taskbar entry.
  // Returns a window handle the manager drives (el, browser, show/hide/restore,
  // maximize, minimized flag, landHome).
  function createWindow(doc, opts, hooks) {
    var fetchDmsg = opts.fetchDmsg, serveContent = opts.serveContent;
    var wrap = doc.createElement("div");
    wrap.className = "skywire-browse-window";
    // Anchored top-left; RESIZABLE via the bottom-right corner (resize:both needs
    // overflow:hidden). The title bar is the drag handle; _ / ▢ minimize+maximize.
    wrap.style.cssText = "position:fixed;top:40px;left:40px;width:74vw;height:80vh;" +
      "min-width:340px;min-height:220px;max-width:99vw;max-height:96vh;resize:both;" +
      "background:#15131c;color:#cdd2da;font:12px/1.4 monospace;border:1px solid #2a2342;border-radius:8px;" +
      "box-shadow:0 8px 30px rgba(0,0,0,.55);z-index:2147483000;display:flex;flex-direction:column;overflow:hidden";
    wrap.innerHTML =
      '<div class="sbw-bar" style="display:flex;gap:.4em;align-items:center;padding:.5em;background:#1b1726;border-bottom:1px solid #2a2342">' +
      '<b style="color:#9d7cff;cursor:move">skynet</b>' +
      '<button id="sb-back" title="back" disabled style="cursor:pointer">◀</button>' +
      '<button id="sb-fwd" title="forward" disabled style="cursor:pointer">▶</button>' +
      '<button id="sb-reload" title="reload" style="cursor:pointer">⟳</button>' +
      '<input id="sb-pk" placeholder="site pk, pk:port, or https://clearnet (via skysocks)" style="flex:1;min-width:0;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.25em">' +
      '<input id="sb-path" value="/" size="6" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.25em">' +
      '<button id="sb-go" style="cursor:pointer">go</button>' +
      '<button id="sb-host-t" title="host a page" style="cursor:pointer">host</button>' +
      '<button id="sb-proxy-t" title="clearnet upstream proxy" style="cursor:pointer">⚙</button>' +
      '<button id="sb-min" title="minimize" style="cursor:pointer">_</button>' +
      '<button id="sb-max" title="maximize / restore" style="cursor:pointer">▢</button>' +
      '<button id="sb-x" title="close" style="cursor:pointer">×</button>' +
      '</div>' +
      '<div id="sb-host" style="display:none;gap:.3em;padding:.5em;background:#1a1726;border-bottom:1px solid #2a2342;flex-direction:column">' +
      '<div style="display:flex;gap:.4em;align-items:center;flex-wrap:wrap">path <input id="sb-hpath" value="/" size="6" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.2em">' +
      'port <input id="sb-hport" value="80" size="4" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.2em">' +
      'type <input id="sb-hct" value="text/html" size="9" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.2em">' +
      '<button id="sb-host-go" style="cursor:pointer">serve over dmsg</button></div>' +
      '<div style="display:flex;gap:.4em;align-items:center">file <input id="sb-hfile" type="file" style="flex:1;min-width:0;color:#cdd2da;font:11px monospace"></div>' +
      '<textarea id="sb-hbody" rows="3" placeholder="&lt;h1&gt;hosted from my browser, over dmsg&lt;/h1&gt; — or upload a file above" style="background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;font:12px monospace"></textarea>' +
      '<span id="sb-host-msg" style="color:#9ece6a;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;cursor:pointer" title="click to copy"></span>' +
      '</div>' +
      '<div id="sb-proxy" style="display:none;gap:.4em;padding:.5em;background:#1a1726;border-bottom:1px solid #2a2342;align-items:center;flex-wrap:wrap">' +
      '<span title="blank = clearnet blocked; this visor PK = direct (non-anonymous); another visor PK = via its skysocks (anonymous)">clearnet upstream proxy:</span>' +
      '<input id="sb-proxy-pk" placeholder="skysocks PK · own PK (direct) · blank (blocked)" style="flex:1;min-width:140px;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:.25em">' +
      '<button id="sb-proxy-self" title="use this visor (direct, non-anonymous)" style="cursor:pointer">self</button>' +
      '<button id="sb-proxy-save" style="cursor:pointer">set</button>' +
      '<span id="sb-proxy-msg" style="color:#9ece6a;overflow:hidden;white-space:nowrap;text-overflow:ellipsis"></span>' +
      '</div>' +
      '<iframe id="sb-frame" sandbox="allow-scripts allow-forms" style="flex:1;width:100%;border:0;background:#fff"></iframe>';
    (doc.body || doc.documentElement).appendChild(wrap);

    function $(id) { return wrap.querySelector("#" + id); }
    var win = { el: wrap, minimized: false, maximized: false };
    var loading = false;
    var browser = createBrowser({
      frame: $("sb-frame"), fetchDmsg: fetchDmsg,
      log: function (m) { try { console.log("[skynet] " + m); } catch (e) {} },
      setPK: function (pk) { $("sb-pk").value = pk; if (hooks.onTitle) hooks.onTitle((pk || "").slice(0, 10) || "site"); },
      setPath: function (p) { $("sb-path").value = p; },
      // reflect load state into the reload/cancel button (⟳ idle, ✕ while loading)
      onLoading: function (on) { loading = on; var b = $("sb-reload"); b.textContent = on ? "✕" : "⟳"; b.title = on ? "cancel load" : "reload"; },
      // enable/disable back/forward to match history position
      onNavState: function (canBack, canFwd) { $("sb-back").disabled = !canBack; $("sb-fwd").disabled = !canFwd; }
    });
    win.browser = browser;
    // A clearnet http(s):// URL routes through a skysocks exit (IP-anonymous); a
    // bare PK / pk:port is a dmsg/skynet site fetched over dmsg.
    function go() {
      var v = ($("sb-pk").value || "").trim();
      if (/^https?:\/\//i.test(v)) { browser.browseToClearnet(v); return; }
      browser.browseTo(v, ($("sb-path").value || "/").trim() || "/");
    }
    $("sb-go").onclick = go;
    $("sb-back").onclick = function () { browser.back(); };
    $("sb-fwd").onclick = function () { browser.forward(); };
    // ⟳ reloads the current page; while a load is in flight it becomes ✕ (cancel).
    $("sb-reload").onclick = function () { if (loading) browser.cancel(); else browser.reload(); };
    $("sb-pk").addEventListener("keydown", function (e) { if (e.key === "Enter") go(); });
    $("sb-path").addEventListener("keydown", function (e) { if (e.key === "Enter") go(); });
    $("sb-host-t").onclick = function () { var h = $("sb-host"); h.style.display = h.style.display === "none" ? "flex" : "none"; };
    // clearnet upstream-proxy settings (per window; persists as the global default).
    $("sb-proxy-pk").value = browser.upstream();
    $("sb-proxy-t").onclick = function () { var h = $("sb-proxy"); h.style.display = h.style.display === "none" ? "flex" : "none"; $("sb-proxy-pk").value = browser.upstream(); };
    $("sb-proxy-self").onclick = function () { var pk = ""; try { pk = (opts.selfPK && opts.selfPK()) || ""; } catch (e) {} $("sb-proxy-pk").value = pk; };
    function saveProxy() {
      browser.setUpstream($("sb-proxy-pk").value);
      var up = browser.upstream(), self = ""; try { self = (opts.selfPK && opts.selfPK()) || ""; } catch (e) {}
      var mode = !up ? "blocked" : (up === self ? "direct (non-anonymous)" : "via skysocks " + up.slice(0, 8) + " (anonymous)");
      var m = $("sb-proxy-msg"); m.textContent = "clearnet: " + mode; m.style.color = up ? "#9ece6a" : "#e0af68";
    }
    $("sb-proxy-save").onclick = saveProxy;
    $("sb-proxy-pk").addEventListener("keydown", function (e) { if (e.key === "Enter") saveProxy(); });
    // Raise this window above the others on any interaction.
    wrap.addEventListener("mousedown", function () { if (hooks.onFocus) hooks.onFocus(); }, true);

    // uploaded holds the last picked file as {ct, b64} (base64 so binary — images,
    // fonts, … — round-trips intact); the textarea is the fallback for typed HTML.
    var uploaded = null;
    $("sb-hfile").onchange = function (e) {
      var f = e.target.files && e.target.files[0];
      if (!f) { uploaded = null; return; }
      var rd = new FileReader();
      rd.onload = function () {
        var bytes = new Uint8Array(rd.result);
        uploaded = { ct: f.type || mimeOf(f.name), b64: bytesToB64(bytes) };
        $("sb-hct").value = uploaded.ct;
        $("sb-host-msg").textContent = "loaded " + f.name + " (" + bytes.length + " bytes) — set path + port, then serve";
        $("sb-host-msg").style.color = "#9ece6a";
      };
      rd.readAsArrayBuffer(f);
    };

    $("sb-host-go").onclick = function () {
      if (!serveContent) { $("sb-host-msg").textContent = "serveContent unavailable"; return; }
      var p = ($("sb-hpath").value || "/").trim() || "/";
      var port = parseInt($("sb-hport").value, 10) || 80;
      var entry = uploaded
        ? { ct: uploaded.ct, body: uploaded.b64, b64: true }
        : { ct: ($("sb-hct").value || "text/html").trim(), body: $("sb-hbody").value };
      var m = {}; m[p] = entry;
      serveContent(m, port);
      var pk = ""; try { pk = (opts.selfPK && opts.selfPK()) || ""; } catch (e) {}
      var addr = (pk ? pk : "<this-pk>") + (port === 80 ? "" : ":" + port) + p;
      var msg = $("sb-host-msg");
      msg.textContent = "serving at " + addr + "  (click to copy)";
      msg.style.color = "#9ece6a";
      msg.onclick = function () { try { navigator.clipboard.writeText(addr); msg.textContent = "copied: " + addr; } catch (e) {} };
    };

    // Window controls: minimize (hide, keep taskbar entry), maximize/restore
    // (fill the viewport above the taskbar; resizing disabled while maximized),
    // close (manager removes the window).
    var prevRect = null;
    win.maximize = function () {
      if (win.maximized) {
        if (prevRect) { wrap.style.left = prevRect.left; wrap.style.top = prevRect.top; wrap.style.width = prevRect.width; wrap.style.height = prevRect.height; }
        wrap.style.resize = "both"; win.maximized = false; return;
      }
      prevRect = { left: wrap.style.left, top: wrap.style.top, width: wrap.style.width, height: wrap.style.height };
      wrap.style.left = "0"; wrap.style.top = "0"; wrap.style.width = "100vw"; wrap.style.height = "calc(100vh - 2.8em)";
      wrap.style.resize = "none"; win.maximized = true;
    };
    win.show = function () { win.minimized = false; wrap.style.display = "flex"; };
    win.restore = win.show;
    win.minimize = function () { win.minimized = true; wrap.style.display = "none"; if (hooks.onMinimize) hooks.onMinimize(); };
    win.landHome = function () {
      // Land on home.dmsg (resolver alias for the deployment landing page),
      // matching the socks5 resolving proxy's default. Once per window.
      if (!wrap.dataset.landed) { wrap.dataset.landed = "1"; browser.browseTo("home.dmsg", "/"); }
    };
    $("sb-max").onclick = win.maximize;
    $("sb-min").onclick = win.minimize;
    $("sb-x").onclick = function () { if (hooks.onClose) hooks.onClose(); };

    // Drag-to-move via the title bar (the "skynet" label). Listeners are attached
    // only while dragging, so windows don't accumulate global handlers.
    (function () {
      var handle = wrap.querySelector(".sbw-bar b");
      if (!handle) return;
      var sx, sy, ox, oy;
      function mm(e) { wrap.style.left = Math.max(0, ox + e.clientX - sx) + "px"; wrap.style.top = Math.max(0, oy + e.clientY - sy) + "px"; }
      function mu() { doc.removeEventListener("mousemove", mm); doc.removeEventListener("mouseup", mu); }
      handle.addEventListener("mousedown", function (e) {
        if (win.maximized) return;
        sx = e.clientX; sy = e.clientY; var r = wrap.getBoundingClientRect(); ox = r.left; oy = r.top;
        doc.addEventListener("mousemove", mm); doc.addEventListener("mouseup", mu); e.preventDefault();
      });
    })();
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

  // mountPanel builds a multi-window "skynet" desktop into `doc`: a bottom taskbar
  // plus any number of independent browse windows (each its own dmsg virtual
  // browser), all draggable / resizable / minimizable / maximizable. opts:
  //   fetchDmsg, serveContent — the skywireVisor primitives; selfPK() — optional.
  // Backward-compatible surface: returns { panel, browser, toggle, openWindow }
  // where toggle() shows/hides the desktop (opening a first window on demand), so
  // the existing skynet launcher button keeps working unchanged.
  function mountPanel(doc, opts) {
    var zTop = 2147483000;
    var wins = [];
    var focused = null;
    var visible = false;

    var bar = doc.createElement("div");
    bar.id = "skywire-skynet-taskbar";
    bar.style.cssText = "position:fixed;left:0;right:0;bottom:0;z-index:2147483646;" +
      "display:none;gap:.5em;align-items:center;padding:.4em .6em;background:#0e0b16;" +
      "border-top:1px solid #2a2342;font:12px/1.3 monospace;color:#cdd2da";
    bar.innerHTML =
      '<b style="color:#9d7cff">skynet</b>' +
      '<button id="tb-new" title="new browse window" style="cursor:pointer">+ window</button>' +
      '<span id="tb-items" style="display:flex;gap:.35em;flex:1;flex-wrap:wrap;min-width:0"></span>' +
      '<button id="tb-id" title="export / import this visor\'s identity" style="cursor:pointer">identity</button>' +
      '<button id="tb-hide" title="hide skynet (windows stay open)" style="cursor:pointer">hide</button>';
    (doc.body || doc.documentElement).appendChild(bar);
    function bq(id) { return bar.querySelector("#" + id); }
    var items = bq("tb-items");

    function focus(win) {
      focused = win;
      win.el.style.zIndex = (++zTop);
      wins.forEach(function (w) { if (w.tab) w.tab.style.fontWeight = (w === win) ? "bold" : "normal"; });
    }
    function openWindow() {
      var win = createWindow(doc, opts, {
        onFocus: function () { focus(win); },
        onClose: function () { closeWindow(win); },
        onMinimize: function () { if (win.tab) win.tab.style.opacity = ".55"; },
        onTitle: function (t) { if (win.tab) win.tab.firstChild.textContent = t; }
      });
      wins.push(win);
      var tab = doc.createElement("button");
      tab.style.cssText = "cursor:pointer;max-width:14em;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;background:#1b1726;color:#cdd2da;border:1px solid #2a2342;border-radius:4px;padding:.2em .5em";
      tab.appendChild(doc.createTextNode("site"));
      tab.title = "focus / restore this window";
      tab.onclick = function () { win.show(); tab.style.opacity = "1"; focus(win); };
      items.appendChild(tab);
      win.tab = tab;
      focus(win);
      return win;
    }
    function closeWindow(win) {
      var i = wins.indexOf(win); if (i >= 0) wins.splice(i, 1);
      if (win.tab && win.tab.parentNode) win.tab.parentNode.removeChild(win.tab);
      if (win.el && win.el.parentNode) win.el.parentNode.removeChild(win.el);
      if (focused === win) focused = wins.length ? wins[wins.length - 1] : null;
    }

    bq("tb-new").onclick = function () { var w = openWindow(); w.landHome(); };
    bq("tb-hide").onclick = function () { setDesktop(false); };
    bq("tb-id").onclick = function () { openIdentityDialog(doc, opts); };

    function setDesktop(on) {
      visible = on;
      bar.style.display = on ? "flex" : "none";
      wins.forEach(function (w) { w.el.style.display = (on && !w.minimized) ? "flex" : "none"; });
      if (on && !wins.length) { var w = openWindow(); w.landHome(); }
    }
    function toggle() { setDesktop(!visible); }

    return {
      panel: bar,
      toggle: toggle,
      openWindow: openWindow,
      browser: function () { return focused ? focused.browser : null; }
    };
  }

  globalThis.SkywireBrowse = { createBrowser: createBrowser, mountPanel: mountPanel };
})();
