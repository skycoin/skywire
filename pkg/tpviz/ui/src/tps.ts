// Transport Setup (TPS) functionality — add/remove/refresh transports,
// multi-hop route builder, and TPS mode selector helpers.

import * as S from './state';
import {
    setTpsRunning, setTpsPK, setMhHops,
} from './state';
import { fetchWithTimeout } from './utils';
import { colors, API_BASE } from './constants';
import type { TPSTransportResponse } from './types';

// ── TPS (Transport Setup) Functions ──

export async function checkTPSStatus(): Promise<void> {
    try {
        const resp = await fetch('/api/tps/status');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const data = await resp.json();
        setTpsRunning(data.running);
        setTpsPK(data.tps_pk || '');
        const section = document.getElementById('tps-section');
        if (S.tpsRunning) {
            section!.style.display = '';
            document.getElementById('tps-pk-info')!.textContent = 'TPS PK: ' + S.tpsPK;
        } else {
            section!.style.display = 'none';
        }
    } catch (e) {
        // TPS not available (e.g. running in standalone mode)
        document.getElementById('tps-section')!.style.display = 'none';
    }
}

export async function tpsAddTransport(): Promise<void> {
    const targetMode = (document.getElementById('tps-target-mode') as HTMLSelectElement).value;
    const remoteMode = (document.getElementById('tps-remote-mode') as HTMLSelectElement).value;
    const tpType = (document.getElementById('tps-type') as HTMLSelectElement).value;
    const resultEl = document.getElementById('tps-result') as HTMLElement;
    const addBtn = document.getElementById('tps-add-btn') as HTMLButtonElement;

    // Get target visors
    let targetVisors: string[] = [];
    if (targetMode === 'pk') {
        const pk = (document.getElementById('tps-target-pk') as HTMLInputElement).value.trim();
        if (pk) targetVisors = [pk];
    } else {
        const group = (document.getElementById('tps-target-group') as HTMLSelectElement).value;
        if (group) targetVisors = tpsGetVisorsInGroup(group, targetMode);
    }

    // Get remote visors
    let remoteVisors: string[] = [];
    if (remoteMode === 'pk') {
        const pk = (document.getElementById('tps-remote-pk') as HTMLInputElement).value.trim();
        if (pk) remoteVisors = [pk];
    } else {
        const group = (document.getElementById('tps-remote-group') as HTMLSelectElement).value;
        if (group) remoteVisors = tpsGetVisorsInGroup(group, remoteMode);
    }

    if (targetVisors.length === 0 || remoteVisors.length === 0) {
        resultEl.style.display = 'block';
        resultEl.className = 'tps-error';
        resultEl.textContent = 'Both Target and Remote are required';
        return;
    }

    // Single PK to PK case
    if (targetVisors.length === 1 && remoteVisors.length === 1) {
        if (targetVisors[0] === remoteVisors[0]) {
            resultEl.style.display = 'block';
            resultEl.className = 'tps-error';
            resultEl.textContent = 'Target and Remote must be different';
            return;
        }

        addBtn.disabled = true;
        resultEl.style.display = 'block';
        resultEl.className = 'tps-loading';
        resultEl.textContent = 'Dialing via dmsg...';

        try {
            const resp = await fetch('/api/tps/add-transport', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ target_pk: targetVisors[0], remote_pk: remoteVisors[0], type: tpType })
            });
            const data = await resp.json();
            if (!resp.ok) throw new Error(data.error || 'Request failed');

            resultEl.className = 'tps-success';
            resultEl.innerHTML = 'Created ' + data.type.toUpperCase() + '<br>' +
                data.local_pk.substring(0, 12) + '... \u2194 ' + data.remote_pk.substring(0, 12) + '...';
            addTPSOverlayEdge(data);
        } catch (e: any) {
            resultEl.className = 'tps-error';
            resultEl.textContent = 'Error: ' + e.message;
        } finally {
            addBtn.disabled = false;
        }
        return;
    }

    // Group mode: connect each remote to some target, with retry logic
    // Goal: create one transport to each visor in the smaller group
    // If a target fails, try the next available target for that remote
    const shuffledTargets = [...targetVisors].sort(() => Math.random() - 0.5);
    const shuffledRemotes = [...remoteVisors].sort(() => Math.random() - 0.5);
    const goalCount = Math.min(shuffledTargets.length, shuffledRemotes.length);

    addBtn.disabled = true;
    resultEl.style.display = 'block';
    resultEl.className = 'tps-loading';
    resultEl.innerHTML = `<div>Connecting ${goalCount} transport(s)...</div><div id="tps-group-log" style="margin-top:4px;max-height:100px;overflow-y:auto;font-size:9px;"></div>`;

    const logEl = document.getElementById('tps-group-log') as HTMLElement;
    const log = (msg: string, color?: string): void => {
        logEl.innerHTML += `<div style="color:${color || '#aaa'};">${msg}</div>`;
        logEl.scrollTop = logEl.scrollHeight;
    };

    let successCount = 0;
    const errors: Array<{ target: string; remote: string; error: string }> = [];
    const usedTargets = new Set<string>();
    const connectedRemotes = new Set<string>();

    // For each remote, try to find a working target
    for (const remote of shuffledRemotes) {
        if (connectedRemotes.size >= goalCount) break;

        let connected = false;
        for (const target of shuffledTargets) {
            if (usedTargets.has(target)) continue;
            if (target === remote) continue;

            log(`${target.substring(0, 8)}\u2192${remote.substring(0, 8)}...`, '#ffd166');

            try {
                const resp = await fetch('/api/tps/add-transport', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ target_pk: target, remote_pk: remote, type: tpType })
                });
                const data = await resp.json();
                if (!resp.ok) throw new Error(data.error || 'Failed');

                addTPSOverlayEdge(data);
                usedTargets.add(target);
                connectedRemotes.add(remote);
                successCount++;
                connected = true;
                log(`  \u2713 ${data.type.toUpperCase()}`, '#00d9a5');
                break; // Move to next remote
            } catch (e: any) {
                log(`  \u2717 ${e.message}`, '#e94560');
                errors.push({ target: target.substring(0, 8), remote: remote.substring(0, 8), error: e.message });
                // Don't mark target as used - it might work for a different remote
                // Continue trying next target for this remote
            }
        }

        if (!connected) {
            log(`  No working target for ${remote.substring(0, 12)}`, '#e94560');
        }
    }

    resultEl.className = successCount === goalCount ? 'tps-success' : (successCount > 0 ? 'tps-warning' : 'tps-error');
    const summaryHtml = `<div style="margin-bottom:4px;font-weight:600;">${successCount}/${goalCount} transport(s) created</div>`;
    logEl.insertAdjacentHTML('afterbegin', summaryHtml);
    addBtn.disabled = false;
}

