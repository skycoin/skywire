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
		if (x.status === 401) { window.location.href = 'login'; return; }
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
// search box, the quick filters, the column sort and the nested rows of a
// sub-hypervisor. Applied again after every poll.
(function () {
	'use strict';
	var expanded = {};
	var sortKey = '', sortDesc = false;
	try { var s0 = JSON.parse(localStorage.getItem('ui2-node-sort') || 'null'); if (s0) { sortKey = s0.k || ''; sortDesc = !!s0.d; } } catch (_) { /* none */ }
	function hvKey(k) { return (document.body.getAttribute('data-hvpk') || '') + k; }
	function readJSON(k) {
		try { return JSON.parse(localStorage.getItem(hvKey(k)) || '[]') || []; } catch (_) { return []; }
	}
	function writeJSON(k, v) {
		try { localStorage.setItem(hvKey(k), JSON.stringify(v)); } catch (_) { /* no storage */ }
	}
	function labels() {
		var m = {};
		if (!document.body.getAttribute('data-hvpk')) return m;
		readJSON('labelsData').forEach(function (l) { if (l && l.id && l.label) m[l.id] = l.label; });
		return m;
	}
	// saveLabel follows storage.service.ts saveLabel: an empty label removes it.
	function saveLabel(pk, label, type) {
		var ls = readJSON('labelsData').filter(function (l) { return l && l.id !== pk; });
		if (label) ls.push({ id: pk, label: label, identifiedElementType: type || 'nd' });
		writeJSON('labelsData', ls);
	}
	// hide marks forgotten visors hidden for the Angular list too
	// (storage.service.ts setLocalNodesAsHidden).
	function hide(pks) {
		var ns = readJSON('localNodesData'), seen = {};
		ns.forEach(function (n) { if (pks.indexOf(n.publicKey) >= 0) { n.hidden = true; seen[n.publicKey] = 1; } });
		pks.forEach(function (pk) { if (!seen[pk]) ns.push({ publicKey: pk, hidden: true, ip: null }); });
		writeJSON('localNodesData', ns);
	}
	function sortVal(tr, k) {
		if (k === 'label') { var a = tr.querySelector('[data-label-for]'); return a ? a.textContent.toLowerCase() : ''; }
		if (k === 'pk') return tr.getAttribute('data-pk') || '';
		if (k === 'reward') return tr.getAttribute('data-reward') || '';
		var v = tr.getAttribute('data-sk-' + k) || '';
		return (k === 'tps' || k === 'apps' || k === 'ping') ? Number(v) || 0 : v.toLowerCase();
	}
	function sortRows(card) {
		card.querySelectorAll('[data-node-sort]').forEach(function (b) {
			var on = b.getAttribute('data-node-sort') === sortKey;
			b.parentNode.setAttribute('aria-sort', on ? (sortDesc ? 'descending' : 'ascending') : 'none');
			b.querySelector('.mi').textContent = on ? (sortDesc ? 'arrow_downward' : 'arrow_upward') : '';
		});
		var tb = card.querySelector('#nodes tbody');
		if (!tb || !sortKey) return;
		var groups = [], cur = null;
		Array.prototype.forEach.call(tb.children, function (tr) {
			if (!tr.getAttribute('data-parent') || !cur) { cur = { head: tr, rows: [] }; groups.push(cur); }
			cur.rows.push(tr);
		});
		groups.sort(function (x, y) {
			var a = sortVal(x.head, sortKey), b = sortVal(y.head, sortKey);
			var c = a < b ? -1 : a > b ? 1 : 0;
			return sortDesc ? -c : c;
		});
		groups.forEach(function (g) { g.rows.forEach(function (tr) { tb.appendChild(tr); }); });
	}
	function matches(tr, st) {
		switch (st) {
		case 'on': return tr.getAttribute('data-online') === '1';
		case 'off': return tr.getAttribute('data-online') !== '1';
		case 'outdated': return tr.getAttribute('data-outdated') === '1';
		case 'noreward': return tr.getAttribute('data-reward') !== '1';
		}
		return true;
	}
	var sel = {};
	// syncBulk keeps the ticked visors across polls and fills the bulk bar.
	function syncBulk(card) {
		var bar = document.querySelector('[data-bulk]');
		if (!bar) return;
		var present = {}, pks = [];
		card.querySelectorAll('[data-bulk-sel]').forEach(function (cb) {
			var pk = cb.getAttribute('data-bulk-sel');
			present[pk] = 1;
			cb.checked = !!sel[pk];
		});
		Object.keys(sel).forEach(function (pk) { if (present[pk]) pks.push(pk); else delete sel[pk]; });
		bar.hidden = pks.length === 0;
		var c = bar.querySelector('[data-bulk-count]');
		if (c) c.textContent = c.getAttribute('data-fmt').replace('{{ n }}', pks.length);
		var box = bar.querySelector('[data-bulk-pks]');
		if (box) {
			box.textContent = '';
			pks.forEach(function (pk) { var i = document.createElement('input'); i.type = 'hidden'; i.name = 'pk'; i.value = pk; box.appendChild(i); });
		}
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
				if (a) a.textContent = lab || (h ? h.textContent : a.textContent);
				if (h) h.hidden = !lab || h.textContent === lab;
				var text = (tr.getAttribute('data-text') || '') + ' ' + (lab || '').toLowerCase();
				ok = (!q || text.indexOf(q) >= 0) && matches(tr, st);
			}
			if (!parent) { total++; if (ok) shown++; }
			tr.classList.toggle('open', !parent || !!expanded[parent] || (!!q && ok));
			tr.style.display = ok ? '' : 'none';
		});
		card.querySelectorAll('[data-node-toggle]').forEach(function (b) {
			b.setAttribute('aria-expanded', expanded[b.getAttribute('data-node-toggle')] ? 'true' : 'false');
		});
		sortRows(card);
		syncBulk(card);
		document.querySelectorAll('#bulk-panel [data-label-for]').forEach(function (el) { var l = m[el.getAttribute('data-label-for')]; if (l) el.textContent = l; });
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
		var so = e.target.closest('[data-node-sort]');
		if (so) {
			var k = so.getAttribute('data-node-sort');
			if (k !== sortKey) { sortKey = k; sortDesc = false; } else if (!sortDesc) { sortDesc = true; } else { sortKey = ''; sortDesc = false; }
			try { localStorage.setItem('ui2-node-sort', JSON.stringify({ k: sortKey, d: sortDesc })); } catch (_) { /* no storage */ }
			apply();
			return;
		}
		var el = e.target.closest('[data-edit-label]');
		if (el) {
			var pk = el.getAttribute('data-edit-label');
			var l = window.prompt(el.getAttribute('data-prompt'), labels()[pk] || '');
			if (l !== null) { saveLabel(pk, l.trim(), el.getAttribute('data-label-type')); apply(); document.dispatchEvent(new Event('ui2:labels')); }
			return;
		}
		var t = e.target.closest('[data-node-toggle]');
		if (t) { var tp = t.getAttribute('data-node-toggle'); expanded[tp] = !expanded[tp]; apply(); }
	});
	document.addEventListener('change', function (e) {
		var t = e.target;
		if (!t.matches) return;
		if (t.matches('[data-bulk-sel]')) {
			if (t.checked) sel[t.getAttribute('data-bulk-sel')] = 1; else delete sel[t.getAttribute('data-bulk-sel')];
		} else if (t.matches('[data-bulk-all]')) {
			document.querySelectorAll('#nodes tbody tr').forEach(function (tr) {
				var cb = tr.querySelector('[data-bulk-sel]');
				if (!cb || tr.style.display === 'none' || !tr.classList.contains('open')) return;
				if (t.checked) sel[cb.getAttribute('data-bulk-sel')] = 1; else delete sel[cb.getAttribute('data-bulk-sel')];
			});
		} else return;
		apply();
	});
	document.addEventListener('click', function (e) {
		if (!e.target.closest || !e.target.closest('[data-bulk-clear]')) return;
		sel = {};
		var p = document.getElementById('bulk-panel');
		if (p) p.textContent = '';
		var a = document.querySelector('[data-bulk-all]');
		if (a) a.checked = false;
		apply();
	});
	document.addEventListener('htmx:beforeRequest', function (e) {
		var el = e.detail.elt;
		if (!el || !el.hasAttribute || !el.hasAttribute('data-hide-offline')) return;
		var pks = [];
		document.querySelectorAll('#nodes tbody tr[data-online="0"] [data-hide-pk]').forEach(function (b) { pks.push(b.getAttribute('data-hide-pk')); });
		el.setAttribute('data-hide-list', pks.join(','));
	});
	document.addEventListener('htmx:afterRequest', function (e) {
		var el = e.detail.elt;
		if (!e.detail.successful || !el || !el.getAttribute) return;
		var one = el.getAttribute('data-hide-pk'), list = el.getAttribute('data-hide-list');
		if (one) hide([one]);
		else if (list) hide(list.split(','));
	});
	document.addEventListener('htmx:afterSettle', apply);
	document.addEventListener('DOMContentLoaded', apply);
})();

