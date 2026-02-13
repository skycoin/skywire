// Entry point: initializes the application

import { wireEventListeners } from './events';
import { toggleSection, focusVisor } from './sidebar';

// Expose functions needed by inline onclick handlers in HTML
(window as any).toggleSection = toggleSection;
(window as any).focusVisor = focusVisor;

// Initialize when DOM is ready
wireEventListeners();
