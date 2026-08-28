// responder.js — the trust boundary. Runs first-party on the app origin A.
//
// A browse origin B is embedded as an iframe inside A. B posts
// {type:'realorigin-hello', shortid}; this validates that the sender really is a
// browse origin, resolves the id to a registered target, and hands back a
// private MessagePort bound to that target. B then sends fetch requests on the
// port. It supplies a path; it never supplies a target. An untrusted frame
// therefore cannot ask for another site's content, and never sees whatever
// credential the transport uses.
//
// It must be first-party on A. An earlier design put it in a cross-origin helper
// iframe, and Storage Partitioning placed that helper in a different partition
// from A's own workers, where it could not reach the client at all.
//
// The embedder supplies the transport:
//
//   realOrigin.configure({
//     suffix: '.mesh.localhost',
//     fetch: function (target, req) {
//       // req: {url, method, headers, body(ArrayBuffer|null), path}
//       return Promise.resolve({ status, headers, body });
//     },
//   });
//   var id = await realOrigin.register('canonical-target-string', target);
//
// and may call realOrigin.progress(line, id) to stream status into the frame
// that is still waiting on its first response — the interstitial while a slow
// transport sets up. Omit the id and the line goes to every loading frame.
(function () {
  'use strict';
  if (globalThis.__realOriginInstalled) { return; }
  globalThis.__realOriginInstalled = true;

  var B32 = 'abcdefghijklmnopqrstuvwxyz234567'; // RFC 4648 base32, lowercase

  var cfg = { suffix: '', fetch: null };
  var targets = Object.create(null); // id -> target
  var progressSubs = new Set();

  // id is the first 20 base32 characters of SHA-256 over the canonical target.
  // Truncation is deliberate: the label has to fit a DNS label and stay under a
  // single-level wildcard, and 80 bits is far past collision concern for a map
  // this size. Must match ID() in the Go half.
  function idFor(canonical) {
    return crypto.subtle.digest('SHA-256', new TextEncoder().encode(String(canonical))).then(function (buf) {
      var b = new Uint8Array(buf), out = '', bits = 0, val = 0;
      for (var i = 0; i < b.length && out.length < 20; i++) {
        val = (val << 8) | b[i]; bits += 8;
        while (bits >= 5 && out.length < 20) { out += B32[(val >>> (bits - 5)) & 31]; bits -= 5; }
      }
      return out;
    });
  }

  function isBrowseOrigin(origin) {
    try {
      if (!cfg.suffix) { return false; }
      return new URL(origin).hostname.endsWith(cfg.suffix);
    } catch (e) { return false; }
  }

  function toArrayBuffer(body) {
    if (!body) { return null; }
    if (body instanceof ArrayBuffer) { return body; }
    if (body.buffer) { return body.buffer.slice(body.byteOffset || 0, (body.byteOffset || 0) + body.byteLength); }
    return null;
  }

  globalThis.realOrigin = {
    configure: function (opts) {
      opts = opts || {};
      if (opts.suffix) { cfg.suffix = String(opts.suffix); }
      if (opts.fetch) { cfg.fetch = opts.fetch; }
      return cfg;
    },
    id: idFor,
    register: function (canonical, target) {
      return idFor(canonical).then(function (id) {
        targets[id] = (target === undefined ? canonical : target);
        return id;
      });
    },
    forget: function (id) { delete targets[String(id)]; },
    // progress streams a status line into the frame that is still waiting on its
    // first response — the interstitial, while a slow transport sets up.
    //
    // Pass the frame's id to address one frame. Without it the line goes to
    // every frame currently loading, which is fine when only one is, and leaks
    // one site's progress into another's interstitial when several are. An
    // embedder that knows which frame a line belongs to should say so.
    progress: function (line, id) {
      var want = (id === undefined || id === null) ? null : String(id);
      progressSubs.forEach(function (sub) {
        if (want !== null && sub.id !== want) { return; }
        try { sub.send(String(line)); } catch (e) { /* a dead port */ }
      });
    },
  };

  globalThis.addEventListener('message', function (e) {
    var d = e.data || {};
    if (d.type !== 'realorigin-hello') { return; }
    if (!isBrowseOrigin(e.origin) || !e.source) { return; }

    var target = targets[String(d.shortid || '')];
    if (target === undefined) {
      try { e.source.postMessage({ type: 'realorigin-ready', error: 'unknown browse origin' }, e.origin); } catch (x) {}
      return;
    }
    if (typeof cfg.fetch !== 'function') {
      try { e.source.postMessage({ type: 'realorigin-ready', error: 'no transport configured' }, e.origin); } catch (x) {}
      return;
    }

    var mc = new MessageChannel();

    // Progress is only interesting until the first response lands: that request
    // IS the interstitial. After it, document.write has replaced the shell and
    // there is nothing left listening.
    var firstDone = false;
    var sub = {
      id: String(d.shortid || ''),
      send: function (line) {
        try { mc.port1.postMessage({ progress: { line: line } }); } catch (x) {}
      },
    };
    progressSubs.add(sub);
    function stopProgress() {
      if (firstDone) { return; }
      firstDone = true;
      try { progressSubs.delete(sub); } catch (x) {}
    }

    mc.port1.onmessage = function (ev) {
      var q = ev.data || {};
      Promise.resolve()
        .then(function () { return cfg.fetch(target, q.req || {}); })
        .then(function (r) {
          r = r || {};
          var ab = toArrayBuffer(r.body);
          mc.port1.postMessage({ id: q.id, status: r.status || 200, headers: r.headers || {}, body: ab }, ab ? [ab] : []);
          stopProgress();
        })
        .catch(function (err) {
          mc.port1.postMessage({ id: q.id, error: String((err && err.message) || err) });
          stopProgress();
        });
    };

    // targetOrigin is the frame's own origin, so only it receives the capability.
    e.source.postMessage({ type: 'realorigin-ready' }, e.origin, [mc.port2]);
  });
})();
