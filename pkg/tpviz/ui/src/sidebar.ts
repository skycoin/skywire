// Sidebar: visor list, version stats, legend, section toggling

import * as S from './state';
import { getVisorStatus, countryToFlag, getBaseVersion, compareVersions, formatBytes, getIPGroupColor } from './utils';
import { colors } from './constants';
import { showNodeInfo } from './node-info';

export function toggleSection(sectionId: string): void {
    const section = document.getElementById(sectionId);
    if (section) section.classList.toggle('collapsed');
}

export function populateVisorList(visors: Map<string, any>): void {
    const list = document.getElementById('visor-list');
    if (!list) return;
    const sorted = Array.from(visors.values()).sort((a: any, b: any) => b.connections - a.connections);
    list.innerHTML = sorted.map((v: any) => {
        const status = getVisorStatus(v.id);
        const svcInfo = S.visorServices[v.id];
        const country = svcInfo ? svcInfo.country : '';
        const flag = countryToFlag(country);
        const version = S.visorVersions[v.id] || '';
        const verShort = version ? getBaseVersion(version).replace('v', '') : '';
        const services = svcInfo ? svcInfo.services : [];

        const conns = S.visorConnections[v.id] || [];
        const stcprCount = conns.filter((c: any) => c.type === 'stcpr').length;
        const sudphCount = conns.filter((c: any) => c.type === 'sudph').length;

        const pkClass = status === 'offline' ? 'offline' : (status === 'unknown' ? 'unknown' : '');
        const pkStart = v.id.substring(0, 8);
        const pkMid = v.id.substring(8, v.id.length - 8);
        const pkEnd = v.id.substring(v.id.length - 8);

        let row1 = '<span class="v-flag">' + (flag || '●') + '</span>';
        row1 += '<span class="v-pk ' + pkClass + '" title="' + v.id + '">';
        row1 += '<span class="pk-start">' + pkStart + '</span>';
        row1 += '<span class="pk-mid">' + pkMid + '</span>';
        row1 += '<span class="pk-ellipsis">…</span>';
        row1 += '<span class="pk-end">' + pkEnd + '</span>';
        row1 += '</span>';

        const hasProxy = services.includes('proxy');
        const hasVpn = services.includes('vpn');
        const hasVisor = services.includes('visor');

        let row2 = '<div class="v-row2">';
        row2 += '<span class="v-col-ver">' + (verShort ? '<span class="v-ver">' + verShort + '</span>' : '') + '</span>';
        row2 += '<span class="v-col-svc">' + (hasProxy ? '<span class="v-svc proxy">P</span>' : '') + '</span>';
        row2 += '<span class="v-col-svc">' + (hasVpn ? '<span class="v-svc vpn">V</span>' : '') + '</span>';
        row2 += '<span class="v-col-svc">' + (hasVisor ? '<span class="v-svc visor">R</span>' : '') + '</span>';
        row2 += '<span class="v-col-tp">' + (stcprCount > 0 ? '<span class="v-tp stcpr">S' + stcprCount + '</span>' : '') + '</span>';
        row2 += '<span class="v-col-tp">' + (sudphCount > 0 ? '<span class="v-tp sudph">U' + sudphCount + '</span>' : '') + '</span>';
        row2 += '</div>';

        return '<div class="visor-item" onclick="focusVisor(\'' + v.id + '\')">' + row1 + row2 + '</div>';
    }).join('');

    requestAnimationFrame(updatePkTruncation);
}

export function updatePkTruncation(): void {
    document.querySelectorAll('.v-pk').forEach(pk => {
        const mid = pk.querySelector('.pk-mid') as HTMLElement | null;
        const ellipsis = pk.querySelector('.pk-ellipsis') as HTMLElement | null;
        if (!mid || !ellipsis) return;

        const isTruncated = mid.scrollWidth > mid.clientWidth + 1;
        ellipsis.style.display = isTruncated ? 'inline' : 'none';
        mid.style.display = isTruncated ? 'none' : 'inline';
    });
}

export function focusVisor(pk: string): void {
    if (S.network && S.nodesDataset && S.nodesDataset.get(pk)) {
        S.network.focus(pk, { scale: 1.5, animation: true });
        S.network.selectNodes([pk]);
        showNodeInfo(pk);
    }
}