// The hypervisor settings page: theme, refresh rate and the label list, all
// kept in this browser under the Angular UI's keys (storage.service.ts).
(function () {
	'use strict';
	function hvKey(k) { return (document.body.getAttribute('data-hvpk') || '') + k; }
	function readLabels() {
		try { return JSON.parse(localStorage.getItem(hvKey('labelsData')) || '[]') || []; } catch (_) { return []; }
	}
	function writeLabels(ls) {
		try { localStorage.setItem(hvKey('labelsData'), JSON.stringify(ls)); } catch (_) { /* no storage */ }
	}
	function cell(tr, text, cls) {
		var td = document.createElement('td');
		if (cls) td.className = cls;
		td.textContent = text;
		tr.appendChild(td);
		return td;
	}
	function iconBtn(icon, label, attr, id) {
		var b = document.createElement('button');
		b.type = 'button';
		b.className = 'icon-btn sm';
		b.title = label;
		b.setAttribute('aria-label', label);
		b.setAttribute(attr, id);
		var s = document.createElement('span');
		s.className = 'mi';
		s.textContent = icon;
		b.appendChild(s);
		return b;
	}
	function renderLabels() {
		var box = document.querySelector('[data-labels-list]');
		if (!box) return;
		var d = box.dataset, ls = readLabels();
		box.textContent = '';
		if (ls.length === 0) {
			var p = document.createElement('p');
			p.className = 'dim';
			p.textContent = d.empty;
			box.appendChild(p);
			return;
		}
		var wrap = document.createElement('div'), t = document.createElement('table'), hr = document.createElement('tr');
		wrap.className = 'table-wrap';
		t.className = 'list';
		[d.colLabel, d.colId, d.colType, ''].forEach(function (h) { var th = document.createElement('th'); th.textContent = h; hr.appendChild(th); });
		var th = document.createElement('thead'), tb = document.createElement('tbody');
		th.appendChild(hr);
		ls.forEach(function (l) {
			if (!l || !l.id) return;
			var tr = document.createElement('tr');
			cell(tr, l.label).setAttribute('data-label', d.colLabel);
			cell(tr, l.id, 'mono small pk-text').setAttribute('data-label', d.colId);
			cell(tr, d['type' + (l.identifiedElementType || '').charAt(0).toUpperCase() + (l.identifiedElementType || '').slice(1)] || l.identifiedElementType || '').setAttribute('data-label', d.colType);
			var a = cell(tr, '', 'actions');
			a.appendChild(iconBtn('edit', d.edit, 'data-label-edit', l.id));
			a.appendChild(iconBtn('delete', d['delete'], 'data-label-del', l.id));
			tb.appendChild(tr);
		});
		t.appendChild(th);
		t.appendChild(tb);
		wrap.appendChild(t);
		box.appendChild(wrap);
	}
	document.addEventListener('click', function (e) {
		if (!e.target.closest) return;
		var box = document.querySelector('[data-labels-list]');
		var ed = e.target.closest('[data-label-edit]'), del = e.target.closest('[data-label-del]');
		if (!box || (!ed && !del)) return;
		var id = (ed || del).getAttribute(ed ? 'data-label-edit' : 'data-label-del'), ls = readLabels();
		if (del) {
			if (!window.confirm(box.dataset.confirm)) return;
			writeLabels(ls.filter(function (l) { return l && l.id !== id; }));
		} else {
			var cur = ls.filter(function (l) { return l && l.id === id; })[0];
			var v = window.prompt(box.dataset.prompt, cur ? cur.label : '');
			if (v === null) return;
			v = v.trim();
			writeLabels(v ? ls.map(function (l) { if (l && l.id === id) l.label = v; return l; }) : ls.filter(function (l) { return l && l.id !== id; }));
		}
		renderLabels();
	});
	document.addEventListener('change', function (e) {
		var t = e.target;
		if (!t.matches) return;
		if (t.matches('[data-theme-choice] input')) {
			if (t.value === 'system') document.documentElement.removeAttribute('data-theme');
			else document.documentElement.setAttribute('data-theme', t.value);
			try { if (t.value === 'system') localStorage.removeItem('hvui-theme'); else localStorage.setItem('hvui-theme', t.value); } catch (_) { /* no storage */ }
		} else if (t.matches('[data-refresh]')) {
			document.cookie = 'ui2-refresh=' + t.value + ';path=/;max-age=31536000;samesite=lax';
			try { localStorage.setItem(hvKey('refreshSeconds'), t.value); } catch (_) { /* no storage */ }
			var toast = document.getElementById('toast');
			if (toast) { toast.textContent = t.getAttribute('data-done'); toast.className = 'show'; setTimeout(function () { toast.className = ''; }, 3000); }
		}
	});
	document.addEventListener('DOMContentLoaded', function () {
		var th = document.documentElement.getAttribute('data-theme') || 'system';
		document.querySelectorAll('[data-theme-choice] input').forEach(function (i) { i.checked = i.value === th; });
		renderLabels();
	});
	// First visit after choosing a refresh rate in the Angular UI.
	if (document.cookie.indexOf('ui2-refresh=') < 0) {
		try {
			var r = localStorage.getItem(hvKey('refreshSeconds'));
			if (r) document.cookie = 'ui2-refresh=' + r + ';path=/;max-age=31536000;samesite=lax';
		} catch (_) { /* no storage */ }
	}
})();

