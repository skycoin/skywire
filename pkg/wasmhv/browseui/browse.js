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
  // storageShimSrc: the browsed iframe is sandboxed WITHOUT allow-same-origin, so
  // it runs in an OPAQUE origin — where accessing localStorage/sessionStorage
  // THROWS SecurityError. Sites (and wasm apps) that touch storage at startup then
  // crash — e.g. a Go/wasm app panics (exit 2) and its render loop dies, so the
  // page loads but never animates. Install an in-memory, per-page polyfill so such
  // sites run; nothing persists, which is the correct privacy posture for proxied
  // content anyway. Prepended to the injected shim so it runs before the page's
  // own scripts. (Granting allow-same-origin instead would let untrusted content
  // reach the visor's real origin + identity key — not an option.)
  function storageShimSrc() {
    return "(function(){function bad(g){try{var s=g();return !(s&&typeof s.getItem==='function');}catch(e){return true;}}" +
      "function mk(){var m=Object.create(null);return {getItem:function(k){k=String(k);return (k in m)?m[k]:null;}," +
      "setItem:function(k,v){m[String(k)]=String(v);},removeItem:function(k){delete m[String(k)];}," +
      "clear:function(){m=Object.create(null);},key:function(i){var ks=Object.keys(m);return (i>=0&&i<ks.length)?ks[i]:null;}," +
      "get length(){return Object.keys(m).length;}};}" +
      "if(bad(function(){return window.localStorage;})){try{Object.defineProperty(window,'localStorage',{value:mk(),configurable:true});}catch(e){}}" +
      "if(bad(function(){return window.sessionStorage;})){try{Object.defineProperty(window,'sessionStorage',{value:mk(),configurable:true});}catch(e){}}" +
      // history.replaceState/pushState THROW in an opaque (sandboxed srcdoc)
      // document when handed a cross-origin URL — apps that keep a permalink in
      // sync (e.g. on every param change) then panic mid-run. There's no real
      // address bar in the iframe, so wrap both to swallow that error and keep
      // running. (The proxy browser's own address bar reflects the page.)
      "try{var h=window.history;['replaceState','pushState'].forEach(function(m){var o=h&&h[m];if(typeof o==='function'){h[m]=function(){try{return o.apply(h,arguments);}catch(e){return undefined;}};}});}catch(e){}" +
      // navigator.serviceWorker THROWS a SecurityError when merely READ in an opaque
      // (sandboxed, no allow-same-origin) srcdoc — 'serviceWorker' in navigator is
      // true, but touching it throws. Apps that register a service worker at startup
      // (e.g. Create-React-App) then die before mounting. Replace it with a no-op
      // stub so feature-detection + register() resolve harmlessly and the app runs.
      "try{var swThrows=false;try{void navigator.serviceWorker;}catch(e){swThrows=true;}if(swThrows){var _sw={register:function(){return Promise.reject(new Error('service worker disabled (sandboxed)'));},getRegistration:function(){return Promise.resolve(undefined);},getRegistrations:function(){return Promise.resolve([]);},ready:new Promise(function(){}),addEventListener:function(){},removeEventListener:function(){},controller:null};Object.defineProperty(Navigator.prototype,'serviceWorker',{configurable:true,get:function(){return _sw;}});}}catch(e){}" +
      "})();";
  }

  function navShimSrc(path, cnMode) {
    return (
      storageShimSrc() +
      'var cur=' + JSON.stringify(path) + ';' +
      'function pathOf(h){try{var u=new URL(h,"http://dmsg"+cur);return u.pathname+u.search;}catch(e){return h;}}' +
      'function same(u){return !!u&&u.charAt(0)!=="#"&&!/^https?:\\/\\//i.test(u)&&!/^\\/\\//.test(u)&&!/^[a-z][a-z0-9+.-]*:/i.test(u);}' +
      // meshHost: an ABSOLUTE url whose host is a .dmsg/.skynet name or a bare
      // 66-hex PK is a MESH target, not clearnet — regardless of http/https
      // scheme. Without this, an absolute http://<name>.dmsg/ link/resource on an
      // HTTPS page was misclassified as clearnet and handed to the browser, which
      // blocked it as mixed content ("insecure resource http://skywire.dmsg/")
      // instead of the gateway routing it over the mesh.
      'function meshHost(u){try{var x=new URL(u,"http://dmsg"+cur);return /\\.(dmsg|skynet)$/i.test(x.hostname)||/^[0-9a-f]{66}$/i.test(x.hostname);}catch(e){return false;}}' +
      'function meshAbs(u){try{return new URL(u,"http://dmsg"+cur).href;}catch(e){return u;}}' +
      'document.addEventListener("click",function(e){' +
      'var a=e.target.closest?e.target.closest("a[href]"):null;if(!a)return;' +
      'var h=a.getAttribute("href")||"";' +
      'if(same(h)){e.preventDefault();parent.postMessage({type:"dmsgnav",path:pathOf(h)},"*");return;}' +      // same-site → dmsg path nav
      'if(meshHost(h)){e.preventDefault();parent.postMessage({type:"dmsgnav",url:meshAbs(h)},"*");return;}' +  // cross-site .dmsg/.skynet → resolve+navigate in parent
      '},true);' +
      'var _rq=0,_pend={};' +
      'window.addEventListener("message",function(e){var d=e.data||{};if(d.type!=="dmsgreply")return;var p=_pend[d.id];if(!p)return;delete _pend[d.id];p(d);});' +
      'function relay(t,m,b,cn){return new Promise(function(res){var id=++_rq;_pend[id]=res;parent.postMessage({type:"dmsgreq",id:id,path:t,method:m||"GET",body:b||null,clearnet:!!cn},"*");});}' +
      'function _toResp(r){var body=r.body?Uint8Array.from(atob(r.body),function(c){return c.charCodeAt(0);}):new Uint8Array();return new Response(body,{status:r.status||200,headers:{"Content-Type":r.ct||"application/octet-stream"}});}' +
      'var CNMODE=' + JSON.stringify(cnMode || "block") + ';' +
      'var _f=window.fetch;window.fetch=function(input,init){var u=typeof input==="string"?input:(input&&input.url);var m=(init&&init.method)||"GET",b=(init&&init.body)||null;' +
      'if(same(u))return relay(pathOf(u),m,b,false).then(_toResp);' +           // same-site → dmsg
      // A cross-site mesh (.dmsg/.skynet) resource must NOT reach the browser as
      // http:// (mixed content on an HTTPS page). We don''t inline cross-site mesh
      // subresources here — block it so the page degrades gracefully; a mesh LINK
      // is instead routed via the click handler above.
      'if(meshHost(u))return Promise.resolve(new Response("",{status:502,headers:{"Content-Type":"text/plain"}}));' +
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

  // clearnetShimSrc is JS injected at the top of a PROXY-rendered clearnet page.
  // Clearnet scripts are stripped (static render), so the page's own JS never
  // runs — this restores the two things a reader needs: (1) link clicks navigate
  // to the resolved absolute URL through the proxy (parent "cnnav"); (2) form
  // submits (search boxes!) are serialized and routed through the proxy —
  // method=GET → action?query, method=POST → body — since the CSP's
  // form-action 'none' + the sandbox otherwise make a submit a no-op. Relative
  // URLs resolve against the page's real URL (BASE).
  function clearnetShimSrc(baseUrl) {
    return (
      storageShimSrc() +
      'var BASE=' + JSON.stringify(baseUrl) + ';' +
      'function abs(h){try{return new URL(h,BASE).href;}catch(e){return "";}}' +
      'document.addEventListener("click",function(e){' +
      'var a=e.target.closest?e.target.closest("a[href]"):null;if(!a)return;' +
      'var h=a.getAttribute("href")||"";if(!h||h.charAt(0)==="#")return;' +
      'var u=abs(h);if(/^https?:\\/\\//i.test(u)){e.preventDefault();parent.postMessage({type:"cnnav",url:u},"*");}' +
      '},true);' +
      'document.addEventListener("submit",function(e){' +
      'var f=e.target;if(!f||f.tagName!=="FORM")return;e.preventDefault();' +
      'var method=((f.getAttribute("method")||"GET")).toUpperCase();' +
      'var action=abs(f.getAttribute("action")||BASE)||BASE;' +
      'var fd=new URLSearchParams();' +
      'var sub=e.submitter;if(sub&&sub.name)fd.append(sub.name,sub.value||"");' +
      'for(var i=0;i<f.elements.length;i++){var el=f.elements[i];' +
      'if(!el.name||el.disabled)continue;' +
      'if((el.type==="checkbox"||el.type==="radio")&&!el.checked)continue;' +
      'if(el.type==="submit"||el.type==="button"||el.type==="file"||el.type==="image")continue;' +
      'fd.append(el.name,el.value);}' +
      'if(method==="POST"){parent.postMessage({type:"cnnav",url:action,method:"POST",body:fd.toString()},"*");}' +
      'else{try{var uo=new URL(action);uo.search=fd.toString();parent.postMessage({type:"cnnav",url:uo.href},"*");}catch(_){}}' +
      '},true);'
    );
  }

  // clearnetDynamicShimSrc is injected at the top of a DYNAMIC-mode clearnet page,
  // BEFORE the page's own (inlined) scripts run. It makes the page's runtime network
  // work through the exit without ever touching clearnet directly: window.fetch and
  // XMLHttpRequest are patched to relay http(s) requests to the parent (dmsgreq,
  // clearnet:true → fetchClearnet → exit) and reconstruct the response in-tab; a
  // MutationObserver rewrites any http(s) <img> the page adds to a data: URI fetched
  // the same way. Link clicks / form submits are relayed like the static shim. The
  // dynamic CSP keeps connect-src off http, so anything NOT patched here simply
  // fails closed (no IP leak) rather than reaching clearnet directly.
  function clearnetDynamicShimSrc(baseUrl) {
    return (
      storageShimSrc() +
      'var BASE=' + JSON.stringify(baseUrl) + ';' +
      'function abs(h){try{return new URL(h,BASE).href;}catch(e){return h;}}' +
      'function isCn(u){return /^https?:\\/\\//i.test(u||"");}' +
      'var _rq=0,_pend={};' +
      'window.addEventListener("message",function(e){var d=e.data||{};if(d.type!=="dmsgreply")return;var p=_pend[d.id];if(!p)return;delete _pend[d.id];p(d);});' +
      'function relay(u,m,b,h){return new Promise(function(res){var id=++_rq;_pend[id]=res;parent.postMessage({type:"dmsgreq",id:id,path:u,method:m||"GET",body:(typeof b==="string"?b:null),headers:h||null,clearnet:true},"*");});}' +
      'function _bytes(r){return r.body?Uint8Array.from(atob(r.body),function(c){return c.charCodeAt(0);}):new Uint8Array();}' +
      // fetch(): resolve relative → absolute, relay http(s), pass through data:/blob:
      'var _f=window.fetch;window.fetch=function(input,init){var u=typeof input==="string"?input:(input&&input.url);u=abs(u);var m=(init&&init.method)||"GET",b=(init&&init.body)||null;' +
      'if(!isCn(u))return _f.apply(this,arguments);' +
      'var hh=null;try{if(init&&init.headers){hh={};if(init.headers.forEach)init.headers.forEach(function(v,k){hh[k]=v;});else Object.keys(init.headers).forEach(function(k){hh[k]=init.headers[k];});}}catch(_){}' +
      'return relay(u,m,b,hh).then(function(r){return new Response(_bytes(r),{status:r.status||200,headers:{"Content-Type":r.ct||"application/octet-stream"}});});};' +
      // XMLHttpRequest (Angular's HttpClient default): patch open/send to relay
      '(function(){var _o=XMLHttpRequest.prototype.open,_s=XMLHttpRequest.prototype.send,_h=XMLHttpRequest.prototype.setRequestHeader;' +
      'XMLHttpRequest.prototype.open=function(m,u){this.__m=m;this.__u=abs(u);this.__h={};this.__cn=isCn(this.__u);return _o.apply(this,[m,this.__cn?this.__u:u].concat([].slice.call(arguments,2)));};' +
      'XMLHttpRequest.prototype.setRequestHeader=function(k,v){if(this.__cn){this.__h[k]=v;return;}return _h.apply(this,arguments);};' +
      'XMLHttpRequest.prototype.send=function(b){var x=this;if(!x.__cn)return _s.apply(x,arguments);' +
      'function def(k,val){try{Object.defineProperty(x,k,{configurable:true,get:function(){return val;}});}catch(e){}}' +
      'relay(x.__u,x.__m,b,x.__h).then(function(r){var by=_bytes(r);var tx="";try{tx=new TextDecoder().decode(by);}catch(e){}' +
      'def("readyState",4);def("status",r.status||200);def("statusText","");def("responseURL",x.__u);def("responseText",tx);' +
      'def("response",(x.responseType==="arraybuffer")?by.buffer:(x.responseType==="blob"?new Blob([by]):(x.responseType==="json"?(function(){try{return JSON.parse(tx);}catch(e){return null;}})():tx)));' +
      'x.getAllResponseHeaders=function(){return "content-type: "+(r.ct||"application/octet-stream")+"\\r\\n";};' +
      'x.getResponseHeader=function(k){return /content-type/i.test(k)?(r.ct||"application/octet-stream"):null;};' +
      'try{if(typeof x.onreadystatechange==="function")x.onreadystatechange();}catch(e){}x.dispatchEvent(new Event("readystatechange"));' +
      'try{if(typeof x.onload==="function")x.onload();}catch(e){}x.dispatchEvent(new Event("load"));x.dispatchEvent(new Event("loadend"));' +
      '}).catch(function(){def("readyState",4);def("status",0);x.dispatchEvent(new Event("readystatechange"));try{if(typeof x.onerror==="function")x.onerror();}catch(e){}x.dispatchEvent(new Event("error"));x.dispatchEvent(new Event("loadend"));});};})();' +
      // dynamically-injected external scripts (code-split chunks): intercept the .src
      // setter so a webpack/Gatsby-style loader''s http(s) chunk is fetched via the
      // exit and applied as a data: URI (raw http script-src is CSP-blocked). onload/
      // onerror still fire off the data: script, so the loader''s chunk-ready wiring
      // works. ES-module import() fetches are NOT interceptable this way (browser-
      // internal) — those sites still degrade.
      '(function(){var d=Object.getOwnPropertyDescriptor(HTMLScriptElement.prototype,"src");if(!d||!d.set)return;' +
      'Object.defineProperty(HTMLScriptElement.prototype,"src",{configurable:true,enumerable:true,get:function(){return this.__s||(d.get?d.get.call(this):"");},' +
      'set:function(v){var u=abs(v);if(!isCn(u))return d.set.call(this,v);var self=this;self.__s=u;' +
      'relay(u,"GET",null).then(function(r){if(r&&r.body&&(r.status||200)<400)d.set.call(self,"data:text/javascript;base64,"+r.body);else self.dispatchEvent(new Event("error"));}).catch(function(){self.dispatchEvent(new Event("error"));});}});})();' +
      // dynamic images: rewrite any http(s) <img> src to a data: URI fetched via exit
      'function _img(el){var s=el.getAttribute&&el.getAttribute("src");if(!s)return;var u=abs(s);if(!isCn(u))return;if(el.__d===u)return;el.__d=u;' +
      'relay(u,"GET",null).then(function(r){if(r&&r.body&&(r.status||200)<400)el.src="data:"+(r.ct||"application/octet-stream")+";base64,"+r.body;}).catch(function(){});}' +
      'function _scan(root){var ns=(root&&root.querySelectorAll)?root.querySelectorAll("img[src]"):[];for(var i=0;i<ns.length;i++)_img(ns[i]);}' +
      'document.addEventListener("DOMContentLoaded",function(){_scan(document);});' +
      'new MutationObserver(function(ms){ms.forEach(function(m){if(m.type==="attributes"){if(m.target&&m.target.tagName==="IMG")_img(m.target);return;}if(!m.addedNodes)return;for(var i=0;i<m.addedNodes.length;i++){var n=m.addedNodes[i];if(n.nodeType!==1)continue;if(n.tagName==="IMG")_img(n);_scan(n);}});}).observe(document.documentElement,{childList:true,subtree:true,attributes:true,attributeFilter:["src"]});' +
      // link clicks + form submits → relay to the parent (history-tracked navigation)
      'document.addEventListener("click",function(e){var a=e.target.closest?e.target.closest("a[href]"):null;if(!a)return;var h=a.getAttribute("href")||"";if(!h||h.charAt(0)==="#")return;var u=abs(h);if(isCn(u)){e.preventDefault();parent.postMessage({type:"cnnav",url:u},"*");}},true);' +
      'document.addEventListener("submit",function(e){var f=e.target;if(!f||f.tagName!=="FORM")return;e.preventDefault();var method=((f.getAttribute("method")||"GET")).toUpperCase();var action=abs(f.getAttribute("action")||BASE)||BASE;var fd=new URLSearchParams();var sub=e.submitter;if(sub&&sub.name)fd.append(sub.name,sub.value||"");for(var i=0;i<f.elements.length;i++){var el=f.elements[i];if(!el.name||el.disabled)continue;if((el.type==="checkbox"||el.type==="radio")&&!el.checked)continue;if(el.type==="submit"||el.type==="button"||el.type==="file"||el.type==="image")continue;fd.append(el.name,el.value);}if(method==="POST"){parent.postMessage({type:"cnnav",url:action,method:"POST",body:fd.toString()},"*");}else{try{var uo=new URL(action);uo.search=fd.toString();parent.postMessage({type:"cnnav",url:uo.href},"*");}catch(_){}}},true);'
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
    // h (optional) is a request-headers object forwarded to the exit (6th arg) —
    // used by dynamic mode so a page's XHR/fetch keeps its Content-Type etc.
    var fetchClearnet = function (exit, m, u, b, h) { return rawFetchClearnet(exit, m, u, b, winId, h || null); };
    var log = opts.log || function () {};
    var currentSitePK = "";

    // --- real-origin mode (RFC §4a/§4b) -------------------------------------
    // When the host injected __SKYWIRE_BROWSE_ORIGIN__, a dmsg/skynet site loads
    // from a REAL, isolated origin "<label>[.<net>]<suffix>:<port>" instead of the
    // sandboxed-srcdoc transcoder — so the BROWSER does native subresource
    // loading, cookies, redirects, WASM and streaming, and the visor only proxies
    // the transport (native = local reverse-proxy origin on that port; wasm = the
    // origin's Service Worker → this tab's visor). Transcoder stays as fallback.
    var realOriginCfg = (typeof globalThis !== "undefined" && globalThis.__SKYWIRE_BROWSE_ORIGIN__) || null;
    var B32 = "abcdefghijklmnopqrstuvwxyz234567"; // RFC4648 base32 lowercase (= cipher.DNSLabel)
    function pkDNSLabel(hexPK) {
      var bytes = [];
      for (var i = 0; i + 1 < hexPK.length; i += 2) { bytes.push(parseInt(hexPK.substr(i, 2), 16)); }
      var out = "", bits = 0, val = 0;
      for (var j = 0; j < bytes.length; j++) {
        val = (val << 8) | bytes[j]; bits += 8;
        while (bits >= 5) { out += B32[(val >>> (bits - 5)) & 31]; bits -= 5; }
      }
      if (bits > 0) { out += B32[(val << (5 - bits)) & 31]; }
      return out;
    }
    // labelFor: a 66-hex PK → 53-char base32 DNS label (hex is too long for a DNS
    // label); an alias / already-base32 label passes through unchanged.
    function labelFor(pk) { return /^[0-9a-fA-F]{66}$/.test(pk) ? pkDNSLabel(pk) : pk; }
    // Content-addressed browse origins: origin B is a short, STABLE hash of the
    // target — not the target itself — so ONE wildcard cert covers every site
    // regardless of PK length, clearnet subdomain depth, or dmsg name-vhosts. The
    // visor keeps shortid -> descriptor; the B bootstrap asks V for its descriptor
    // by id at handshake (see browse-responder.js / browse-bootstrap.html).
    var meshOrigins = (globalThis.__meshOrigins = globalThis.__meshOrigins || {});
    function originIdFor(canon) {
      return crypto.subtle.digest("SHA-256", new TextEncoder().encode(canon)).then(function (buf) {
        var b = new Uint8Array(buf), out = "", bits = 0, val = 0;
        for (var i = 0; i < b.length && out.length < 20; i++) {
          val = (val << 8) | b[i]; bits += 8;
          while (bits >= 5 && out.length < 20) { out += B32[(val >>> (bits - 5)) & 31]; bits -= 5; }
        }
        return out;
      });
    }
    // normResolverHost canonicalizes a dmsg/skynet resolver host: strips a trailing
    // .dmsg/.skynet, base32-encodes any 66-hex PK label, re-appends ".<net>". Keeps
    // aliases / base32 / name-vhost labels intact. So "<hex>.dmsg", "<base32>.dmsg",
    // "magnetosphere.net.<hex>.dmsg" all normalize to a stable "<...>.<net>".
    function normResolverHost(pk, net) {
      var h = String(pk || "").replace(/\.(dmsg|skynet)$/i, "");
      h = h.split(".").map(function (l) { return /^[0-9a-fA-F]{66}$/.test(l) ? pkDNSLabel(l) : l; }).join(".");
      return h + "." + net;
    }
    // buildRealOrigin(descriptor, path) -> Promise<url>. descriptor is
    //   {net:'dmsg'|'skynet', host:'<resolverHost>'} or {net:'skysocks', base:'https://<host>'}.
    function buildRealOrigin(descriptor, path) {
      var c = realOriginCfg;
      var canon = descriptor.net === "skysocks" ? ("skysocks|" + descriptor.base) : (descriptor.net + "|" + descriptor.host);
      return originIdFor(canon).then(function (id) {
        meshOrigins[id] = descriptor;
        return (c.scheme || "https") + "://" + id + (c.suffix || ".mesh.localhost") + (c.port ? (":" + c.port) : "") + (path || "/");
      });
    }

    // 1x1 transparent GIF — placeholder src for a deferred (lazy) image so it
    // occupies layout without a broken-image flash until the real bytes arrive.
    var BLANK_IMG = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7";
    // A neutral default title-bar icon (a violet globe) shown while a site's own
    // favicon is fetched — or when it has none. Inline SVG data URI (no fetch).
    var DEFAULT_ICON = "data:image/svg+xml;base64," + btoa(
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="#9d7cff" stroke-width="1.3">' +
      '<circle cx="8" cy="8" r="6.3"/><path d="M1.7 8h12.6M8 1.7c2 2.4 2 10.2 0 12.6M8 1.7c-2 2.4-2 10.2 0 12.6"/></svg>');
    // faviconHref picks the best <link rel=icon> from a parsed doc (falling back to
    // /favicon.ico), resolved to an absolute URL against baseURL.
    function faviconHref(doc, baseURL) {
      var sels = ["link[rel~='icon' i]", "link[rel='shortcut icon' i]", "link[rel='apple-touch-icon' i]"];
      for (var i = 0; i < sels.length; i++) {
        var el = doc.querySelector(sels[i]);
        var href = el && el.getAttribute("href");
        if (href) { try { return new URL(href, baseURL).href; } catch (e) {} }
      }
      try { return new URL("/favicon.ico", baseURL).href; } catch (e) { return null; }
    }
    // faviconGen guards against a stale favicon fetch clobbering a newer navigation.
    // resetFavicon (on every nav) shows the default and invalidates in-flight fetches.
    var faviconGen = 0;
    function resetFavicon() { faviconGen++; if (opts.setIcon) { try { opts.setIcon(DEFAULT_ICON); } catch (e) {} } }
    // setFaviconVia fetches the page's favicon over the SAME channel the page came
    // in on (clearnet exit / dmsg) and shows it in the WinBox title bar. Best-effort
    // and non-blocking: a missing/slow/oversized/non-image favicon never affects the
    // render — it just leaves the default icon in place. fetchIcon(absURL) returns
    // the same {status, body, headers} shape as fetchDmsg/fetchClearnet.
    function setFaviconVia(fetchIcon, doc, baseURL) {
      if (!opts.setIcon) return;
      var u = faviconHref(doc, baseURL);
      if (!u) return;
      var myGen = ++faviconGen;
      Promise.resolve().then(function () { return fetchIcon(u); }).then(function (r) {
        if (myGen !== faviconGen) return;                  // superseded by a newer nav
        if (!r || (r.status || 200) >= 400 || !r.body || !r.body.length) return;
        if (r.body.length > 262144) return;                // sanity cap (256KB)
        var ct = ctOf(r.headers, u);
        if (!/^image\//i.test(ct)) return;                 // not an image → ignore
        try { opts.setIcon("data:" + ct + ";base64," + bytesToB64(r.body)); } catch (e) {}
      }).catch(function () {});
    }
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

    // Clearnet analogue of inlineCss: rewrite url(...) refs in a fetched clearnet
    // stylesheet by re-fetching each through the SAME skysocks exit and inlining
    // as data: — so CSS background-images and fonts (e.g. a site's logo drawn via
    // background-image) survive the strict CSP instead of being blocked.
    function inlineCssClearnet(exit, baseURL, css) {
      var uniq = [...new Set([...css.matchAll(CSS_URL)].map(function (m) { return m[2]; })
        .filter(function (u) { return u && !/^data:/i.test(u); }))];
      if (!uniq.length) return Promise.resolve(css);
      var map = {};
      return Promise.all(uniq.map(function (u) {
        var abs; try { abs = new URL(u, baseURL).href; } catch (e) { return; }
        if (!/^https?:\/\//i.test(abs)) return;
        return fetchClearnet(exit, "GET", abs, null)
          .then(function (r) { if (r && r.body && (r.status || 200) < 400) map[u] = "data:" + ctOf(r.headers, abs) + ";base64," + bytesToB64(r.body); })
          .catch(function () {});
      })).then(function () {
        return css.replace(CSS_URL, function (m, q, u) { return map[u] ? 'url("' + map[u] + '")' : m; });
      });
    }

    async function renderSite(pk, path, html, scheme) {
      hideConnecting();
      currentSitePK = pk;
      // Preserve the scheme the user navigated with (http/https) — the fetch is
      // over dmsg either way (Noise-encrypted, no in-tab TLS), so the scheme is
      // cosmetic, but echoing back what was entered avoids a surprising rewrite.
      if (opts.setAddr) opts.setAddr((scheme || "http") + "://" + pk + (path || "/"));
      var docHtml;
      try {
        var doc = new DOMParser().parseFromString(html, "text/html");
        // Show the site's favicon in the title bar (fetched over dmsg, same site).
        setFaviconVia(function (u) {
          var p; try { var x = new URL(u); p = x.pathname + (x.search || ""); } catch (e) { p = "/favicon.ico"; }
          return fetchDmsg(pk, "GET", p, null);
        }, doc, "http://dmsg" + path);
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
    // dmsgStatus resolves the visor's connection status ({dmsg_connected,
    // dmsg_sessions}). Returns {} if the core isn't booted yet (status() throws).
    function dmsgStatus() {
      return Promise.resolve().then(function () {
        var v = globalThis.skywireVisor;
        if (v && v.status) { return v.status() || {}; }
        // Native / hosted HV UI (directViaBackend, no in-tab wasm core to
        // introspect): the BACKEND visor owns the dmsg connection and fetchDmsg
        // is proxied to it over /api/browse/*, so the page can't read a session
        // count. Report "connected" — otherwise waitForDmsg spins the full
        // connect-overlay timeout on EVERY dmsg navigation even though the visor
        // is connected (the bug that made every .dmsg site show "Connecting to
        // the mesh…"). A genuinely-disconnected visor still surfaces as a real
        // fetchDmsg error, not an endless spinner.
        if (opts.directViaBackend) { return { dmsg_connected: true }; }
        return {};
      }).catch(function () { return {}; });
    }
    // dmsg is "up" when there's a live session. Prefer the explicit fields
    // (dmsg_connected / dmsg_sessions), but also treat "dmsg client exists AND
    // has transports" as connected: transports run over dmsg sessions, so
    // having them means dmsg is up. This fallback matters for an already-booted
    // shared visor whose status() (older wasm blobs) reports dmsg+transports but
    // not the session count — without it the deep-link "Connecting…" overlay
    // spun the full timeout on an already-connected visor.
    function dmsgUp(st) { return !!(st && (st.dmsg_connected || (st.dmsg_sessions | 0) > 0 || (st.dmsg && (st.transports | 0) > 0))); }

    // --- live connection journey ------------------------------------------
    // A dmsg/skynet connection is a multi-step, adaptive process, not a single
    // request → status code. So instead of one terminal error we show the
    // SEQUENCE OF EVENTS as it happens — dmsg boot/seeding, session
    // establishment, (and, once routed through the log ring, route setup) —
    // sourced live from the visor's own log stream (runtime-logs, cursor-
    // streamed). Shown while connecting; frozen as the trail if the attempt
    // ultimately fails, so the "error page" is the whole journey, not a code.
    var journeyLines = [], journeyCursor = 0;
    function selfPKOf() { try { return (opts.selfPK && opts.selfPK()) || ""; } catch (e) { return ""; } }
    function resetJourney() { journeyLines = []; journeyCursor = 0; }
    function pollJourney() {
      var api = globalThis.skywireVisor && globalThis.skywireVisor.hvApi;
      if (!api) { return Promise.resolve(); }
      // Resolve the self PK from status() (the runtime-logs route is keyed by
      // the visor PK), falling back to opts.selfPK() — status().pk is the source
      // that's reliably populated in the standalone.
      return dmsgStatus().then(function (st) {
        var pk = (st && st.pk) || selfPKOf();
        if (!pk) { return; }
        return Promise.resolve(api("GET", "/api/visors/" + pk + "/runtime-logs?since=" + journeyCursor, null))
          .then(function (r) {
            var j = JSON.parse(new TextDecoder().decode(r.body));
            if (typeof j.latest === "number") { journeyCursor = j.latest; }
            (j.entries || []).forEach(function (e) {
              var msg = e; try { var o = JSON.parse(e); msg = o.msg || e; } catch (_) {}
              journeyLines.push(String(msg).replace(/^\[visor\]\s*/, ""));
            });
            if (journeyLines.length > 80) { journeyLines = journeyLines.slice(-80); }
          });
      }).catch(function () {});
    }
    function journeyHTML(maxLines) {
      var tail = journeyLines.slice(-(maxLines || 12));
      if (!tail.length) { return ""; }
      return '<div style="text-align:left;margin-top:1.1rem;max-height:40vh;overflow:auto;background:#08060d;border:1px solid #1c1830;border-radius:6px;padding:.6em .8em;font:11px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;color:#8b93a7;white-space:pre-wrap;word-break:break-word">' +
        tail.map(function (l) { return esc(l); }).join('\n') + '</div>';
    }

    // The connecting/journey state renders into the sb-connect DOM OVERLAY (a
    // sibling of the iframe), NOT the iframe's srcdoc — so it never reloads the
    // iframe. The shell (spinner) is rendered ONCE; only the dynamic parts
    // (session count + journey) are mutated each tick, so the spinner animation
    // stays smooth and nothing strobes.
    function connectEl() { try { return frame.parentNode && frame.parentNode.querySelector("#sb-connect"); } catch (e) { return null; } }
    function hideConnecting() { var el = connectEl(); if (el) { el.style.display = "none"; el.innerHTML = ""; } }
    function showConnecting(target) {
      var el = connectEl(); if (!el) { return; }
      el.innerHTML =
        '<div style="min-height:100%;box-sizing:border-box;display:flex;align-items:center;justify-content:center;padding:1.5rem">' +
        '<div style="max-width:520px;width:100%;text-align:center">' +
        '<div style="width:26px;height:26px;margin:0 auto 1.1rem;border:3px solid #2a2342;border-top-color:#9d7cff;border-radius:50%;animation:sbspin 1s linear infinite"></div>' +
        '<div style="color:#9d7cff;font-weight:600;margin-bottom:.5rem">Connecting to the mesh…</div>' +
        '<div style="opacity:.75">Establishing a dmsg session to reach<br><b style="word-break:break-all">' + esc(target) + '</b></div>' +
        '<div id="sb-connect-status" style="opacity:.5;font-size:.85em;margin-top:.9rem"></div>' +
        '<div id="sb-connect-journey"></div>' +
        '</div></div>' +
        '<style>@keyframes sbspin{to{transform:rotate(360deg)}}</style>';
      el.style.display = "block";
    }
    function updateConnecting(sessions, note) {
      var el = connectEl(); if (!el) { return; }
      var s = el.querySelector("#sb-connect-status");
      if (s) { s.textContent = "dmsg sessions: " + (sessions | 0) + (note ? " · " + note : ""); }
      var j = el.querySelector("#sb-connect-journey");
      if (j) { j.innerHTML = journeyHTML(12); }
    }

    // waitForDmsg holds a dmsg navigation until the visor has a live dmsg
    // session, showing the connecting overlay + live journey meanwhile. Returns
    // when connected, when a newer navigation supersedes this one, or after ~30s
    // (then the caller proceeds; the overlay is cleared by renderSite/showError).
    async function waitForDmsg(entry, gen) {
      var st = await dmsgStatus();
      if (dmsgUp(st)) { return; }
      resetJourney();
      showConnecting(entry.pk);
      for (var i = 0; i < 60; i++) {
        if (gen !== loadGen) { hideConnecting(); return; }
        await pollJourney();
        updateConnecting((st && st.dmsg_sessions) | 0, i > 20 ? "still connecting…" : "");
        await new Promise(function (r) { setTimeout(r, 500); });
        st = await dmsgStatus();
        if (dmsgUp(st)) { return; }
      }
    }

    async function render(entry) {
      var gen = ++loadGen;
      resetFavicon(); // default icon now; the site's own favicon replaces it once fetched
      setLoading(true);
      try {
        // Real-origin: hand the whole load to the browser via an isolated origin.
        if (realOriginCfg && entry.kind === "dmsg") {
          var dnet = (entry.scheme === "skynet") ? "skynet" : "dmsg";
          var rurl = await buildRealOrigin({ net: dnet, host: normResolverHost(entry.pk, dnet) }, entry.path);
          frame.removeAttribute("srcdoc");
          frame.src = rurl;
          currentSitePK = entry.pk;
          try { if (typeof hideConnecting === "function") { hideConnecting(); } } catch (e) {}
          if (opts.setAddr) {
            // Show a readable, re-typable address WITH a protocol, e.g.
            // "http://<pk>.dmsg/path" (or https://, or .skynet). A host already
            // carrying a dot (name-vhost / alias.dmsg) keeps it. The scheme is the
            // HTTP scheme carried over the mesh transport (http unless TLS/https).
            var disp = entry.pk;
            if (disp.indexOf(".") < 0) { disp += (entry.scheme === "skynet" ? ".skynet" : ".dmsg"); }
            if (entry.path && entry.path !== "/") { disp += entry.path; }
            opts.setAddr((entry.scheme === "https" ? "https" : "http") + "://" + disp);
          }
          log("real-origin " + rurl);
          setLoading(false);
          return { status: 0, realOrigin: true };
        }
        if (entry.kind === "clearnet") {
          var rc = await fetchClearnetEntry(entry.url, gen);
          return rc;
        }
        // Wait for a live dmsg session before fetching (shows connectingPage),
        // so a navigation issued during boot doesn't render a "not booted" error.
        await waitForDmsg(entry, gen);
        if (gen !== loadGen) return { status: 0, cancelled: true };
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
        // For dmsg targets, freeze the connection journey below the error so the
        // trail (what was tried, incl. any dmsg error codes) is visible — not
        // just the terminal message.
        if (entry.kind !== "clearnet") { await pollJourney(); }
        var where = entry.kind === "clearnet" ? entry.url : ("dmsg://" + entry.pk + (entry.path || ""));
        var trail = (entry.kind !== "clearnet" && journeyLines.length) ? (msg + "\n\n— connection log —\n" + journeyLines.slice(-24).join("\n")) : msg;
        showError("Couldn't reach this site", where, trail);
        return { status: 0, error: msg };
      } finally {
        if (gen === loadGen) setLoading(false);
      }
    }

    async function fetchClearnetEntry(url, gen) {
      hideConnecting();
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
      // Real-origin clearnet: load the site at its own isolated origin
      // <host>.skysocks.<suffix>; the origin's SW relays every fetch through
      // this visor's skysocks-lite. (Transcoder below stays as the fallback.)
      if (realOriginCfg) {
        var cu; try { cu = new URL(url); } catch (e) { cu = null; }
        if (cu && (cu.protocol === "http:" || cu.protocol === "https:")) {
          if (gen !== loadGen) return { status: 0, cancelled: true };
          var crurl = await buildRealOrigin({ net: "skysocks", base: cu.origin }, cu.pathname + cu.search);
          currentSitePK = "";
          frame.removeAttribute("srcdoc");
          frame.src = crurl;
          if (opts.setAddr) opts.setAddr(url); // show the REAL clearnet URL, not the B origin
          log("real-origin clearnet " + url);
          setLoading(false);
          return { status: 0, realOrigin: true };
        }
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
    // Dynamic mode: run a clearnet page's OWN scripts and proxy its runtime network
    // (fetch / XHR / images) through the exit, so JS-rendered sites (SPAs) work like
    // in a normal browser. Off by default — static render is lighter and doesn't
    // execute untrusted site JS. Per-window with a persisted global default. Egress
    // still goes only through the postMessage bridge → exit (the dynamic CSP keeps
    // connect-src off http), so it stays IP-anonymous.
    var winDynamic = null; // per-window override; null → global default
    function globalDynamic() { try { return localStorage.getItem("skywire-clearnet-dynamic") === "1"; } catch (_) { return false; } }
    function clearnetDynamic() { return winDynamic !== null ? winDynamic : globalDynamic(); }
    function setClearnetDynamic(on) { winDynamic = !!on; try { localStorage.setItem("skywire-clearnet-dynamic", on ? "1" : "0"); } catch (_) {} }
    // clearnetPolicy: {mode:'block'} | {mode:'direct'} | {mode:'proxy', exit}.
    function clearnetPolicy() {
      var up = upstream();
      if (up === "block") return { mode: "block" }; // explicit opt-out
      // Default (unset) and "auto" both proxy through the DEFAULT
      // skysocks-client-lite instance so regular clearnet sites JUST WORK. Passing
      // exit "" makes the wasm side resolve the current auto-selected (and
      // failover-managed) pool exit per fetch, so browsing survives a dead exit —
      // a fetch retries across exits and waits for the pool to re-select. A user
      // can still pin a specific exit, or "block", via the ⚙ menu.
      if (!up || up === "auto") return { mode: "proxy", exit: "" };
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
      // proxy: static render drops clearnet scripts; dynamic mode inlines them (as a
      // text/javascript data: URI, so they execute in order) so the page's JS runs.
      if (el.tagName === "SCRIPT") {
        if (!policy.dynamic) { el.remove(); return null; }
        return fetchClearnet(policy.exit, "GET", absURL, null).then(function (r) {
          if (r && r.body && (r.status || 200) < 400) el.setAttribute("src", "data:text/javascript;base64," + bytesToB64(r.body));
          else el.remove();
        }).catch(function () { el.remove(); });
      }
      return fetchClearnet(policy.exit, "GET", absURL, null).then(function (r) {
        if (el.tagName === "LINK") {
          // Rewrite url() refs inside the fetched stylesheet (background-images,
          // fonts, @import assets) through the same exit before inlining it.
          return inlineCssClearnet(policy.exit, absURL, new TextDecoder().decode(r.body)).then(function (css) {
            var s = doc.createElement("style"); s.textContent = css; el.replaceWith(s);
          });
        }
        el.setAttribute(attr, "data:" + ctOf(r.headers, absURL) + ";base64," + bytesToB64(r.body));
      }).catch(function () { if (el.tagName === "LINK") el.remove(); else el.removeAttribute(attr); });
    }

    // gateAllClearnet walks every resource element and applies the policy to those
    // whose URL is clearnet (http/https). resolveRelative=true (a clearnet page)
    // resolves relative URLs against baseURL; false (a dmsg site) gates ONLY hrefs
    // that are themselves absolute http(s):// — relative URLs there are same-site
    // dmsg, left to the caller's own inliner. Returns the jobs to await.
    function gateAllClearnet(doc, baseURL, policy, resolveRelative) {
      // isMeshRes: an absolute .dmsg/.skynet/PK-host subresource. It's NOT
      // clearnet — and it must never be left for the browser to load as http://
      // (mixed content on the HTTPS page). We don't inline cross-site mesh
      // subresources, so strip them (the element is removed / attr cleared).
      function isMeshRes(href) {
        if (!href) return false;
        try { var x = new URL(href.trim(), baseURL); return /^https?:$/i.test(x.protocol) && (/\.(dmsg|skynet)$/i.test(x.hostname) || /^[0-9a-f]{66}$/i.test(x.hostname)); } catch (e) { return false; }
      }
      function stripMesh(el, attr) {
        if (el.tagName === "SCRIPT" || el.tagName === "LINK") { el.remove(); } else { el.removeAttribute(attr); }
      }
      function absC(href) {
        if (!href) return null; href = href.trim();
        if (resolveRelative) { try { var u = new URL(href, baseURL); return /^https?:$/i.test(u.protocol) ? u.href : null; } catch (e) { return null; } }
        if (!/^https?:\/\//i.test(href)) return null;
        try { return new URL(href).href; } catch (e) { return null; }
      }
      var jobs = [];
      doc.querySelectorAll("link[rel~='stylesheet'][href]").forEach(function (el) { if (isMeshRes(el.getAttribute("href"))) { stripMesh(el, "href"); return; } var a = absC(el.getAttribute("href")); if (a) { var j = gateClearnetResource(doc, el, "href", a, policy); if (j) jobs.push(j); } });
      doc.querySelectorAll("img[src],source[src],script[src],audio[src],video[src]").forEach(function (el) { if (isMeshRes(el.getAttribute("src"))) { stripMesh(el, "src"); return; } var a = absC(el.getAttribute("src")); if (a) { var j = gateClearnetResource(doc, el, "src", a, policy); if (j) jobs.push(j); } });
      return jobs;
    }

    // renderClearnet renders a clearnet page fetched in PROXY mode. Every clearnet
    // resource (relative or absolute) is re-fetched through the SAME exit and
    // inlined as a data: URI; scripts are stripped. The sandboxed iframe therefore
    // never reaches clearnet directly — a static, read-mostly, IP-anonymous render.
    async function renderClearnet(exit, url, html) {
      hideConnecting();
      currentSitePK = "";
      if (opts.setAddr) opts.setAddr(url);
      var docHtml;
      try {
        var doc = new DOMParser().parseFromString(html, "text/html");
        // Show the site's favicon in the title bar (fetched through the SAME exit).
        setFaviconVia(function (u) { return fetchClearnet(exit, "GET", u, null); }, doc, url);
        var head = doc.head || doc.documentElement;
        var base = doc.createElement("base"); base.setAttribute("href", url); base.setAttribute("target", "_self");
        // Dynamic mode runs the page's OWN scripts (inlined below) and bridges its
        // runtime fetch/XHR/images through the exit; static mode strips scripts and
        // only restores link navigation + form submission through the proxy.
        var dyn = clearnetDynamic();
        var sc = doc.createElement("script"); sc.textContent = dyn ? clearnetDynamicShimSrc(url) : clearnetShimSrc(url);
        // Light canvas default so an unstyled/partly-styled page stays legible —
        // the sandboxed iframe otherwise inherits the HV UI's dark color-scheme.
        // Inserted FIRST so the page's own (inlined) CSS overrides it.
        var baseStyle = doc.createElement("style");
        baseStyle.textContent = ":root{color-scheme:light}html{background:#fff;color:#111}";
        head.insertBefore(sc, head.firstChild);
        head.insertBefore(base, head.firstChild);
        head.insertBefore(baseStyle, head.firstChild);
        applyCSP(doc, dyn); // proxy mode: everything is inlined/bridged; block direct egress
        var cnJobs = gateAllClearnet(doc, url, { mode: "proxy", exit: exit, dynamic: dyn }, true);
        // Inline <style> url() refs (background-images/fonts) through the exit too,
        // so CSS-referenced assets (e.g. a logo drawn via background-image) load.
        doc.querySelectorAll("style").forEach(function (el) {
          if (el === baseStyle || !/url\(/i.test(el.textContent || "")) return;
          cnJobs.push(inlineCssClearnet(exit, url, el.textContent).then(function (css) { el.textContent = css; }).catch(function () {}));
        });
        await Promise.all(cnJobs);
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
    function applyCSP(doc, dynamic) {
      var head = doc.head || doc.documentElement;
      var m = doc.createElement("meta");
      m.setAttribute("http-equiv", "Content-Security-Policy");
      if (dynamic) {
        // Dynamic mode: the page's own scripts run and its runtime fetch/XHR/images
        // are relayed through the exit by the injected shim (via postMessage, which
        // CSP does not gate). We additionally allow 'unsafe-eval' + blob: so app
        // frameworks and blob workers work. connect-src is kept OFF http(s) (blob:
        // only) so anything the shim doesn't patch fails closed — no direct clearnet
        // egress, so the render stays IP-anonymous.
        m.setAttribute("content",
          "default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; " +
          "style-src data: 'unsafe-inline'; script-src data: blob: 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval'; " +
          "connect-src blob:; worker-src blob:; child-src blob:; frame-src 'none'; form-action 'none'");
        head.insertBefore(m, head.firstChild);
        return;
      }
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
      hideConnecting();
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
      // Each browser window installs its own listener on the SAME top window, so a
      // message must be handled ONLY by the window whose iframe sent it — otherwise
      // a link click (dmsgnav) or in-page fetch (dmsgreq) from one window drives
      // every open window (they'd all navigate to the clicked path). Match e.source
      // to THIS window's iframe.
      if (e.source !== frame.contentWindow) { return; }
      var d = e.data || {};
      if (d.type === "dmsgnav") {
        // Cross-site mesh link (absolute .dmsg/.skynet/PK URL): resolve host+path
        // +scheme and navigate to that site over the mesh — never let the browser
        // load http://<name>.dmsg (mixed content). Mirrors the address-bar classifier.
        if (d.url) {
          try {
            var mk = /^(dmsg|skynet):\/\//i.exec(d.url);
            if (mk) {
              var mrest = d.url.slice(mk[0].length), msl = mrest.indexOf("/");
              browseTo(msl >= 0 ? mrest.slice(0, msl) : mrest, msl >= 0 ? mrest.slice(msl) : "/", mk[1].toLowerCase());
              return;
            }
            var mu = new URL(d.url), mhost = mu.hostname;
            if (/\.(dmsg|skynet)$/i.test(mhost) || /^[0-9a-f]{66}$/i.test(mhost)) {
              browseTo(mhost + (mu.port ? ":" + mu.port : ""), (mu.pathname || "/") + (mu.search || ""), (mu.protocol || "http:").replace(":", ""));
            }
          } catch (_) {}
          return;
        }
        if (currentSitePK) { browseTo(currentSitePK, d.path); return; }
        return;
      }
      if (d.type === "cnnav" && d.url) {
        // A link click / form submit inside a proxy-rendered clearnet page.
        var cpol = clearnetPolicy();
        if (cpol.mode === "block") { log("clearnet nav blocked (no upstream): " + d.url); return; }
        if (d.method === "POST") {
          if (cpol.mode !== "proxy") { log("clearnet POST needs a proxy upstream: " + d.url); return; }
          loadGen++; var pg = loadGen; setLoading(true);
          fetchClearnet(cpol.exit, "POST", d.url, d.body || null).then(function (r) {
            if (pg !== loadGen) return;
            log("clearnet POST " + d.url + " via " + cpol.exit.slice(0, 8) + "… → " + r.status);
            renderClearnet(cpol.exit, d.url, new TextDecoder().decode(r.body || new Uint8Array()));
            setLoading(false);
          }).catch(function (err) { if (pg === loadGen) { setLoading(false); showError("POST failed", d.url, String((err && err.message) || err)); } });
          return;
        }
        browseToClearnet(d.url); // GET → normal clearnet navigation (history-tracked)
        return;
      }
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
            r = await fetchClearnet(pol.exit, d.method || "GET", d.path, d.body || null, d.headers || null);
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
      clearnetDynamic: clearnetDynamic, setClearnetDynamic: setClearnetDynamic,
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
      // Dark-theme the toolbar buttons explicitly — the UA default is light-gray
      // (rgb(239,239,239)), and disabled buttons render at 30% opacity, which
      // read as faded/see-through against the dark bar.
      '<style>' +
      '.skywire-browse-window .sbw-bar button{background:#2a2342;color:#cdd2da;border:1px solid #3a3352;border-radius:3px;padding:.3em .55em;cursor:pointer;font:inherit;line-height:1}' +
      '.skywire-browse-window .sbw-bar button:hover:not(:disabled){background:#3a3352;color:#fff}' +
      '.skywire-browse-window .sbw-bar button:disabled{background:#201c2c;color:#5a5470;border-color:#2a2342;cursor:default}' +
      '</style>' +
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
      '<button id="sb-proxy-auto" title="auto: use the default skysocks-client-lite pool (IP-anonymous + automatic failover)" style="cursor:pointer">auto</button>' +
      '<button id="sb-proxy-run" title="pick from skysocks-client-lite instances already running in this visor" style="cursor:pointer">⌄ running</button>' +
      '<button id="sb-proxy-list-btn" title="pick a public skysocks server from service discovery" style="cursor:pointer">⌄ servers</button>' +
      '<button id="sb-proxy-rnd" title="pick a random skysocks server from service discovery" style="cursor:pointer">🎲 random</button>' +
      '<button id="sb-proxy-save" style="cursor:pointer">set</button>' +
      '<button id="sb-proxy-stop" title="stop this window\'s skysocks-lite: release its route + session (re-establishes on the next clearnet request)" style="cursor:pointer">■ stop</button>' +
      '<button id="sb-proxy-dbg" title="stream the wasm visor\'s own detailed [skysocks-lite]/[resolve-proxy] lines to the visor-log window too" style="cursor:pointer">🐞 verbose: off</button>' +
      '<button id="sb-proxy-dyn" title="dynamic rendering: run the site\'s own scripts and route its fetch/XHR/images through the exit — needed for JS-heavy sites (SPAs). Off = static, lighter render that does not execute the page\'s JS." style="cursor:pointer">⚡ dynamic: off</button>' +
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
      (globalThis.__SKYWIRE_BROWSE_ORIGIN__ ?
        // Real-origin mode (RFC §4a/§4b): each site is its own genuine origin.
        '<div>Each mesh site loads from its <b>own real, isolated origin</b> (<code>&lt;pk&gt;.mesh.localhost</code>), fetched over <b>this visor\'s own dmsg / skynet transports</b> — no DNS, no certificate authorities, IP-anonymous. Clearnet sites route through a skysocks exit (set in ⚙).</div>' +
        '<div style="border-top:1px solid #2a2342;padding-top:.5em"><b style="color:#9ece6a">Works like a normal browser:</b></div>' +
        '<ul style="margin:.1em 0 0;padding-left:1.2em;display:flex;flex-direction:column;gap:.25em">' +
        '<li><b>Persistent storage.</b> Cookies, localStorage, logins and service workers <b>persist</b> across reloads — each site in its own real origin.</li>' +
        '<li><b>Isolated per-site.</b> A real cross-origin boundary keeps sites from reading each other\'s data, and none can reach this visor\'s keys.</li>' +
        '<li><b>Full scripts</b>, subresources, redirects, WASM and streaming — the browser renders the page natively; the visor only proxies the transport.</li>' +
        '<li>Address bar: a visor <b>PK</b>, <b>pk.dmsg</b>, <b>alias.dmsg</b>, <b>dmsg://</b>/<b>skynet://</b>, or an <b>https://</b> clearnet site.</li>' +
        '</ul>'
      :
        // Transcoder fallback: sandboxed opaque-origin srcdoc (the old limits).
        '<div>Fetches sites over <b>dmsg</b> (skynet) — no DNS, no certificate authorities, IP-anonymous. Address bar accepts a visor <b>PK</b>, <b>pk.dmsg</b>, an <b>alias.dmsg</b> (e.g. <b>home.dmsg</b>), or an <b>https://</b> clearnet site (routed through a skysocks exit — set in ⚙).</div>' +
        '<div style="border-top:1px solid #2a2342;padding-top:.5em"><b style="color:#e0af68">Limitations (by design):</b></div>' +
        '<ul style="margin:.1em 0 0;padding-left:1.2em;display:flex;flex-direction:column;gap:.25em">' +
        '<li><b>No persistent storage.</b> Every page runs in a sandboxed frame with an opaque origin — <b>cookies, localStorage and logins do not persist</b>, even across a reload. Each visit is effectively fresh/incognito.</li>' +
        '<li><b>Isolated.</b> Sites cannot read each other\'s data, and cannot read this visor\'s keys/storage.</li>' +
        '<li><b>Scripts limited</b> (sandbox: allow-scripts allow-forms); no plugins, popups, or top-level navigation. Some clearnet sites that require cookies/service-workers may misbehave.</li>' +
        '<li>Per-site persistent storage like a normal browser would need the <b>native desktop</b> app (each site on its own local origin) — not possible in a keyless browser tab.</li>' +
        '</ul>'
      ) +
      '</div>' +
      // The page iframe + a DOM overlay (sb-connect) for the connecting/journey
      // state. The overlay is rendered/updated in the PARENT dom — NOT via the
      // iframe's srcdoc — so the "connecting" panel never reloads the iframe
      // (re-setting srcdoc every tick flashed the iframe's background = strobe).
      // iframe background is dark (not #fff) so any transition is not a white flash.
      '<div id="sb-frame-wrap" style="position:relative;flex:1;display:flex;min-height:0">' +
      // Real-origin mode (RFC): NO sandbox — B is a genuine isolated origin that
      // must have its own storage/cookies/service-worker/secure-context. The old
      // transcoder keeps the opaque-origin sandbox. Cross-origin isolation between
      // sites is by ORIGIN (<pk>.mesh.localhost), not the sandbox attribute.
      '<iframe id="sb-frame" ' + (globalThis.__SKYWIRE_BROWSE_ORIGIN__ ? '' : 'sandbox="allow-scripts allow-forms" ') + 'style="flex:1;width:100%;border:0;background:#0e0c14"></iframe>' +
      '<div id="sb-connect" style="position:absolute;inset:0;display:none;background:#0e0c14;color:#cdd2da;font:14px/1.6 system-ui,-apple-system,sans-serif;overflow:auto"></div>' +
      '</div>';

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
      // Reflect the site's favicon (fetched over the proxy/dmsg) into the title bar.
      setIcon: function (u) { try { wb.setIcon(u); } catch (e) {} },
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
      // dmsg:// and skynet:// are explicit dmsg-site schemes. `new URL` treats them
      // as opaque (and prepending http:// mangles them — "http://dmsg://<pk>" parses
      // to host "dmsg"), so peel the scheme off and browse the rest over dmsg
      // directly, preserving the scheme for the address bar.
      var sk = /^(dmsg|skynet):\/\//i.exec(v);
      if (sk) {
        var rest = v.slice(sk[0].length);
        var slash = rest.indexOf("/");
        var shost = slash >= 0 ? rest.slice(0, slash) : rest;
        var spath = slash >= 0 ? rest.slice(slash) : "/";
        if (shost) { browser.browseTo(shost, spath || "/", sk[1].toLowerCase()); return; }
      }
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
    $("sb-proxy-auto").onclick = function () { $("sb-proxy-pk").value = "auto"; saveProxy(); };
    $("sb-proxy-run").onclick = function () {
      var sel = $("sb-proxy-list");
      var v = globalThis.skywireVisor;
      if (!v || !v.proxyInstances) { plog("● no in-tab visor here to list running instances"); return; }
      Promise.resolve(v.proxyInstances()).then(function (s) {
        var list = []; try { list = JSON.parse(s) || []; } catch (e) {}
        list = list.filter(function (p) { return p.exit; });
        sel.innerHTML = '<option value="">— ' + list.length + ' running instance(s) — pick one —</option>';
        var oa = doc.createElement("option"); oa.value = "auto"; oa.textContent = "auto (default pool + failover)"; sel.appendChild(oa);
        list.forEach(function (p) { var o = doc.createElement("option"); o.value = p.exit; o.textContent = (p.label || p.name) + " · " + p.exit.slice(0, 8) + "…"; sel.appendChild(o); });
        sel.style.display = "";
        plog("● " + list.length + " running skysocks-client-lite instance(s) — pick one (or auto)");
      }).catch(function (e) { plog("● proxyInstances failed: " + String((e && e.message) || e)); });
    };
    function saveProxy() {
      browser.setUpstream($("sb-proxy-pk").value);
      var up = browser.upstream(), self = ""; try { self = (opts.selfPK && opts.selfPK()) || ""; } catch (e) {}
      var mode = !up ? "clearnet BLOCKED (no upstream set)"
        : (up === "auto" ? "clearnet via the AUTO skysocks pool (default instance · IP-anonymous · failover)"
          : (up === self ? "clearnet DIRECT via self " + up.slice(0, 8) + "… (non-anonymous)"
            : "clearnet via skysocks " + up.slice(0, 8) + "… (IP-anonymous exit)"));
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
    $("sb-proxy-rnd").onclick = function () {
      plog("● 🎲 fetching skysocks servers to pick a random exit…");
      Promise.resolve(fdmsg("sd.dmsg", "GET", "/api/services?type=proxy", null)).then(function (r) {
        var list = [];
        try { list = JSON.parse(new TextDecoder().decode(r.body)) || []; } catch (e) {}
        var pks = list.map(function (s) { return String(s.address || "").split(":")[0]; }).filter(function (pk) { return /^[0-9a-f]{66}$/i.test(pk); });
        if (!pks.length) { plog("● no skysocks servers found"); return; }
        var pk = pks[Math.floor(Math.random() * pks.length)];
        $("sb-proxy-pk").value = pk; saveProxy();
        plog("● 🎲 random exit → " + pk.slice(0, 8) + "… (of " + pks.length + ")");
      }).catch(function (e) { plog("● SD fetch failed: " + String((e && e.message) || e)); });
    };
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
    // Dynamic-rendering toggle (per-window): run the clearnet page's own scripts and
    // bridge its runtime fetch/XHR/images through the exit. Reload so it takes effect.
    var dynBtn = $("sb-proxy-dyn");
    function renderDynBtn() { var on = browser.clearnetDynamic(); dynBtn.textContent = "⚡ dynamic: " + (on ? "on" : "off"); dynBtn.style.color = on ? "#9ece6a" : ""; }
    renderDynBtn();
    dynBtn.onclick = function () { browser.setClearnetDynamic(!browser.clearnetDynamic()); renderDynBtn(); try { browser.reload(); } catch (e) {} };

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
  // openAboutDialog shows the visor's version / build / identity + dmsg status,
  // read from GET /api/about via opts.api — so it renders identically on the
  // wasm visor (hvApi routes to the in-tab core) and the native HV (fetch to the
  // visor's HV server). It's the ☰ menu's "about" entry, added to both
  // hypervisor UIs so that menu stays equivalent across native and wasm.
  function openAboutDialog(doc, opts) {
    var existing = doc.getElementById("skywire-about-dialog");
    if (existing) { existing.style.display = "flex"; return; }
    var ov = doc.createElement("div");
    ov.id = "skywire-about-dialog";
    ov.style.cssText = "position:fixed;inset:0;z-index:2147483647;display:flex;align-items:center;justify-content:center;background:rgba(8,6,16,.62)";
    var box = doc.createElement("div");
    box.style.cssText = "width:min(520px,92vw);max-height:90vh;overflow:auto;background:#15131c;color:#cdd2da;border:1px solid #2a2342;border-radius:10px;box-shadow:0 10px 40px rgba(0,0,0,.6);font:12px/1.6 monospace;padding:1em;box-sizing:border-box";
    box.innerHTML =
      '<div style="display:flex;align-items:center;gap:.5em;margin-bottom:.7em">' +
      '<b style="color:#9d7cff;font-size:14px">about skywire</b><span style="flex:1"></span>' +
      '<button id="ab-x" style="cursor:pointer;background:transparent;color:#cdd2da;border:0;font-size:15px">×</button></div>' +
      '<div id="ab-body" style="opacity:.9">loading…</div>';
    ov.appendChild(box);
    (doc.body || doc.documentElement).appendChild(ov);
    function close() { ov.style.display = "none"; }
    box.querySelector("#ab-x").onclick = close;
    ov.addEventListener("click", function (e) { if (e.target === ov) { close(); } });
    function row(k, v) {
      if (!v || !String(v).trim()) { return ""; }
      return '<div style="display:flex;gap:.6em;margin:.15em 0"><span style="min-width:8.5em;color:#8b93a7">' + k +
        '</span><span style="flex:1;color:#cdd2da;word-break:break-all">' + v + '</span></div>';
    }
    // opts.api's r.body is a Uint8Array under hvApi (wasm core) but a plain
    // string under the harness/native fetch path — decode both.
    function decBody(r) {
      var b = r && r.body; if (b == null) { return ""; }
      if (typeof b === "string") { return b; }
      try { return new TextDecoder().decode(b); } catch (e) {}
      try { return new TextDecoder().decode(new Uint8Array(b)); } catch (e) {}
      return "";
    }
    Promise.resolve(opts.api("GET", "/api/about", null)).then(function (r) {
      var a = {}; try { a = JSON.parse(decBody(r)); } catch (e) {}
      var b = a.build || a.Build || {};
      var pk = a.public_key || a.PubKey || (opts.selfPK && opts.selfPK()) || "";
      var mode = globalThis.skywireVisor ? "browser (wasm) visor" : "native visor";
      // Static build/identity info only — live status (dmsg sessions, transports,
      // routes) lives on the dashboard, not here.
      box.querySelector("#ab-body").innerHTML =
        row("mode", mode) +
        row("version", b.version || b.Version) +
        row("commit", b.commit || b.Commit) +
        row("built", b.date || b.Date) +
        row("go", b.go || b.Go) +
        row("platform", (b.os || b.OS) ? ((b.os || b.OS) + "/" + (b.arch || b.Arch || "")) : "") +
        row("public key", pk);
    }).catch(function (e) { box.querySelector("#ab-body").textContent = "failed to load /api/about: " + e; });
  }

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
    if (globalThis.__tourWB || doc.getElementById("skywire-tour-hl")) { return; }
    var win = doc.defaultView || window;
    // Platform: the wasm-visor PWA sets window.__SKYWIRE_HV__ in hv-boot.js before
    // Angular boots; the native hypervisor UI serves this SAME browse.js but never
    // sets it. So its presence means we're the wasm visor running in a browser tab.
    // (SKYWIRE_HV_MODE is an explicit override the serving layer may inject later.)
    var mode = (win.__SKYWIRE_HV__ || win.SKYWIRE_HV_MODE === "wasm") ? "wasm" : "native";
    // pick resolves per-platform copy: a plain string, {wasm,native,both}, or fn(mode).
    function pick(v) {
      if (typeof v === "function") { return v(mode); }
      if (v && typeof v === "object" && !v.nodeType) { return v[mode] != null ? v[mode] : (v.both != null ? v.both : ""); }
      return v == null ? "" : v;
    }
    // Resolve THIS visor's PK so navigating steps can open the local visor's own
    // tabs (#/nodes/<pk>/info|transports|routing|apps). The top-bar always carries
    // the local visor's chip whose href holds the 66-hex PK; fall back to the URL.
    // resolveSelfPK finds THIS visor's PK from any /nodes/<pk>/ link in the DOM (the
    // visor-list rows, the top-bar chip, or the URL) — robust to whatever page the
    // tour is opened from (the chip is absent on the list page). Re-tried lazily so
    // a step still resolves it even if the DOM wasn't ready at tour start. The
    // single-visor wasm PWA has exactly one such PK; the native hypervisor uses the
    // first listed visor.
    var SELFPK = "";
    var visorList = "#/nodes/list/1";
    function resolveSelfPK() {
      if (SELFPK) { return SELFPK; }
      try {
        var m = (win.location.hash || "").match(/nodes\/([0-9a-fA-F]{66})/);
        if (m) { SELFPK = m[1]; return SELFPK; }
        var links = doc.querySelectorAll('a[href*="/nodes/"]');
        for (var k = 0; k < links.length; k++) {
          var mm = (links[k].getAttribute("href") || "").match(/nodes\/([0-9a-fA-F]{66})/);
          if (mm) { SELFPK = mm[1]; return SELFPK; }
        }
      } catch (e) {}
      return SELFPK;
    }
    resolveSelfPK();
    // nodePath(tab) → the local visor's tab hash (falls back to the overview if the
    // PK can't be resolved, so a step navigates somewhere valid rather than skipping).
    function nodePath(tab) { var pk = resolveSelfPK(); return pk ? "#/nodes/" + pk + "/" + tab : visorList; }
    var steps = [
      // --- start on the front page: the visor list, exactly as it loads ---
      { route: visorList,
        title: "This is not a normal web page",
        body: {
          wasm: "You're running a full <b>Skywire visor</b> — a live routing peer on an encrypted, peer-to-peer mesh — <b>inside this browser tab</b>. No install, no account, no server, no one in the middle. It lives only here: close the tab and it's gone (unless you export your key). We'll start right here, on the <b>visor list</b> in front of you, and read it left to right.",
          native: "You're running a full <b>Skywire visor</b> on this machine — a persistent routing peer on an encrypted, peer-to-peer mesh. No account, no central server, no one in the middle. We'll start right here, on your hypervisor's <b>visor list</b>, and read it left to right." },
        disc: { summary: "Open-source, no warranty — your keys are yours",
          details: "Skywire and the Skycoin wallet are experimental, open-source software, provided as-is and without warranty. You run this visor yourself and hold your own keys and coins; no one else can access or recover them. Understand the risks before relying on it or storing value." } },

      // Identity first — the public key IS the visor, right there in the list.
      { route: visorList, sel: "td.key-cell",
        title: "You are your public key",
        body: {
          wasm: "Every visor is named by a <b>public key</b> — this 66-character hex string, here in the list. There are no usernames and no accounts on the mesh: the key <i>is</i> the identity and the address. It's one half of a cryptographic keypair held in <b>this browser</b>; the secret half never leaves it, and every link your visor makes is encrypted end-to-end with it.",
          native: "Every visor is named by a <b>public key</b> — this 66-character hex string, here in the list. No usernames, no accounts: the key <i>is</i> the identity and the address. It's one half of a keypair on <b>this machine</b>; the secret half never leaves it, and every link is encrypted end-to-end with it." },
        more: { summary: "Why a key instead of a name or IP",
          panel: "On the clearnet you're found by IP and trusted through a certificate authority vouching for a name. On Skywire the <b>public key</b> is both at once: it's the address other visors route to, and the thing they encrypt to — so there's nothing to spoof and no authority to trust. Whoever holds the matching secret key <i>is</i> this visor; back it up (the tour shows where) and you can move your identity to another device." } },

      // How it's connected — the dmsg-server count itself, broken down by carrier.
      // Anchored to the live count cell (.dmsg-counts) FIRST, falling back to the
      // always-present column header only if a browser visor's sessions momentarily
      // flap to zero (the cell then renders "-" with no .dmsg-counts). pickTarget
      // tries the comma list in priority order.
      { route: visorList, sel: ".dmsg-counts, th.dmsg-column",
        title: "How you're connected: dmsg",
        body: {
          wasm: "Right here is your <b>live count</b> of <b>dmsg servers</b> your visor is connected to, broken down by <b>carrier</b>. dmsg is an encrypted relay network: any two visors reach each other through these servers, without connecting directly. A browser can't open raw TCP, so it joins over <b>WSS</b> (WebSocket-over-TLS) and <b>WT</b> (WebTransport) — the carriers counted here.",
          native: "Right here is your <b>live count</b> of <b>dmsg servers</b> your visor is connected to, broken down by <b>carrier</b>. dmsg is an encrypted relay network: any two visors reach each other through these servers, without connecting directly. A native visor joins over <b>TCP</b> (and QUIC where a server offers it)." },
        more: { summary: "The four carriers",
          panel: "dmsg carries the control plane and a fallback data path. The <b>carrier</b> — how a visor reaches a dmsg server — depends on the host:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>tcp</b> — native visors; a raw TCP socket.</li>' +
            '<li style="margin:.3em 0"><b>ws</b> / <b>wss</b> — WebSocket, the browser\'s only option. <code>wss</code> (TLS) is required on an https page — browsers block mixed-content <code>ws</code>.</li>' +
            '<li style="margin:.3em 0"><b>webtransport</b> — HTTP/3 datagrams; browser-dialable.</li>' +
            '<li style="margin:.3em 0"><b>quic</b> — QUIC over UDP; native. The one carrier that also passes unreliable <b>datagrams</b> (UDP-over-dmsg), not just reliable streams.</li>' +
            '</ul>' +
            "A wasm visor in a tab has no raw sockets, so it leans on <b>wss + WebTransport</b>; a native visor can also make direct TCP/UDP transports. These four carriers are the <i>same four protocols</i> Skywire uses for direct transports (next column) — dmsg is simply the <b>relayed</b> version: same wire, reached through a server instead of directly." } },

      // Direct links — the transports count itself, right beside dmsg. Anchored to
      // the live .tp-counts cell first, header fallback (a fresh visor with no
      // transports yet renders "-", not .tp-counts) — same priority trick as dmsg.
      { route: visorList, sel: ".tp-counts, th.transports-column",
        title: "Direct links: transports",
        body: "Right beside it, this is your visor's live <b>transports</b> count — direct links to peers, by type (STCPR, SUDPH, SWTR, WEBRTC…). Where dmsg <i>relays</i> through a server, a transport is point-to-point. It's already dialing peers around the world; the total climbs as it settles in.",
        more: { summary: "Transport types, and how they mirror dmsg",
          panel: "A transport is a direct link between two visors. <b>Four mirror the dmsg carriers exactly</b> — same wire, dialed peer-to-peer instead of to a server:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>stcpr</b> — direct TCP, found via the address resolver <span style="opacity:.65">(≙ the tcp carrier)</span>.</li>' +
            '<li style="margin:.3em 0"><b>ws</b> — direct WebSocket <span style="opacity:.65">(≙ ws / wss)</span>.</li>' +
            '<li style="margin:.3em 0"><b>webtransport</b> — direct WebTransport <span style="opacity:.65">(≙ webtransport)</span>.</li>' +
            '<li style="margin:.3em 0"><b>quic</b> — QUIC over UDP; also carries faithful <b>UDP datagrams</b> end-to-end <span style="opacity:.65">(≙ quic)</span>.</li>' +
            '</ul>' +
            "<b>Two only make sense peer-to-peer</b> — a dmsg server is a fixed, public endpoint, so there's nothing to holepunch to:" +
            '<ul style="margin:.55em 0;padding-left:1.15em;list-style:disc">' +
            '<li style="margin:.3em 0"><b>sudph</b> — direct UDP, NAT-holepunched (a reliable stream, via KCP).</li>' +
            '<li style="margin:.3em 0"><b>webrtc</b> — browser-to-browser DataChannel, NAT-traversed via ICE; dmsg carries only the signaling.</li>' +
            '</ul>' +
            "And <b>dmsg</b> itself is a transport — the <i>relayed</i> one. Most transports hand up a reliable stream; QUIC (direct or over dmsg) can also carry true unreliable UDP end-to-end. More direct transports = less relaying = lower latency." } },

      // Label + location — the friendly name and where it's connecting from.
      { route: visorList, sel: "td.label-cell",
        title: "Label & location",
        body: "Finally on this row: a human <b>label</b> you can set to recognise a visor at a glance, and — in the next column — its detected <b>IP address and location</b>. The label is just local, cosmetic naming; the identity that actually matters is the public key. That's the whole front page — <i>who</i> the visor is, <i>how</i> it's connected, and <i>where</i>. Now let's open the visor itself." },

      // --- into the local visor's own tabs: the same concepts, up close ---
      { route: function () { return nodePath("info"); }, sel: "app-node-info-content .info-line", has: "DMSG servers",
        title: "Inside your visor: Info",
        body: {
          wasm: "Opening your visor lands on its <b>Info</b> tab. The <b>DMSG servers</b> line spells out the same connection the list summarised, next to version, uptime and identity. This is the relay layer up close — your control plane and a fallback data path, all end-to-end encrypted, reached over wss / WebTransport from the browser.",
          native: "Opening your visor lands on its <b>Info</b> tab — <b>DMSG servers</b>, version, uptime and identity in one place. This is the relay layer up close: the control plane and a fallback data path, reached over TCP." } },

      { route: function () { return nodePath("transports"); }, sel: "app-transport-list",
        title: "Inside your visor: Transports",
        body: {
          wasm: "The visor's <b>Transports</b> tab: each row is one direct link to a peer. In a browser that's mostly <b>WebTransport</b> and dmsg; a native visor also builds stcpr/sudph/quic. More direct transports = less relaying = lower latency.",
          native: "The visor's <b>Transports</b> tab: each row is a direct link — stcpr, sudph, dmsg, quic — punching through NATs where needed, so traffic takes the shortest path instead of always relaying." } },

      { route: function () { return nodePath("routing"); }, sel: "app-route-list",
        title: "Inside your visor: Routing",
        body: "The <b>Routing</b> tab shows the paths traffic takes across those transports. A route can be <b>DIRECT</b> (a single hop straight to the peer), <b>MULTIHOP</b> (through several visors, so no single hop sees both ends), or <b>MULTIPLEXED</b> (spread across parallel paths for resilience and throughput). The route-finder builds them on demand.",
        more: { summary: "Hops &amp; the privacy trade-off",
          panel: "A route is an ordered path of transports. One hop is fastest; more hops mean no single intermediary knows both source and destination, at the cost of latency. Skywire's route-finder builds these paths on demand, and apps like the skynet browser ride them." } },

      { route: function () { return nodePath("apps"); }, sel: "app-node-app-list tr", has: "skysocks",
        title: "Inside your visor: Apps",
        body: {
          wasm: "The visor's <b>Apps</b>. The default is <b>skysocks-client-lite</b> — an in-browser proxy client that routes a browse window's clearnet fetches out through an <b>exit</b> visor, IP-anonymously. Unlike the native <b>skysocks-client</b>, it serves <i>no local port</i> — it works only inside this tab. The rest of the tour's demos are optional to try: a mesh <b>browser</b>, encrypted <b>skychat</b>, and the <b>Skycoin wallet</b> — each opened from the ☰ menu, each running over the mesh.",
          native: "The visor's <b>Apps</b> — start, stop and configure them here: the <b>skysocks</b> proxy client + server, <b>VPN</b> client + server, and <b>skychat</b>. On a native visor these bind real local ports and can serve other machines on your network." },
        more: { summary: "skysocks-client vs. skysocks-client-lite",
          panel: "<b>skysocks-client</b> (native) runs as an app process and serves a local <b>SOCKS5 port</b> other programs point at. <b>skysocks-client-lite</b> (browser) has no process and no port — it lives in this tab and proxies only this visor's own browse windows and wallet. Both dial an <b>exit</b> visor that does the clearnet egress, so a site sees the exit's IP, not yours. (Naming: the browser one is the <i>lite</i> client — same idea, no listener.)" } },

      // The visor's remaining app tabs — so the inside-visor walk covers ALL of
      // them (Info/Transports/Routing/Apps above, then these four). Each is also
      // openable as a floating desktop window, demoed near the end of the tour;
      // here we just visit the tab so nothing is skipped.
      { route: function () { return nodePath("chat"); }, sel: "app-skychat",
        title: "Inside your visor: Chat",
        body: "The <b>Chat</b> tab is <b>Skychat</b> — end-to-end encrypted messaging to any visor by <b>public key</b>, right here. No server stores your messages; delivery rides dmsg. You can also pop it out as a floating desktop window (shown later)." },
      { route: function () { return nodePath("skynet"); }, sel: "app-skynet",
        title: "Inside your visor: Skynet",
        body: "The <b>Skynet</b> tab is a <b>mesh browser</b> in a tab — fetch <code>&lt;pk&gt;.dmsg</code> sites over dmsg, with no DNS and no certificate authority. The public key is the address <i>and</i> the authentication." },
      { route: function () { return nodePath("wallet"); }, sel: "app-wallet",
        title: "Inside your visor: Wallet",
        body: "The <b>Wallet</b> tab is the <b>Skycoin wallet</b>, served from this tab — your keys never leave the browser, and only node / BTC queries cross the mesh. This is where the reward system pays visors that stay online." },
      { route: function () { return nodePath("logs"); }, sel: "app-logs",
        title: "Inside your visor: Logs",
        body: "The <b>Logs</b> tab is a live tail of this visor's own runtime — dmsg, transports, routing and apps — straight from the wasm core in this tab. That's every tab this visor exposes; now let's step back out to the whole network." },

      // --- back to the top level: this hypervisor's cluster ---
      // A short PROGRESSIVE demo of hypervisor capability: the real list as-is,
      // then a visor connects, then it turns out to be a hypervisor, then it has
      // its own connected visor — then the simulated rows are dropped (the next
      // step has no `cluster`, so go() resets the level to 0). cluster steps 1-3
      // are wasm-only (the mock rides the wasm data bridge); a native HV shows its
      // real cluster, so it only gets the "as-is" explainer.
      { route: visorList, sel: "app-node-list", cluster: 0,
        title: "Your cluster: the hypervisor UI",
        body: {
          wasm: "Back at the top level. This visor list <b>is the hypervisor UI</b> — every visor connected to <i>this</i> hypervisor is shown here. Right now that's just this one visor, in this browser tab. To <b>manage a remote visor</b> from here, add its public key (the ⋯ menu → add visor); to have this visor <b>managed by a remote hypervisor</b> instead, set that hypervisor's public key in this visor's config. Let's simulate what attaching a visor looks like…",
          native: "This visor list <b>is the hypervisor UI</b> — every visor connected to this hypervisor is shown here. To <b>manage a remote visor</b>, add its public key (⋯ → add visor); to have this visor <b>managed by a remote hypervisor</b>, set that hypervisor's public key in this visor's config. Connected visors appear here, each fully controllable over the mesh." },
        more: { summary: "Hypervisor ⇄ visor: who manages whom",
          panel: "A <b>hypervisor</b> is just a visor that also serves this management UI and holds the keys of the visors it manages. The relationship is set in config (or at runtime): give this hypervisor a remote visor's PK and address to <i>manage</i> it, or set a remote hypervisor's PK on this visor to be <i>managed</i> by it. Management runs over the same encrypted mesh — a hypervisor in a browser tab can drive a native visor on the other side of the world, and vice-versa." } },
      { wasmOnly: true, route: visorList, sel: "app-node-list", cluster: 1,
        title: "A visor connects",
        body: "<b>Watch the list</b> — a remote visor is connecting to your hypervisor (a tour simulation). In a moment <b>demo-frankfurt-hv</b> appears as a second row. It's a full peer you could then inspect and control from this browser tab — its transports, routes and apps — all over the mesh, without it ever exposing an address to the outside world." },
      { wasmOnly: true, route: visorList, sel: "app-node-list", cluster: 2,
        title: "…and it's a hypervisor too",
        body: "That connected visor turns out to be <b>itself a hypervisor</b> — a ★ appears beside it shortly. A hypervisor is only a visor that <i>also</i> manages others, so nothing stops a visor you manage from managing visors of its own. Your hypervisor is now managing another hypervisor, entirely over the mesh." },
      { wasmOnly: true, route: visorList, sel: "app-node-list", cluster: 3,
        title: "…with its own connected visor",
        body: "And that connected hypervisor has <b>its own connected visor</b> — <b>demo-paris-visor</b> — which appears in its own section below it. Hypervisors nest: from this one tab you can see and manage a whole tree of visors. (These simulated rows disappear as we move on.)" },

      // Whole-mesh overview tabs — VISIT each (not just point at it) so the tour
      // doubles as a functional check. Rewards is native-only (hidden on wasm).
      { nativeOnly: true, route: "#/nodes/rewards", sel: "app-node-list", title: "The mesh: rewards",
        body: "<b>Rewards</b> — the Skycoin paid out to visors that stay online and reachable. This is where a visor appears once it qualifies." },
      { route: "#/nodes/transports", title: "The mesh: all transports",
        body: "<b>Transports</b> — every direct link across the <i>whole</i> mesh (thousands of them), with per-type bandwidth: the live edge list the route-finder draws on. Toggle Compact / Tree to explore it. Fetched <b>peer-to-peer over dmsg</b>, never from a web server." },
      { route: "#/nodes/network", sel: "app-network-view", title: "The mesh: every visor",
        body: "<b>Network</b> — a searchable directory of <b>every visor on the mesh</b> (the running count is top-left). Filter by country, version or transport type, and read each visor's transport mix and uptime status at a glance. The next tab draws this same set as a live <b>graph</b>." },
      { route: "#/nodes/visualizer", sel: "app-network-visualizer", title: "The mesh: visualizer",
        body: "<b>Network Visualizer</b> — an interactive, geographic render of the same graph (Flat, Globe and WebGL views)." },
      { route: "#/nodes/uptime", sel: "app-multi-visor-uptime", title: "The mesh: uptime",
        body: "<b>Uptime</b> — how consistently each visor has stayed online over 1d / 7d / 30d, the basis for rewards." },
      { route: "#/nodes/services-health", sel: "app-services-health", title: "The mesh: services",
        body: {
          wasm: "<b>Deployment</b> — the health of the shared services (config, discovery, route-finder, address resolver, uptime tracker) that keep the mesh working. <b>This browser tab probes each one live over dmsg</b> — status, version and latency — right here, the same reach a native visor has. Green is up; a red row is a service that didn't answer.",
          native: "<b>Deployment</b> — the health of the shared services (config, discovery, route-finder, address resolver, uptime tracker) that keep the mesh working. All of it fetched over dmsg — a visor talking directly to visors around the world." } },

      { sel: "#skywire-skynet-taskbar", title: "The mesh desktop",
        body: "Tool windows float above the UI. The <b>skynet browser</b> fetches sites over dmsg — anonymous, no DNS, no certificate authorities; sites are addressed by public key (e.g. <code>&lt;pk&gt;.dmsg</code>).",
        more: { summary: "How mesh sites stay safe",
          panel: "Fetched pages run sandboxed and isolated from your keys — a site can't read your identity or reach the clearnet on its own. There's no DNS and no certificate authority; the public key <i>is</i> the address and the authentication. The ⓘ button on a browse window explains the isolation model." } },

      // App demos (wasm desktop only — a native visor surfaces these as its own
      // Angular tabs). Each step OPENS the app window and spotlights it; go()
      // closes it again when the tour moves to a non-app step.
      { wasmOnly: true, action: "browse", sel: "@appwin", title: "App: the mesh browser",
        body: "The tour just opened the <b>skynet browser</b> — a window that fetches sites <b>over dmsg</b>, addressed by public key (<code>&lt;pk&gt;.dmsg</code>), with no DNS and no certificate authority. It's landed on <b>home.dmsg</b>, a start page this visor serves from the tab — and it lists <b>named</b> mesh sites you can click: <code>skywire.dmsg</code>, <code>tpd.dmsg</code>, and more. A name like <code>skywire.dmsg</code> is just a friendly alias the resolver maps to a public key; type any <code>&lt;pk&gt;.dmsg</code> or <code>&lt;name&gt;.dmsg</code> in the bar to visit a mesh site.",
        more: { summary: "Named .dmsg sites, no DNS",
          panel: "There's no DNS on the mesh — a public key is the real address. But typing 66 hex characters is unfriendly, so the resolving proxy keeps a small <b>alias</b> table (<code>skywire.dmsg</code>, <code>tpd.dmsg</code>, <code>home.dmsg</code>, …) that maps a readable name to a PK, exactly like the native socks5 resolving proxy. Resolution is local to your visor's alias set, not a global namespace anyone can squat — and in this tab <code>skywire.dmsg</code> resolves to <i>this</i> visor, so the page you're seeing is served from the browser you're reading this in." } },
      { wasmOnly: true, action: "chat", sel: "@appwin", title: "App: encrypted chat",
        body: "<b>Skychat</b> — end-to-end encrypted messaging straight between visors over dmsg, with no server storing your messages. Address a peer by public key and type; delivery rides the same mesh your visor is already on." },
      { wasmOnly: true, action: "wallet", sel: "@appwin", title: "App: Skycoin wallet",
        body: "The <b>Skycoin wallet</b> — a client-side wallet served from this tab. Your keys never leave the browser; only node and BTC queries cross the mesh. This is where the reward system pays visors that stay online." },
      { wasmOnly: true, action: "log", sel: "@appwin", title: "App: live visor logs",
        body: "The <b>visor log</b> — a live tail of this visor's own runtime (dmsg, transports, routing, apps), straight from the wasm core in this tab. Watch the mesh work in real time — and the <b>console</b>, next, drives the same visor from a command line." },
      { wasmOnly: true, action: "cli", sel: "@appwin", title: "App: the visor console",
        body: "The <b>console</b> is a real shell running <i>inside</i> this browser visor — pipes, globbing, control flow, an editor, even <code>jq</code> and <code>awk</code>. Its built-in visor commands — <code>pk</code>, <code>about</code>, <code>visors</code>, <code>net</code>, <code>health</code>, <code>apps</code>, <code>tps</code>, <code>routes</code> — emit JSON straight into the terminal, so you can inspect and script this visor exactly like a native one. No SSH, no install: a full shell for your visor, in the tab.",
        more: { summary: "A shell, in a browser tab",
          panel: "This isn't a remote terminal — there's no server on the other end. The shell (<b>websh</b>) runs in the same wasm runtime as the visor, and its visor commands call straight into the in-tab core, emitting JSON you can pipe through <code>jq</code>/<code>awk</code> like any Unix pipeline. A native visor exposes the same commands through <code>skywire cli</code> on the host; here they live in the browser." } },

      { sel: "#tb-menu", title: "Your keys, your visor",
        body: {
          wasm: "Your visor is the keypair we started with, held in <b>this browser</b> (Apps → identity). <b>Export it</b> to back up or move your visor to another device — that key <i>is</i> your identity on the mesh, and this tab holds the only copy.",
          native: "Your visor is the keypair we started with, on <b>this machine</b> (Apps → identity). Back it up — that key <i>is</i> your identity on the mesh. Whoever holds it is this visor." },
        more: { summary: "Wasm visor vs. a native visor",
          panel: "Same protocol, different host. A <b>wasm visor</b> lives in a browser tab: ephemeral by default (export your key to persist it), dials over WebSocket/WebTransport, and hosts a lighter app set. A <b>native visor</b> runs as a system service: persistent on disk, can make direct TCP/UDP transports and bind low ports, and can host the full app set (VPN, skysocks server, and more). Both are first-class peers on the same mesh." } },

      { title: { wasm: "A self-hosting internet, in a tab", native: "A self-hosting internet" },
        body: {
          wasm: "No server. No account. No IP address handed out. Just your browser, cryptographic keys, and a global peer-to-peer mesh — reachable from anywhere, run by nobody, and gone when you close the tab unless you export your key. Reopen this tour any time from the Apps (☰) menu → tour. Welcome to Skywire.",
          native: "No server. No account. Just cryptographic keys and a global peer-to-peer mesh — reachable from anywhere, run by nobody. Reopen this tour any time from the Apps (☰) menu → tour. Welcome to Skywire." } }
    ];
    // Drop native-only steps (e.g. the fleet Rewards overview) on a wasm visor,
    // where those tabs are hidden — see home-tabs.ts isWasmHvCore(). Filtering
    // here (rather than skipping at render time) keeps the "N / total" count right.
    steps = steps.filter(function (s) { return !(s.nativeOnly && mode !== "native") && !(s.wasmOnly && mode !== "wasm"); });

    // --- tour-only simulated cluster (wasm visor demo) ---
    // A fresh browser visor manages no one, so "Your cluster, live" would show a
    // one-row list. For the tour we splice in synthetic visors so it SHOWS what a
    // managed cluster looks like: a remote visor this hypervisor manages, which is
    // itself a hypervisor with its own connected visor (mirrors how the native HV
    // renders attached visors + nested hypervisors). Done by wrapping the wasm
    // core's JS data bridge (skywireVisor.hvApi) — the same path Angular's
    // SkywireHttpBackend fetches through — so the rows flow through the real render
    // pipeline (list + tree) and survive Angular's refresh poll. Torn down (hvApi
    // restored) in cleanup(), so the next poll drops the rows. Wasm-only: a native
    // HV serves its data over real HTTP, not hvApi, and has real peers to show.
    var MOCK_REMOTE_PK = "03" + "a7f3c9d1e2b4f608".repeat(4).slice(0, 64);
    var MOCK_SUB_PK = "02" + "5b8e1d4a7c0f36e9".repeat(4).slice(0, 64);
    var MOCK_D1 = "02" + "0281a102c82820e8".repeat(4).slice(0, 64);
    var MOCK_D2 = "03" + "0326978fa1b2c3d4".repeat(4).slice(0, 64);
    function mockTp(local, remote, type) { return { id: "", local_pk: local, remote_pk: remote, type: type, is_setup: false, label: "automatic", initiator: true }; }
    function mockDs(pk, carrier, proto) { return { pk: pk, latency: 0, carrier: carrier, protocol: proto }; }
    function mockEntry(o) {
      return {
        overview: {
          local_pk: o.pk, hostname: o.host || "",
          build_info: { version: o.version || "v1.3.91", os: o.os || "linux", arch: o.arch || "amd64", commit: "", date: "" },
          app_protocol_version: "", routes_count: o.routes || 2,
          local_ip: "", public_ip: o.ip || "", is_symmetic_nat: false,
          country_code: o.cc || "", region_name: o.region || "", city_name: o.city || "",
          hypervisors: null, connected_hypervisor: null, apps: o.apps || [], transports: o.transports || []
        },
        health: { services_health: "healthy" },
        dmsg_stats: { public_key: o.pk, server_public_key: (o.dmsg && o.dmsg[0] ? o.dmsg[0].pk : ""), round_trip: 0 },
        dmsg_servers: o.dmsg || [], uptime: o.uptime || 86400, online: true, min_hops: o.minHops || 1,
        reward_address: o.reward || "", build_tag: o.buildTag || "", config_version: o.version || "v1.3.91",
        public_autoconnect: true, is_public: !!o.isPublic, is_hypervisor: !!o.isHv
      };
    }
    // The cluster demo is PROGRESSIVE, driven by clusterLevel (set per-step via
    // s.cluster in go()), so the tour can show a hypervisor's capability build up:
    //   0 — no mock (the real list as-is)
    //   1 — a regular visor connects (demo-frankfurt-hv, NOT a hypervisor)
    //   2 — that visor is ALSO a hypervisor (★ appears; is_hypervisor=true)
    //   3 — that hypervisor has its OWN connected visor (demo-paris-visor, shown
    //       in its own nested section)
    // Level resets to 0 when the tour leaves the cluster section, dropping the rows.
    var clusterLevel = 0;
    function buildMockCluster(level) {
      var remote = mockEntry({
        pk: MOCK_REMOTE_PK, host: "demo-frankfurt-hv", version: "v1.3.91", os: "linux", arch: "arm64",
        ip: "185.130.44.12", cc: "DE", region: "Hesse", city: "Frankfurt", isHv: level >= 2, uptime: 1209600,
        reward: "2RkZ7wFm3nDqA8sT1yV6bXcJ9pLhGw4uEo",
        transports: [mockTp(MOCK_REMOTE_PK, SELFPK || "", "stcpr"), mockTp(MOCK_REMOTE_PK, MOCK_SUB_PK, "dmsg"), mockTp(MOCK_REMOTE_PK, MOCK_SUB_PK, "sudph")],
        dmsg: [mockDs(MOCK_D1, "tcp", "tcp"), mockDs(MOCK_D2, "tcp", "tcp")]
      });
      var sub = (level >= 3) ? mockEntry({
        pk: MOCK_SUB_PK, host: "demo-paris-visor", version: "v1.3.91", os: "linux", arch: "amd64",
        ip: "51.75.20.7", cc: "FR", region: "Île-de-France", city: "Paris", uptime: 432000,
        reward: "2Hn9dQ4vC7bR2xW8yK1mF6tL3sA5gZ9jUe",
        transports: [mockTp(MOCK_SUB_PK, MOCK_REMOTE_PK, "stcpr")],
        dmsg: [mockDs(MOCK_D1, "tcp", "tcp")]
      }) : null;
      return { remote: remote, sub: sub };
    }
    function spliceMock(path, data) {
      if (clusterLevel <= 0) { return data; }
      var m = buildMockCluster(clusterLevel);
      if (/visors-tree-summary/.test(path)) {
        if (data && data.sections && data.sections[0] && Array.isArray(data.sections[0].visors)) {
          data.sections[0].visors = data.sections[0].visors.concat([m.remote]);
          // A hypervisor only gets its own section once it actually manages a
          // visor (level 3) — level 2 just flips the ★ on the row above.
          if (clusterLevel >= 3 && m.sub) {
            data.sections.push({ hypervisor_pk: MOCK_REMOTE_PK, via_chain: [MOCK_REMOTE_PK], visors: [m.remote, m.sub] });
          }
        }
      } else if (Array.isArray(data)) { // visors-summary (flat)
        data = data.concat(m.sub ? [m.remote, m.sub] : [m.remote]);
      }
      return data;
    }
    // setClusterLevel changes what the mock injects and nudges the node list to
    // refetch so the change shows within ~1-2s (the raw 5s poll + the wasm core's
    // slow fetch otherwise lags a change to the NEXT step). The refetch is a single
    // DEBOUNCED click of the top-bar refresh button: firing it rapidly across
    // consecutive steps (a fast click-through) backed up the wasm core's request
    // queue and wedged it, so we coalesce to one refresh ~450ms after the last
    // level change. Spaced-out steps (real reading pace) each still refresh once.
    var _clRefreshT = null;
    function setClusterLevel(n) {
      clusterLevel = n;
      if (_clRefreshT) { clearTimeout(_clRefreshT); }
      _clRefreshT = setTimeout(function () {
        _clRefreshT = null;
        try { var rb = doc.querySelector("app-refresh-button"); if (rb) { rb.click(); } } catch (e) {}
      }, 450);
    }
    function installClusterMock() {
      if (mode !== "wasm") { return; }
      var v = win.skywireVisor;
      if (!v || typeof v.hvApi !== "function" || v.__tourMockOn) { return; }
      var orig = v.hvApi.bind(v);
      v.__tourOrigHvApi = orig; v.__tourMockOn = true;
      v.hvApi = function (method, path, body) {
        var pr = orig(method, path, body);
        if ((method || "GET").toUpperCase() !== "GET" || !/\/(visors-tree-summary|visors-summary)(\?|$)/.test(path || "")) { return pr; }
        return pr.then(function (r) {
          try {
            var data = JSON.parse(new TextDecoder().decode((r && r.body) || new Uint8Array()));
            return { status: r.status, body: new TextEncoder().encode(JSON.stringify(spliceMock(path, data))) };
          } catch (e) { return r; }
        });
      };
    }
    function removeClusterMock() {
      var v = win.skywireVisor;
      if (!v || !v.__tourMockOn) { return; }
      try { v.hvApi = v.__tourOrigHvApi; } catch (e) {}
      v.__tourMockOn = false; v.__tourOrigHvApi = null;
    }
    installClusterMock();

    var host = doc.body || doc.documentElement;
    // Ensure the WinBox dark chrome exists even if the skynet desktop (which
    // normally injects it) hasn't mounted yet. Distinct id from mountPanel's
    // #skywire-wb-style so we never shadow its browse-iframe fix; the shared
    // .winbox.skywire-wb rules are identical, so co-existing is harmless.
    if (!doc.getElementById("skywire-tour-style")) {
      var tst = doc.createElement("style");
      tst.id = "skywire-tour-style";
      tst.textContent = ".winbox.skywire-wb{background:#15131c}.skywire-wb .wb-header{background:#1b1726}.skywire-wb .wb-body{background:#15131c}.skywire-wb{pointer-events:auto}";
      (doc.head || doc.documentElement).appendChild(tst);
    }
    // Non-modal tour: the callout lives in a movable / minimizable / closable
    // WinBox window (makeWin), and the current step's target gets a LIGHT highlight
    // ring — pointer-events:none, no full-screen dim — instead of a blocking
    // spotlight. So the HV UI behind the tour stays fully interactive: the user can
    // drag the window aside, minimize it, or close it without finishing the tour.
    // `spot` is that ring; `call` is the window body.
    var spot = doc.createElement("div");
    spot.id = "skywire-tour-hl";
    spot.style.cssText = "position:fixed;border-radius:8px;border:2px solid #9d7cff;box-shadow:0 0 0 3px rgba(157,124,255,.22),0 0 22px rgba(157,124,255,.5);transition:all .18s ease;pointer-events:none;z-index:2147483001;display:none";
    host.appendChild(spot);
    var call = doc.createElement("div");
    call.style.cssText = "color:#cdd2da;font:13px/1.55 system-ui,sans-serif;padding:1em 1.15em;box-sizing:border-box;height:100%;overflow:auto;background:#15131c";
    var i = 0, showMore = false, closed = false, wb = null;
    // reposition keeps the ring glued to its target when the page scrolls/resizes
    // behind the (now non-modal) window; per-step positioning happens in render().
    function reposition() {
      var s = steps[i], el = s && pickTarget(s.sel, s.has);
      if (el && spot.style.display === "block") {
        var r = el.getBoundingClientRect(), pad = 6;
        spot.style.left = (r.left - pad) + "px"; spot.style.top = (r.top - pad) + "px";
        spot.style.width = (r.width + pad * 2) + "px"; spot.style.height = (r.height + pad * 2) + "px";
      }
    }
    function cleanup() {
      if (closed) { return; }
      closed = true;
      removeClusterMock(); // drop the tour-only synthetic cluster rows (next poll refetches real data)
      try { if (globalThis.__skywireDemoApps) { globalThis.__skywireDemoApps.closeAll(); } } catch (e) {} // close any app-demo windows the tour opened
      try { localStorage.setItem(TOUR_SEEN_KEY, "1"); } catch (e) {}
      try { win.removeEventListener("scroll", reposition, true); win.removeEventListener("resize", reposition); } catch (e) {}
      if (spot.parentNode) { spot.parentNode.removeChild(spot); }
      globalThis.__tourWB = null;
    }
    // done() is the tour-driven close (skip / final "done"); it closes the WinBox,
    // whose onclose routes back through cleanup(). The window's own × also lands in
    // cleanup(). The `closed` guard makes both paths idempotent.
    function done() { cleanup(); try { if (wb) { wb.close(true); } } catch (e) {} }
    wb = makeWin(doc, {
      title: "Skywire tour", root: host, width: "380px", height: "240px",
      mount: call, onclose: function () { cleanup(); return false; }
    });
    globalThis.__tourWB = wb;
    win.addEventListener("scroll", reposition, true);
    win.addEventListener("resize", reposition);
    // pickTarget returns the first VISIBLE, non-tiny element matching sel (there
    // can be hidden/collapsed duplicates — e.g. a loading vs loaded top-bar — and
    // spotlighting a 1px ghost looks broken).
    // lastAppEl holds the window a step's `action` just opened (an app demo), so
    // the special selector "@appwin" spotlights it. Reset per step in go().
    var lastAppEl = null;
    function pickTarget(sel, has) {
      if (!sel) { return null; }
      if (sel === "@appwin") {
        var e = lastAppEl;
        if (e) { var er = e.getBoundingClientRect(); if (er.width >= 24 && er.height >= 10) { return e; } }
        return null;
      }
      // sel may be a COMMA-separated PRIORITY list, tried in order (not document
      // order) — so a step can prefer the live value cell (e.g. .dmsg-counts) and
      // fall back to the always-present column header only when that cell renders
      // "-" (no counts div) during a transient flap to zero sessions.
      var groups = sel.split(",");
      for (var g = 0; g < groups.length; g++) {
        var one = groups[g].trim();
        if (!one) { continue; }
        var cand = doc.querySelectorAll(one);
        for (var j = 0; j < cand.length; j++) {
          var el = cand[j], cr = el.getBoundingClientRect();
          if (cr.width >= 24 && cr.height >= 10 && getComputedStyle(el).visibility !== "hidden") {
            // `has` disambiguates when a selector matches several elements (e.g. the
            // DMSG line among many .info-line rows, or the skysocks app row).
            if (has && (el.innerText || "").indexOf(has) < 0) { continue; }
            return el;
          }
        }
      }
      return null;
    }
    // go() runs a step: navigate to its HV route (s.route, a hash — the local
    // visor's info/transports/routing/apps tabs live at #/nodes/<pk>/<tab>), then
    // wait for its target to appear (Angular renders the view async) before
    // drawing the callout + spotlight. Steps with no route/target render at once.
    function go() {
      var s = steps[i];
      var navigated = false;
      // App-demo steps: open (or switch to) the app window this step demonstrates,
      // and remember it so "@appwin" spotlights it. Steps WITHOUT an action close
      // any lingering demo window, so a following step (e.g. the ☰-menu step) isn't
      // covered by a left-over app window.
      if (globalThis.__skywireDemoApps) {
        if (s.action) { try { lastAppEl = globalThis.__skywireDemoApps.open(s.action); } catch (e) { lastAppEl = null; } }
        else { lastAppEl = null; try { globalThis.__skywireDemoApps.closeAll(); } catch (e) {} }
      }
      // Progressive cluster demo: apply the mock level this step wants; any step
      // outside the cluster section drops the simulated rows (level 0).
      if (s.cluster != null) { setClusterLevel(s.cluster); }
      else if (clusterLevel !== 0) { setClusterLevel(0); }
      if (s.route) {
        var target = (typeof s.route === "function") ? s.route(SELFPK) : s.route;
        if (target && win.location.hash !== target) { try { win.location.hash = target; navigated = true; } catch (e) {} }
      }
      if (s.sel) {
        var t0 = Date.now();
        (function poll() {
          if (closed) { return; }
          if (pickTarget(s.sel, s.has) || Date.now() - t0 > 3000) { render(); return; }
          setTimeout(poll, 150);
        })();
      } else if (navigated) {
        // A "visit" step (route, no specific target): give the routed view a beat
        // to render behind the callout before we describe it.
        setTimeout(function () { if (!closed) { render(); } }, 550);
      } else {
        render();
      }
    }
    function render() {
      var s = steps[i];
      var el = pickTarget(s.sel, s.has);
      if (s.sel && !el && i < steps.length - 1) { i++; showMore = false; return go(); } // absent/hidden target → skip (navigate for the next step)
      var last = (i === steps.length - 1);
      var btn = 'cursor:pointer;border-radius:6px;padding:.4em .85em';
      if (showMore && s.more) {
        // drill-down side view: the step's "more" panel, off the linear spine.
        call.innerHTML =
          '<div style="display:flex;align-items:center;gap:.5em;margin-bottom:.55em">' +
          '<b style="color:#9d7cff;font-size:14px">' + pick(s.title) + '</b><span style="flex:1"></span>' +
          '<span style="opacity:.55;font-size:11px">more</span></div>' +
          '<div style="margin-bottom:.9em;color:#9aa0ad;font-size:12px;line-height:1.5">' + s.more.panel + '</div>' +
          '<div style="display:flex;gap:.5em;align-items:center;position:sticky;bottom:0;background:#15131c;padding-top:.6em">' +
          '<button id="tour-skip" style="cursor:pointer;background:transparent;color:#8b93a7;border:0">skip</button>' +
          '<span style="flex:1"></span>' +
          '<button id="tour-moreback" style="' + btn + ';background:#241f33;color:#cdd2da;border:1px solid #2a2342">&larr; back</button>' +
          '</div>';
      } else {
        call.innerHTML =
          '<div style="display:flex;align-items:center;gap:.5em;margin-bottom:.55em">' +
          '<b style="color:#9d7cff;font-size:14px">' + pick(s.title) + '</b><span style="flex:1"></span>' +
          '<span style="opacity:.55;font-size:11px">' + (i + 1) + " / " + steps.length + '</span></div>' +
          '<div style="margin-bottom:.9em">' + pick(s.body) + '</div>' +
          (s.disc ?
            '<details style="margin:-.35em 0 .85em;border-top:1px solid #2a2342;padding-top:.6em">' +
            '<summary style="cursor:pointer;color:#8b93a7;font-size:11.5px;outline:none">' + s.disc.summary + '</summary>' +
            '<div style="margin-top:.5em;color:#9aa0ad;font-size:11.5px">' + s.disc.details + '</div>' +
            '</details>' : '') +
          '<div style="display:flex;gap:.5em;align-items:center;position:sticky;bottom:0;background:#15131c;padding-top:.6em">' +
          '<button id="tour-skip" style="cursor:pointer;background:transparent;color:#8b93a7;border:0">skip</button>' +
          '<span style="flex:1"></span>' +
          (s.more ? '<button id="tour-more" style="' + btn + ';padding:.4em .7em;background:transparent;color:#8b93a7;border:1px solid #2a2342">more</button>' : '') +
          (i > 0 ? '<button id="tour-back" style="' + btn + ';background:#241f33;color:#cdd2da;border:1px solid #2a2342">back</button>' : '') +
          '<button id="tour-next" style="cursor:pointer;background:#9d7cff;color:#0e0c14;border:0;border-radius:6px;padding:.4em 1em;font-weight:600">' + (last ? "done" : "next") + '</button>' +
          '</div>';
      }
      if (el) {
        try { el.scrollIntoView({ block: "nearest" }); } catch (e) {}
        var r = el.getBoundingClientRect(), pad = 6;
        spot.style.display = "block";
        spot.style.left = (r.left - pad) + "px"; spot.style.top = (r.top - pad) + "px";
        spot.style.width = (r.width + pad * 2) + "px"; spot.style.height = (r.height + pad * 2) + "px";
      } else {
        spot.style.display = "none";
      }
      var b;
      if ((b = call.querySelector("#tour-skip"))) b.onclick = done;
      if ((b = call.querySelector("#tour-more"))) b.onclick = function () { showMore = true; render(); };
      if ((b = call.querySelector("#tour-moreback"))) b.onclick = function () { showMore = false; render(); };
      if ((b = call.querySelector("#tour-back"))) b.onclick = function () { showMore = false; if (i > 0) i--; go(); };
      if ((b = call.querySelector("#tour-next"))) b.onclick = function () { showMore = false; if (last) { done(); } else { i++; go(); } };
      // Fit the window to the step's content: no dead space on short steps, no
      // nested scroll on long "more" panels. Cap to 82vh (the body scrolls, and
      // the button row is sticky, so controls stay reachable past the cap).
      try {
        var winv = doc.defaultView || window;
        var vpW = winv.innerWidth || 1000, vpH = winv.innerHeight || 700;
        // Measure the callout's TRUE content height. `call` is height:100% +
        // overflow:auto, so scrollHeight otherwise reports the (possibly stale,
        // taller) WINDOW height — which left the window oversized after a "more"
        // panel collapsed back to a short step. Read it at height:auto, restore.
        var prevH = call.style.height;
        call.style.height = "auto";
        var contentH = call.scrollHeight;
        call.style.height = prevH || "100%";
        var ch = Math.max(170, Math.min(contentH + 38, Math.round(vpH * 0.82)));
        wb.resize(380, ch);
        // Keep the callout from covering the very content it describes. A full-tab
        // target (the mesh map / visualizer / all-transports) makes the spotlight
        // ring "everything" — noise — so hide it; and for full-tab, targetless, or
        // app-demo steps, dock the callout to the bottom-left corner. Small,
        // specific targets (a row/cell/line) re-center it (clear of a single row).
        var el2 = pickTarget(s.sel, s.has);
        var huge = false;
        if (el2 && s.sel !== "@appwin") {
          var hr = el2.getBoundingClientRect();
          huge = hr.width > vpW * 0.8 && hr.height > vpH * 0.6;
        }
        if (huge) { spot.style.display = "none"; }
        if (huge || !el2 || s.sel === "@appwin") {
          wb.move(16, Math.max(16, Math.round(vpH - ch - 16)));
        } else {
          // restore centered placement (a prior full-tab step may have docked it)
          try { wb.move("center", "center"); } catch (e2) { wb.move(Math.round((vpW - 380) / 2), Math.round((vpH - ch) / 2)); }
        }
      } catch (e) {}
    }
    go();
  }
  globalThis.skywireStartTour = function () { startTour(document); };

  // mountPanel builds a multi-window "skynet" desktop into `doc`: a bottom taskbar
  // plus any number of independent browse windows (each its own dmsg virtual
  // browser), all draggable / resizable / minimizable / maximizable. opts:
  //   fetchDmsg, serveContent — the skywireVisor primitives; selfPK() — optional.
  // Backward-compatible surface: returns { panel, browser, toggle, openWindow }
  // where toggle() shows/hides the desktop (opening a first window on demand), so
  // the existing skynet launcher button keeps working unchanged.
  // resolveSelfPKAsync resolves this visor's PK for the embed-iframe windows,
  // robust to BOTH skywireVisor API shapes: in-page status() returns the object
  // synchronously; the SharedWorker proxy returns a Promise (every proxied call
  // is a MessagePort round-trip). The sync opts.selfPK() getter is tried first
  // but can legitimately be empty right after page load (the desktop attaches
  // before CFG.ready resolves) — which made a freshly-opened chat/log window
  // show "boot the visor first" on a fully-booted visor. Polls briefly so a
  // window opened during boot fills in as soon as the visor is up.
  function resolveSelfPKAsync(opts, timeoutMs) {
    var deadline = Date.now() + (timeoutMs || 20000);
    function attempt(resolve) {
      var pk = "";
      try { pk = (opts.selfPK && opts.selfPK()) || ""; } catch (_) {}
      if (pk) { resolve(pk); return; }
      var sv = globalThis.skywireVisor || {};
      Promise.resolve(sv.status ? sv.status() : null).then(function (st) {
        var p = (st && st.pk) || "";
        if (p) { resolve(p); return; }
        if (Date.now() > deadline) { resolve(""); return; }
        setTimeout(function () { attempt(resolve); }, 500);
      }).catch(function () {
        if (Date.now() > deadline) { resolve(""); return; }
        setTimeout(function () { attempt(resolve); }, 500);
      });
    }
    return new Promise(attempt);
  }

  // createLogWindow hosts the ONE Angular Logs tab — the same component the
  // node page's Logs tab renders — in a WinBox window, chrome-less via the
  // node page's ?embed=1 mode. This replaced a bespoke console-capture viewer
  // (window.skywireLog) that showed DIFFERENT lines in a DIFFERENT format from
  // the Logs tab (the operator-flagged dual-surface divergence; the tab reads
  // /runtime-logs, which since the vlogHook change carries the full subsystem
  // firehose with real levels). One implementation now serves both surfaces;
  // the window is just a movable viewport onto the tab. Raw page-console
  // capture (window.skywireLog) remains available in browser devtools.
  function createLogWindow(doc, opts) {
    opts = opts || {};
    function selfPK() {
      try { if (opts.selfPK && opts.selfPK()) return opts.selfPK(); } catch (_) {}
      try { var st = (globalThis.skywireVisor || {}).status; var o = st ? st() : null; return (o && o.pk) || ""; } catch (_) { return ""; }
    }
    var wrap = doc.createElement("div");
    wrap.style.cssText = "position:absolute;inset:0;background:#0e0c14;display:flex;flex-direction:column;overflow:hidden";
    wrap.innerHTML = '<div style="margin:auto;color:#9aa0a6;font:12px monospace">connecting…</div>';
    var ngHandle = null;
    resolveSelfPKAsync(opts).then(function (pk) {
      wrap.innerHTML = "";
      if (!pk) {
        wrap.innerHTML = '<div style="margin:auto;color:#9aa0a6;font:12px monospace">boot the visor first</div>';
        return;
      }
      // Prefer mounting the real Angular LogsComponent in-context (ONE Angular
      // runtime, no self-iframe) via the SkywireNg bridge; fall back to the
      // ?embed=1 iframe if the bridge isn't present (e.g. a non-Angular host).
      var ng = globalThis.SkywireNg;
      if (ng && typeof ng.mountComponent === "function") {
        var host = doc.createElement("div");
        host.style.cssText = "width:100%;height:100%;flex:1;overflow:auto;background:#0e0c14";
        wrap.appendChild(host);
        ngHandle = ng.mountComponent(host, "logs", { nodeKey: pk });
        if (ngHandle) return;
      }
      var f = doc.createElement("iframe");
      f.src = "/#/nodes/" + pk + "/logs?embed=1";
      f.style.cssText = "border:0;width:100%;height:100%;flex:1;background:#0e0c14";
      wrap.appendChild(f);
    });
    var wb = makeWin(doc, {
      title: "visor log", root: opts.root, top: opts.top, bottom: opts.bottom, width: "46%", height: "60%",
      mount: wrap, onclose: function () { if (ngHandle) { try { ngHandle.dispose(); } catch (_) {} } if (opts.onClose) opts.onClose(); }
    });
    return { wb: wb, close: function () { wb.close(); } };
  }

  // ensureShell resolves globalThis.skywireShell — websh, the Bash/POSIX shell
  // + terminal compiled into the wasm visor (cmd/wasm-visor/shell_js.go).
  //
  // When the visor runs IN THIS PAGE it already installed skywireShell and this
  // resolves immediately. Normally it runs in a (Shared)Worker, which has no
  // DOM, so the tab instantiates the SAME wasm binary a second time with
  // __SKYWIRE_WASM_ROLE__='shell': that instance skips the visor entirely and
  // only draws terminals, calling back into the worker over the existing
  // skywireVisor proxy. One binary, two roles — no second blob to build, ship
  // or cache. Rejects where there is no visor at all (the native HV UI).
  var shellReady = null;
  function ensureShell() {
    if (globalThis.skywireShell) { return Promise.resolve(globalThis.skywireShell); }
    if (shellReady) { return shellReady; }
    if (!globalThis.skywireVisor) { return Promise.reject(new Error("no visor in this tab")); }
    shellReady = (function () {
      var vqs = "";
      try { var v = localStorage.getItem("skywire-visor-variant"); if (v) { vqs = "?variant=" + encodeURIComponent(v); } } catch (e) {}
      var loadExec = (typeof globalThis.Go === "function") ? Promise.resolve() : new Promise(function (res, rej) {
        var s = document.createElement("script");
        s.src = "wasm_exec.js" + vqs;
        s.onload = function () { res(); };
        s.onerror = function () { rej(new Error("failed to load wasm_exec.js")); };
        document.head.appendChild(s);
      });
      return loadExec.then(function () {
        var go = new globalThis.Go();
        // TinyGo's wasm_exec.js omits the gojs getRandomData import the
        // crypto-using runtime needs (mirrors hv-boot.js / worker.js).
        if (go.importObject.gojs && !go.importObject.gojs["runtime.getRandomData"]) {
          go.importObject.gojs["runtime.getRandomData"] = function (ptr, len) {
            crypto.getRandomValues(new Uint8Array(go._inst.exports.memory.buffer, ptr >>> 0, len >>> 0));
          };
        }
        globalThis.__SKYWIRE_WASM_ROLE__ = "shell";
        return WebAssembly.instantiateStreaming(fetch("wasm-visor.wasm" + vqs), go.importObject)
          .then(function (r) {
            go.run(r.instance); // installs globalThis.skywireShell, then blocks
            return new Promise(function (res, rej) {
              var tries = 0;
              (function wait() {
                if (globalThis.skywireShell) { res(globalThis.skywireShell); return; }
                if (++tries > 600) { rej(new Error("shell wasm did not come up")); return; }
                setTimeout(wait, 50);
              })();
            });
          });
      });
    })();
    return shellReady;
  }

  // createShellWindow opens websh as a WinBox window: a real shell (pipes,
  // globbing, control flow, an editor, jq/awk) whose visor commands — pk about
  // visors net health apps tps routes hvapi — emit JSON straight into it. Go
  // owns everything inside the mount element; this side supplies the frame and
  // closes the session.
  function createShellWindow(doc, opts) {
    var mount = doc.createElement("div");
    mount.style.cssText = "position:absolute;inset:0;background:#000;overflow:hidden";
    var note = doc.createElement("div");
    note.style.cssText = "position:absolute;inset:0;display:flex;align-items:center;justify-content:center;color:#9aa0a6;font:12px monospace";
    note.textContent = "starting the shell…";
    mount.appendChild(note);
    var handle = null;
    var wb = makeWin(doc, {
      title: opts.title || "shell", root: opts.root, top: opts.top, bottom: opts.bottom,
      width: "56%", height: "62%", mount: mount,
      onclose: function () {
        try { if (handle && handle.close) { handle.close(); } } catch (e) {}
        if (opts.onClose) { opts.onClose(); }
      }
    });
    ensureShell().then(function (sh) {
      note.remove();
      handle = sh.open(mount);
      if (handle && handle.error) { mount.textContent = "shell: " + handle.error; return; }
      if (handle && handle.focus) { handle.focus(); }
    }).catch(function (e) {
      note.textContent = "shell failed to start: " + (e.message || e);
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

  // createChatWindow hosts the ONE Angular skychat client — the exact same
  // component as the HV's top-level Chat tab — in a WinBox window, chrome-less
  // via the node page's ?embed=1 mode, with ?peer= preselecting a conversation
  // (deep links). This replaced a 270-line bespoke DOM chat that had its own
  // rendering rules (no reply-to, no delete, no receipts) — the recurring
  // dual-surface divergence class (desktop window vs Angular tab reading the
  // same data through DIFFERENT implementations). One implementation now
  // serves both surfaces; the window is just a movable viewport onto the tab.
  function createChatWindow(doc, opts) {
    function selfPK() {
      try { if (opts.selfPK && opts.selfPK()) return opts.selfPK(); } catch (_) {}
      try { var st = (globalThis.skywireVisor || {}).status; var o = st ? st() : null; return (o && o.pk) || ""; } catch (_) { return ""; }
    }
    function chatURL(peer) {
      return "/#/nodes/" + (resolvedPK || (opts.selfPK && opts.selfPK()) || "") + "/chat?embed=1" + (peer ? "&peer=" + encodeURIComponent(peer) : "");
    }
    var wrap = doc.createElement("div");
    wrap.style.cssText = "position:absolute;inset:0;background:#0e0c14;display:flex;flex-direction:column;overflow:hidden";
    wrap.innerHTML = '<div style="margin:auto;color:#9aa0a6;font:12px monospace">connecting…</div>';
    var frame = null;      // iframe-fallback element
    var ngHandle = null;   // SkywireNg bridge mount handle
    var ngHost = null;     // container the component mounts into
    var resolvedPK = "";
    // Bridge path: (re)mount the real Angular SkychatComponent in-context, with
    // the peer preselected. skychat reads its peer once at init, so retargeting
    // re-mounts (dispose + mount) rather than reloading an iframe.
    function mountChat(peer) {
      if (!(globalThis.SkywireNg && typeof globalThis.SkywireNg.mountComponent === "function")) return false;
      if (ngHandle) { try { ngHandle.dispose(); } catch (_) {} ngHandle = null; }
      if (!ngHost) {
        ngHost = doc.createElement("div");
        ngHost.style.cssText = "width:100%;height:100%;flex:1;overflow:auto;background:#0e0c14";
        wrap.appendChild(ngHost);
      } else { ngHost.innerHTML = ""; }
      ngHandle = globalThis.SkywireNg.mountComponent(ngHost, "skychat", { nodeKey: resolvedPK, peer: peer || "" });
      return !!ngHandle;
    }
    resolveSelfPKAsync(opts).then(function (pk) {
      wrap.innerHTML = "";
      if (!pk) {
        wrap.innerHTML = '<div style="margin:auto;color:#9aa0a6;font:12px monospace">boot the visor first</div>';
        return;
      }
      resolvedPK = pk;
      // Prefer the in-context Angular mount (ONE Angular runtime, no self-iframe);
      // fall back to the ?embed=1 iframe if the bridge isn't present.
      if (mountChat(opts.initialPeer || "")) return;
      frame = doc.createElement("iframe");
      frame.src = chatURL(opts.initialPeer || "");
      frame.style.cssText = "border:0;width:100%;height:100%;flex:1;background:#0e0c14";
      frame.setAttribute("allow", "microphone; autoplay"); // voice calls live in the component
      wrap.appendChild(frame);
    });
    // Deep-link retarget: bridge path re-mounts with the new peer; iframe path
    // reloads the embedded tab (the component reads ?peer= once at init).
    function setPeer(pk) {
      if (!pk) return;
      if (ngHost) { mountChat(pk); }
      else if (frame) { frame.src = chatURL(pk); }
    }
    var wb = makeWin(doc, {
      title: "skychat", root: opts.root, top: opts.top, bottom: opts.bottom, width: "42%", height: "62%",
      mount: wrap, onclose: function () { if (ngHandle) { try { ngHandle.dispose(); } catch (_) {} } if (opts.onClose) opts.onClose(); }
    });
    return { wb: wb, close: function () { wb.close(); }, setPeer: setPeer };
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
    // High z-index: the desktop (and its windows) must paint ABOVE the Angular
    // HV-UI chrome (its top tab bar — "Visor list" etc. — is positioned/z-indexed
    // and otherwise bleeds over a window's own nav bar). pointer-events stays
    // none so the HV UI is still clickable wherever no window covers it.
    root.style.cssText = "position:fixed;left:0;top:0;right:0;bottom:0;pointer-events:none;z-index:2147483000";
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
        ".skywire-wb #sb-frame{position:relative!important;height:auto!important;min-height:0!important;flex:1 1 auto!important}" +
        // WinBox's own skin leaves .wb-header transparent and .wb-body white, so
        // the HV UI behind bleeds through the title bar and white flashes before
        // the body paints. Give the window opaque dark chrome to match the nav bar.
        ".winbox.skywire-wb{background:#15131c}" +
        ".skywire-wb .wb-header{background:#1b1726}" +
        ".skywire-wb .wb-body{background:#15131c}";
      (doc.head || doc.documentElement).appendChild(st);
    }

    // Always-on taskbar: [menu] [open-window chips…] [dock]. Top by default; the
    // dock button flips it top↔bottom (remembered in localStorage). No hide.
    var bar = doc.createElement("div");
    bar.id = "skywire-skynet-taskbar";
    bar.style.cssText = "position:fixed;left:0;right:0;height:" + BARH + "px;box-sizing:border-box;z-index:2147483646;" +
      "display:flex;gap:.5em;align-items:center;padding:0 .6em;background:#0e0b16;" +
      "font:12px/1.3 monospace;color:#cdd2da";
    // Chrome-less embed mode (?embed=1 — a WinBox window iframing one Angular
    // tab, e.g. the ☰ Chat / visor-log windows): hide the taskbar so the
    // embedded page can't spawn a desktop INSIDE a desktop window. Everything
    // else initializes normally, so the page's contract is unchanged.
    try { if (((doc.defaultView || window).location.hash || "").indexOf("embed=1") >= 0) { bar.style.display = "none"; } } catch (_) {}
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
    // 'wallet' is the skycoin-web thin-client served same-origin at /wallet/ —
    // the app never loads over dmsg, only its node API does. Ungated: both the
    // wasm visor (browser fetchDmsg shim) and the native HV (server-side dmsg
    // proxy, hypervisor_handlers_wallet.go) serve /wallet/, so the ☰ wallet
    // opens the hosting visor's wallet on either.
    addApp("wallet", function () { openWallet(); });
    addApp("console", function () { openCli(); });
    if (opts.ptyURL) addApp("terminal", function () { openTerm(); });
    addApp("logs", function () { openLog(); });
    addApp("identity", function () { openIdentityDialog(doc, opts); });
    // 'about' works on both HV UIs (reads /api/about via opts.api) — kept
    // ungated so the native and wasm ☰ menus stay equivalent.
    addApp("about", function () { openAboutDialog(doc, opts); });
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
    function openBrowse(skipLanding) {
      var win = createWindow(doc, withRoot(), function () { untrack(win); });
      track(win, "browser");
      // skipLanding: don't auto-navigate to home.dmsg — used by the deep-link
      // path, which navigates straight to its own target (so there's no
      // home.dmsg load flashing before the target).
      if (!skipLanding) { win.landHome(); }
      return win;
    }
    var walletWin = null, walletCfgListener = null;
    function openWallet() {
      if (focusExisting(walletWin)) { return; }
      // The ☰ wallet: the PWA-bundled skycoin-web wallet iframe, with a ⚙
      // settings toggle that reveals the ONE shared config page (/wallet/config)
      // — the SAME page the Angular wallet tab embeds. Same origin → shared
      // localStorage; on Apply the config posts back and we reload the wallet.
      var isWasm = !!(globalThis.skywireVisor);
      var cfgSrc = "/wallet/config" + (isWasm ? "?wasm=1" : "");
      var wrap = doc.createElement("div");
      wrap.style.cssText = "position:absolute;inset:0;display:flex;flex-direction:column;background:#15131c;overflow:hidden;font:12px monospace;color:#cdd2da";
      wrap.innerHTML =
        '<style>' +
        '.sww-bar{display:flex;gap:.5em;align-items:center;padding:.4em .55em;background:#1b1726;border-bottom:1px solid #2a2342}' +
        '.sww-bar button{background:#2a2342;color:#e8e8f0;border:1px solid #3a3352;border-radius:3px;padding:.35em .65em;cursor:pointer;font:inherit;line-height:1}' +
        '.sww-bar button:hover{background:#3a3352;color:#fff}.sww-bar button.on{border-color:#6f4bd8;color:#cbb8ff;background:rgba(111,75,216,.2)}' +
        '.sww-served{font-size:.92em;padding:.15em .6em;border-radius:999px;background:rgba(74,222,128,.18);color:#4ade80}' +
        // WinBox forces `.winbox iframe{position:absolute}`, which yanks a bare
        // iframe out of the flex column (collapsing #sww-cfg to a thin strip).
        // So give the config overlay its OWN relative flex container (same trick
        // as the wallet iframe below) with a real height; the iframe fills it and
        // scrolls its own ~644px content.
        '#sww-cfg-wrap{display:none;position:relative;flex:0 0 60%;min-height:0;border-bottom:1px solid #2a2342}' +
        '#sww-cfg-wrap.open{display:block}' +
        '#sww-cfg{position:absolute;inset:0;width:100%;height:100%;border:0;background:#15131c}' +
        '</style>' +
        '<div class="sww-bar">' +
        '<button id="sww-gear" title="wallet configuration">⚙ settings</button>' +
        '<span class="sww-served" title="Served by this visor at /wallet/ — wallets are client-side; only the node/BTC queries cross the mesh.">served</span>' +
        '<span style="flex:1"></span>' +
        '</div>' +
        '<div id="sww-cfg-wrap"><iframe id="sww-cfg" src="' + cfgSrc + '"></iframe></div>' +
        // WinBox styles window-body iframes position:absolute; give the wallet
        // iframe a relative flex container so it fills only the region below.
        '<div style="flex:1;position:relative;min-height:0">' +
        '<iframe id="sww-frame" src="/wallet/" allowfullscreen style="position:absolute;inset:0;width:100%;height:100%;border:0;background:#fff"></iframe>' +
        '</div>';
      walletWin = makeWin(doc, withRoot({
        title: "skycoin wallet", width: "500px", height: "800px",
        top: barTop, bottom: barBottom, mount: wrap,
        onclose: function () {
          untrack(walletWin); walletWin = null;
          if (walletCfgListener) { try { window.removeEventListener("message", walletCfgListener); } catch (e) {} walletCfgListener = null; }
        }
      }));
      var frame = wrap.querySelector("#sww-frame");
      var cfg = wrap.querySelector("#sww-cfg-wrap");
      wrap.querySelector("#sww-gear").onclick = function () {
        this.classList.toggle("on", cfg.classList.toggle("open"));
      };
      // Reload the wallet iframe when the config page applies (same-origin).
      walletCfgListener = function (ev) {
        if (ev && ev.data && ev.data.type === "skywire-wallet-config") {
          try { frame.src = "/wallet/?_=" + Date.now(); } catch (e) {}
        }
      };
      window.addEventListener("message", walletCfgListener);
      track(walletWin, "wallet");
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
      var close = function () { untrack(cliWin); cliWin = null; };
      // A tab with a visor in it runs the real shell; the native HV UI overlay
      // (no in-tab visor) keeps the REPL.
      cliWin = globalThis.skywireVisor
        ? createShellWindow(doc, withRoot({ title: "visor shell", onClose: close }))
        : createCliWindow(doc, withRoot({ onClose: close }));
      track(cliWin, "console");
    }
    var termWin = null;
    function openTerm() {
      if (!opts.ptyURL || focusExisting(termWin)) { return; }
      termWin = createTerminalWindow(doc, withRoot({ onClose: function () { untrack(termWin); termWin = null; } }));
      track(termWin, "terminal");
    }
    var chatWin = null;
    function openChat(initialPeer) {
      if (focusExisting(chatWin)) {
        // Already open — retarget it to the deep-linked peer if one was given.
        if (initialPeer && chatWin && chatWin.setPeer) { try { chatWin.setPeer(initialPeer); } catch (e) {} }
        return chatWin;
      }
      chatWin = createChatWindow(doc, withRoot({ onClose: function () { untrack(chatWin); chatWin = null; }, initialPeer: initialPeer || "" }));
      track(chatWin, "skychat");
      return chatWin;
    }

    // --- Desktop notifications for inbound skychat DMs (wasm visor only). ---
    // The browser analogue of the native skychat app's --os-notify (osnotify.go
    // fires when no browser UI is attached): notify when this tab is HIDDEN or
    // no chat window is open — i.e. the operator isn't already looking at the
    // conversation. Clicking the notification focuses the tab and opens the
    // chat window addressed to the sender. Poll-based over skychatMessages()
    // (the in-process buffer the Angular tab reads too — same source, no new
    // surface). Baseline on start so history never re-notifies; ids are the
    // stable envelope ids. Permission is requested lazily on the first user
    // gesture on the taskbar (browsers reject requests without a gesture).
    (function chatNotifier() {
      var sv = globalThis.skywireVisor || {};
      if (!sv.skychatMessages || typeof Notification === "undefined") { return; } // native HV desktop / no API → no-op
      var seen = null; // null until baselined
      function wantNotify() {
        if (Notification.permission !== "granted") { return false; }
        if (doc.hidden) { return true; }        // tab in background → notify
        return !chatWin;                         // tab visible but no chat UI open
      }
      function shortPK(pk) { return pk && pk.length > 12 ? pk.slice(0, 8) + "…" : (pk || "peer"); }
      function fire(m) {
        try {
          var n = new Notification("skychat — " + shortPK(m.from), {
            body: (m.text || "").slice(0, 140), tag: "skychat-" + m.from, renotify: true
          });
          n.onclick = function () {
            try { window.focus(); } catch (e) {}
            try { openChat(m.from); } catch (e) {}
            try { n.close(); } catch (e) {}
          };
        } catch (e) {}
      }
      function tick() {
        Promise.resolve(sv.skychatMessages()).then(function (raw) {
          var msgs = [];
          try { msgs = typeof raw === "string" ? JSON.parse(raw) : (raw || []); } catch (e) { return; }
          if (seen === null) { // first sample = baseline, never notify history
            seen = {};
            msgs.forEach(function (m) { if (m && m.id) { seen[m.id] = 1; } });
            return;
          }
          msgs.forEach(function (m) {
            if (!m || !m.id || seen[m.id]) { return; }
            seen[m.id] = 1;
            if (!m.out && !m.deleted && wantNotify()) { fire(m); }
          });
        }).catch(function () {});
      }
      setInterval(tick, 4000);
      tick();
      // Lazy permission request on the first taskbar interaction (a user gesture).
      if (Notification.permission === "default") {
        var asked = false;
        bar.addEventListener("click", function () {
          if (asked) { return; }
          asked = true;
          try { Notification.requestPermission().catch(function () {}); } catch (e) {}
        }, { once: false });
      }
    })();
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
        if (pre && (pre.target || pre.skydm || pre.skygroup)) {
          return { target: pre.target || "", skydm: pre.skydm || "", skygroup: pre.skygroup || "", kiosk: !!pre.kiosk };
        }
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
        var md = h.match(/[#&]skydm=([^&]+)/);
        if (md && !qs.skydm) { qs.skydm = decodeURIComponent(md[1]); }
        var mg = h.match(/[#&]skygroup=([^&]+)/);
        if (mg && !qs.skygroup) { qs.skygroup = decodeURIComponent(mg[1]); }
        if (/[#&]kiosk=1\b/.test(h)) { qs.kiosk = "1"; }
      } catch (e) {}
      if (!qs.skynet && !qs.skydm && !qs.skygroup) { return null; }
      return { target: qs.skynet || "", skydm: qs.skydm || "", skygroup: qs.skygroup || "", kiosk: qs.kiosk === "1" || qs.kiosk === "true" };
    }
    // whenVisorConnected fires cb once the wasm visor has a live dmsg session (so a
    // fetch over dmsg won't just error), bounded to ~20s so a stuck visor still lands
    // on the browser's own error page instead of hanging forever.
    function whenVisorConnected(cb) {
      var tries = 0;
      (function poll() {
        Promise.resolve().then(function () { return self.skywireVisor && self.skywireVisor.status(); })
          .then(function (st) {
            if ((st && (st.dmsg_connected || (st.dmsg_sessions | 0) > 0 || (st.dmsg && (st.transports | 0) > 0))) || tries > 40) { cb(); }
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
      // ?skydm=<pk> — open a 1:1 skychat pre-addressed to the peer ("message me"
      // links). wasm-visor only (needs the in-tab skychatSend hook); a native HV
      // has its own Angular skychat tab. Handled before the skynet-browse path so
      // a chat deep-link never falls through to openBrowse.
      if (dl && dl.skydm && globalThis.skywireVisor && globalThis.skywireVisor.skychatSend) {
        var cwin = openChat(dl.skydm);
        if (cwin && dl.kiosk) { enterKiosk(cwin); }
        dl = null; // handled — skip the skynet-browse path below
      }
      // ?skygroup=<invite> — join the federated group from the invite blob so a
      // shared link drops the visitor straight into membership. browse.js has no
      // group message view (that lives in the Angular skychat tab, which owns the
      // group UI); this wiring performs the JOIN, then the operator opens the
      // skychat tab to read/post. Best-effort: a failed join is logged, not fatal.
      if (dl && dl.skygroup && globalThis.skywireVisor && globalThis.skywireVisor.skychatGroupJoin) {
        Promise.resolve(skywireVisor.skychatGroupJoin(dl.skygroup)).then(function (g) {
          try { log("skygroup: joined " + ((g && g.name) ? g.name : "group") + " — open the skychat tab to view it"); } catch (e) {}
        }).catch(function (e) {
          try { log("skygroup: join failed: " + ((e && e.message) || e)); } catch (e2) {}
        });
        dl = null; // handled — skip the skynet-browse path below
      }
      if (dl && dl.target) {
        var dlWin = openBrowse(true); // skip home.dmsg auto-land; go to the deep-link target
        if (dl.kiosk) { enterKiosk(dlWin); }
        // Navigate straight to the target — do NOT wait for dmsg first. render()'s
        // waitForDmsg shows the "connecting to the mesh" overlay + live journey
        // WHILE the session comes up, so the user sees the connecting experience
        // from the moment the window opens, instead of a blank window that then
        // jumps to an already-loaded page. Retry on TRANSIENT failure only
        // (status 0 with an error — the target's dmsg server may not be reachable
        // on the very first fetch; not a deliberate block/direct/cancel or a real
        // HTTP response). Linear backoff.
        var attempts = 0, maxAttempts = 6;
        function tryOpen() {
          attempts++;
          var retry = function () {
            if (attempts < maxAttempts) { setTimeout(tryOpen, 1500 * attempts); }
          };
          try {
            var p = dlWin.browser.browseTo(dl.target, "/");
            if (p && p.then) {
              p.then(function (res) {
                if (res && res.status === 0 && res.error && !res.blocked && !res.direct && !res.cancelled) { retry(); }
              }).catch(retry);
            }
          } catch (e) { retry(); }
        }
        tryOpen();
      }
    } catch (e) {}

    // Tour hook: the onboarding tour (startTour) lives in a sibling scope and
    // can't reach these open* closures. Expose a small facade so tour steps can
    // OPEN an app window (browser / chat / wallet / logs) to demonstrate it, get
    // its DOM element back to spotlight, and have them all torn down when the
    // tour closes. "One demo window at a time": each open() first closes the
    // previous tour-opened window, so a demo walk stays uncluttered.
    var _demoWins = [];
    function _topDemoWin() {
      // The just-opened window is focused → highest z among .winbox. Exclude the
      // tour's own callout window so we spotlight the app, not the tour. (This
      // bundled WinBox exposes its root element as `.window` / `.g`, not `.dom`.)
      var twb = globalThis.__tourWB;
      var tourDom = twb ? (twb.window || twb.g || twb.dom) : null;
      var best = null, bz = -1, ws = doc.querySelectorAll(".winbox");
      for (var i = 0; i < ws.length; i++) {
        if (ws[i] === tourDom) { continue; }
        var z = parseInt(getComputedStyle(ws[i]).zIndex, 10) || 0;
        if (z >= bz) { bz = z; best = ws[i]; }
      }
      return best;
    }
    function _closeDemoWins() {
      _demoWins.forEach(function (el) { try { var c = el && el.querySelector(".wb-close"); if (c) { c.click(); } } catch (e) {} });
      _demoWins = [];
    }
    globalThis.__skywireDemoApps = {
      open: function (kind) {
        _closeDemoWins();
        try {
          if (kind === "browse") { openBrowse(); }
          else if (kind === "chat") { if (globalThis.skywireVisor && globalThis.skywireVisor.skychatSend) { openChat(); } else { return null; } }
          else if (kind === "wallet") { openWallet(); }
          else if (kind === "log") { openLog(); }
          else if (kind === "cli") { openCli(); }
          else { return null; }
        } catch (e) { return null; }
        var el = _topDemoWin();
        if (el && _demoWins.indexOf(el) < 0) { _demoWins.push(el); }
        return el;
      },
      closeAll: function () { _closeDemoWins(); }
    };

    return {
      panel: bar,
      toggle: toggle,
      openWindow: openBrowse,
      browser: function () { for (var i = wins.length - 1; i >= 0; i--) { if (wins[i].browser) return wins[i].browser; } return null; }
    };
  }

  globalThis.SkywireBrowse = { createBrowser: createBrowser, mountPanel: mountPanel };
})();

// voice-audio.js — main-thread WebAudio proxy for the wasm-visor's 1:1 voice.
//
// getUserMedia + AudioContext are main-thread-only, but the visor's audio
// Source/Sink live in the wasm (pkg/skychat/voice/audio_wasm.go). Audio crosses
// the boundary like the WebRTC/STUN proxies:
//
//   CAPTURE:  this file records the mic → deliverMic(bytes):
//               worker mode  → globalThis.__skyvoiceToWorker(bytes)  (hv-boot.js posts it)
//               in-page mode → globalThis.__skyvoiceMic(bytes)       (same global)
//   PLAYBACK: the wasm Sink emits frames → globalThis.__skyvoiceOnPlay(bytes),
//             which this file queues and the playback graph drains.
//             (In-page, the Sink calls __skyvoiceEmit, aliased here to __skyvoiceOnPlay.)
//
// PCM is 48 kHz mono int16 to match pkg/skychat/voice.
(function () {
  var RATE = 48000;
  var ctx = null, micStream = null, capNode = null, playNode = null, src = null;
  var playFrames = [], playPos = 0;

  function have(fn) { return typeof globalThis[fn] === 'function'; }

  // pushPlay queues a decoded frame (Uint8Array of LE int16) for playback.
  function pushPlay(bytes) {
    if (!bytes || !bytes.buffer) return;
    var f = new Int16Array(new Int16Array(bytes.buffer, bytes.byteOffset, bytes.byteLength >> 1)); // copy
    playFrames.push(f);
    if (playFrames.length > 50) { playFrames.splice(0, playFrames.length - 25); playPos = 0; } // cap backlog
  }

  function deliverMic(u8) {
    if (have('__skyvoiceToWorker')) globalThis.__skyvoiceToWorker(u8);
    else if (have('__skyvoiceMic')) globalThis.__skyvoiceMic(u8);
  }

  async function start() {
    if (ctx) return; // already running
    globalThis.__skyvoiceOnPlay = pushPlay;   // worker mode: hv-boot forwards here
    globalThis.__skyvoiceEmit = pushPlay;     // in-page mode: the Sink calls this directly

    ctx = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: RATE });
    if (ctx.state === 'suspended') await ctx.resume();

    // capture: mic -> deliverMic. Optional: if there's no microphone or the user
    // denies access, the call still connects receive-only (we can hear the peer,
    // we just send silence) rather than failing outright. Returns whether the mic
    // is live so the caller can surface "receive-only".
    var micOk = false;
    try {
      micStream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
      src = ctx.createMediaStreamSource(micStream);
      capNode = ctx.createScriptProcessor(2048, 1, 1);
      capNode.onaudioprocess = function (e) {
        var f32 = e.inputBuffer.getChannelData(0);
        var i16 = new Int16Array(f32.length);
        for (var i = 0; i < f32.length; i++) {
          var s = Math.max(-1, Math.min(1, f32[i]));
          i16[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
        }
        deliverMic(new Uint8Array(i16.buffer));
      };
      src.connect(capNode);
      capNode.connect(ctx.destination); // node only runs when connected; its own output is silent
      micOk = true;
    } catch (e) {
      try { console.warn('skyvoice: no microphone (' + (e && e.name ? e.name : e) + ') — receive-only'); } catch (_) {}
    }

    // playback: queued frames -> speakers
    playNode = ctx.createScriptProcessor(2048, 1, 1);
    playNode.onaudioprocess = function (e) {
      var out = e.outputBuffer.getChannelData(0);
      for (var i = 0; i < out.length; i++) {
        if (playFrames.length === 0) { out[i] = 0; continue; }
        var fr = playFrames[0];
        out[i] = fr[playPos++] / 0x8000;
        if (playPos >= fr.length) { playFrames.shift(); playPos = 0; }
      }
    };
    playNode.connect(ctx.destination);
    return micOk;
  }

  function stop() {
    try { if (capNode) capNode.disconnect(); } catch (_) {}
    try { if (playNode) playNode.disconnect(); } catch (_) {}
    try { if (src) src.disconnect(); } catch (_) {}
    try { if (micStream) micStream.getTracks().forEach(function (t) { t.stop(); }); } catch (_) {}
    try { if (ctx) ctx.close(); } catch (_) {}
    ctx = micStream = capNode = playNode = src = null;
    playFrames = []; playPos = 0;
    try { delete globalThis.__skyvoiceOnPlay; delete globalThis.__skyvoiceEmit; } catch (_) {}
  }

  globalThis.skywireVoiceAudioStart = start;
  globalThis.skywireVoiceAudioStop = stop;
})();
