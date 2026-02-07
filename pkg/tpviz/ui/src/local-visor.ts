// Local visor: WebSocket/HTTP polling, display, edge styling, route management

import * as S from './state';
import {
    setLocalVisorData, setLocalVisorWS, setWSReconnectAttempts,
    setLocalVisorRefreshInterval, setPreviousRouteIds,
} from './state';
import { formatBytes, fetchWithTimeout } from './utils';
import { colors, API_BASE, WS_MAX_RECONNECT_DELAY } from './constants';
import { highlightRoute } from './node-info';
import { localCreateTransport } from './tps';

export function focusLocalVisor(): void {
    if (!S.localVisorData || !S.localVisorData.connected || !S.network) return;
    const pk = S.localVisorData.pub_key;

    if (!S.nodesDataset!.get(pk)) {
        console.log('Local visor node not in network graph yet:', pk.substring(0, 16) + '...');
        return;
    }

    S.network.selectNodes([pk]);
    S.network.focus(pk, {
        scale: 1.5,
        animation: {
            duration: 500,
            easingFunction: 'easeInOutQuad'
        }
    });
}

export function updateLocalVisorDisplay(): void {
    const section = document.getElementById('local-visor-section');
    const content = document.getElementById('local-visor-content');
    if (!section || !content) return;

    if (!S.localVisorData || !S.localVisorData.connected) {
        section.style.display = 'none';
        return;
    }

    section.style.display = 'block';
    const pk = S.localVisorData.pub_key;
    const tps = S.localVisorData.transports || [];
    const routes = S.localVisorData.routes_count || 0;

    let totalSent = 0, totalRecv = 0;
    tps.forEach(t => {
        totalSent += t.sent_bytes || 0;
        totalRecv += t.recv_bytes || 0;
    });

    content.innerHTML = `
        <div style="font-size:0.85em;margin-bottom:8px;display:flex;justify-content:space-between;align-items:center;">
            <div>
                <strong style="color:#e94560;">Local Visor</strong><br>
                <span style="color:#888;font-size:0.8em;">${pk.substring(0, 16)}...</span>
            </div>
            <button onclick="focusLocalVisor()" style="background:#e94560;color:#fff;border:none;padding:4px 8px;border-radius:4px;cursor:pointer;font-size:0.75em;font-weight:bold;">
                FOCUS
            </button>
        </div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:4px;font-size:0.8em;">
            <div>Transports: <strong>${tps.length}</strong></div>
            <div>Routes: <strong>${routes}</strong></div>
            <div>Sent: <strong>${formatBytes(totalSent)}</strong></div>
            <div>Recv: <strong>${formatBytes(totalRecv)}</strong></div>
        </div>
        <div style="margin-top:8px;max-height:150px;overflow-y:auto;font-size:0.75em;">
            ${tps.map(t => `
                <div style="padding:3px 0;border-bottom:1px solid #0f3460;cursor:pointer;" onclick="focusNode('${t.remote_pk}')">
                    <span style="color:${colors[t.type] || '#888'}">${t.type}</span>
                    → ${t.remote_pk.substring(0, 8)}...
                    <span style="color:#666;float:right;">↑${formatBytes(t.sent_bytes)} ↓${formatBytes(t.recv_bytes)}</span>
                </div>
            `).join('')}
        </div>
    `;
}

