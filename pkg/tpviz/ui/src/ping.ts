// Ping: network ping functionality for route and DMSG connectivity testing

import * as S from './state';
import { fetchWithTimeout } from './utils';
import { API_BASE } from './constants';
import type { PingResponse } from './types';

/**
 * Performs a network ping to a remote visor.
 * Mode can be 'route' (via Skywire routes) or 'dmsg' (direct DMSG connection).
 * For route mode, localRoute option uses cached TPD data instead of route finder.
 */
export async function performPing(): Promise<void> {
    const pkInput = document.getElementById('ping-pk') as HTMLInputElement;
    const useDmsgCheckbox = document.getElementById('ping-use-dmsg') as HTMLInputElement;
    const triesInput = document.getElementById('ping-tries') as HTMLInputElement;
    const localRouteCheckbox = document.getElementById('ping-local-route') as HTMLInputElement;
    const resultEl = document.getElementById('ping-result') as HTMLElement;

    const pk = pkInput.value.trim();
    const useDmsg = useDmsgCheckbox?.checked ?? true;
    const tries = parseInt(triesInput.value, 10) || 3;
    const localRoute = localRouteCheckbox?.checked || false;

    const mode = useDmsg ? 'dmsg' : 'route';

    if (!pk || pk.length !== 66) {
        resultEl.style.display = 'block';
        resultEl.style.background = '#e94560';
        resultEl.style.color = '#fff';
        resultEl.textContent = 'Invalid PK (must be 66 chars)';
        return;
    }

    resultEl.style.display = 'block';
    resultEl.style.background = '#0f3460';
    resultEl.style.color = '#00d9a5';

    let modeText = mode.toUpperCase();
    if (mode === 'route' && localRoute) {
        modeText = 'ROUTE (local calc)';
    }
    resultEl.textContent = `Pinging via ${modeText}...`;

    try {
        let url = `${API_BASE}/api/ping?pk=${encodeURIComponent(pk)}&mode=${mode}&tries=${tries}`;
        if (mode === 'route' && localRoute) {
            url += '&local=true';
        }

        const resp = await fetchWithTimeout(url, 90000); // 90s timeout for multiple pings
        const data: PingResponse = await resp.json();

        if (data.error || data.status === 'error') {
            resultEl.style.background = '#e94560';
            resultEl.style.color = '#fff';
            resultEl.textContent = `Error: ${data.error || 'Unknown error'}`;
        } else if (data.status === 'timeout') {
            resultEl.style.background = '#ffd166';
            resultEl.style.color = '#000';
            resultEl.textContent = 'Timeout - no response received';
        } else if (data.status === 'success') {
            resultEl.style.background = '#00d9a5';
            resultEl.style.color = '#000';

            // Format latencies
            const latencyText = data.latencies && data.latencies.length > 0
                ? data.latencies.map(l => `${l}ms`).join(', ')
                : `${data.avg_ms?.toFixed(1)}ms`;

            let statsText = `${data.mode?.toUpperCase() || mode.toUpperCase()}: `;
            if (data.latencies && data.latencies.length > 1) {
                statsText += `min=${data.min_ms?.toFixed(1)}ms, avg=${data.avg_ms?.toFixed(1)}ms, max=${data.max_ms?.toFixed(1)}ms`;
                if (data.packet_loss && data.packet_loss > 0) {
                    statsText += `, loss=${data.packet_loss.toFixed(0)}%`;
                }
            } else {
                statsText += latencyText;
            }

            resultEl.textContent = statsText;
        } else {
            resultEl.style.background = '#ffd166';
            resultEl.style.color = '#000';
            resultEl.textContent = JSON.stringify(data);
        }
    } catch (err: any) {
        resultEl.style.background = '#e94560';
        resultEl.style.color = '#fff';
        resultEl.textContent = `Failed: ${err.message}`;
    }
}

/**
 * Sets the ping target PK from the graph selection.
 */
export function setPingPK(pk: string): void {
    const pkInput = document.getElementById('ping-pk') as HTMLInputElement;
    if (pkInput) {
        pkInput.value = pk;
    }
    S.setPingPickMode(false);
    document.body.style.cursor = '';
}

/**
 * Handles node click when in ping pick mode.
 */
export function handlePingNodeClick(nodeId: string): boolean {
    if (!S.pingPickMode) return false;

    // Strip dmsg-srv- prefix if clicking a DMSG server node
    const pk = nodeId.startsWith('dmsg-srv-') ? nodeId.substring(9) : nodeId;
    setPingPK(pk);
    return true;
}

/**
 * Updates the visibility of the local route checkbox based on DMSG checkbox state.
 */
export function updateLocalRouteVisibility(): void {
    const useDmsgCheckbox = document.getElementById('ping-use-dmsg') as HTMLInputElement;
    const localRouteRow = document.getElementById('ping-local-route-row') as HTMLElement;

    if (useDmsgCheckbox && localRouteRow) {
        // Show local route option only when NOT using DMSG (i.e., using routes)
        localRouteRow.style.display = useDmsgCheckbox.checked ? 'none' : 'block';
    }
}

/**
 * Shows or hides the ping section based on visor connection.
 */
export function showPingSection(show: boolean): void {
    const section = document.getElementById('section-ping');
    if (section) {
        section.style.display = show ? 'block' : 'none';
    }
}