// A [data-labels] section outside the visor list shows the same labels.
(function () {
	'use strict';
	function apply() {
		var hv = document.body.getAttribute('data-hvpk'), m = {};
		if (!hv) return;
		try {
			(JSON.parse(localStorage.getItem(hv + 'labelsData') || '[]') || []).forEach(function (l) { if (l && l.id && l.label) m[l.id] = l.label; });
		} catch (_) { return; }
		document.querySelectorAll('[data-labels] [data-label-for]').forEach(function (a) {
			var pk = a.getAttribute('data-label-for'), lab = m[pk], h = a.closest('td, div').parentNode.querySelector('[data-host-for="' + pk + '"]');
			if (a.hasAttribute('data-label-only')) { a.textContent = lab || ''; a.hidden = !lab; return; }
			if (lab) a.textContent = lab;
			if (h) h.hidden = !lab || h.textContent === lab;
		});
	}
	document.addEventListener('ui2:labels', apply);
	document.addEventListener('htmx:afterSettle', apply);
	document.addEventListener('DOMContentLoaded', apply);
})();

// Chat: reply to a message, Ctrl+Enter sends, and a sent form is cleared.
(function () {
	'use strict';
	document.addEventListener('click', function (e) {
		if (!e.target.closest) return;
		var f = document.getElementById('chat-form');
		var r = e.target.closest('[data-reply]');
		if (r && f) {
			f.querySelector('[data-reply-input]').value = r.getAttribute('data-reply');
			f.querySelector('[data-reply-text]').textContent = '↩ ' + (r.getAttribute('data-reply-quote') || '').slice(0, 80);
			f.querySelector('[data-reply-show]').hidden = false;
			var ta = f.querySelector('textarea');
			if (ta) ta.focus();
			return;
		}
		if (e.target.closest('[data-reply-cancel]') && f) {
			f.querySelector('[data-reply-input]').value = '';
			f.querySelector('[data-reply-show]').hidden = true;
		}
	});
	// The log stays at its newest message across polls unless the reader scrolled up.
	var atEnd = true;
	function toEnd() { var l = document.getElementById('chat-log'); if (l && atEnd) l.scrollTop = l.scrollHeight; }
	document.addEventListener('htmx:beforeSwap', function (e) {
		var l = document.getElementById('chat-log');
		if (l && e.detail.target === l) atEnd = l.scrollHeight - l.scrollTop - l.clientHeight < 40;
	});
	document.addEventListener('htmx:afterSettle', toEnd);
	document.addEventListener('DOMContentLoaded', toEnd);
	window.addEventListener('load', toEnd);
	document.addEventListener('keydown', function (e) {
		if (e.key !== 'Enter' || !(e.ctrlKey || e.metaKey) || !e.target.matches || !e.target.matches('[data-ctrl-enter]')) return;
		e.preventDefault();
		if (e.target.form) e.target.form.requestSubmit();
	});
	document.addEventListener('htmx:afterRequest', function (e) {
		var el = e.detail.elt;
		if (!e.detail.successful || !el || !el.matches || !el.matches('form[data-reset-on-ok]')) return;
		var ta = el.querySelector('textarea');
		if (ta) ta.value = '';
		var ri = el.querySelector('[data-reply-input]');
		if (ri) { ri.value = ''; el.querySelector('[data-reply-show]').hidden = true; }
	});
})();

