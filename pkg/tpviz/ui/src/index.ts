// Entry point: initializes the application

import { wireEventListeners } from './events';
import { toggleSection, focusVisor } from './sidebar';
import { showGlobe, hideGlobe, toggleGlobeView, setVoronoiMode, isVoronoiModeActive } from './globe';

// Expose functions needed by inline onclick handlers in HTML
(window as any).toggleSection = toggleSection;
(window as any).focusVisor = focusVisor;
(window as any).showGlobe = showGlobe;
(window as any).hideGlobe = hideGlobe;
(window as any).toggleGlobeView = toggleGlobeView;
(window as any).setVoronoiMode = setVoronoiMode;
(window as any).isVoronoiModeActive = isVoronoiModeActive;

// Initialize when DOM is ready
wireEventListeners();