export async function tpsRefreshTransports(): Promise<void> {
    const targetPK = (document.getElementById('tps-target-pk') as HTMLInputElement).value.trim();
    const resultEl = document.getElementById('tps-result') as HTMLElement;
    const refreshBtn = document.getElementById('tps-refresh-btn') as HTMLButtonElement;

    if (!targetPK) {
        resultEl.style.display = 'block';
        resultEl.className = 'tps-error';
        resultEl.textContent = 'Target PK is required to refresh transports';
        return;
    }

    refreshBtn.disabled = true;
    resultEl.style.display = 'block';
    resultEl.className = 'tps-loading';
    resultEl.textContent = 'Querying remote visor via dmsg...';

    try {
        const resp = await fetch('/api/tps/refresh-transports?pk=' + encodeURIComponent(targetPK));
        const data = await resp.json();
        if (!resp.ok) {
            throw new Error(data.error || 'Request failed');
        }
        if (!Array.isArray(data) || data.length === 0) {
            resultEl.className = 'tps-success';
            resultEl.textContent = 'Remote visor reports 0 transports';
        } else {
            resultEl.className = 'tps-success';
            // Store targetPK for remove operations
            resultEl.dataset.targetPk = targetPK;
            resultEl.innerHTML = 'Remote visor reports ' + data.length + ' transport(s):<div style="margin-top:6px;">' +
                data.map((t: any) =>
                    '<div style="margin:4px 0;padding:5px;background:rgba(0,0,0,0.2);border-radius:3px;position:relative;" data-tp-id="' + t.id + '">' +
                    '<div style="display:flex;justify-content:space-between;align-items:center;">' +
                    '<span style="color:' + (colors[t.type] || '#aaa') + ';font-weight:600;font-size:0.8em;">' + t.type.toUpperCase() + '</span>' +
                    '<button onclick="tpsRemoveTransport(\'' + targetPK + '\', \'' + t.id + '\', \'' + t.local_pk + '\', \'' + t.remote_pk + '\', \'' + t.type + '\', this)" ' +
                    'style="padding:1px 5px;font-size:0.65em;background:#e94560;color:#fff;border:none;border-radius:2px;cursor:pointer;line-height:1;">\u2715</button>' +
                    '</div>' +
                    '<div style="font-size:0.7em;color:#aaa;margin-top:2px;word-break:break-all;">' +
                    t.remote_pk.substring(0, 20) + '...' +
                    '</div></div>'
                ).join('') + '</div>';

            // Overlay these edges on the graph
            data.forEach((t: any) => addTPSOverlayEdge(t));
        }
    } catch (e: any) {
        resultEl.className = 'tps-error';
        resultEl.textContent = 'Error: ' + e.message;
    } finally {
        refreshBtn.disabled = false;
    }
}