// A polled region keeps its open [data-open-key] details open across swaps.
(function () {
	'use strict';
	var open = {};
	document.addEventListener('htmx:beforeSwap', function (e) {
		var t = e.detail.target;
		if (!t || !t.querySelectorAll) return;
		t.querySelectorAll('details[data-open-key]').forEach(function (d) { open[d.getAttribute('data-open-key')] = d.open; });
	});
	document.addEventListener('htmx:afterSettle', function () {
		document.querySelectorAll('details[data-open-key]').forEach(function (d) {
			var k = d.getAttribute('data-open-key');
			if (open[k]) d.open = true;
		});
	});
})();

// A polled [data-keep-checked] list keeps its ticked boxes across swaps.
(function () {
	'use strict';
	var kept = {};
	document.addEventListener('htmx:beforeSwap', function (e) {
		var t = e.detail.target;
		if (!t || !t.hasAttribute || !t.hasAttribute('data-keep-checked')) return;
		var m = {};
		t.querySelectorAll('input[type=checkbox]:checked').forEach(function (c) { m[c.name + '=' + c.value] = 1; });
		kept[t.id] = m;
	});
	document.addEventListener('htmx:afterSettle', function () {
		Object.keys(kept).forEach(function (id) {
			var el = document.getElementById(id);
			if (!el) return;
			el.querySelectorAll('input[type=checkbox]').forEach(function (c) { if (kept[id][c.name + '=' + c.value]) c.checked = true; });
		});
	});
})();

