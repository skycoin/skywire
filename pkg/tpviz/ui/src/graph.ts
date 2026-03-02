// Graph: vis-network creation, processData, node/edge building

import { DataSet } from 'vis-data';
import { Network } from 'vis-network';
import * as S from './state';
import {
    setNetwork, setNodesDataset, setEdgesDataset,
    setVisorConnections, setVisorVersions, setVisorServices,
    setLastNewNodeIds, setSelectedClusterId, setSelectedNodeId,
} from './state';
import {
    getVisorStatus, countryToFlag, getVersionMass, isLocalVisor,
    isLocalTransport, getLocalTransportStats, formatBytes,
} from './utils';
import { colors, LOCAL_VISOR_COLOR, LOCAL_EDGE_COLOR, ROUTE_DEST_COLOR } from './constants';
import { getNodeColor, populateVersionFilter, applyFilters } from './filters';
import { showNodeInfo, hideNodeInfo, showNodeEdgesOnly, showAllEdges, showClusterEdgesOnly } from './node-info';
import { populateVisorList } from './sidebar';
import { setupGroupDrawing, hitTestClusterBoundary } from './grouping';
import { tpsUpdateInfo, tpsPopulateGroupSelect, mhAddHop } from './tps';

export function processUptimeData(): void {
    S.onlineVisors.clear();
    S.offlineVisors.clear();
    setVisorVersions({} as Record<string, string>);
    if (!S.uptimeData) return;

    S.uptimeData.forEach(entry => {
        if (entry.on) {
            S.onlineVisors.add(entry.pk);
        } else {
            S.offlineVisors.add(entry.pk);
        }
        if (entry.version) {
            S.visorVersions[entry.pk] = entry.version;
        }
    });
}

export function processServicesData(): void {
    setVisorServices({} as Record<string, any>);
    if (!S.servicesData) return;

    for (const [pk, info] of Object.entries(S.servicesData)) {
        S.visorServices[pk] = {
            services: info.services || [],
            country: info.country || ''
        };
    }
}

export function processDMSGData(): void {
    S.dmsgEntries.clear();
    if (!S.dmsgData) return;

    if (S.dmsgData.entries) {
        S.dmsgData.entries.forEach(pk => S.dmsgEntries.add(pk));
    }

    const serverCountEl = document.getElementById('dmsg-server-count');
    const totalSessionsEl = document.getElementById('dmsg-total-sessions');
    const entryCountEl = document.getElementById('dmsg-entry-count');

    if (serverCountEl) serverCountEl.textContent = String(S.dmsgData.servers?.length || 0);
    if (entryCountEl) entryCountEl.textContent = String(S.dmsgData.entries_count || 0);

    let totalSessions = 0;
    if (S.dmsgData.servers) {
        S.dmsgData.servers.forEach(srv => {
            totalSessions += srv.available_sessions || 0;
        });
    }
    if (totalSessionsEl) totalSessionsEl.textContent = String(totalSessions);

    const serverListEl = document.getElementById('dmsg-server-list');
    if (serverListEl && S.dmsgData.servers) {
        // Table header
        let html = '<table style="width:100%;border-collapse:collapse;">';
        html += '<tr style="color:#888;font-size:0.9em;border-bottom:1px solid #0f3460;">';
        html += '<td style="padding:2px 0;">Server</td>';
        html += '<td style="text-align:right;padding:2px 4px;">Sess</td>';
        html += '<td style="text-align:right;padding:2px 0;">Clients</td>';
        html += '</tr>';

        S.dmsgData.servers.forEach(srv => {
            const flag = srv.country ? countryToFlag(srv.country) : '';
            const shortPK = srv.pk ? srv.pk.substring(0, 8) + '...' : '';
            const sessions = srv.available_sessions || 0;
            const clients = srv.clients?.length || 0;
            const sessionColor = sessions > 50 ? '#00d9a5' : sessions > 10 ? '#ffd166' : '#e94560';
            const nodeId = 'dmsg-srv-' + srv.pk;

            html += '<tr style="border-bottom:1px solid #0f3460;">';
            html += '<td style="padding:2px 0;">';
            html += '<span title="' + srv.pk + '" style="cursor:pointer;color:#9f6efc;" onclick="focusDMSGServer(\'' + nodeId + '\')">' + flag + ' ' + shortPK + '</span>';
            html += '</td>';
            html += '<td style="text-align:right;padding:2px 4px;color:' + sessionColor + ';">' + sessions + '</td>';
            html += '<td style="text-align:right;padding:2px 0;color:#6ec8ff;">' + clients + '</td>';
            html += '</tr>';
        });
        html += '</table>';
        serverListEl.innerHTML = html || '<div style="color:#666;">No servers</div>';
    }
}

