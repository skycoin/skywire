// Entry point: initializes the application

import { wireEventListeners } from './events';
import { toggleSection, focusVisor } from './sidebar';
import { showGlobe, hideGlobe, toggleGlobeView, setVoronoiMode, isVoronoiModeActive } from './globe';

import { mount } from './mount';
import { installVisibilityGate } from './lifecycle';
import { normalizeView } from './persist';

// Expose functions needed by inline onclick handlers in HTML
(window as any).toggleSection = toggleSection;
(window as any).focusVisor = focusVisor;
(window as any).showGlobe = showGlobe;
(window as any).hideGlobe = hideGlobe;
(window as any).toggleGlobeView = toggleGlobeView;
(window as any).setVoronoiMode = setVoronoiMode;
(window as any).isVoronoiModeActive = isVoronoiModeActive;

// Embeddable entry: a host (Angular tab / WinBox window) calls
// SkywireTpviz.mount(container). See docs/design/gui-embedding-standardization.md.
(window as any).SkywireTpviz = {
  mount,
  // setView lets a host switch the view on an already-mounted instance (e.g. an
  // Angular tab reacting to a ?view= change). No-op until mount() has wired it.
  setView(v: string) {
    const nv = normalizeView(v);
    const fn = (window as any).__tpvizSetView;
    if (nv && typeof fn === 'function') { fn(nv); }
  },
};

// Standalone page: index.html already carries the app DOM (+ global <style>), so
// just wire it. When embedded, #container is absent until mount() injects it, so
// this no-ops and the host drives mount() instead.
if (document.getElementById('container')) {
  wireEventListeners();
  // Standalone page: suspend data sync + rendering while the tab is backgrounded.
  installVisibilityGate(document.getElementById('container'));
}