export function updateLocalVisorDisplayEnhanced(): void {
    const section = document.getElementById('local-visor-section');
    const content = document.getElementById('local-visor-content');
    if (!section || !content) return;

    if (!S.localVisorData || !S.localVisorData.connected) {
        section.style.display = 'none';
        return;
    }

    // Preserve input field value before rebuilding UI
    const existingInput = document.getElementById('local-tp-remote-pk') as HTMLInputElement;
    const preservedPK = existingInput ? existingInput.value : '';
    const inputHadFocus = existingInput && document.activeElement === existingInput;

    section.style.display = 'block';
    const pk = S.localVisorData.pub_key;
    const tps = S.localVisorData.transports || [];
    const routes = S.localVisorData.routes || [];
    const routesCount = S.localVisorData.routes_count || routes.length;

    let totalSent = 0, totalRecv = 0;
    const totalSentDelta = S.localVisorData.total_sent_delta || 0;
    const totalRecvDelta = S.localVisorData.total_recv_delta || 0;
    tps.forEach(t => {
        totalSent += t.sent_bytes || 0;
        totalRecv += t.recv_bytes || 0;
    });

    const isActive = totalSentDelta > 0 || totalRecvDelta > 0;

    const tpByType: Record<string, number> = { stcpr: 0, sudph: 0, dmsg: 0 };
    tps.forEach(t => { if (tpByType[t.type] !== undefined) tpByType[t.type]++; });

    content.innerHTML = `
        <!-- Header -->
        <div class="local-visor-header">
            <span class="local-visor-icon">⬢</span>
            <div style="flex:1;">
                <div style="display:flex;align-items:center;gap:8px;">
                    <span class="local-visor-title">Local Visor</span>
                    ${isActive ? '<span class="live-indicator"><span class="live-dot"></span>LIVE</span>' : '<span style="color:#555;font-size:0.7em;">idle</span>'}
                </div>
                <div class="local-visor-pk">${pk.substring(0, 20)}...</div>
            </div>
            <button onclick="focusLocalVisor()" style="background:linear-gradient(135deg,#00ffff,#ff00ff);color:#000;border:none;padding:6px 12px;border-radius:6px;cursor:pointer;font-size:0.75em;font-weight:bold;box-shadow:0 2px 8px rgba(0,255,255,0.3);">
                ◎ FOCUS
            </button>
        </div>

        <!-- Stats Grid -->
        <div style="display:flex;gap:8px;margin:8px 0;">
            <div class="local-stat" style="flex:1;">
                <div class="local-stat-label">↑ Sent</div>
                <div class="local-stat-value">${formatBytes(totalSent)}${totalSentDelta > 0 ? `<span class="local-stat-delta">+${formatBytes(totalSentDelta)}/s</span>` : ''}</div>
            </div>
            <div class="local-stat" style="flex:1;">
                <div class="local-stat-label">↓ Received</div>
                <div class="local-stat-value">${formatBytes(totalRecv)}${totalRecvDelta > 0 ? `<span class="local-stat-delta">+${formatBytes(totalRecvDelta)}/s</span>` : ''}</div>
            </div>
        </div>

        <!-- Transports List -->
        <div class="section-label transports">
            <span>Transports</span>
            <span class="count">${tps.length}</span>
        </div>
        <div style="max-height:120px;overflow-y:auto;">
            ${tps.length === 0 ? '<div style="color:#555;font-size:0.8em;padding:8px;text-align:center;">No transports established</div>' : ''}
            ${tps.map(t => {
                const tpActive = (t.sent_delta || 0) > 0 || (t.recv_delta || 0) > 0;
                return `
                <div class="local-transport ${t.type} ${tpActive ? 'active' : ''}" onclick="focusNode('${t.remote_pk}')">
                    <span class="tp-type-badge ${t.type}">${t.type}</span>
                    <span class="tp-remote">${t.remote_pk.substring(0, 12)}...</span>
                    <div class="tp-traffic">
                        <span class="up">↑${formatBytes(t.sent_bytes)}</span>
                        <span class="down">↓${formatBytes(t.recv_bytes)}</span>
                    </div>
                </div>
            `;}).join('')}
        </div>

        <!-- Routes List -->
        ${routesCount > 0 ? `
        <div class="section-label routes">
            <span>Active Routes</span>
            <span class="count">${routesCount}</span>
        </div>
        <div style="max-height:120px;overflow-y:auto;">
            ${routes.slice(0, 20).map((r: any, idx: number) => `
                <div class="local-route clickable" onclick="highlightRouteByIndex(${idx})" title="Click to highlight route path">
                    <span class="route-type-badge ${r.type}">${r.type}</span>
                    <span class="route-id">#${r.route_id || '?'}</span>
                    ${r.dst_pk ? `<span class="route-dest">${r.dst_pk.substring(0,10)}...</span>` : '<span style="color:#555;">—</span>'}
                    ${r.transport_id ? `<span class="route-via">via tp:${r.transport_id.substring(0,6)}</span>` : ''}
                </div>
            `).join('')}
            ${routes.length > 20 ? `<div style="color:#666;padding:6px;text-align:center;font-size:0.75em;">...and ${routes.length - 20} more routes</div>` : ''}
        </div>
        ` : `
        <div class="section-label routes">
            <span>Routes</span>
            <span class="count">0</span>
        </div>
        <div style="color:#555;font-size:0.8em;padding:8px;text-align:center;">No active routes</div>
        `}

        <!-- Create Transport -->
        <div class="section-label" style="margin-top:6px;border-top:1px solid #0f3460;padding-top:4px;">
            <span style="color:#00ffff;">Create Transport</span>
        </div>
        <div style="display:flex;gap:2px;margin-top:3px;align-items:stretch;">
            <input type="text" id="local-tp-remote-pk" placeholder="Remote PK" style="flex:1;min-width:0;padding:6px 10px;background:#1a1a2e;border:1px solid #00ffff;color:#eee;border-radius:3px;font-size:13px;font-family:monospace;">
            <button id="local-tp-pick-btn" title="Pick from graph" style="padding:0 4px;background:#00ffff;color:#000;border:none;border-radius:2px;cursor:pointer;font-size:9px;">⊕</button>
        </div>
        <div style="display:flex;gap:3px;margin-top:3px;align-items:center;">
            <select id="local-tp-type" style="padding:3px 6px;background:#1a1a2e;border:1px solid #0f3460;color:#eee;border-radius:2px;font-size:11px;">
                <option value="sudph">SUDPH</option>
                <option value="stcpr">STCPR</option>
            </select>
            <button id="local-tp-create-btn" style="padding:3px 8px;background:linear-gradient(135deg,#00ffff,#00d9a5);color:#000;border:none;border-radius:2px;cursor:pointer;font-size:10px;font-weight:600;">Create</button>
        </div>
        <div id="local-tp-result" style="display:none;margin-top:3px;font-size:10px;padding:3px;border-radius:2px;"></div>
    `;

    // Wire up the local transport create button
    document.getElementById('local-tp-create-btn')!.addEventListener('click', localCreateTransport);
    document.getElementById('local-tp-pick-btn')!.addEventListener('click', () => {
        S.setLocalTpPickMode(true);
        document.body.style.cursor = 'crosshair';
    });

    // Restore preserved input value
    const newInput = document.getElementById('local-tp-remote-pk') as HTMLInputElement;
    if (newInput && preservedPK) {
        newInput.value = preservedPK;
        if (inputHadFocus) {
            newInput.focus();
        }
    }
}