// The apps list's selection bar shows while any app is ticked.
(function () {
	'use strict';
	function sync() {
		var bar = document.getElementById('apps-bulk');
		if (bar) bar.hidden = !document.querySelector('#apps-rows input[name=sel]:checked');
	}
	document.addEventListener('change', function (e) { if (e.target.matches && e.target.matches('#apps-rows input[name=sel]')) sync(); });
	document.addEventListener('htmx:afterSettle', sync);
})();

// History charts: each poll of #res-live carries its figures as data-s-*
// attributes, kept here for the last 120 samples and drawn as lines.
(function () {
	'use strict';
	var hist = {}, max = 120, seen = null;
	function fmt(v, unit) {
		if (unit === '%') return v.toFixed(1) + '%';
		if (unit === 'B' || unit === 'B/s') {
			var u = ['B', 'KiB', 'MiB', 'GiB'], i = 0;
			while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
			return v.toFixed(1) + ' ' + u[i] + (unit === 'B/s' ? '/s' : '');
		}
		return String(Math.round(v));
	}
	function draw() {
		var live = document.getElementById('res-live');
		if (!live || live === seen || !document.querySelector('[data-charts]')) return;
		seen = live;
		document.querySelectorAll('[data-chart]').forEach(function (c) {
			var k = c.getAttribute('data-chart'), raw = live.getAttribute('data-s-' + k);
			if (raw === null) return;
			var h = hist[k] = hist[k] || [];
			h.push(parseFloat(raw));
			if (h.length > max) h.shift();
			var unit = c.getAttribute('data-unit'), top = unit === '%' ? 100 : Math.max.apply(null, h) || 1;
			var pts = h.map(function (v, i) { return (i * 120 / (max - 1)).toFixed(1) + ',' + (40 - v / top * 38).toFixed(1); });
			c.querySelector('polyline').setAttribute('points', pts.join(' '));
			c.querySelector('.chart-now').textContent = fmt(h[h.length - 1], unit);
		});
	}
	document.addEventListener('htmx:afterSettle', draw);
	document.addEventListener('DOMContentLoaded', draw);
})();

