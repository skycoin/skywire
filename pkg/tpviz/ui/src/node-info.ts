// Node info panel: selection, edge highlighting, cluster boundaries

import * as S from './state';
import { getVisorStatus, isLocalVisor } from './utils';
import { LOCAL_EDGE_COLOR } from './constants';
import { applyFilters } from './filters';

export function showNodeInfo(nodeId: string): void {
    const info = document.getElementById('selected-info');
    const node = S.nodesDataset!.get(nodeId);

    // Handle DMSG server nodes specially
    if (node && node.isDMSGServer) {
        const serverPK = nodeId.replace('dmsg-srv-', '');
        const srv = S.dmsgData && S.dmsgData.servers ? S.dmsgData.servers.find(s => s.pk === serverPK) : null;

        document.getElementById('selected-pk')!.textContent = serverPK;
        document.getElementById('selected-conn-count')!.textContent = String(srv ? (srv.clients ? srv.clients.length : 0) : 0);

        const statusEl = document.getElementById('selected-status')!;
        statusEl.innerHTML = '<span class="status-dot" style="display:inline-block;vertical-align:middle;margin-right:5px;background:#9f6efc;border:2px solid #7c3aed;"></span>DMSG Server';

        const versionEl = document.getElementById('selected-version')!;
        if (srv) {
            versionEl.textContent = 'Sessions: ' + (srv.available_sessions || 0) + ' | Type: ' + ((srv as any).server_type || 'unknown');
            versionEl.style.display = 'block';
        } else {
            versionEl.style.display = 'none';
        }

        const countryEl = document.getElementById('selected-country')!;
        if (srv && srv.country) {
            countryEl.textContent = 'Country: ' + srv.country;
            countryEl.style.display = 'block';
        } else {
            countryEl.style.display = 'none';
        }

        document.getElementById('selected-services')!.style.display = 'none';

        const clients = srv && srv.clients ? srv.clients : [];
        document.getElementById('conn-list')!.innerHTML = clients.map((clientPK: string) => {
            const cStatus = getVisorStatus(clientPK);
            return '<div class="conn-item"><span class="conn-type" style="background:rgba(159,110,252,0.3);color:#9f6efc;">CLIENT</span><span class="status-dot status-' + cStatus + '" style="display:inline-block;width:8px;height:8px;margin-right:5px;"></span>' + clientPK.substring(0, 16) + '...</div>';
        }).join('');

        info!.classList.add('visible');
        S.setSelectedNodeId(nodeId);
        showNodeEdgesOnly(nodeId);
        return;
    }

    const connections = S.visorConnections[nodeId] || [];
    const status = getVisorStatus(nodeId);
    const svcInfo = S.visorServices[nodeId];
    const version = S.visorVersions[nodeId];

    document.getElementById('selected-pk')!.textContent = nodeId;
    document.getElementById('selected-conn-count')!.textContent = String(connections.length);

    const statusEl = document.getElementById('selected-status')!;
    statusEl.innerHTML = '<span class="status-dot status-' + status + '" style="display:inline-block;vertical-align:middle;margin-right:5px;"></span>' + status.charAt(0).toUpperCase() + status.slice(1);

    const versionEl = document.getElementById('selected-version')!;
    if (version) {
        versionEl.textContent = 'Version: ' + version;
        versionEl.style.display = 'block';
    } else {
        versionEl.style.display = 'none';
    }

    const countryEl = document.getElementById('selected-country')!;
    if (svcInfo && svcInfo.country) {
        countryEl.textContent = 'Country: ' + svcInfo.country;
        countryEl.style.display = 'block';
    } else {
        countryEl.style.display = 'none';
    }

    const servicesEl = document.getElementById('selected-services')!;
    if (svcInfo && svcInfo.services.length > 0) {
        servicesEl.innerHTML = svcInfo.services.map(s => '<span class="service-badge ' + s + '">' + s.toUpperCase() + '</span>').join('');
        servicesEl.style.display = 'block';
    } else {
        servicesEl.style.display = 'none';
    }

    document.getElementById('conn-list')!.innerHTML = connections.sort((a, b) => a.type.localeCompare(b.type))
        .map(c => {
            const cStatus = getVisorStatus(c.pk);
            const cVersion = S.visorVersions[c.pk];
            const versionTag = cVersion ? ' <span style="color:#0f9b8e;font-size:0.8em;">' + cVersion + '</span>' : '';
            return '<div class="conn-item"><span class="conn-type ' + c.type + '">' + c.type.toUpperCase() + '</span><span class="status-dot status-' + cStatus + '" style="display:inline-block;width:8px;height:8px;margin-right:5px;"></span>' + c.pk.substring(0, 16) + '...' + versionTag + '</div>';
        }).join('');

    info!.classList.add('visible');
    S.setSelectedNodeId(nodeId);
    showNodeEdgesOnly(nodeId);
}

