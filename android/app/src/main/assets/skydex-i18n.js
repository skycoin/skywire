// Translates the SkyDEX trading page, which ships English only. DexWebView
// runs this with the language's pack from skydex-i18n.json as `pack`.
(function (pack) {
  if (window.__skywireI18n) return;
  window.__skywireI18n = true;
  var has = Object.prototype.hasOwnProperty;
  var strings = pack.strings || {};
  var patterns = (pack.patterns || []).map(function (p) { return [new RegExp(p[0]), p[1]]; });
  var ATTRS = ['title', 'placeholder', 'aria-label'];
  var SKIP = 'script, style, code, input, textarea';
  var wrote = new WeakMap();

  function lookup(text) {
    var key = String(text || '').replace(/\s+/g, ' ').trim();
    if (!key) return null;
    var out = has.call(strings, key) ? strings[key] : null;
    for (var i = 0; out === null && i < patterns.length; i++) {
      if (patterns[i][0].test(key)) out = key.replace(patterns[i][0], patterns[i][1]);
    }
    if (out === null) return null;
    var lead = /^\s*/.exec(text)[0];
    var tail = /\s*$/.exec(text)[0];
    if (out.charAt(0) === '~') { out = out.slice(1); lead = ''; }
    if (out.slice(-1) === '~') { out = out.slice(0, -1); tail = ''; }
    return lead + out + tail;
  }
  function skipped(node) {
    var el = node.nodeType === 1 ? node : node.parentElement;
    return !el || !!el.closest(SKIP);
  }
  function text(node) {
    var v = node.nodeValue;
    if (wrote.get(node) === v || skipped(node)) return;
    var hit = lookup(v);
    if (hit === null || hit === v) return;
    wrote.set(node, hit);
    node.nodeValue = hit;
  }
  function attrs(el) {
    for (var i = 0; i < ATTRS.length; i++) {
      var v = el.getAttribute(ATTRS[i]);
      var hit = v && lookup(v);
      if (hit && hit !== v) el.setAttribute(ATTRS[i], hit);
    }
  }
  function walk(root) {
    if (root.nodeType === 3) { text(root); return; }
    if (root.nodeType !== 1 || skipped(root)) return;
    attrs(root);
    var w = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    for (var n = w.nextNode(); n; n = w.nextNode()) {
      if (n.nodeType === 3) text(n); else attrs(n);
    }
  }
  // The page's confirm() and alert() questions never reach the DOM.
  ['confirm', 'alert'].forEach(function (name) {
    var original = window[name];
    window[name] = function (message) { return original.call(window, lookup(message) || message); };
  });
  function start() {
    walk(document.body);
    new MutationObserver(function (muts) {
      muts.forEach(function (mu) {
        if (mu.type === 'childList') mu.addedNodes.forEach(walk);
        else if (mu.type === 'characterData') text(mu.target);
        else attrs(mu.target);
      });
    }).observe(document.body, {
      childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ATTRS,
    });
  }
  if (document.body) start(); else document.addEventListener('DOMContentLoaded', start);
})
