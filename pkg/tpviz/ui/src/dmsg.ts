// DMSG: server overlay nodes, client edges, health checks

import * as S from './state';
import { countryToFlag, fetchWithTimeout, getVisorStatus } from './utils';
import { API_BASE } from './constants';

export function addDMSGGraphElements(): void {
    if (!S.dmsgData || !S.dmsgData.servers || !S.nodesDataset || !S.edgesDataset) return;

    // Suspend physics while adding potentially thousands of edges
    if (S.network) S.network.setOptions({ physics: { enabled: false } });

    const showDMSGServers = (document.getElementById('show-dmsg-servers') as HTMLInputElement)?.checked;
    const showFlags = (document.getElementById('show-flags') as HTMLInputElement)?.checked;

    // Find max client count for size scaling
    let maxClients = 1;
    S.dmsgData.servers.forEach(srv => {
        const count = srv.clients ? srv.clients.length : 0;
        if (count > maxClients) maxClients = count;
    });

    // Build fast lookup of all visor node IDs for edge target checks
    const visorNodeIds = new Set(S.nodesDataset.getIds());

    // Collect existing DMSG server node IDs and edge IDs for incremental update
    const existingDMSGServerIds = new Set<string>();
    S.nodesDataset.forEach((node: any) => {
        if (node.isDMSGServer) existingDMSGServerIds.add(node.id);
    });
    const existingDMSGEdgeIds = new Set<string>(
        (S.edgesDataset.getIds() as string[]).filter(id => {
            const e = S.edgesDataset!.get(id);
            return e && e.isDMSGConnection;
        })
    );

    const newDMSGServerIds = new Set<string>();
    const desiredEdgeIds = new Set<string>();
    const dmsgNodes: any[] = [];
    const dmsgEdges: any[] = [];

    S.dmsgData.servers.forEach(srv => {
        if (!srv.pk) return;
        const nodeId = 'dmsg-srv-' + srv.pk;
        newDMSGServerIds.add(nodeId);

        const clientCount = srv.clients ? srv.clients.length : 0;
        const sessions = srv.available_sessions || 0;
        const country = srv.country || '';
        const flag = country ? countryToFlag(country) : '';
        const shortPK = srv.pk.substring(0, 8);

        // Register in visorServices so country grouping picks it up
        S.visorServices[nodeId] = { services: ['dmsg-server'], country: country };

        const title = 'DMSG Server: ' + srv.pk +
            '\nAddress: ' + (srv.address || '') +
            '\nCountry: ' + (country || 'unknown') +
            '\nAvailable Sessions: ' + sessions +
            '\nConnected Clients: ' + clientCount;

        const size = 8 + (clientCount / maxClients) * 20;
        const useFlag = showFlags && !!flag;

        dmsgNodes.push({
            id: nodeId,
            label: showDMSGServers ? (useFlag ? flag : shortPK) : '',
            title: title,
            size: showDMSGServers ? size : 0.5,
            shape: (showDMSGServers && useFlag) ? 'text' : 'dot',
            font: showDMSGServers
                ? (useFlag ? { size: Math.max(16, size * 1.2), color: '#fff' } : { size: 10, color: '#c9a8ff' })
                : { size: 0, color: 'transparent' },
            color: showDMSGServers
                ? { background: '#9f6efc', border: '#7c3aed', highlight: { background: '#b388ff', border: '#9f6efc' } }
                : { background: 'transparent', border: 'transparent' },
            mass: 2,
            borderWidth: showDMSGServers ? 2 : 0,
            isDMSGServer: true,
            country: country,
            hidden: false,  // Never hidden — grouping needs to see this node
        });

        // Build edges to connected clients that exist in the graph
        if (srv.clients) {
            srv.clients.forEach((clientPK: string) => {
                if (visorNodeIds.has(clientPK)) {
                    const edgeId = 'dmsg-conn-' + srv.pk.substring(0, 8) + '-' + clientPK.substring(0, 8);
                    desiredEdgeIds.add(edgeId);
                    if (!existingDMSGEdgeIds.has(edgeId)) {
                        dmsgEdges.push({
                            id: edgeId,
                            from: nodeId,
                            to: clientPK,
                            color: { color: '#9f6efc', opacity: 0.3 },
                            width: 0.3,
                            smooth: false,
                            title: 'DMSG connection',
                            isDMSGConnection: true,
                            type: 'dmsg-connection',
                            hidden: !showDMSGServers
                        });
                    }
                }
            });
        }
    });

    // Remove stale DMSG server nodes
    const staleDMSGIds = [...existingDMSGServerIds].filter(id => !newDMSGServerIds.has(id));
    if (staleDMSGIds.length > 0) {
        S.nodesDataset.remove(staleDMSGIds);
    }

    // Remove only stale DMSG edges
    const staleEdgeIds = [...existingDMSGEdgeIds].filter(id => !desiredEdgeIds.has(id));
    if (staleEdgeIds.length > 0) {
        S.edgesDataset.remove(staleEdgeIds);
    }

    // Add/update DMSG server nodes and new edges
    if (dmsgNodes.length > 0) {
        S.nodesDataset.update(dmsgNodes);
    }
    if (dmsgEdges.length > 0) {
        S.edgesDataset.update(dmsgEdges);
    }

    console.log('DMSG graph:', dmsgNodes.length, 'server nodes,', dmsgEdges.length, 'new edges,', staleEdgeIds.length, 'removed edges');
}

export function updateNotInDMSGCount(): void {
    const notInDmsgEl = document.getElementById('count-not-in-dmsg');
    if (!notInDmsgEl) return;

    let notInDmsg = 0;
    S.onlineVisors.forEach(pk => {
        if (!S.dmsgEntries.has(pk)) {
            notInDmsg++;
        }
    });
    notInDmsgEl.textContent = String(notInDmsg);
}

export async function dmsgHealthCheck(): Promise<void> {
    const pkInput = document.getElementById('dmsg-health-pk') as HTMLInputElement;
    const resultEl = document.getElementById('dmsg-health-result') as HTMLElement;
    const pk = pkInput.value.trim();

    if (!pk || pk.length !== 66) {
        resultEl.style.display = 'block';
        resultEl.style.background = '#e94560';
        resultEl.style.color = '#fff';
        resultEl.textContent = 'Invalid PK (must be 66 chars)';
        return;
    }

    resultEl.style.display = 'block';
    resultEl.style.background = '#0f3460';
    resultEl.style.color = '#9f6efc';
    resultEl.textContent = 'Checking health via DMSG...';

    try {
        const resp = await fetchWithTimeout(API_BASE + '/api/dmsg/health?pk=' + encodeURIComponent(pk), 30000);
        const data = await resp.json();

        if (data.error) {
            resultEl.style.background = '#e94560';
            resultEl.style.color = '#fff';
            resultEl.textContent = 'Error: ' + data.error;
        } else if (data.status === 'healthy') {
            resultEl.style.background = '#00d9a5';
            resultEl.style.color = '#000';
            resultEl.textContent = 'Healthy: ' + (data.build_info || data.message || 'OK');
        } else {
            resultEl.style.background = '#ffd166';
            resultEl.style.color = '#000';
            resultEl.textContent = 'Response: ' + JSON.stringify(data);
        }
    } catch (err: any) {
        resultEl.style.background = '#e94560';
        resultEl.style.color = '#fff';
        resultEl.textContent = 'Failed: ' + err.message;
    }
}