export function updateLegend(): void {
    const showServices = (document.getElementById('show-services') as HTMLInputElement)?.checked;
    const showRoutes = (document.getElementById('show-routes') as HTMLInputElement)?.checked;
    const groupCountry = (document.getElementById('cluster-country') as HTMLInputElement)?.checked;
    const groupIP = (document.getElementById('cluster-ip') as HTMLInputElement)?.checked;
    const legendEl = document.getElementById('node-legend');
    if (!legendEl) return;

    let localLegend = '';
    if (S.localVisorData && S.localVisorData.connected) {
        localLegend = '<div class="status-item"><div class="status-dot" style="background:linear-gradient(135deg,#00ffff,#ff00ff);border:2px solid #fff;"></div><span style="color:#00ffff;">Local Visor</span></div>';
        if (showRoutes && S.routeDestinations.size > 0) {
            localLegend += '<div class="status-item"><div class="status-dot" style="background:#ff00ff;border:2px solid #fff;"></div><span style="color:#ff00ff;">Route Destination (' + S.routeDestinations.size + ')</span></div>';
        }
        localLegend += '<div style="font-size:0.7em;color:#00ffff;margin:4px 0;">━━ Local Transport</div>';
    }

    let dmsgServerLegend = '';
    if (S.dmsgData && S.dmsgData.servers && S.dmsgData.servers.length > 0) {
        dmsgServerLegend = '<div class="status-item"><div class="status-dot" style="background:#9f6efc;border:2px solid #7c3aed;"></div><span>DMSG Server</span></div>';
    }

    if (groupCountry && groupIP && S.ipGroupsEnabled && S.ipGroupsData) {
        const countries = new Set<string>();
        Object.values(S.visorServices).forEach(svc => {
            if (svc.country) countries.add(svc.country);
        });
        let html = localLegend;
        html += '<div style="font-size:0.75em;color:#888;margin-bottom:4px;">Country Groups (' + countries.size + ') + IP Groups (' + S.ipGroupsData.total_groups + ')</div>';
        html += '<div style="font-size:0.75em;color:#aaa;">Dashed outer: country &bull; Solid inner: IP group</div>';
        html += dmsgServerLegend;
        legendEl.innerHTML = html;
    } else if (groupCountry) {
        const countries = new Set<string>();
        Object.values(S.visorServices).forEach(svc => {
            if (svc.country) countries.add(svc.country);
        });
        let html = localLegend + '<div style="font-size:0.75em;color:#888;margin-bottom:4px;">Country Groups (' + countries.size + ')</div>';
        html += '<div style="font-size:0.75em;color:#aaa;">Colored boundaries show country groups</div>';
        html += dmsgServerLegend;
        legendEl.innerHTML = html;
    } else if (groupIP && S.ipGroupsEnabled && S.ipGroupsData) {
        let html = localLegend + '<div style="font-size:0.75em;color:#888;margin-bottom:4px;">IP Groups (' + S.ipGroupsData.total_groups + ')</div>';
        html += '<div style="font-size:0.75em;color:#aaa;">Colored boundaries show IP groups</div>';
        html += dmsgServerLegend;
        legendEl.innerHTML = html;
    } else if (showServices) {
        legendEl.innerHTML = localLegend +
            '<div class="status-item"><div class="status-dot status-vpn"></div><span>VPN</span></div>' +
            '<div class="status-item"><div class="status-dot status-proxy"></div><span>Proxy</span></div>' +
            '<div class="status-item"><div class="status-dot status-online"></div><span>Online (no service)</span></div>' +
            '<div class="status-item"><div class="status-dot status-offline"></div><span>Offline</span></div>' +
            '<div class="status-item"><div class="status-dot status-unknown"></div><span>Not in UT</span></div>' +
            dmsgServerLegend;
    } else {
        legendEl.innerHTML = localLegend +
            '<div class="status-item"><div class="status-dot status-online"></div><span>Online (in UT)</span></div>' +
            '<div class="status-item"><div class="status-dot status-offline"></div><span>Offline (in UT)</span></div>' +
            '<div class="status-item"><div class="status-dot status-unknown"></div><span>Not in UT</span></div>' +
            dmsgServerLegend;
    }
}

export function updateVersionStats(): void {
    const statsEl = document.getElementById('version-stats');
    if (!statsEl) return;
    if (!S.uptimeData || Object.keys(S.visorVersions).length === 0) {
        statsEl.innerHTML = '<div class="stat"><span class="stat-label">No data</span></div>';
        return;
    }

    const versionCounts: Record<string, number> = {};
    for (const [, version] of Object.entries(S.visorVersions)) {
        const base = getBaseVersion(version);
        if (base) {
            versionCounts[base] = (versionCounts[base] || 0) + 1;
        }
    }

    const sorted = Object.entries(versionCounts)
        .sort((a, b) => compareVersions(b[0], a[0]));

    const total = Object.values(versionCounts).reduce((a, b) => a + b, 0);
    let html = '<div style="max-height:200px;overflow-y:auto;">';
    html += '<table style="width:100%;font-size:0.85em;border-collapse:collapse;">';
    html += '<tr style="color:#888;font-size:0.9em;position:sticky;top:0;background:#1a1a2e;"><td>Version</td><td style="text-align:right;">Count</td><td style="text-align:right;">%</td></tr>';
    sorted.forEach(([ver, count]) => {
        const pct = ((count / total) * 100).toFixed(1);
        html += '<tr><td style="color:#0f9b8e;">' + ver + '</td><td style="text-align:right;">' + count + '</td><td style="text-align:right;color:#888;">' + pct + '%</td></tr>';
    });
    html += '</table></div>';
    html += '<div style="color:#888;font-size:0.75em;margin-top:4px;">' + sorted.length + ' versions total</div>';
    statsEl.innerHTML = html;
}