// Focus on a DMSG server in the graph
(window as any).focusDMSGServer = (nodeId: string) => {
    if (S.network && S.nodesDataset && S.nodesDataset.get(nodeId)) {
        S.network.focus(nodeId, { scale: 1.5, animation: true });
        S.network.selectNodes([nodeId]);
        showNodeInfo(nodeId);
    }
};

export function processData(): void {
    const visors = new Map<string, any>();
    setVisorConnections({} as Record<string, any[]>);
    let countStcpr = 0, countSudph = 0, countDmsg = 0, totalTransports = 0;

    const seenEdges = new Set<string>();
    const makeEdgeKey = (pk1: string, pk2: string, type: string) => {
        return pk1 < pk2 ? `${pk1}-${pk2}-${type}` : `${pk2}-${pk1}-${type}`;
    };

    S.allData!.forEach(transport => {
        const [pk1, pk2] = transport.edges;
        const type = transport.type;

        if (pk1 === pk2) return;

        seenEdges.add(makeEdgeKey(pk1, pk2, type));

        totalTransports++;
        if (type === 'stcpr') countStcpr++;
        else if (type === 'sudph') countSudph++;
        else if (type === 'dmsg') countDmsg++;

        [pk1, pk2].forEach(pk => {
            if (!visors.has(pk)) visors.set(pk, { id: pk, connections: 0, types: new Set() });
            visors.get(pk).connections++;
            visors.get(pk).types.add(type);
        });

        if (!S.visorConnections[pk1]) S.visorConnections[pk1] = [];
        if (!S.visorConnections[pk2]) S.visorConnections[pk2] = [];
        S.visorConnections[pk1].push({ pk: pk2, type });
        S.visorConnections[pk2].push({ pk: pk1, type });
    });

    // Always add local visor to the visualization
    if (S.localVisorData && S.localVisorData.connected && S.localVisorData.pub_key) {
        const localPK = S.localVisorData.pub_key;

        if (!visors.has(localPK)) {
            visors.set(localPK, { id: localPK, connections: 0, types: new Set(), isLocal: true });
        }
        visors.get(localPK).isLocal = true;

        if (S.localVisorData.country) {
            S.visorServices[localPK] = {
                services: S.visorServices[localPK]?.services || [],
                country: S.localVisorData.country
            };
        } else if (!S.visorServices[localPK]) {
            S.visorServices[localPK] = { services: [], country: '' };
        }
    }

    // Merge local visor transports that aren't in TPD yet
    if (S.localVisorData && S.localVisorData.connected && S.localVisorData.transports) {
        const localPK = S.localVisorData.pub_key;

        S.localVisorData.transports.forEach(tp => {
            const remotePK = tp.remote_pk;
            const type = tp.type;
            const edgeKey = makeEdgeKey(localPK, remotePK, type);

            if (seenEdges.has(edgeKey)) return;
            seenEdges.add(edgeKey);

            (S.allData as any[]).push({
                edges: [localPK, remotePK],
                type: type,
                isLocalOnly: true
            });

            totalTransports++;
            if (type === 'stcpr') countStcpr++;
            else if (type === 'sudph') countSudph++;
            else if (type === 'dmsg') countDmsg++;

            if (!visors.has(remotePK)) {
                visors.set(remotePK, { id: remotePK, connections: 0, types: new Set() });
            }

            visors.get(localPK).connections++;
            visors.get(localPK).types.add(type);
            visors.get(remotePK).connections++;
            visors.get(remotePK).types.add(type);

            if (!S.visorConnections[localPK]) S.visorConnections[localPK] = [];
            if (!S.visorConnections[remotePK]) S.visorConnections[remotePK] = [];
            S.visorConnections[localPK].push({ pk: remotePK, type });
            S.visorConnections[remotePK].push({ pk: localPK, type });
        });

        if (S.localVisorData.routes) {
            S.localVisorData.routes.forEach(route => {
                if (route.dst_pk && !visors.has(route.dst_pk)) {
                    visors.set(route.dst_pk, {
                        id: route.dst_pk,
                        connections: 0,
                        types: new Set(),
                        isRouteDestination: true
                    });
                }
                if (route.next_hop_pk && !visors.has(route.next_hop_pk)) {
                    visors.set(route.next_hop_pk, {
                        id: route.next_hop_pk,
                        connections: 0,
                        types: new Set(),
                        isRouteHop: true
                    });
                }
            });
        }
    }

    // Count graph visors not in uptime tracker
    let countUnknown = 0;
    visors.forEach(v => {
        const status = getVisorStatus(v.id);
        if (status === 'unknown') countUnknown++;
    });

    document.getElementById('total-transports')!.textContent = String(totalTransports);
    document.getElementById('total-visors')!.textContent = String(visors.size);
    // Show uptime tracker totals (not filtered to graph visors)
    document.getElementById('count-online')!.textContent = String(S.onlineVisors.size);
    document.getElementById('count-offline')!.textContent = String(S.offlineVisors.size);
    document.getElementById('count-unknown')!.textContent = String(countUnknown);
    document.getElementById('count-stcpr')!.textContent = String(countStcpr);
    document.getElementById('count-sudph')!.textContent = String(countSudph);
    document.getElementById('count-dmsg')!.textContent = String(countDmsg);

    populateVisorList(visors);

    const maxConn = Math.max(...Array.from(visors.values()).map((v: any) => v.connections));
    const showFlags = (document.getElementById('show-flags') as HTMLInputElement)?.checked;
    const nodes = Array.from(visors.values()).map((v: any) => {
        const color = getNodeColor(v);
        const status = getVisorStatus(v.id);
        const svcInfo = S.visorServices[v.id];
        const version = S.visorVersions[v.id] || '';
        const country = svcInfo ? svcInfo.country : '';
        const flag = countryToFlag(country);

        let title = v.id + '\nTransports: ' + v.connections + '\nStatus: ' + status;
        if (version) title += '\nVersion: ' + version;
        if (svcInfo && svcInfo.services.length > 0) title += '\nServices: ' + svcInfo.services.join(', ');
        if (country) title += '\nCountry: ' + country;

        const useFlag = showFlags && flag;
        const baseSize = v.isLocal ? Math.max(20, 5 + (v.connections / maxConn) * 25) : 5 + (v.connections / maxConn) * 25;

        return {
            id: v.id,
            label: v.isLocal ? '⬢ YOU' : (useFlag ? flag : v.id.substring(0, 8)),
            title: title,
            size: useFlag && !v.isLocal ? baseSize * 1.5 : baseSize,
            shape: (useFlag && !v.isLocal) ? 'text' : 'dot',
            font: v.isLocal ? { size: 14, color: '#00ffff', strokeWidth: 2, strokeColor: '#000' } : (useFlag ? { size: Math.max(16, baseSize * 1.2), color: '#fff' } : { size: 10, color: '#aaa' }),
            color: { background: color.background, border: color.border, highlight: v.isLocal ? { background: '#00ffff', border: '#ffffff' } : { background: '#e94560', border: '#ff6b6b' } },
            connections: v.connections,
            status: status,
            version: version,
            country: country,
            mass: v.isLocal ? 5 : getVersionMass(version),
            borderWidth: v.isLocal ? 4 : (status === 'offline' ? 3 : 2)
        };
    });

    const makeEdgeId = (t: any) => t.edges[0] < t.edges[1]
        ? t.edges[0] + '-' + t.edges[1] + '-' + t.type
        : t.edges[1] + '-' + t.edges[0] + '-' + t.type;

    const edges: any[] = S.allData!
        .filter(transport => transport.edges[0] !== transport.edges[1])
        .map(transport => {
            const isLocal = isLocalTransport(transport.edges[0], transport.edges[1]);
            const isLocalOnly = (transport as any).isLocalOnly;
            const stats = isLocal ? getLocalTransportStats(
                transport.edges[0] === S.localVisorData?.pub_key ? transport.edges[1] : transport.edges[0]
            ) : null;

            let title = transport.type.toUpperCase();
            if (isLocalOnly) title += ' (local only - not in TPD)';
            if (stats) {
                const sent = formatBytes(stats.sent_bytes);
                const recv = formatBytes(stats.recv_bytes);
                title += `\nSent: ${sent}\nRecv: ${recv}`;
            }

            const isLocalEdge = isLocal || isLocalOnly;

            return {
                id: makeEdgeId(transport),
                from: transport.edges[0],
                to: transport.edges[1],
                color: { color: isLocalEdge ? LOCAL_EDGE_COLOR : colors[transport.type], opacity: isLocalEdge ? 1 : 0.6 },
                type: transport.type,
                width: isLocalEdge ? 4 : 1,
                dashes: isLocalOnly ? [5, 5] : false,
                smooth: { type: 'continuous' },
                title: title,
                isLocal: isLocal,
                isLocalOnly: isLocalOnly,
                shadow: isLocalEdge ? { enabled: true, color: LOCAL_EDGE_COLOR, size: 10, x: 0, y: 0 } : false
            };
        });

    // Add route path edges
    if (S.localVisorData && S.localVisorData.connected && S.localVisorData.routes) {
        const localPK = S.localVisorData.pub_key;
        const existingEdgePairs = new Set(edges.map(e =>
            e.from < e.to ? `${e.from}-${e.to}` : `${e.to}-${e.from}`
        ));

        S.localVisorData.routes.forEach(route => {
            const nextHopPK = route.next_hop_pk;
            const dstPK = route.dst_pk;

            if (nextHopPK && dstPK && nextHopPK !== dstPK) {
                const edgePair = nextHopPK < dstPK ? `${nextHopPK}-${dstPK}` : `${dstPK}-${nextHopPK}`;
                if (!existingEdgePairs.has(edgePair)) {
                    existingEdgePairs.add(edgePair);
                    edges.push({
                        id: `route-${nextHopPK.substring(0,8)}-${dstPK.substring(0,8)}`,
                        from: nextHopPK,
                        to: dstPK,
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
    }

    if (S.network) {
        const positions = S.network.getPositions();
        S.network.setOptions({ physics: { enabled: false } });

        const existingNodeIds = new Set(S.nodesDataset!.getIds());
        const newNodeIds = new Set(nodes.map(n => n.id));

        const nodesToRemove = [...existingNodeIds].filter(id => {
            if (newNodeIds.has(id as string)) return false;
            const existingNode = S.nodesDataset!.get(id);
            if (existingNode && existingNode.isDMSGServer) return false;
            return true;
        });

        setLastNewNodeIds(new Set([...newNodeIds].filter(id => !existingNodeIds.has(id))));
        const newNodesCount = S.lastNewNodeIds.size;

        const nodesToUpdate = nodes.map(node => {
            if (positions[node.id]) {
                return { ...node, x: positions[node.id].x, y: positions[node.id].y };
            }
            return node;
        });

        if (nodesToRemove.length > 0) {
            S.nodesDataset!.remove(nodesToRemove as string[]);
        }
        S.nodesDataset!.update(nodesToUpdate);

        const existingEdgeIds = new Set(S.edgesDataset!.getIds());
        const newEdgeIds = new Set(edges.map(e => e.id));

        const edgesToRemove = [...existingEdgeIds].filter(id => {
            if (newEdgeIds.has(id as string)) return false;
            if (S.tpsOverlayEdges.has(id as string)) return false;
            const existingEdge = S.edgesDataset!.get(id);
            if (existingEdge && existingEdge.isDMSGConnection) return false;
            return true;
        });

        if (edgesToRemove.length > 0) {
            S.edgesDataset!.remove(edgesToRemove as string[]);
        }
        S.edgesDataset!.update(edges);

        populateVersionFilter();
        applyFilters();

        if (newNodesCount > 0 && !S.userPhysicsDisabled) {
            S.network.setOptions({
                physics: {
                    enabled: true,
                    barnesHut: { gravitationalConstant: -500, springConstant: 0.0001, springLength: 300 }
                }
            });
            S.network.stabilize(50);
            S.network.once('stabilizationIterationsDone', () => {
                S.network!.setOptions({ physics: { enabled: false } });
            });
        }

        console.log('Graph updated:', nodesToUpdate.length, 'nodes,', edges.length, 'edges,',
            newNodesCount, 'new nodes,', nodesToRemove.length, 'removed');
    } else {
        setNodesDataset(new DataSet(nodes));
        setEdgesDataset(new DataSet(edges));
        createNetwork();
        populateVersionFilter();
        applyFilters();
    }
    const loadingEl = document.getElementById('loading');
    if (loadingEl) loadingEl.style.display = 'none';
}

export function createNetwork(): void {
    const container = document.getElementById('network')!;
    const data = { nodes: S.nodesDataset!, edges: S.edgesDataset! };
    const options = {
        nodes: { shape: 'dot', font: { size: 10, color: '#aaa' }, borderWidth: 2 },
        edges: { smooth: { enabled: true, type: 'continuous', roundness: 0.5 } },
        layout: { improvedLayout: false },
        physics: { stabilization: { iterations: 100, fit: true }, barnesHut: { gravitationalConstant: -3000, springConstant: 0.001, springLength: 200 } },
        interaction: { hover: true, tooltipDelay: 999999999, selectConnectedEdges: false }
    };
    const net = new Network(container, data, options);
    setNetwork(net);

    net.on('click', params => {
        if (params.nodes.length > 0) {
            const nodeId = params.nodes[0];
            if (S.tpsPickMode && !net.isCluster(nodeId)) {
                if (S.tpsPickMode === 'target') {
                    (document.getElementById('tps-target-pk') as HTMLInputElement).value = nodeId;
                } else if (S.tpsPickMode === 'remote') {
                    (document.getElementById('tps-remote-pk') as HTMLInputElement).value = nodeId;
                }
                S.setTpsPickMode(null);
                document.body.style.cursor = 'default';
                tpsUpdateInfo();
                return;
            }
            if (S.tpsGroupPickMode && !net.isCluster(nodeId)) {
                const mode = S.tpsGroupPickMode === 'target'
                    ? (document.getElementById('tps-target-mode') as HTMLSelectElement).value
                    : (document.getElementById('tps-remote-mode') as HTMLSelectElement).value;
                let groupValue: any = null;
                if (mode === 'ip' && S.ipGroupsData && S.ipGroupsData.groups) {
                    groupValue = S.ipGroupsData.groups[nodeId];
                } else if (mode === 'country' && S.visorServices[nodeId]) {
                    groupValue = S.visorServices[nodeId].country;
                }
                if (groupValue !== null && groupValue !== undefined) {
                    const selectEl = document.getElementById(S.tpsGroupPickMode === 'target' ? 'tps-target-group' : 'tps-remote-group') as HTMLSelectElement;
                    tpsPopulateGroupSelect(selectEl, mode);
                    selectEl.value = String(groupValue);
                    tpsUpdateInfo();
                }
                S.setTpsGroupPickMode(null);
                document.body.style.cursor = 'default';
                return;
            }
            if (S.localTpPickMode && !net.isCluster(nodeId)) {
                const input = document.getElementById('local-tp-remote-pk') as HTMLInputElement;
                if (input) input.value = nodeId;
                S.setLocalTpPickMode(false);
                document.body.style.cursor = 'default';
                return;
            }
            if (S.mhPickMode && !net.isCluster(nodeId)) {
                mhAddHop(nodeId);
                S.setMhPickMode(false);
                document.body.style.cursor = 'default';
                return;
            }
            if (S.dmsgHealthPickMode && !net.isCluster(nodeId)) {
                (document.getElementById('dmsg-health-pk') as HTMLInputElement).value = nodeId;
                S.setDmsgHealthPickMode(false);
                document.body.style.cursor = 'default';
                return;
            }
            if (S.pingPickMode && !net.isCluster(nodeId)) {
                // Strip dmsg-srv- prefix if clicking a DMSG server node
                const pk = nodeId.startsWith('dmsg-srv-') ? nodeId.substring(9) : nodeId;
                (document.getElementById('ping-pk') as HTMLInputElement).value = pk;
                S.setPingPickMode(false);
                document.body.style.cursor = 'default';
                return;
            }
            if (!net.isCluster(nodeId)) {
                setSelectedClusterId(null);
                showNodeInfo(nodeId);
            }
        } else {
            const hitBoundary = hitTestClusterBoundary(params);
            if (hitBoundary) {
                setSelectedClusterId(hitBoundary.groupKey);
                setSelectedNodeId(null);
                hideNodeInfo();
                showClusterEdgesOnly(hitBoundary.groupKey);
                net.redraw();
            } else {
                setSelectedClusterId(null);
                hideNodeInfo();
                net.redraw();
            }
        }
    });
    net.on('hoverNode', params => {
        if (!S.selectedNodeId && !S.selectedClusterId) showNodeEdgesOnly(params.node);
    });
    net.on('blurNode', () => {
        if (!S.selectedNodeId && !S.selectedClusterId) showAllEdges();
        else if (S.selectedClusterId) showClusterEdgesOnly(S.selectedClusterId);
    });
    net.once('stabilizationIterationsDone', () => {
        net.fit();
        net.setOptions({ physics: { enabled: false } });
    });
    net.on('dragStart', params => {
        if (params.nodes.length === 1 && S.satelliteOrbits.has(params.nodes[0])) {
            const orbit = S.satelliteOrbits.get(params.nodes[0])!;
            orbit.isDragged = true;
        }
    });
    net.on('dragEnd', params => {
        if (params.nodes.length === 1 && S.satelliteOrbits.has(params.nodes[0])) {
            const nodeId = params.nodes[0];
            setTimeout(() => {
                const orbit = S.satelliteOrbits.get(nodeId);
                if (orbit) orbit.isDragged = false;
            }, 5000);
        }
    });
    setupGroupDrawing();
}

// Expose for inline onclick handlers
(window as any).copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text).catch(() => {
        // Fallback: create temp textarea
        const ta = document.createElement('textarea');
        ta.value = text;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
    });
};