export function detectAndHighlightNewRoutes(routes: any[]): void {
    if (!routes || routes.length === 0) return;

    const currentRouteIds = new Set(routes.map(r => r.route_id || r.dst_pk || JSON.stringify(r)));

    routes.forEach(route => {
        const routeKey = route.route_id || route.dst_pk || JSON.stringify(route);
        if (!S.previousRouteIds.has(routeKey)) {
            setTimeout(() => highlightRoute(route, false), 100);
        }
    });

    setPreviousRouteIds(currentRouteIds);
}

export function connectLocalVisorWS(): void {
    if (S.localVisorWS && (S.localVisorWS.readyState === WebSocket.OPEN || S.localVisorWS.readyState === WebSocket.CONNECTING)) {
        return;
    }

    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsHost = window.location.hostname || 'localhost';
    const wsPort = window.location.port || (window.location.protocol === 'https:' ? '443' : '80');
    const wsUrl = `${wsProtocol}//${wsHost}:${wsPort}/ws/local-visor`;

    console.log('Connecting to local visor WebSocket:', wsUrl);

    try {
        const ws = new WebSocket(wsUrl);
        setLocalVisorWS(ws);

        ws.onopen = function() {
            console.log('Local visor WebSocket connected');
            setWSReconnectAttempts(0);
        };

        ws.onmessage = function(event) {
            try {
                const newData = JSON.parse(event.data);
                if (newData && newData.connected) {
                    detectAndHighlightNewRoutes(newData.routes);
                    setLocalVisorData(newData);
                    if (newData.pub_key && newData.country) {
                        S.visorServices[newData.pub_key] = {
                            services: S.visorServices[newData.pub_key]?.services || [],
                            country: newData.country
                        };
                    }
                    updateLocalVisorDisplayEnhanced();
                    updateLocalEdgeStyling();
                    updateRoutePathEdges();
                }
            } catch (e) {
                console.log('Error parsing local visor WebSocket message:', e);
            }
        };

        ws.onclose = function(event) {
            console.log('Local visor WebSocket closed:', event.code, event.reason);
            setLocalVisorWS(null);
            scheduleWSReconnect();
        };

        ws.onerror = function(error) {
            console.log('Local visor WebSocket error:', error);
        };
    } catch (e) {
        console.log('Failed to create WebSocket:', e);
        startLocalVisorFastRefreshHTTP();
    }
}

