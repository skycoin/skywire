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
        await Promise.all(jobs);
        docHtml = "<!doctype html>" + doc.documentElement.outerHTML;
      } catch (e) {
        docHtml = html; // parse failed → render the raw HTML
      }
      frame.srcdoc = docHtml;
    }

    async function browseTo(pk, path) {
      pk = (pk || "").trim();
      path = path || "/";
      if (!pk) { log("browse: enter a site PK"); return { status: 0 }; }
      try {
        var r = await fetchDmsg(pk, "GET", path, null);
        var html = new TextDecoder().decode(r.body);
        await renderSite(pk, path, html);
        log("browsed dmsg://" + pk + path + " → " + r.status + " (" + r.body.length + " bytes)");
        return { status: r.status, bytes: r.body.length, html: html };
      } catch (e) { log("browse error: " + (e.message || e)); return { status: 0, error: String(e.message || e) }; }
    }

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

    return { renderSite: renderSite, browseTo: browseTo, currentPK: function () { return currentSitePK; } };
  }

  // mountPanel builds a floating, toggleable browse/host panel into `doc` (the
  // standalone-HV overlay surface) and wires it to a browser instance. opts:
  //   fetchDmsg, serveContent — the skywireVisor primitives; selfPK() — optional.
  // Returns { panel, browser, toggle }.
  function mountPanel(doc, opts) {
    var fetchDmsg = opts.fetchDmsg, serveContent = opts.serveContent;
    var wrap = doc.createElement("div");
    wrap.id = "skywire-browse-panel";
    // A large, RESIZABLE window (drag the bottom-right corner). Anchored top-left
    // so the resize grows into the viewport; the header is a drag handle (below).
    wrap.style.cssText = "position:fixed;top:5vh;left:5vw;width:74vw;height:82vh;" +
      "min-width:360px;min-height:260px;max-width:97vw;max-height:94vh;resize:both;" +
      "background:#15171c;color:#cdd2da;font:12px/1.4 monospace;border:1px solid #333;border-radius:8px;" +
      "box-shadow:0 8px 30px rgba(0,0,0,.5);z-index:2147483000;display:none;flex-direction:column;overflow:hidden";
    wrap.innerHTML =
      '<div style="display:flex;gap:.4em;align-items:center;padding:.5em;background:#1d2026;border-bottom:1px solid #333">' +
      '<b style="color:#7aa2f7">skynet</b>' +
      '<input id="sb-pk" placeholder="site pk or pk:port" style="flex:1;min-width:0;background:#0e0f12;color:#cdd2da;border:1px solid #333;padding:.25em">' +
      '<input id="sb-path" value="/" size="6" style="background:#0e0f12;color:#cdd2da;border:1px solid #333;padding:.25em">' +
      '<button id="sb-go" style="cursor:pointer">go</button>' +
      '<button id="sb-host-t" title="host a page" style="cursor:pointer">host</button>' +
      '<button id="sb-x" title="close" style="cursor:pointer">×</button>' +
      '</div>' +
      '<div id="sb-host" style="display:none;gap:.3em;padding:.5em;background:#1a1d22;border-bottom:1px solid #333;flex-direction:column">' +
      '<div style="display:flex;gap:.4em;align-items:center;flex-wrap:wrap">path <input id="sb-hpath" value="/" size="6" style="background:#0e0f12;color:#cdd2da;border:1px solid #333;padding:.2em">' +
      'port <input id="sb-hport" value="80" size="4" style="background:#0e0f12;color:#cdd2da;border:1px solid #333;padding:.2em">' +
      'type <input id="sb-hct" value="text/html" size="9" style="background:#0e0f12;color:#cdd2da;border:1px solid #333;padding:.2em">' +
      '<button id="sb-host-go" style="cursor:pointer">serve over dmsg</button></div>' +
      '<div style="display:flex;gap:.4em;align-items:center">file <input id="sb-hfile" type="file" style="flex:1;min-width:0;color:#cdd2da;font:11px monospace"></div>' +
      '<textarea id="sb-hbody" rows="3" placeholder="&lt;h1&gt;hosted from my browser, over dmsg&lt;/h1&gt; — or upload a file above" style="background:#0e0f12;color:#cdd2da;border:1px solid #333;font:12px monospace"></textarea>' +
      '<span id="sb-host-msg" style="color:#9ece6a;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;cursor:pointer" title="click to copy"></span>' +
      '</div>' +
      '<iframe id="sb-frame" sandbox="allow-scripts allow-forms" style="flex:1;width:100%;border:0;background:#fff"></iframe>';
    (doc.body || doc.documentElement).appendChild(wrap);

    function $(id) { return wrap.querySelector("#" + id); }
    var browser = createBrowser({
      frame: $("sb-frame"), fetchDmsg: fetchDmsg,
      log: function (m) { try { console.log("[skynet] " + m); } catch (e) {} },
      setPK: function (pk) { $("sb-pk").value = pk; }, setPath: function (p) { $("sb-path").value = p; }
    });
    function go() { browser.browseTo($("sb-pk").value, ($("sb-path").value || "/").trim() || "/"); }
    $("sb-go").onclick = go;
    $("sb-pk").addEventListener("keydown", function (e) { if (e.key === "Enter") go(); });
    $("sb-path").addEventListener("keydown", function (e) { if (e.key === "Enter") go(); });
    $("sb-x").onclick = function () { wrap.style.display = "none"; };
    $("sb-host-t").onclick = function () { var h = $("sb-host"); h.style.display = h.style.display === "none" ? "flex" : "none"; };

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

    // Drag-to-move via the "skynet" label (cursor:move); the window is large +
    // resizable (bottom-right handle), so let it be repositioned too.
    (function () {
      var handle = wrap.querySelector("b");
      if (!handle) return;
      handle.style.cursor = "move";
      var sx, sy, ox, oy, dragging = false;
      handle.addEventListener("mousedown", function (e) {
        dragging = true; sx = e.clientX; sy = e.clientY;
        var r = wrap.getBoundingClientRect(); ox = r.left; oy = r.top;
        e.preventDefault();
      });
      doc.addEventListener("mousemove", function (e) {
        if (!dragging) return;
        wrap.style.left = Math.max(0, ox + e.clientX - sx) + "px";
        wrap.style.top = Math.max(0, oy + e.clientY - sy) + "px";
      });
      doc.addEventListener("mouseup", function () { dragging = false; });
    })();

    function toggle() {
      var showing = wrap.style.display === "none";
      wrap.style.display = showing ? "flex" : "none";
      // First open: land on home.dmsg (resolver alias for the deployment landing
      // page), matching the socks5 resolving proxy's default.
      if (showing && !wrap.dataset.landed) {
        wrap.dataset.landed = "1";
        browser.browseTo("home.dmsg", "/");
      }
    }
    return { panel: wrap, browser: browser, toggle: toggle };
  }

  globalThis.SkywireBrowse = { createBrowser: createBrowser, mountPanel: mountPanel };
})();