export function hideNodeInfo(): void {
    document.getElementById('selected-info')!.classList.remove('visible');
    S.setSelectedNodeId(null);
    if (!S.selectedClusterId) showAllEdges();
}

export function focusNode(pk: string): void {
    if (!S.network || !S.nodesDataset!.get(pk)) return;
    S.network.selectNodes([pk]);
    S.network.focus(pk, {
        scale: 1.5,
        animation: { duration: 500, easingFunction: 'easeInOutQuad' }
    });
}

export function showNodeEdgesOnly(nodeId: string): void {
    const node = S.nodesDataset!.get(nodeId);
    const isDMSGServer = node && node.isDMSGServer;

    const isLocal = isLocalVisor(nodeId);
    const routeRelatedPKs = new Set<string>();
    if (isLocal && S.localVisorData && S.localVisorData.routes) {
        S.localVisorData.routes.forEach(route => {
            if (route.dst_pk) routeRelatedPKs.add(route.dst_pk);
            if (route.next_hop_pk) routeRelatedPKs.add(route.next_hop_pk);
        });
    }

    const edgeUpdates: any[] = [];
    S.edgesDataset!.forEach((edge: any) => {
        const isConnected = edge.from === nodeId || edge.to === nodeId;
        const isRouteEdge = isLocal && edge.isRoutePath &&
            (routeRelatedPKs.has(edge.from) || routeRelatedPKs.has(edge.to));
        const isDMSGConn = !isDMSGServer && edge.isDMSGConnection &&
            (edge.from === nodeId || edge.to === nodeId);
        edgeUpdates.push({ id: edge.id, hidden: !(isConnected || isRouteEdge || isDMSGConn) });
    });
    S.edgesDataset!.update(edgeUpdates);
}

export function showAllEdges(): void {
    applyFilters();
}

// hitTestClusterBoundary is in grouping.ts to avoid circular imports

export function showClusterEdgesOnly(groupKey: string): void {
    const nodeSet = S.groupNodeMap.get(groupKey);
    if (!nodeSet || !S.edgesDataset) return;

    const edgeUpdates: any[] = [];
    S.edgesDataset.forEach((edge: any) => {
        const connected = nodeSet.has(edge.from) || nodeSet.has(edge.to);
        edgeUpdates.push({ id: edge.id, hidden: !connected });
    });
    S.edgesDataset.update(edgeUpdates);
}

export function updateLocalTransportStyles(): void {
    if (!S.localVisorData || !S.localVisorData.connected || !S.edgesDataset) return;

    const localPK = S.localVisorData.pub_key;
    const localTransportRemotes = new Set<string>();

    if (S.localVisorData.transports) {
        S.localVisorData.transports.forEach(tp => {
            localTransportRemotes.add(tp.remote_pk);
        });
    }

    const edgeUpdates: any[] = [];
    S.edgesDataset.forEach((edge: any) => {
        const involvesLocal = edge.from === localPK || edge.to === localPK;
        const remotePK = edge.from === localPK ? edge.to : edge.from;
        const isLocalTp = involvesLocal && localTransportRemotes.has(remotePK);
        const isLocalEdge = isLocalTp || edge.isLocal || edge.isLocalOnly;

        if (isLocalEdge) {
            edgeUpdates.push({
                id: edge.id,
                color: { color: LOCAL_EDGE_COLOR, opacity: 1 },
                width: 4,
                shadow: { enabled: true, color: LOCAL_EDGE_COLOR, size: 10, x: 0, y: 0 }
            });
        }
    });

    if (edgeUpdates.length > 0) {
        S.edgesDataset.update(edgeUpdates);
        console.log('Updated', edgeUpdates.length, 'local transport edges to cyan');
    }
}