// Voice call: the timer ticks between polls, and the level meter draws the
// call's send and receive levels (you violet, peer green) as the Angular tab.
(function () {
	'use strict';
	var sent = [], recv = [], max = 90, busy = false, clock = null, base = 0, secs = 0;
	function draw(c) {
		var ctx = c.getContext('2d'), w = c.width, h = c.height, bw = w / max;
		ctx.clearRect(0, 0, w, h);
		function bars(hist, color, wf) {
			ctx.fillStyle = color;
			hist.forEach(function (v, i) {
				var bh = Math.max(1, Math.pow(v, 0.6) * h);
				ctx.fillRect(i * bw + bw * (1 - wf) / 2, (h - bh) / 2, Math.max(1, bw * wf - 1), bh);
			});
		}
		bars(sent, 'rgba(157,124,255,0.55)', 1);
		bars(recv, 'rgba(158,206,106,0.95)', 0.6);
	}
	function tick() {
		var t = document.querySelector('[data-voice-secs]');
		if (t !== clock) { clock = t; base = Date.now(); secs = t ? parseInt(t.getAttribute('data-voice-secs'), 10) || 0 : 0; }
		if (t) {
			var s = secs + Math.floor((Date.now() - base) / 1000);
			t.textContent = Math.floor(s / 60) + ':' + ('0' + (s % 60)).slice(-2);
		}
		var c = document.querySelector('canvas[data-voice-levels]');
		if (!c) { sent = []; recv = []; return; }
		if (busy || document.visibilityState !== 'visible') return;
		busy = true;
		fetch(c.getAttribute('data-voice-levels'), { credentials: 'same-origin' })
			.then(function (r) { return r.ok ? r.json() : null; })
			.then(function (l) {
				busy = false;
				if (!l) return;
				sent.push(Math.min(1, l.sent || 0));
				recv.push(Math.min(1, l.recv || 0));
				if (sent.length > max) sent.shift();
				if (recv.length > max) recv.shift();
				var cur = document.querySelector('canvas[data-voice-levels]');
				if (cur) draw(cur);
			})
			.catch(function () { busy = false; });
	}
	setInterval(tick, 150);
	document.addEventListener('htmx:afterSettle', function () {
		var c = document.querySelector('canvas[data-voice-levels]');
		if (c) draw(c);
	});
})();

