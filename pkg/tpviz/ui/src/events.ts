// Events: all DOM event listener wiring

import * as S from './state';
import { applyFilters } from './filters';
import { updateLegend } from './sidebar';
import { updatePkTruncation } from './sidebar';
import { applyGrouping } from './grouping';
import { clearFlowAnimation } from './flow-animation';
import {
    checkTPSStatus, tpsAddTransport, tpsRefreshTransports,
    tpsUpdateTargetMode, tpsUpdateRemoteMode, tpsUpdateInfo,
    mhBuildRoute, mhClearHops, mhRenderHops,
} from './tps';
import { dmsgHealthCheck } from './dmsg';
import { performPing, updateLocalRouteVisibility } from './ping';
import { checkServer, fetchAllData } from './api';
import { startFlowAnimation } from './flow-animation';
import { showGlobe, hideGlobe, updateGlobeData, isGlobeViewActive } from './globe';

export function wireEventListeners(): void {
    // Filter listeners
    document.getElementById('show-stcpr')!.addEventListener('change', applyFilters);
    document.getElementById('show-sudph')!.addEventListener('change', applyFilters);
    document.getElementById('show-dmsg')!.addEventListener('change', applyFilters);
    document.getElementById('show-dmsg-servers')!.addEventListener('change', () => { applyFilters(); updateLegend(); });
    document.getElementById('show-online')!.addEventListener('change', applyFilters);
    document.getElementById('show-offline')!.addEventListener('change', applyFilters);
    document.getElementById('show-unknown')!.addEventListener('change', applyFilters);
    document.getElementById('show-services')!.addEventListener('change', () => { applyFilters(); updateLegend(); });
    document.getElementById('show-flags')!.addEventListener('change', applyFilters);
    document.getElementById('cluster-country')!.addEventListener('change', () => {
        applyGrouping();
        updateLegend();
    });
    document.getElementById('cluster-ip')!.addEventListener('change', () => {
        applyGrouping();
        updateLegend();
    });
    document.getElementById('version-filter')!.addEventListener('change', applyFilters);
    document.getElementById('search')!.addEventListener('input', applyFilters);

    // Zoom/fit listeners
    document.getElementById('btn-fit')!.addEventListener('click', () => S.network && S.network.fit());
    document.getElementById('zoom-in')!.addEventListener('click', () => {
        if (!S.network) return;
        const scale = S.network.getScale();
        S.network.moveTo({ scale: scale * 1.3 });
    });
    document.getElementById('zoom-out')!.addEventListener('click', () => {
        if (!S.network) return;
        const scale = S.network.getScale();
        S.network.moveTo({ scale: scale / 1.3 });
    });
    document.getElementById('zoom-fit')!.addEventListener('click', () => {
        if (S.network) S.network.fit();
    });

    // Resizable sidebar
    const resizeHandle = document.getElementById('resize-handle')!;
    const sidebar = document.getElementById('sidebar')!;
    let isResizing = false;
    let truncationTimeout: ReturnType<typeof setTimeout> | null = null;

    resizeHandle.addEventListener('mousedown', () => {
        isResizing = true;
        document.body.style.cursor = 'col-resize';
        document.body.style.userSelect = 'none';
    });

    document.addEventListener('mousemove', (e) => {
        if (!isResizing) return;
        const newWidth = e.clientX;
        if (newWidth >= 200 && newWidth <= 600) {
            sidebar.style.width = newWidth + 'px';
        }
    });

    document.addEventListener('mouseup', () => {
        if (isResizing) {
            isResizing = false;
            document.body.style.cursor = '';
            document.body.style.userSelect = '';
            if (truncationTimeout) clearTimeout(truncationTimeout);
            truncationTimeout = setTimeout(updatePkTruncation, 100);
        }
    });

    // TPS button event listeners
    document.getElementById('tps-pick-target-btn')!.addEventListener('click', () => {
        S.setTpsPickMode('target');
        document.body.style.cursor = 'crosshair';
    });
    document.getElementById('tps-pick-remote-btn')!.addEventListener('click', () => {
        S.setTpsPickMode('remote');
        document.body.style.cursor = 'crosshair';
    });
    document.getElementById('tps-pick-target-group-btn')!.addEventListener('click', () => {
        S.setTpsGroupPickMode('target');
        document.body.style.cursor = 'crosshair';
    });
    document.getElementById('tps-pick-remote-group-btn')!.addEventListener('click', () => {
        S.setTpsGroupPickMode('remote');
        document.body.style.cursor = 'crosshair';
    });
    document.getElementById('tps-add-btn')!.addEventListener('click', tpsAddTransport);
    document.getElementById('tps-refresh-btn')!.addEventListener('click', tpsRefreshTransports);

    // TPS mode selector listeners
    document.getElementById('tps-target-mode')!.addEventListener('change', tpsUpdateTargetMode);
    document.getElementById('tps-remote-mode')!.addEventListener('change', tpsUpdateRemoteMode);
    document.getElementById('tps-target-group')!.addEventListener('change', tpsUpdateInfo);
    document.getElementById('tps-remote-group')!.addEventListener('change', tpsUpdateInfo);
    document.getElementById('tps-target-pk')!.addEventListener('input', tpsUpdateInfo);
    document.getElementById('tps-remote-pk')!.addEventListener('input', tpsUpdateInfo);

    // Multi-hop route builder listeners
    document.getElementById('mh-add-hop-btn')!.addEventListener('click', () => {
        S.setMhPickMode(true);
        document.body.style.cursor = 'crosshair';
    });
    document.getElementById('mh-build-btn')!.addEventListener('click', mhBuildRoute);
    document.getElementById('mh-clear-btn')!.addEventListener('click', mhClearHops);
    mhRenderHops(); // Initial render

    // DMSG Health Check event handlers
    document.getElementById('dmsg-health-pick-btn')!.addEventListener('click', () => {
        S.setDmsgHealthPickMode(true);
        document.body.style.cursor = 'crosshair';
    });
    document.getElementById('dmsg-health-btn')!.addEventListener('click', dmsgHealthCheck);

    // Ping event handlers
    document.getElementById('ping-pick-btn')!.addEventListener('click', () => {
        S.setPingPickMode(true);
        document.body.style.cursor = 'crosshair';
    });
    document.getElementById('ping-btn')!.addEventListener('click', performPing);
    document.getElementById('ping-use-dmsg')!.addEventListener('change', updateLocalRouteVisibility);

    // Animation controls
    document.getElementById('show-dataflow')!.addEventListener('change', () => {
        if (!(document.getElementById('show-dataflow') as HTMLInputElement).checked) {
            clearFlowAnimation();
        }
    });
    document.getElementById('show-routes')!.addEventListener('change', () => { applyFilters(); updateLegend(); });

    // Tooltip toggle
    document.getElementById('toggle-tooltips')!.addEventListener('change', function(this: HTMLInputElement) {
        if (S.network) {
            S.network.setOptions({ interaction: { tooltipDelay: this.checked ? 100 : 999999999 } });
        }
    });

    // Physics toggle
    document.getElementById('toggle-physics')!.addEventListener('change', function(this: HTMLInputElement) {
        if (!S.network) return;
        if (this.checked) {
            S.setUserPhysicsDisabled(false);
            if (S.groupingMode) {
                S.network.setOptions({
                    physics: {
                        enabled: true,
                        barnesHut: {
                            gravitationalConstant: -500,
                            springConstant: 0.0001,
                            springLength: 50,
                            damping: 0.5
                        }
                    }
                });
            } else {
                S.network.setOptions({
                    physics: {
                        enabled: true,
                        barnesHut: {
                            gravitationalConstant: -3000,
                            springConstant: 0.001,
                            springLength: 200
                        }
                    }
                });
            }
        } else {
            S.setUserPhysicsDisabled(true);
            S.network.setOptions({ physics: { enabled: false } });
        }
    });

    // View toggle listeners (Globe vs Flat)
    const viewGlobeBtn = document.getElementById('view-globe');
    const viewFlatBtn = document.getElementById('view-flat');

    if (viewGlobeBtn && viewFlatBtn) {
        viewGlobeBtn.addEventListener('click', () => {
            viewGlobeBtn.classList.add('active');
            viewFlatBtn.classList.remove('active');
            S.setGlobeViewActive(true);
            showGlobe();
        });

        viewFlatBtn.addEventListener('click', () => {
            viewFlatBtn.classList.add('active');
            viewGlobeBtn.classList.remove('active');
            S.setGlobeViewActive(false);
            hideGlobe();
        });
    }

    // Data initialization
    checkServer();
    fetchAllData().then(() => {
        // Show globe view by default after data loads
        if (viewGlobeBtn && viewFlatBtn) {
            viewGlobeBtn.classList.add('active');
            viewFlatBtn.classList.remove('active');
            S.setGlobeViewActive(true);
            showGlobe();
        }
    });
    checkTPSStatus();

    // Start data flow animation after a short delay (for flat view)
    setTimeout(() => {
        startFlowAnimation();
    }, 1000);

    // Update globe when filters change
    const filterChangeHandler = () => {
        if (isGlobeViewActive()) {
            updateGlobeData();
        }
    };

    // Add globe update to filter changes
    document.getElementById('show-stcpr')!.addEventListener('change', filterChangeHandler);
    document.getElementById('show-sudph')!.addEventListener('change', filterChangeHandler);
    document.getElementById('show-dmsg')!.addEventListener('change', filterChangeHandler);
    document.getElementById('show-online')!.addEventListener('change', filterChangeHandler);
    document.getElementById('show-offline')!.addEventListener('change', filterChangeHandler);
    document.getElementById('show-unknown')!.addEventListener('change', filterChangeHandler);
}