// Remove a transport from a remote visor via TPS
export async function tpsRemoveTransport(
    targetPK: string,
    tpID: string,
    localPK: string,
    remotePK: string,
    tpType: string,
    btnElement: HTMLButtonElement,
): Promise<void> {
    btnElement.disabled = true;
    btnElement.textContent = '...';

    try {
        const resp = await fetch('/api/tps/remove-transport', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ target_pk: targetPK, id: tpID })
        });
        const data = await resp.json();
        if (!resp.ok) {
            throw new Error(data.error || 'Request failed');
        }
        // Remove the row from UI
        const row = btnElement.closest('[data-tp-id]');
        if (row) row.remove();

        // Remove the edge from the graph
        // Edge ID format: pk1-pk2-type (sorted alphabetically)
        const edgeId = localPK < remotePK
            ? localPK + '-' + remotePK + '-' + tpType
            : remotePK + '-' + localPK + '-' + tpType;

        if (S.edgesDataset && S.edgesDataset.get(edgeId)) {
            S.edgesDataset.remove(edgeId);
        }
        S.tpsOverlayEdges.delete(edgeId);

        if (S.network) S.network.redraw();
    } catch (e: any) {
        alert('Failed to remove transport: ' + e.message);
        btnElement.disabled = false;
        btnElement.textContent = '\u2715';
    }
}

// Expose to window for inline onclick handlers
(window as any).tpsRemoveTransport = tpsRemoveTransport;

// Add an overlay edge from TPS response to the vis.js graph
export function addTPSOverlayEdge(tp: { local_pk: string; remote_pk: string; type?: string }): void {
    if (!S.edgesDataset || !S.nodesDataset) return;

    const pk1 = tp.local_pk;
    const pk2 = tp.remote_pk;
    const type = tp.type || 'stcpr';

    // Create deterministic edge ID
    const edgeId = pk1 < pk2
        ? pk1 + '-' + pk2 + '-' + type
        : pk2 + '-' + pk1 + '-' + type;

    // Check if this edge already exists in the graph
    const existing = S.edgesDataset.get(edgeId);
    if (existing && !existing.isTpsOverlay) {
        // Edge already exists from TPD data, no overlay needed
        return;
    }

    // Ensure both nodes exist in the graph
    [pk1, pk2].forEach(pk => {
        if (!S.nodesDataset!.get(pk)) {
            S.nodesDataset!.add({
                id: pk,
                label: pk.substring(0, 8),
                title: pk + '\n(added via TPS)',
                size: 8,
                shape: 'dot',
                color: { background: '#ff6b35', border: '#ffc800' },
                font: { size: 10, color: '#aaa' },
                borderWidth: 2
            });
        }
    });

    const overlayEdge: any = {
        id: edgeId,
        from: pk1,
        to: pk2,
        color: { color: '#ff6b35', opacity: 0.9 },
        type: type,
        width: 3,
        dashes: [6, 3],
        smooth: { type: 'continuous' },
        title: type.toUpperCase() + ' (via TPS \u2014 not yet in TPD)',
        isTpsOverlay: true,
        shadow: { enabled: true, color: '#ff6b35', size: 8, x: 0, y: 0 }
    };

    S.tpsOverlayEdges.set(edgeId, overlayEdge);

    if (existing) {
        S.edgesDataset.update(overlayEdge);
    } else {
        S.edgesDataset.add(overlayEdge);
    }

    if (S.network) S.network.redraw();
}

