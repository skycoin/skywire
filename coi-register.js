// coi-register.js — the page half of coi-sw.js.
//
// Isolation needs the worker to be CONTROLLING the page, and the first
// navigation is never controlled: it came from the server, without the
// headers. So this registers the worker and reloads once, after control
// changes, and from then on the navigation is served from the worker with
// COOP/COEP attached.
//
// Guarded three ways, because a reload loop on a page that can never be
// isolated is worse than no isolation: it does nothing when the page is
// already isolated or the browser has no service workers, it does not reload
// when a controller is already present (tried, still not isolated — the
// browser is refusing, so stop), and it marks sessionStorage so the reload
// happens at most once per session.
//
// Load it before anything that wants SharedArrayBuffer. Callers must still
// tolerate crossOriginIsolated being false on first paint: proc.spawnWorker
// does, by refusing and letting the caller fall back to proc.spawn.
(function () {
	if (globalThis.crossOriginIsolated || !('serviceWorker' in navigator)) return;
	var KEY = 'coi-sw-reloaded';
	navigator.serviceWorker.register('coi-sw.js', { scope: './' }).then(function () {
		if (navigator.serviceWorker.controller) return;
		navigator.serviceWorker.addEventListener('controllerchange', function () {
			if (sessionStorage.getItem(KEY)) return;
			try { sessionStorage.setItem(KEY, '1'); } catch (e) { /* storage denied — reload anyway, once */ }
			location.reload();
		});
	}).catch(function () { /* registration refused — no isolation, callers fall back */ });
})();
