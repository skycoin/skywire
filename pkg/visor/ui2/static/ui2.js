// pkg/visor/ui2/static/ui2.js c3-vis-core
// Glue for the htmx pages: a fresh CSRF token per write, confirms, toasts,
// copy-to-clipboard, readable errors, the theme and the language.
(function () {
	'use strict';
	var token = '';
	var toastTimer = 0;

	function toast(msg, isErr) {
		var t = document.getElementById('toast');
		if (!t) return;
		t.textContent = msg;
		t.className = 'show' + (isErr ? ' err' : '');
		arm();
	}
	function arm() {
		clearTimeout(toastTimer);
		toastTimer = setTimeout(function () {
			var t = document.getElementById('toast');
			if (t) t.className = '';
		}, 3000);
	}
	function store(k, v) {
		try { if (v === null) localStorage.removeItem(k); else localStorage.setItem(k, v); } catch (_) { /* private mode */ }
	}
	function load(k) {
		try { return localStorage.getItem(k); } catch (_) { return null; }
	}

	// The hypervisor checks X-CSRF-Token on every write and a token lives 30s,
	// so fetch one right before the request (pkg/visor/csrf.go).
	document.addEventListener('htmx:confirm', function (e) {
		if (e.detail.verb === 'get') return;
		e.preventDefault();
		if (e.detail.question && !window.confirm(e.detail.question)) return;
		fetch('api/csrf', { credentials: 'same-origin' })
			.then(function (r) { return r.json(); })
			.then(function (j) { token = j.csrf_token || ''; e.detail.issueRequest(true); })
			.catch(function (err) { toast('csrf: ' + err, true); });
	});
	document.addEventListener('htmx:configRequest', function (e) {
		if (e.detail.verb !== 'get' && token) e.detail.headers['X-CSRF-Token'] = token;
	});

	document.addEventListener('htmx:oobAfterSwap', function (e) {
		if (e.detail.target && e.detail.target.id === 'toast') arm();
	});

	// The JSON handlers answer errors as {"error": "..."}.
	document.addEventListener('htmx:responseError', function (e) {
		var x = e.detail.xhr, msg = x.status + ' ' + x.statusText;
		if (x.status === 401) { window.location.href = 'ui2/login'; return; }
		try { msg = JSON.parse(x.responseText).error || msg; } catch (_) { /* not JSON */ }
		toast(msg, true);
	});
	document.addEventListener('htmx:sendError', function () { toast(document.body.getAttribute('data-unreachable'), true); });

	// Capture phase, so a click on a key copies it instead of opening the row.
	document.addEventListener('click', function (e) {
		var k = e.target.closest && e.target.closest('[data-copy]');
		if (k) {
			e.stopPropagation();
			e.preventDefault();
			if (navigator.clipboard) {
				navigator.clipboard.writeText(k.getAttribute('data-copy')).then(function () { toast('✓'); });
			}
			return;
		}
		var row = e.target.closest && e.target.closest('[data-href]');
		if (row && !e.target.closest('a, button, input, select, textarea, label')) {
			window.location.href = row.getAttribute('data-href');
		}
	}, true);
	// data-clear="#id" empties a panel; a sub nav marks the view it opened.
	document.addEventListener('click', function (e) {
		var c = e.target.closest && e.target.closest('[data-clear]');
		if (c) { var p = document.querySelector(c.getAttribute('data-clear')); if (p) p.innerHTML = ''; return; }
		var s = e.target.closest && e.target.closest('[data-subnav]');
		if (s) { s.parentNode.querySelectorAll('[data-subnav]').forEach(function (a) { a.classList.toggle('primary', a === s); }); }
	});
	document.addEventListener('keydown', function (e) {
		if (e.key === 'Enter' && e.target.classList && e.target.classList.contains('row')) e.target.click();
	});

	// Theme: system, light, dark, kept under the Angular UI's own key so the
	// two UIs agree.
	document.addEventListener('click', function (e) {
		if (!e.target.closest || !e.target.closest('[data-theme-toggle]')) return;
		var cur = document.documentElement.getAttribute('data-theme');
		var next = cur === 'light' ? 'dark' : cur === 'dark' ? null : 'light';
		if (next) document.documentElement.setAttribute('data-theme', next);
		else document.documentElement.removeAttribute('data-theme');
		store('hvui-theme', next);
	});

	// Language: the server reads a cookie, the Angular UI reads localStorage.
	function setLang(code) {
		document.cookie = 'ui2-lang=' + code + ';path=/;max-age=31536000;samesite=lax';
		store('lang', code);
	}
	document.addEventListener('change', function (e) {
		if (!e.target.matches || !e.target.matches('[data-lang]')) return;
		setLang(e.target.value);
		window.location.reload();
	});
	// First visit after choosing a language in the Angular UI.
	if (document.cookie.indexOf('ui2-lang=') < 0) {
		var ng = load('lang');
		if (ng && ng !== document.documentElement.lang) { setLang(ng); window.location.reload(); }
		else if (document.documentElement.lang) setLang(document.documentElement.lang);
	}
})();

// A [data-follow] list keeps showing its newest line while the reader is at
// the bottom, and a [data-max-children] list drops its oldest.
(function () {
	'use strict';
	var stick = true;
	document.addEventListener('htmx:oobBeforeSwap', function (e) {
		var el = document.querySelector('[data-follow]');
		if (el) stick = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
	});
	function settle() {
		document.querySelectorAll('[data-max-children]').forEach(function (el) {
			var max = parseInt(el.getAttribute('data-max-children'), 10) || 0;
			while (max > 0 && el.children.length > max) el.removeChild(el.firstElementChild);
		});
		document.querySelectorAll('[data-follow]').forEach(function (el) {
			if (stick) el.scrollTop = el.scrollHeight;
		});
	}
	document.addEventListener('htmx:oobAfterSwap', settle);
	document.addEventListener('htmx:afterSettle', function (e) {
		var el = e.detail.elt;
		if (el && el.matches && (el.matches('[data-follow]') || el.querySelector('[data-follow]'))) { stick = true; settle(); }
	});
	document.addEventListener('DOMContentLoaded', settle);
})();

// Filter: an input with data-filter="<selector>" shows the matching elements
// whose data-text holds its text; a data-filter-set checkbox keeps only those
// with data-set="1". A [data-filter-count] in the same form counts them.
(function () {
	'use strict';
	function apply(form) {
		var q = form.querySelector('[data-filter]');
		var only = form.querySelector('[data-filter-set]');
		var sel = (q || only).getAttribute(q ? 'data-filter' : 'data-filter-set');
		var text = q ? q.value.toLowerCase().trim() : '';
		var shown = 0;
		form.querySelectorAll(sel).forEach(function (el) {
			var ok = (!text || (el.getAttribute('data-text') || '').indexOf(text) >= 0) &&
				(!only || !only.checked || el.getAttribute('data-set') === '1');
			el.style.display = ok ? '' : 'none';
			if (ok) shown++;
		});
		var c = form.querySelector('[data-filter-count]');
		if (c) c.textContent = String(shown);
	}
	function onEvent(e) {
		if (!e.target.matches || !e.target.matches('[data-filter],[data-filter-set]')) return;
		apply(e.target.closest('form') || document);
	}
	document.addEventListener('input', onEvent);
	document.addEventListener('change', onEvent);
})();

// Keep the current tab in view when the tab bar scrolls (narrow screens).
document.addEventListener('DOMContentLoaded', function () {
	var a = document.querySelector('nav.tabs .tab.active');
	if (a && a.scrollIntoView) a.scrollIntoView({ block: 'nearest', inline: 'center' });
});