// Reconcile TPS overlay edges after a TPD cache refresh
// Edges that now exist in fresh TPD data are removed from overlay
export function reconcileTPSOverlays(): void {
    if (S.tpsOverlayEdges.size === 0) return;

    const toRemove: string[] = [];
    S.tpsOverlayEdges.forEach((edge: any, edgeId: string) => {
        const current = S.edgesDataset!.get(edgeId);
        // If the edge was replaced by real TPD data (no longer marked as overlay), remove from tracking
        if (current && !current.isTpsOverlay) {
            toRemove.push(edgeId);
        }
    });
    toRemove.forEach(id => S.tpsOverlayEdges.delete(id));
}

// ── Local Visor Transport Functions ──

// Helper to update result element (re-fetches from DOM to handle refreshes)
function updateLocalTPResult(style: 'error' | 'warning' | 'success', message: string): void {
    const el = document.getElementById('local-tp-result') as HTMLElement | null;
    if (!el) return;
    el.style.display = 'block';
    if (style === 'error') {
        el.style.background = 'rgba(233,69,96,0.2)';
        el.style.color = '#e94560';
    } else if (style === 'warning') {
        el.style.background = 'rgba(255,209,102,0.2)';
        el.style.color = '#ffd166';
    } else {
        el.style.background = 'rgba(0,217,165,0.2)';
        el.style.color = '#00d9a5';
    }
    el.textContent = message;
}

export async function localCreateTransport(): Promise<void> {
    const remoteInput = document.getElementById('local-tp-remote-pk') as HTMLInputElement | null;
    const typeSelect = document.getElementById('local-tp-type') as HTMLSelectElement | null;
    const createBtn = document.getElementById('local-tp-create-btn') as HTMLButtonElement | null;

    if (!remoteInput || !typeSelect || !createBtn) return;

    const remotePK = remoteInput.value.trim();
    const tpType = typeSelect.value;

    if (!remotePK) {
        updateLocalTPResult('error', 'Remote PK required');
        return;
    }

    createBtn.disabled = true;
    createBtn.textContent = '...';
    updateLocalTPResult('warning', 'Creating transport...');

    try {
        const resp = await fetch('/api/local/add-transport', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ remote_pk: remotePK, type: tpType })
        });
        const data = await resp.json();
        if (!resp.ok) {
            throw new Error(data.error || 'Failed');
        }
        updateLocalTPResult('success', 'Created: ' + data.type.toUpperCase() + ' → ' + data.remote_pk.substring(0, 16) + '...');

        // Clear input after success
        const input = document.getElementById('local-tp-remote-pk') as HTMLInputElement | null;
        if (input) input.value = '';

        // Add to graph as overlay
        addTPSOverlayEdge(data);
    } catch (e: any) {
        updateLocalTPResult('error', 'Error: ' + e.message);
    } finally {
        const btn = document.getElementById('local-tp-create-btn') as HTMLButtonElement | null;
        if (btn) {
            btn.disabled = false;
            btn.textContent = 'Create';
        }
    }
}

// ── Multi-Hop Route Builder Functions ──

export function mhAddHop(pk: string): void {
    if (!pk || S.mhHops.includes(pk)) return;
    S.mhHops.push(pk);
    mhRenderHops();
}

export function mhRemoveHop(index: number): void {
    S.mhHops.splice(index, 1);
    mhRenderHops();
}

export function mhClearHops(): void {
    setMhHops([]);
    mhRenderHops();
    document.getElementById('mh-progress')!.style.display = 'none';
}