export function scheduleWSReconnect(): void {
    setWSReconnectAttempts(S.wsReconnectAttempts + 1);
    const delay = Math.min(1000 * Math.pow(2, S.wsReconnectAttempts - 1), WS_MAX_RECONNECT_DELAY);
    console.log(`Scheduling WebSocket reconnect in ${delay}ms (attempt ${S.wsReconnectAttempts})`);
    setTimeout(() => {
        if (!S.localVisorWS || S.localVisorWS.readyState === WebSocket.CLOSED) {
            connectLocalVisorWS();
        }
    }, delay);
}

export function startLocalVisorFastRefresh(): void {
    if ('WebSocket' in window && API_BASE === '') {
        connectLocalVisorWS();
    } else {
        startLocalVisorFastRefreshHTTP();
    }
}

export function startLocalVisorFastRefreshHTTP(): void {
    if (S.localVisorRefreshInterval) return;
    console.log('Using HTTP polling for local visor data');
    setLocalVisorRefreshInterval(setInterval(async () => {
        try {
            const resp = await fetchWithTimeout(API_BASE + '/api/local-visor', 15000);
            if (resp && resp.ok) {
                const newData = await resp.json();
                if (newData && newData.connected) {
                    detectAndHighlightNewRoutes(newData.routes);
                    setLocalVisorData(newData);
                    if (newData.pub_key && newData.country) {
                        S.visorServices[newData.pub_key] = {
                            services: S.visorServices[newData.pub_key]?.services || [],
                            country: newData.country
                        };
                    }
                    updateLocalVisorDisplayEnhanced();
                    updateLocalEdgeStyling();
                    updateRoutePathEdges();
                }
            }
        } catch (e) {
            // Silently ignore refresh errors
        }
    }, 2000));
}

export function updateLocalEdgeStyling(): void {
    if (!S.network || !S.edgesDataset || !S.localVisorData || !S.localVisorData.connected) return;

    const localPK = S.localVisorData.pub_key;
    const localTransportRemotes = new Set(
        (S.localVisorData.transports || []).map(t => t.remote_pk)
    );

    const edgeUpdates: any[] = [];
    S.edgesDataset.forEach((edge: any) => {
        const involvesLocal = edge.from === localPK || edge.to === localPK;
        const remotePK = edge.from === localPK ? edge.to : edge.from;
        const isLocalTp = involvesLocal && localTransportRemotes.has(remotePK);

        if (isLocalTp && !edge.isLocal) {
            edgeUpdates.push({
                id: edge.id,
                isLocal: true,
                color: { color: '#00ffff', opacity: 1 },
                width: 4,
                shadow: { enabled: true, color: '#00ffff', size: 10, x: 0, y: 0 }
            });
        }
    });

    if (edgeUpdates.length > 0) {
        S.edgesDataset.update(edgeUpdates);
    }
}