// Push alerts: "Notify this device" subscribes this browser as the Angular
// panel does (utils/web-push.ts) and posts the subscription in the form.
(function () {
	'use strict';
	var nav = navigator;
	function hint(f) {
		var ios = /iPad|iPhone|iPod/.test(nav.userAgent) || (nav.platform === 'MacIntel' && nav.maxTouchPoints > 1);
		var standalone = nav.standalone === true || window.matchMedia('(display-mode: standalone)').matches;
		if (ios && !standalone) return f.dataset.ios;
		if (!window.isSecureContext) return f.dataset.insecure;
		if (!('serviceWorker' in nav) || !('PushManager' in window)) return f.dataset.unsupported;
		return '';
	}
	function keyBytes(b64url) {
		var raw = atob(b64url.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - b64url.length % 4) % 4));
		var out = new Uint8Array(raw.length);
		for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
		return out;
	}
	function label(ua) {
		var os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' :
			/Windows/.test(ua) ? 'Windows' : /Mac OS X|Macintosh/.test(ua) ? 'Mac' : /Linux/.test(ua) ? 'Linux' : 'Device';
		var br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /(Chrome|CriOS)\//.test(ua) ? 'Chrome' :
			/Safari\//.test(ua) ? 'Safari' : '';
		return br ? os + ' ' + br : os;
	}
	// Under a desk the root worker (pkg/wasmhv/sw.js) carries the push
	// handlers, and registering another would replace it.
	function registration() {
		var sw = nav.serviceWorker;
		return sw.getRegistration('/').then(function (r) {
			return r || sw.register('push-sw.js').then(function () { return sw.ready; });
		});
	}
	function sameKey(a, b) {
		if (!a) return false;
		var x = new Uint8Array(a);
		return x.length === b.length && x.every(function (v, i) { return v === b[i]; });
	}
	// A frame from another origin, such as a desk's browser, cannot ask for
	// notification permission. The desk subscribes for it (pkg/wasmhv/browse-transport.js).
	function viaDesk(op, key) {
		var framed = window.parent !== window;
		try { if (framed && window.parent.location.href) framed = false; } catch (e) { /* cross-origin */ }
		if (!framed) return Promise.resolve(null);
		var id = Math.random().toString(36).slice(2);
		return new Promise(function (resolve, reject) {
			var timer = setTimeout(function () { done(); resolve(null); }, 1500);
			function done() { clearTimeout(timer); window.removeEventListener('message', on); }
			function on(e) {
				var d = e.data || {};
				if (e.source !== window.parent || d.type !== 'skywire-push-reply' || d.id !== id) return;
				if (d.ack) { clearTimeout(timer); return; }
				done();
				if (d.error) reject(new Error(d.error)); else resolve(d);
			}
			window.addEventListener('message', on);
			window.parent.postMessage({ type: 'skywire-push', id: id, op: op, key: key || '' }, '*');
		});
	}
	function subscribe(f) {
		return viaDesk('subscribe', f.dataset.pushKey).then(function (d) {
			return d ? { toJSON: function () { return d.sub; } } : subscribeHere(f);
		});
	}
	function subscribeHere(f) {
		return Notification.requestPermission().then(function (perm) {
			if (perm !== 'granted') throw new Error(f.dataset.blocked);
			return registration();
		}).then(function (reg) {
			var key = keyBytes(f.dataset.pushKey);
			return reg.pushManager.getSubscription().then(function (sub) {
				if (sub && !sameKey(sub.options.applicationServerKey, key)) return sub.unsubscribe().then(function () { return null; });
				return sub;
			}).then(function (sub) {
				return sub || reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
			});
		});
	}
	function endpoint() {
		return viaDesk('endpoint').catch(function () { return null; }).then(function (d) {
			return d ? d.endpoint || '' : endpointHere();
		});
	}
	function endpointHere() {
		if (!('serviceWorker' in nav)) return Promise.resolve('');
		return nav.serviceWorker.getRegistration('/').then(function (reg) {
			return reg && reg.pushManager ? reg.pushManager.getSubscription() : null;
		}).then(function (sub) { return sub ? sub.endpoint : ''; }).catch(function () { return ''; });
	}
	function mark() {
		var f = document.querySelector('form[data-push]');
		if (!f) return;
		var h = hint(f), hb = f.querySelector('[data-push-hint]');
		hb.textContent = h;
		hb.hidden = !h;
		if (h) f.querySelector('[data-push-add]').disabled = true;
		endpoint().then(function (ep) {
			var mine = false;
			document.querySelectorAll('[data-push-endpoint]').forEach(function (row) {
				var me = !!ep && row.getAttribute('data-push-endpoint') === ep;
				row.querySelector('[data-push-this]').hidden = !me;
				mine = mine || me;
			});
			if (mine) f.querySelector('[data-push-label]').textContent = f.dataset.again;
		});
	}
	document.addEventListener('click', function (e) {
		var b = e.target.closest && e.target.closest('[data-push-add]');
		if (!b) return;
		var f = b.form, lab = f.querySelector('[data-push-label]'), was = lab.textContent;
		b.disabled = true;
		lab.textContent = f.dataset.busy;
		subscribe(f).then(function (sub) {
			var j = sub.toJSON();
			j.label = label(nav.userAgent);
			f.querySelector('input[name=sub]').value = JSON.stringify(j);
			f.requestSubmit();
		}).catch(function (err) {
			b.disabled = false;
			lab.textContent = was;
			var hb = f.querySelector('[data-push-hint]');
			hb.textContent = (err && err.message) || String(err);
			hb.hidden = false;
		});
	});
	document.addEventListener('htmx:afterSettle', mark);
	document.addEventListener('DOMContentLoaded', mark);
})();
