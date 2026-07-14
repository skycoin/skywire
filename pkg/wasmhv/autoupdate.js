// autoupdate.js — wasm-visor self-update for the `skywire cli hv serve` page.
//
// The standalone wasm-visor's ONLY update step is a page reload: the server
// (hv serve) embeds the wasm in the running skywire binary, so when that binary
// updates the served /wasm-visor.wasm changes and a reload picks it up. This
// script polls a tiny /wasm-version fingerprint and, when it differs from the
// version this page booted with, reloads to the new build — by default, with a
// user-visible toast that lets them keep the current version (disabling
// auto-update). It is injected ONLY by hv serve, so it never runs for a
// native-hosted hypervisor UI.
(function () {
  'use strict';
  var POLL_MS = 60000;          // how often to check for a new build
  var GRACE_MS = 8000;          // countdown before auto-reloading
  var KEY = 'skywire.autoupdate'; // localStorage: 'off' disables auto-reload
  var booted = window.__SKYWIRE_WASM_VERSION__ || '';
  if (!booted) { return; }      // server didn't inject a version → nothing to do

  function enabled() { try { return localStorage.getItem(KEY) !== 'off'; } catch (e) { return true; } }
  function setEnabled(on) { try { localStorage.setItem(KEY, on ? 'on' : 'off'); } catch (e) {} }

  // Public API (a settings-menu control can call these; also handy from the console).
  window.skywireAutoUpdate = {
    bootVersion: booted,
    isEnabled: enabled,
    enable: function () { setEnabled(true); },
    disable: function () { setEnabled(false); },
    checkNow: function () { check(true); },
  };

  var notifying = false;

  // The wasm runs in a SharedWorker that stays alive across a tab's
  // location.reload() (it dies only when the LAST tab disconnects), so a plain
  // reload reconnects to the SAME stale runtime — the new wasm never loads.
  // Before reloading, tell the worker to self.close() so the post-reload page
  // boots a FRESH worker that fetches the new wasm-visor.wasm. (The dedicated-
  // Worker fallback already dies on unload, so the message is a no-op there.)
  function reloadFresh() {
    try {
      if (typeof SharedWorker !== 'undefined') {
        var w = new SharedWorker('worker.js');
        if (w.port.start) { w.port.start(); }
        w.port.postMessage({ t: 'shutdown' });
      }
    } catch (e) { /* fall through to a plain reload */ }
    setTimeout(function () { location.reload(); }, 200);
  }

  function toast(latest) {
    if (notifying) { return; }
    notifying = true;
    var box = document.createElement('div');
    box.style.cssText = 'position:fixed;z-index:2147483647;right:16px;bottom:16px;max-width:320px;' +
      'background:#2b2540;color:#eee;font:13px/1.4 system-ui,sans-serif;padding:12px 14px;' +
      'border:1px solid #6c5ce7;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.4)';
    var secs = Math.round(GRACE_MS / 1000);
    box.innerHTML = '<b>New wasm-visor available</b><br>Updating in <span id="su-c">' + secs +
      '</span>s… <div style="margin-top:8px"><button id="su-now" style="margin-right:8px">Update now</button>' +
      '<button id="su-keep">Keep this version</button></div>';
    document.body.appendChild(box);
    var t = setInterval(function () {
      secs -= 1;
      var c = document.getElementById('su-c');
      if (c) { c.textContent = String(secs); }
      if (secs <= 0) { clearInterval(t); reloadFresh(); }
    }, 1000);
    document.getElementById('su-now').onclick = function () { clearInterval(t); reloadFresh(); };
    document.getElementById('su-keep').onclick = function () {
      clearInterval(t); setEnabled(false); box.remove(); notifying = false;
      console.log('[autoupdate] disabled — staying on ' + booted + ' (re-enable: skywireAutoUpdate.enable())');
    };
  }

  function check(force) {
    fetch('wasm-version', { cache: 'no-store' }).then(function (r) {
      return r.ok ? r.text() : null;
    }).then(function (latest) {
      if (!latest) { return; }
      latest = latest.trim();
      if (latest === booted) { return; }
      console.log('[autoupdate] new build ' + latest + ' (running ' + booted + ')');
      if (force || enabled()) { toast(latest); }
    }).catch(function () { /* server unreachable — ignore, try next tick */ });
  }

  setInterval(check, POLL_MS);

  // Background tabs get their setInterval heavily throttled (and frozen entirely
  // when discarded), so a never-focused tab may miss the poll for a long time.
  // Re-check the moment the tab becomes visible again: an unfocused tab then
  // updates as soon as you return to it, rather than only on the next throttled
  // tick. Also check right after a reconnect (pageshow, incl. bfcache restore).
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible') { check(false); }
  });
  window.addEventListener('pageshow', function () { check(false); });

  // First check shortly after boot (don't wait a full POLL_MS to notice a build
  // that landed while this tab was loading).
  setTimeout(function () { check(false); }, 3000);
})();
