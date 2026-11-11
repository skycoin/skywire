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

// The tab bar scrolls on phones: keep the current tab in view and fade an
// edge that has more tabs behind it.
document.addEventListener('DOMContentLoaded', function () {
	var nav = document.querySelector('nav.tabs');
	if (!nav) return;
	function fades() {
		var more = nav.scrollWidth - nav.clientWidth;
		nav.classList.toggle('fade-l', more > 1 && nav.scrollLeft > 1);
		nav.classList.toggle('fade-r', more > 1 && nav.scrollLeft < more - 1);
	}
	var a = nav.querySelector('.tab.active');
	if (a && nav.scrollWidth > nav.clientWidth) nav.scrollLeft = a.offsetLeft - nav.offsetLeft - (nav.clientWidth - a.offsetWidth) / 2;
	fades();
	nav.addEventListener('scroll', fades, { passive: true });
	window.addEventListener('resize', fades);
	if (document.fonts) document.fonts.ready.then(fades);
});

// The top bar's menu on narrow screens: the button opens it, and a click
// outside it or Escape closes it.
(function () {
	'use strict';
	function set(open) {
		var top = document.querySelector('header.top');
		var b = top && top.querySelector('[data-menu-toggle]');
		if (!b) return;
		top.classList.toggle('open', open);
		b.setAttribute('aria-expanded', open ? 'true' : 'false');
	}
	document.addEventListener('click', function (e) {
		if (!e.target.closest) return;
		if (e.target.closest('[data-menu-toggle]')) { set(!document.querySelector('header.top.open')); return; }
		if (!e.target.closest('.menu-items')) set(false);
	});
	document.addEventListener('keydown', function (e) { if (e.key === 'Escape') set(false); });
})();

// The visor list: the labels set in the Angular UI (kept in this origin's
// localStorage under <hypervisor key>labelsData, storage.service.ts), the
// search box, the online and offline filters and the nested rows of a
// sub-hypervisor. Applied again after every poll.
(function () {
	'use strict';
	var expanded = {};
	function labels() {
		var hv = document.body.getAttribute('data-hvpk'), m = {};
		if (!hv) return m;
		try {
			(JSON.parse(localStorage.getItem(hv + 'labelsData') || '[]') || []).forEach(function (l) {
				if (l && l.id && l.label) m[l.id] = l.label;
			});
		} catch (_) { /* no storage or bad data */ }
		return m;
	}
	function apply() {
		var card = document.querySelector('[data-nodes]');
		if (!card) return;
		var m = labels();
		var qi = card.querySelector('[data-node-q]');
		var q = qi ? qi.value.toLowerCase().trim() : '';
		var sb = card.querySelector('[data-node-state][aria-pressed="true"]');
		var st = sb ? sb.getAttribute('data-node-state') : '';
		var shown = 0, total = 0;
		card.querySelectorAll('#nodes tbody tr').forEach(function (tr) {
			var pk = tr.getAttribute('data-pk'), parent = tr.getAttribute('data-parent'), ok = true;
			if (pk) {
				var lab = m[pk], a = tr.querySelector('[data-label-for]'), h = tr.querySelector('[data-host-for]');
				if (lab && a && a.textContent !== lab) a.textContent = lab;
				if (h) h.hidden = !lab || h.textContent === lab;
				var text = (tr.getAttribute('data-text') || '') + ' ' + (lab || '').toLowerCase();
				ok = (!q || text.indexOf(q) >= 0) && (!st || (st === 'on') === (tr.getAttribute('data-online') === '1'));
			}
			if (!parent) { total++; if (ok) shown++; }
			tr.classList.toggle('open', !parent || !!expanded[parent] || (!!q && ok));
			tr.style.display = ok ? '' : 'none';
		});
		card.querySelectorAll('[data-node-toggle]').forEach(function (b) {
			b.setAttribute('aria-expanded', expanded[b.getAttribute('data-node-toggle')] ? 'true' : 'false');
		});
		var c = card.querySelector('[data-node-shown]');
		if (c) c.textContent = (q || st) ? c.getAttribute('data-fmt').replace('{{ n }}', shown).replace('{{ total }}', total) : '';
	}
	document.addEventListener('input', function (e) { if (e.target.matches && e.target.matches('[data-node-q]')) apply(); });
	document.addEventListener('click', function (e) {
		if (!e.target.closest) return;
		var s = e.target.closest('[data-node-state]');
		if (s) {
			var on = s.getAttribute('aria-pressed') !== 'true';
			document.querySelectorAll('[data-node-state]').forEach(function (b) { b.setAttribute('aria-pressed', 'false'); });
			s.setAttribute('aria-pressed', on ? 'true' : 'false');
			apply();
			return;
		}
		var t = e.target.closest('[data-node-toggle]');
		if (t) { var pk = t.getAttribute('data-node-toggle'); expanded[pk] = !expanded[pk]; apply(); }
	});
	document.addEventListener('htmx:afterSettle', apply);
	document.addEventListener('DOMContentLoaded', apply);
})();