export function mhRenderHops(): void {
    const wrapper = document.getElementById('mh-hops-container');
    const container = document.getElementById('mh-hops-list');
    const countEl = document.getElementById('mh-hop-count');
    if (!container || !wrapper) return;

    if (S.mhHops.length === 0) {
        wrapper.style.display = 'none';
        if (countEl) countEl.textContent = '';
        return;
    }

    wrapper.style.display = 'block';
    if (countEl) countEl.textContent = `${S.mhHops.length} hop${S.mhHops.length > 1 ? 's' : ''}`;

    container.innerHTML = S.mhHops.map((pk: string, i: number) => `
        <div style="display:flex;align-items:center;gap:4px;margin:2px 0;padding:3px 5px;background:rgba(0,0,0,0.2);border-radius:2px;border-left:2px solid ${i === 0 ? '#00ffff' : '#ffc800'};">
            <span style="color:#888;font-size:0.65em;min-width:14px;">${i + 1}.</span>
            <span style="flex:1;font-size:0.7em;font-family:monospace;color:#eee;">${pk.substring(0, 16)}...</span>
            <button onclick="mhRemoveHop(${i})" style="padding:0 4px;font-size:0.55em;background:#e94560;color:#fff;border:none;border-radius:2px;cursor:pointer;line-height:1.2;">\u2715</button>
        </div>
    `).join('');
}

// Expose for inline onclick
(window as any).mhRemoveHop = mhRemoveHop;

export async function mhBuildRoute(): Promise<void> {
    if (S.mhHops.length < 2) {
        alert('Need at least 2 hops to build a route');
        return;
    }

    const tpType = (document.getElementById('tps-type') as HTMLSelectElement).value;
    const progressEl = document.getElementById('mh-progress') as HTMLElement;
    const buildBtn = document.getElementById('mh-build-btn') as HTMLButtonElement;

    buildBtn.disabled = true;
    progressEl.style.display = 'block';
    progressEl.innerHTML = '<div style="color:#ffd166;">Building route...</div>';

    const results: Array<{ success: boolean; data?: any; error?: string }> = [];
    let failed = false;

    // Build transports sequentially: hop[0]->hop[1], hop[1]->hop[2], etc.
    for (let i = 0; i < S.mhHops.length - 1; i++) {
        const targetPK = S.mhHops[i];
        const remotePK = S.mhHops[i + 1];

        progressEl.innerHTML += `<div style="color:#aaa;font-size:0.9em;">Hop ${i + 1}: ${targetPK.substring(0, 8)}... \u2192 ${remotePK.substring(0, 8)}... <span id="mh-status-${i}" style="color:#ffd166;">\u23F3</span></div>`;

        try {
            const resp = await fetch('/api/tps/add-transport', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ target_pk: targetPK, remote_pk: remotePK, type: tpType })
            });
            const data = await resp.json();
            if (!resp.ok) throw new Error(data.error || 'Failed');

            document.getElementById(`mh-status-${i}`)!.innerHTML = '<span style="color:#00d9a5;">\u2713</span>';
            results.push({ success: true, data });
            addTPSOverlayEdge(data);
        } catch (e: any) {
            document.getElementById(`mh-status-${i}`)!.innerHTML = `<span style="color:#e94560;">\u2717 ${e.message}</span>`;
            results.push({ success: false, error: e.message });
            failed = true;
            // Continue trying remaining hops even if one fails
        }
    }

    const successCount = results.filter(r => r.success).length;
    progressEl.innerHTML += `<div style="margin-top:6px;padding:4px;background:${failed ? 'rgba(233,69,96,0.2)' : 'rgba(0,217,165,0.2)'};border-radius:3px;color:${failed ? '#e94560' : '#00d9a5'};">
        ${failed ? `Partial: ${successCount}/${S.mhHops.length - 1} transports created` : `Success: ${successCount} transport(s) created`}
    </div>`;

    buildBtn.disabled = false;
}

// ── TPS Mode Selector Functions ──

export function tpsUpdateTargetMode(): void {
    const mode = (document.getElementById('tps-target-mode') as HTMLSelectElement).value;
    const pkRow = document.getElementById('tps-target-pk-row') as HTMLElement;
    const groupRow = document.getElementById('tps-target-group-row') as HTMLElement;
    const groupSelect = document.getElementById('tps-target-group') as HTMLSelectElement;

    if (mode === 'pk') {
        pkRow.style.display = 'flex';
        groupRow.style.display = 'none';
    } else {
        pkRow.style.display = 'none';
        groupRow.style.display = 'flex';
        tpsPopulateGroupSelect(groupSelect, mode);
    }
    tpsUpdateInfo();
}

