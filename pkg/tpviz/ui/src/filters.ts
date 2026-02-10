// Filters: visibility filters, version filtering, search

import * as S from './state';
import { getVisorStatus, countryToFlag, getBaseVersion, getUniqueVersions, isLocalVisor } from './utils';
import { LOCAL_VISOR_COLOR, ROUTE_DEST_COLOR } from './constants';
import { showNodeInfo } from './node-info';

export function populateVersionFilter(): void {
    const select = document.getElementById('version-filter') as HTMLSelectElement | null;
    if (!select) return;
    const currentValue = select.value;
    select.innerHTML = '<option value="">All Versions</option>';
    const versions = getUniqueVersions();
    versions.forEach(v => {
        const opt = document.createElement('option');
        opt.value = v;
        opt.textContent = v;
        select.appendChild(opt);
    });
    select.value = currentValue;
}

export function getNodeColor(visor: { id: string }): { background: string; border: string; highlight?: { background: string; border: string } } {
    if (isLocalVisor(visor.id)) {
        return LOCAL_VISOR_COLOR;
    }

    const status = getVisorStatus(visor.id);
    const hasService = S.visorServices[visor.id] && S.visorServices[visor.id].services.length > 0;
    const showServices = (document.getElementById('show-services') as HTMLInputElement)?.checked;
    const showRoutes = (document.getElementById('show-routes') as HTMLInputElement)?.checked;

    if (showRoutes && S.routeDestinations.has(visor.id)) {
        return ROUTE_DEST_COLOR;
    }

    if (showServices && hasService) {
        const services = S.visorServices[visor.id].services;
        if (services.includes('vpn')) {
            return { background: '#9f6efc', border: '#7c3aed' };
        } else if (services.includes('proxy')) {
            return { background: '#ffa500', border: '#cc8400' };
        }
    }

    if (status === 'online') {
        return { background: '#00d9a5', border: '#00b386' };
    } else if (status === 'offline') {
        return { background: '#e94560', border: '#fff' };
    } else {
        return { background: '#ffd166', border: '#ccaa52' };
    }
}

export function applyFilters(): void {
    if (!S.nodesDataset || !S.edgesDataset || !S.network) return;

    const showStcpr = (document.getElementById('show-stcpr') as HTMLInputElement)?.checked;
    const showSudph = (document.getElementById('show-sudph') as HTMLInputElement)?.checked;
    const showDmsg = (document.getElementById('show-dmsg') as HTMLInputElement)?.checked;
    const showDMSGServers = (document.getElementById('show-dmsg-servers') as HTMLInputElement)?.checked;
    const showOnline = (document.getElementById('show-online') as HTMLInputElement)?.checked;
    const showOffline = (document.getElementById('show-offline') as HTMLInputElement)?.checked;
    const showUnknown = (document.getElementById('show-unknown') as HTMLInputElement)?.checked;
    const showFlags = (document.getElementById('show-flags') as HTMLInputElement)?.checked;
    const searchTerm = (document.getElementById('search') as HTMLInputElement)?.value.toLowerCase() || '';
    const versionFilter = (document.getElementById('version-filter') as HTMLSelectElement)?.value || '';

    const nodeUpdates: any[] = [];
    const hiddenNodes = new Set<string>();

    S.nodesDataset.forEach((node: any) => {
        if (node.isDMSGServer) {
            const srvInfo = S.visorServices[node.id];
            const srvCountry = srvInfo ? srvInfo.country : '';
            const srvFlag = countryToFlag(srvCountry);
            const useSrvFlag = showFlags && srvFlag;
            const shortPK = node.id.replace('dmsg-srv-', '').substring(0, 8);

            if (showDMSGServers) {
                nodeUpdates.push({
                    id: node.id,
                    hidden: false,
                    label: useSrvFlag ? srvFlag : shortPK,
                    shape: useSrvFlag ? 'text' : 'dot',
                    font: useSrvFlag ? { size: Math.max(16, node.size * 1.2), color: '#fff' } : { size: 10, color: '#c9a8ff' },
                    color: { background: '#9f6efc', border: '#7c3aed', highlight: { background: '#b388ff', border: '#9f6efc' } },
                    size: node.size || 8,
                    borderWidth: 2,
                });
            } else {
                // Invisible but not hidden — still participates in grouping layout
                nodeUpdates.push({
                    id: node.id,
                    hidden: false,
                    label: '',
                    shape: 'dot',
                    font: { size: 0, color: 'transparent' },
                    color: { background: 'transparent', border: 'transparent' },
                    size: 0.5,
                    borderWidth: 0,
                });
            }
            return;
        }

        const status = getVisorStatus(node.id);
        let visible = true;

        if (status === 'online' && !showOnline) visible = false;
        if (status === 'offline' && !showOffline) visible = false;
        if (status === 'unknown' && !showUnknown) visible = false;

        if (versionFilter) {
            const nodeVersion = S.visorVersions[node.id];
            const nodeBaseVersion = getBaseVersion(nodeVersion);
            if (nodeBaseVersion !== versionFilter) visible = false;
        }

        if (!visible) hiddenNodes.add(node.id);

        const color = getNodeColor({ id: node.id });
        const svcInfo = S.visorServices[node.id];
        const country = svcInfo ? svcInfo.country : '';
        const flag = countryToFlag(country);
        const useFlag = showFlags && flag;

        const isSatellite = S.satelliteOrbits.has(node.id);
        const orbit = isSatellite ? S.satelliteOrbits.get(node.id) : null;

        nodeUpdates.push({
            id: node.id,
            hidden: !visible,
            label: isSatellite ? orbit!.emoji : (useFlag ? flag : node.id.substring(0, 8)),
            shape: isSatellite ? 'text' : (useFlag ? 'text' : 'dot'),
            font: isSatellite ? { size: 20, color: '#ffffff' } : (useFlag ? { size: Math.max(16, node.size * 1.2), color: '#fff' } : { size: 10, color: '#aaa' }),
            color: { background: color.background, border: color.border, highlight: { background: '#e94560', border: '#ff6b6b' } },
        });
    });
    S.nodesDataset.update(nodeUpdates);

    const edgeUpdates: any[] = [];
    S.edgesDataset.forEach((edge: any) => {
        let visible = true;
        if (edge.type === 'stcpr' && !showStcpr) visible = false;
        if (edge.type === 'sudph' && !showSudph) visible = false;
        if (edge.type === 'dmsg' && !showDmsg) visible = false;
        if (edge.isDMSGConnection && !showDMSGServers) visible = false;
        if (hiddenNodes.has(edge.from) || hiddenNodes.has(edge.to)) {
            visible = false;
        }
        edgeUpdates.push({ id: edge.id, hidden: !visible });
    });
    S.edgesDataset.update(edgeUpdates);

    if (searchTerm.length >= 4) {
        let foundNode: string | null = null;
        S.nodesDataset.forEach((node: any) => {
            if (node.id.toLowerCase().startsWith(searchTerm)) {
                foundNode = node.id;
            }
        });
        if (foundNode) {
            S.nodesDataset.update({ id: foundNode, color: { background: '#e94560', border: '#ff6b6b' } });
            S.network.focus(foundNode, { scale: 1.5, animation: true });
            showNodeInfo(foundNode);
        }
    }
}