export function highlightRoute(route: any, autoZoom = false): void {
    if (!S.network || !S.localVisorData || !S.localVisorData.connected) return;

    const localPK = S.localVisorData.pub_key;
    const transportId = route.transport_id;
    const nextHopPK = route.next_hop_pk;
    const dstPK = route.dst_pk;

    if (S.highlightedRouteTimeout) {
        clearTimeout(S.highlightedRouteTimeout);
    }

    let firstHopPK = nextHopPK || dstPK;

    if (transportId && S.localVisorData.transports) {
        const tp = S.localVisorData.transports.find(t => t.id === transportId);
        if (tp) {
            firstHopPK = tp.remote_pk;
        }
    }

    const nodesToSelect = [localPK];
    const pathSegments: Array<{ from: string; to: string }> = [];

    if (firstHopPK) {
        nodesToSelect.push(firstHopPK);
        pathSegments.push({ from: localPK, to: firstHopPK });
    }

    if (dstPK && dstPK !== firstHopPK) {
        nodesToSelect.push(dstPK);
        pathSegments.push({ from: firstHopPK, to: dstPK });
    }

    const edgesToSelect: string[] = [];
    if (S.edgesDataset) {
        pathSegments.forEach(segment => {
            S.edgesDataset!.forEach((edge: any) => {
                if ((edge.from === segment.from && edge.to === segment.to) ||
                    (edge.from === segment.to && edge.to === segment.from)) {
                    if (!edgesToSelect.includes(edge.id)) {
                        edgesToSelect.push(edge.id);
                    }
                }
            });
        });
    }

    S.network.setSelection({
        nodes: nodesToSelect,
        edges: edgesToSelect
    }, { highlightEdges: true });

    console.log('Route highlight:', {
        local: localPK.substring(0, 12) + '...',
        firstHop: firstHopPK ? firstHopPK.substring(0, 12) + '...' : 'none',
        destination: dstPK ? dstPK.substring(0, 12) + '...' : 'none',
        segments: pathSegments.length,
        edgesFound: edgesToSelect.length
    });

    if (edgesToSelect.length > 0) {
        const originalColors: Record<string, any> = {};
        const originalWidths: Record<string, any> = {};
        const originalShadows: Record<string, any> = {};
        edgesToSelect.forEach(edgeId => {
            const edge = S.edgesDataset!.get(edgeId);
            if (edge) {
                originalColors[edgeId] = edge.color;
                originalWidths[edgeId] = edge.width;
                originalShadows[edgeId] = edge.shadow;
                S.edgesDataset!.update({
                    id: edgeId,
                    color: { color: '#ff00ff', highlight: '#ff66ff' },
                    width: 6,
                    shadow: { enabled: true, color: '#ff00ff', size: 15, x: 0, y: 0 }
                });
            }
        });

        S.setHighlightedRouteTimeout(setTimeout(() => {
            edgesToSelect.forEach(edgeId => {
                if (originalColors[edgeId] !== undefined) {
                    const edge = S.edgesDataset!.get(edgeId);
                    if (edge) {
                        S.edgesDataset!.update({
                            id: edgeId,
                            color: originalColors[edgeId],
                            width: originalWidths[edgeId] || (edge.isLocal || edge.isLocalOnly ? 4 : 1),
                            shadow: originalShadows[edgeId] || (edge.isLocal || edge.isLocalOnly ?
                                { enabled: true, color: '#00ffff', size: 10, x: 0, y: 0 } : false)
                        });
                    }
                }
            });
        }, 3000));
    } else if (nodesToSelect.length > 1) {
        console.log('Route path nodes selected but no matching edges in TPD data');
    }

    if (autoZoom && nodesToSelect.length > 1) {
        S.network.fit({
            nodes: nodesToSelect,
            animation: { duration: 500, easingFunction: 'easeInOutQuad' }
        });
    }
}

// Expose for inline onclick handlers
(window as any).focusNode = focusNode;
(window as any).highlightRouteByIndex = highlightRouteByIndex;

export function highlightRouteByIndex(idx: number): void {
    if (!S.localVisorData || !S.localVisorData.routes || !S.localVisorData.routes[idx]) return;
    highlightRoute(S.localVisorData.routes[idx], true);
}