export function updateRoutePathEdges(): void {
    if (!S.edgesDataset || !S.nodesDataset || !S.localVisorData) return;

    const routes = S.localVisorData.routes || [];

    // Update routeDestinations set for node highlighting
    S.routeDestinations.clear();
    routes.forEach(route => {
        if (route.dst_pk) S.routeDestinations.add(route.dst_pk);
        if (route.next_hop_pk) S.routeDestinations.add(route.next_hop_pk);
    });

    const validRouteEdgeIds = new Set<string>();
    const validRouteNodeIds = new Set<string>();
    const neededRouteEdges: Array<{ id: string; from: string; to: string }> = [];

    routes.forEach(route => {
        const nextHopPK = route.next_hop_pk;
        const dstPK = route.dst_pk;

        if (dstPK) validRouteNodeIds.add(dstPK);
        if (nextHopPK) validRouteNodeIds.add(nextHopPK);

        if (nextHopPK && dstPK && nextHopPK !== dstPK) {
            const edgeId = `route-${nextHopPK.substring(0,8)}-${dstPK.substring(0,8)}`;
            validRouteEdgeIds.add(edgeId);
            neededRouteEdges.push({ id: edgeId, from: nextHopPK, to: dstPK });
        }
    });

    // Remove stale route edges
    const edgesToRemove: string[] = [];
    S.edgesDataset.forEach((edge: any) => {
        if (edge.isRoutePath && !validRouteEdgeIds.has(edge.id)) {
            edgesToRemove.push(edge.id);
        }
    });

    if (edgesToRemove.length > 0) {
        console.log('Removing stale route edges:', edgesToRemove.length);
        S.edgesDataset.remove(edgesToRemove);
    }

    // Add new route edges
    const existingEdgeIds = new Set(S.edgesDataset.getIds() as string[]);
    const edgesToAdd: any[] = [];

    neededRouteEdges.forEach(({ id, from, to }) => {
        if (!existingEdgeIds.has(id)) {
            let hasTransportEdge = false;
            S.edgesDataset!.forEach((edge: any) => {
                if ((edge.from === from && edge.to === to) ||
                    (edge.from === to && edge.to === from)) {
                    if (!edge.isRoutePath) hasTransportEdge = true;
                }
            });

            if (!hasTransportEdge) {
                edgesToAdd.push({
                    id: id,
                    from: from,
                    to: to,
                    color: { color: '#ff00ff', opacity: 0.7 },
                    type: 'route',
                    width: 2,
                    dashes: [8, 4],
                    smooth: { type: 'continuous' },
                    title: 'Route path (next hop → destination)',
                    isRoutePath: true,
                    shadow: { enabled: true, color: '#ff00ff', size: 5, x: 0, y: 0 }
                });
            }
        }
    });

    if (edgesToAdd.length > 0) {
        console.log('Adding new route edges:', edgesToAdd.length);
        S.edgesDataset.add(edgesToAdd);
    }

    // Remove route-only nodes that are no longer referenced
    const nodesToRemove: string[] = [];
    S.nodesDataset.forEach((node: any) => {
        if ((node.isRouteDestination || node.isRouteHop) && !validRouteNodeIds.has(node.id)) {
            let hasEdges = false;
            S.edgesDataset!.forEach((edge: any) => {
                if (edge.from === node.id || edge.to === node.id) {
                    if (!edge.isRoutePath) hasEdges = true;
                }
            });

            if (!hasEdges) {
                nodesToRemove.push(node.id);
            }
        }
    });

    if (nodesToRemove.length > 0) {
        console.log('Removing stale route-only nodes:', nodesToRemove.length);
        S.nodesDataset.remove(nodesToRemove);
    }
}

// Expose for inline onclick handlers
(window as any).focusLocalVisor = focusLocalVisor;
