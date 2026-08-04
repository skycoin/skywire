// Entry point: initializes the application

import { wireEventListeners } from './events';
import { toggleSection, focusVisor } from './sidebar';
import { showGlobe, hideGlobe, toggleGlobeView, setVoronoiMode, isVoronoiModeActive } from './globe';

import { mount } from './mount';

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
(window as any).SkywireTpviz = { mount };

// Standalone page: index.html already carries the app DOM (+ global <style>), so
// just wire it. When embedded, #container is absent until mount() injects it, so
// this no-ops and the host drives mount() instead.
if (document.getElementById('container')) {
  wireEventListeners();
}