export function tpsUpdateRemoteMode(): void {
    const mode = (document.getElementById('tps-remote-mode') as HTMLSelectElement).value;
    const pkRow = document.getElementById('tps-remote-pk-row') as HTMLElement;
    const groupRow = document.getElementById('tps-remote-group-row') as HTMLElement;
    const groupSelect = document.getElementById('tps-remote-group') as HTMLSelectElement;

    if (mode === 'pk') {
        pkRow.style.display = 'flex';
        groupRow.style.display = 'none';
    } else {
        pkRow.style.display = 'none';
        groupRow.style.display = 'flex';
        tpsPopulateGroupSelect(groupSelect, mode);
    }
    tpsUpdateInfo();
}

export function tpsPopulateGroupSelect(selectEl: HTMLSelectElement, mode: string): void {
    selectEl.innerHTML = '<option value="">Select...</option>';
    let groups: any[] = [];

    if (mode === 'ip' && S.ipGroupsData && S.ipGroupsData.groups) {
        const ipGroups = new Set<number>();
        Object.values(S.ipGroupsData.groups).forEach((g: number) => ipGroups.add(g));
        groups = Array.from(ipGroups).sort((a: number, b: number) => a - b);
    } else if (mode === 'country') {
        const countries = new Set<string>();
        Object.values(S.visorServices).forEach((s: { services?: string[]; country?: string }) => {
            if (s.country) countries.add(s.country);
        });
        groups = Array.from(countries).sort();
    }

    groups.forEach((g: any) => {
        const count = tpsGetVisorsInGroup(g, mode).length;
        const label = mode === 'ip' ? `IP Group ${g}` : g;
        selectEl.innerHTML += `<option value="${g}">${label} (${count})</option>`;
    });
}

export function tpsGetVisorsInGroup(groupId: string | number, mode: string): string[] {
    const visors: string[] = [];
    if (mode === 'ip' && S.ipGroupsData && S.ipGroupsData.groups) {
        Object.entries(S.ipGroupsData.groups).forEach(([pk, g]) => {
            if (g == groupId) visors.push(pk);
        });
    } else if (mode === 'country') {
        Object.entries(S.visorServices).forEach(([pk, s]) => {
            if (s.country === groupId) visors.push(pk);
        });
    }
    return visors;
}

export function tpsUpdateInfo(): void {
    const infoEl = document.getElementById('tps-info') as HTMLElement;
    const targetMode = (document.getElementById('tps-target-mode') as HTMLSelectElement).value;
    const remoteMode = (document.getElementById('tps-remote-mode') as HTMLSelectElement).value;

    let targetCount = 0, remoteCount = 0;

    if (targetMode === 'pk') {
        const pk = (document.getElementById('tps-target-pk') as HTMLInputElement).value.trim();
        targetCount = pk ? 1 : 0;
    } else {
        const group = (document.getElementById('tps-target-group') as HTMLSelectElement).value;
        if (group) targetCount = tpsGetVisorsInGroup(group, targetMode).length;
    }

    if (remoteMode === 'pk') {
        const pk = (document.getElementById('tps-remote-pk') as HTMLInputElement).value.trim();
        remoteCount = pk ? 1 : 0;
    } else {
        const group = (document.getElementById('tps-remote-group') as HTMLSelectElement).value;
        if (group) remoteCount = tpsGetVisorsInGroup(group, remoteMode).length;
    }

    if (targetCount === 0 || remoteCount === 0) {
        infoEl.textContent = '';
        return;
    }

    const pairCount = Math.min(targetCount, remoteCount);
    if (targetCount === 1 && remoteCount === 1) {
        infoEl.textContent = '1 transport to create';
    } else {
        infoEl.innerHTML = `<span style="color:#ff6b35;">${targetCount}</span> \u2194 <span style="color:#ff6b35;">${remoteCount}</span> \u2192 <span style="color:#ffc800;">${pairCount}</span> transport(s)`;
    }
}
