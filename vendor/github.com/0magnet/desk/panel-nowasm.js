// panel-nowasm.js — the desk chrome as a plain-JS asset, for no-wasm pages.
//
// The persistent taskbar + window registry the desk surfaces share, in plain
// JS with no wasm dependency. It replaced the retired browse.js panel
// (SkywireBrowse.mountPanel): the browsing ENGINE moved into the wasm-visor
// binary (globalThis.skywireBrowser), but the CHROME — the always-on bar, the
// ☰ launcher menu, per-window taskbar buttons — must exist even on pages that
// load no wasm at all (the native hypervisor's desk is a shell OVER the host
// visor; nothing wasm-shaped boots there). Launcher entries appear only for
// capabilities that are actually present on the page (a terminal needs
// skywireShell, a browser window needs SkywireGoBrowser), so one panel serves
// every desk mode.
//
//	globalThis.skywireDeskPanel.mount(document, opts) → panel
//	  opts.dashboardURL — same-origin URL for the dashboard window (omit = no entry)
//	  opts.title       — taskbar label (default "skywire")
//	panel.open(winboxOpts) → WinBox registered on the taskbar
//	panel.root / panel.bar — the windows root / taskbar elements
//
// Published as globalThis.__skywireDesk by the callers (desk-boot, the native
// launcher) once mounted — the signal desk-boot's waiters key on. Element ids
// (skywire-skynet-root / skywire-skynet-taskbar) are kept from the previous
// panel so existing geometry code keeps working.
(function () {
	'use strict';
	if (globalThis.skywireDeskPanel) return;

	function mount(doc, opts) {
		opts = opts || {};
		var root = doc.getElementById('skywire-skynet-root');
		if (!root) {
			root = doc.createElement('div');
			root.id = 'skywire-skynet-root';
			root.style.cssText = 'position:fixed;inset:0;pointer-events:none;z-index:50';
			// The root spans the viewport and is pointer-events:none so clicks fall
			// through to the page wherever no window covers it. Anything mounted in
			// it must opt back in or it renders and updates but cannot be clicked --
			// the taskbar and menu set auto inline, windows had nothing, so every
			// WinBox was inert. A rule rather than a per-instance assignment: it
			// covers windows opened later and does not depend on the WinBox API
			// exposing its root element.
			var peStyle = doc.createElement('style');
			peStyle.textContent = '#skywire-skynet-root .winbox{pointer-events:auto}';
			root.appendChild(peStyle);
			doc.body.appendChild(root);
		}
		var bar = doc.getElementById('skywire-skynet-taskbar');
		if (!bar) {
			bar = doc.createElement('div');
			bar.id = 'skywire-skynet-taskbar';
			bar.style.cssText = 'position:fixed;left:0;right:0;bottom:0;height:36px;display:flex;' +
				'align-items:center;gap:6px;padding:0 8px;background:#100d18;color:#cdd2da;' +
				'border-top:1px solid #2a2342;font:12px monospace;z-index:60;pointer-events:auto';
			doc.body.appendChild(bar);
		}
		bar.innerHTML = '';

		var menuBtn = doc.createElement('button');
		menuBtn.textContent = '☰';
		menuBtn.title = 'desk menu';
		menuBtn.style.cssText = 'background:#2a2342;color:#cdd2da;border:1px solid #3a3352;' +
			'cursor:pointer;font:14px monospace;padding:2px 10px;border-radius:4px';
		bar.appendChild(menuBtn);

		var label = doc.createElement('span');
		label.textContent = opts.title || 'skywire';
		label.style.cssText = 'color:#9d7cff;font-weight:600;margin-right:6px';
		bar.appendChild(label);

		var winArea = doc.createElement('div');
		winArea.style.cssText = 'display:flex;gap:4px;overflow-x:auto;flex:1';
		bar.appendChild(winArea);

		var menu = doc.createElement('div');
		menu.style.cssText = 'position:fixed;left:8px;bottom:40px;display:none;flex-direction:column;' +
			'background:#17141f;border:1px solid #2a2342;border-radius:6px;padding:4px;z-index:61;' +
			'pointer-events:auto;font:12px monospace;min-width:160px';
		doc.body.appendChild(menu);
		menuBtn.addEventListener('click', function () {
			menu.style.display = menu.style.display === 'none' ? 'flex' : 'none';
		});

		function menuItem(text, fn) {
			var b = doc.createElement('button');
			b.textContent = text;
			b.style.cssText = 'background:none;border:none;color:#cdd2da;text-align:left;' +
				'cursor:pointer;font:inherit;padding:6px 10px';
			b.addEventListener('mouseenter', function () { b.style.background = '#2a2342'; });
			b.addEventListener('mouseleave', function () { b.style.background = 'none'; });
			b.addEventListener('click', function () { menu.style.display = 'none'; fn(); });
			menu.appendChild(b);
			return b;
		}

		var panel = {
			root: root,
			bar: bar,
			// open wraps WinBox: window mounts in the panel's root (shared
			// stacking context) above the taskbar, and gets a taskbar button
			// that focuses/minimizes it and disappears on close.
			open: function (wo) {
				wo = wo || {};
				wo.root = root;
				wo.bottom = wo.bottom === undefined ? 36 : wo.bottom;
				if (!wo['class']) wo['class'] = ['skywire-wb', 'no-full'];
				var btn = doc.createElement('button');
				btn.textContent = wo.title || 'window';
				btn.style.cssText = 'background:#1b1726;color:#cdd2da;border:1px solid #2a2342;' +
					'cursor:pointer;font:inherit;padding:2px 8px;border-radius:4px;white-space:nowrap';
				var onclose = wo.onclose;
				wo.onclose = function (force) {
					btn.remove();
					if (onclose) return onclose.call(this, force);
					return false;
				};
				var wb = new globalThis.WinBox(wo);
				btn.addEventListener('click', function () {
					if (wb.min) { wb.minimize(false); }
					wb.focus();
				});
				winArea.appendChild(btn);
				return wb;
			},
		};

		if (opts.dashboardURL) {
			menuItem('dashboard', function () {
				panel.open({ title: 'dashboard', url: opts.dashboardURL, x: 'center', y: 'center', width: '85%', height: '85%' });
			});
		}
		// Capability-gated entries: present only when the page actually carries
		// the machinery (the wasm-visor DOM instance installs these; the native
		// desk page has neither and shows neither).
		if (globalThis.skywireShell && typeof globalThis.skywireShell.open === 'function') {
			menuItem('terminal', function () {
				var wb = panel.open({ title: 'terminal', x: 'center', y: 'center', width: '70%', height: '60%' });
				globalThis.skywireShell.open(wb.body);
			});
		}
		if (globalThis.SkywireGoBrowser && typeof globalThis.SkywireGoBrowser.open === 'function') {
			menuItem('browser', function () { globalThis.SkywireGoBrowser.open(); });
		}
		return panel;
	}

	globalThis.skywireDeskPanel = { mount: mount };
})();
