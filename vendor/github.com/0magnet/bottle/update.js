// update.js — tells an open page that a newer build has been deployed.
//
//	<script src="update.js" data-stamp="version.txt"></script>
//
// data-stamp names a file the build writes beside what it built, holding a
// hash of it (resolved against this script's own URL; version.txt by
// default). The page keeps the stamp it loaded with, reads it again past
// every cache every five minutes and whenever the tab comes back into view,
// and when it has changed puts up a notice. Never more than that: these pages
// hold work a reload would lose (a shell, open windows, a program being
// built), so the reload is the user's.
//
// The reload re-validates every file the page loaded before reloading it. A
// static host's files are cached for minutes (GitHub Pages: max-age=600), and
// a plain reload in that window would rebuild the new page from the old
// wasm; fetch with cache:'no-cache' asks the server again, gets a 304 for
// what is unchanged and stores what is new. Files only a worker fetched are
// not in the page's list and are left to their cache.
//
// The stamp the page started with is read through the cache, deliberately:
// a page served its wasm from the cache was served the stamp beside it from
// the cache too, so the two agree on the build that is running.
(function () {
	'use strict';
	var EVERY = 5 * 60e3;
	var me = document.currentScript;
	var stampURL = new URL((me && me.getAttribute('data-stamp')) || 'version.txt', me ? me.src : location.href).href;
	var build = '', seen = '', note = null;
	try { performance.setResourceTimingBufferSize(5000); } catch (e) { /* the list stays at its default size */ }

	function read(cache) {
		var u = cache === 'no-store' ? stampURL + '?t=' + Date.now() : stampURL;
		return fetch(u, {cache: cache}).then(function (r) {
			if (!r.ok) throw new Error(stampURL + ': ' + r.status);
			return r.text();
		}).then(function (s) { return s.trim(); });
	}

	function check() {
		read('no-store').then(function (s) {
			if (s && s !== build && s !== seen) show(s);
		}, function () { /* offline, or between deploys: the next check will tell */ });
	}

	// refresh re-validates what the page loaded, four at a time, reading
	// each body so the cache keeps it, then reloads.
	function refresh() {
		var urls = [location.href.split('#')[0]], have = {};
		performance.getEntriesByType('resource').forEach(function (e) {
			var u = e.name;
			if (u.indexOf(location.origin + '/') === 0 && u.indexOf('?t=') < 0 && !have[u]) {
				have[u] = true;
				urls.push(u);
			}
		});
		var next = 0;
		function one() {
			if (next >= urls.length) return Promise.resolve();
			var u = urls[next++];
			return fetch(u, {cache: 'no-cache'}).then(function (r) { return r.arrayBuffer(); })
				.catch(function () { /* a file that is gone is the new build's business */ }).then(one);
		}
		return Promise.all([one(), one(), one(), one()]).then(function () { location.reload(); });
	}

	function show(s) {
		if (!note) {
			note = document.createElement('div');
			note.setAttribute('role', 'status');
			note.style.cssText = 'position:fixed;right:12px;bottom:12px;z-index:2147483647;' +
				'font:13px/1.4 system-ui,sans-serif;color:#e5e7eb;background:#1f2430;border:1px solid #3b4252;' +
				'border-radius:6px;padding:8px 10px;box-shadow:0 4px 16px rgba(0,0,0,.4);display:flex;gap:10px;align-items:center;';
			note.innerHTML = '<span>A newer build of this page is available.</span>' +
				'<button type="button" data-a="reload">Reload</button>' +
				'<button type="button" data-a="dismiss" aria-label="Dismiss">×</button>';
			note.querySelectorAll('button').forEach(function (b) {
				b.style.cssText = 'font:inherit;color:inherit;background:#2e3440;border:1px solid #4c566a;border-radius:4px;padding:2px 8px;cursor:pointer;';
			});
			note.addEventListener('click', function (e) {
				var a = e.target.getAttribute('data-a');
				if (a === 'reload') {
					note.firstChild.textContent = 'Updating…';
					refresh();
				}
				if (a === 'dismiss') {
					seen = note.getAttribute('data-build'); // until a build newer than this one
					note.style.display = 'none';
				}
			});
			document.body.appendChild(note);
		}
		note.setAttribute('data-build', s);
		note.style.display = 'flex';
	}

	read('default').then(function (s) {
		build = s;
		setInterval(check, EVERY);
		document.addEventListener('visibilitychange', function () { if (!document.hidden) check(); });
	}, function () { /* no stamp beside this page: nothing to watch */ });
})();
