// API: data fetching, auto-refresh, server sync

import * as S from './state';
import {
    setAllData, setUptimeData, setServicesData,
    setIPGroupsData, setIPGroupsEnabled, setLocalVisorData,
    setDmsgData, setNextRefreshTime, setCountdownInterval,
    setServerCacheAge, setIsRefreshing, setVisorConnected,
} from './state';
import { fetchWithTimeout, formatCountdown } from './utils';
import { API_BASE } from './constants';
import { processData, processUptimeData, processServicesData, processDMSGData } from './graph';
import { updateVersionStats, updateLegend } from './sidebar';
import { addDMSGGraphElements, updateNotInDMSGCount } from './dmsg';
import { updateLocalTransportStyles } from './node-info';
import { updateLocalVisorDisplayEnhanced, startLocalVisorFastRefresh } from './local-visor';
import { reconcileTPSOverlays } from './tps';
import { applyGrouping } from './grouping';

export async function fetchAllData(): Promise<boolean> {
    const isRefresh = S.network !== null;
    const loadingEl = document.getElementById('loading');
    const errorEl = document.getElementById('error');

    // Save current view state to restore after refresh
    let savedViewState: { position: { x: number; y: number }; scale: number } | null = null;
    if (isRefresh && S.network) {
        savedViewState = {
            position: S.network.getViewPosition(),
            scale: S.network.getScale()
        };
    }

    if (isRefresh) {
        console.log('Starting auto-refresh...');
    } else if (loadingEl) {
        loadingEl.style.display = 'block';
    }
    if (errorEl) errorEl.style.display = 'none';

    try {
        let tpResp;
        try {
            tpResp = await fetchWithTimeout(API_BASE + '/api/transports');
        } catch (e: any) {
            console.log('Primary fetch failed, trying fallback:', e.message);
            tpResp = await fetchWithTimeout('https://tpd.skywire.skycoin.com/all-transports');
        }

        if (!tpResp.ok) throw new Error('HTTP ' + tpResp.status);
        setAllData(await tpResp.json());
        console.log('Transport data loaded:', S.allData!.length, 'transports');

        const [utResp, sdResp, ipResp, lvResp, dmsgResp] = await Promise.all([
            fetchWithTimeout(API_BASE + '/api/uptimes').catch(e => { console.log('UT fetch error:', e.message); return null; }),
            fetchWithTimeout(API_BASE + '/api/services').catch(e => { console.log('SD fetch error:', e.message); return null; }),
            fetchWithTimeout(API_BASE + '/api/ip-groups').catch(e => { console.log('IP groups fetch error:', e.message); return null; }),
            fetchWithTimeout(API_BASE + '/api/local-visor').catch(e => { console.log('Local visor fetch error:', e.message); return null; }),
            fetchWithTimeout(API_BASE + '/api/dmsg/servers').catch(e => { console.log('DMSG fetch error:', e.message); return null; })
        ]);

        if (utResp && utResp.ok) {
            setUptimeData(await utResp.json());
            processUptimeData();
            updateVersionStats();
            console.log('Uptime data loaded:', S.uptimeData!.length, 'entries');
        }

        if (sdResp && sdResp.ok) {
            setServicesData(await sdResp.json());
            processServicesData();
            console.log('Services data loaded:', Object.keys(S.servicesData!).length, 'services');
        }

        if (ipResp && ipResp.ok) {
            setIPGroupsData(await ipResp.json());
            setIPGroupsEnabled(!!(S.ipGroupsData && S.ipGroupsData.enabled));
            const ipGroupsLabel = document.getElementById('ip-groups-label');
            const uniqueIpsStat = document.getElementById('unique-ips-stat');
            // These IP-group controls live in the standalone tp-viz index.html but
            // are absent when the bundle is embedded elsewhere (e.g. the HV visualizer
            // tab) — and the auto-refresh timer can also fire fetchAllData after the
            // host DOM has been torn down. Guard every element (as uniqueIpsStat
            // already is) so a null never throws and kills the whole data load.
            const clusterIp = document.getElementById('cluster-ip') as HTMLInputElement | null;
            const totalUniqueIps = document.getElementById('total-unique-ips');
            if (S.ipGroupsEnabled && S.ipGroupsData!.total_groups > 1) {
                if (ipGroupsLabel) ipGroupsLabel.style.display = 'block';
                if (clusterIp) clusterIp.checked = true;
                if (uniqueIpsStat) {
                    uniqueIpsStat.style.display = '';
                    if (totalUniqueIps) totalUniqueIps.textContent = String(S.ipGroupsData!.total_groups);
                }
                console.log('IP groups data loaded:', S.ipGroupsData!.total_groups, 'groups,', Object.keys(S.ipGroupsData!.groups).length, 'visors');
            } else {
                if (ipGroupsLabel) ipGroupsLabel.style.display = 'none';
                if (uniqueIpsStat) uniqueIpsStat.style.display = 'none';
                console.log('IP groups not available or only 1 group');
            }
        }

        if (lvResp && lvResp.ok) {
            setLocalVisorData(await lvResp.json());
            updateLocalVisorDisplayEnhanced();

            S.routeDestinations.clear();
            if (S.localVisorData!.connected && S.localVisorData!.routes) {
                S.localVisorData!.routes.forEach(route => {
                    if (route.dst_pk) S.routeDestinations.add(route.dst_pk);
                    if (route.next_hop_pk) S.routeDestinations.add(route.next_hop_pk);
                });
            }

            if (S.localVisorData!.connected) {
                setVisorConnected(true);
                console.log('Local visor connected:', S.localVisorData!.pub_key.substring(0, 16) + '...',
                    'Transports:', S.localVisorData!.transports?.length || 0,
                    'Routes:', S.localVisorData!.routes_count,
                    'Country:', S.localVisorData!.country || 'unknown',
                    'Route destinations:', S.routeDestinations.size);
                startLocalVisorFastRefresh();

                if (S.localVisorData!.pub_key) {
                    S.visorServices[S.localVisorData!.pub_key] = {
                        services: S.visorServices[S.localVisorData!.pub_key]?.services || [],
                        country: S.localVisorData!.country || S.visorServices[S.localVisorData!.pub_key]?.country || ''
                    };
                }
            } else {
                setVisorConnected(false);
            }
        } else {
            // Visor API not available (standalone mode)
            setVisorConnected(false);
        }

        // Hide visor-specific UI elements when visor is not connected
        const dmsgHealthRow = document.getElementById('dmsg-health-row');
        if (dmsgHealthRow) {
            dmsgHealthRow.style.display = S.visorConnected ? '' : 'none';
        }

        if (dmsgResp && dmsgResp.ok) {
            setDmsgData(await dmsgResp.json());
            processDMSGData();
            console.log('DMSG data loaded:', S.dmsgData!.servers?.length || 0, 'servers,', S.dmsgData!.entries_count || 0, 'entries');
        }

        processData();
        console.log('Data processing complete');

        addDMSGGraphElements();
        reconcileTPSOverlays();

        // Re-add any remaining TPS overlay edges
        S.tpsOverlayEdges.forEach((edge: any, edgeId: string) => {
            const current = S.edgesDataset!.get(edgeId);
            if (!current) {
                S.edgesDataset!.add(edge);
            } else if (!current.isTpsOverlay) {
                S.tpsOverlayEdges.delete(edgeId);
            }
        });

        updateLocalTransportStyles();
        applyGrouping(isRefresh);

        // Restore view state after refresh (delayed to allow grouping to settle)
        if (savedViewState && S.network) {
            setTimeout(() => {
                S.network!.moveTo({
                    position: savedViewState!.position,
                    scale: savedViewState!.scale,
                    animation: false
                });
            }, 150);
        }

        return true;
    } catch (err: any) {
        console.error('fetchAllData error:', err);
        if (loadingEl) loadingEl.style.display = 'none';
        if (errorEl) {
            errorEl.style.display = 'block';
            errorEl.textContent = 'Failed to load data: ' + err.message;
        }
        throw err;
    }
}

export function updateCountdown(): void {
    if (!S.nextRefreshTime) return;
    const remaining = Math.max(0, Math.ceil((S.nextRefreshTime - Date.now()) / 1000));

    // cache-info is a standalone-tp-viz element; null when embedded (HV visualizer)
    // or after the host DOM is torn down while this timer keeps firing. Guard it.
    const cacheInfo = document.getElementById('cache-info');
    if (remaining === 0 && !S.isRefreshing) {
        setIsRefreshing(true);
        if (cacheInfo) cacheInfo.innerHTML = 'Refreshing...';
        console.log('Auto-refresh triggered at', new Date().toLocaleTimeString());

        fetchAllData()
            .then(() => {
                console.log('Auto-refresh succeeded at', new Date().toLocaleTimeString());
                const el = document.getElementById('cache-info');
                if (el) el.innerHTML = 'Refresh complete';
            })
            .catch(err => {
                console.error('Auto-refresh failed:', err);
                const el = document.getElementById('cache-info');
                if (el) el.innerHTML = 'Refresh failed: ' + err.message;
            })
            .finally(() => {
                console.log('Auto-refresh finished, syncing with server');
                setIsRefreshing(false);
                setTimeout(() => {
                    syncWithServer();
                }, 500);
            });
    } else if (!S.isRefreshing) {
        if (cacheInfo) cacheInfo.innerHTML = 'Auto-refresh in: ' + formatCountdown(remaining);
    }
}

export async function syncWithServer(): Promise<void> {
    try {
        const resp = await fetchWithTimeout('/api/health', 5000);
        if (resp.ok) {
            const info = await resp.json();
            console.log('Server health:', info);
            if (info.auto_refresh && typeof info.next_refresh_in === 'number') {
                const seconds = Math.max(5, info.next_refresh_in);
                setNextRefreshTime(Date.now() + (seconds * 1000));
                console.log('Next refresh in', seconds, 'seconds');
            } else {
                setNextRefreshTime(Date.now() + (S.serverCacheAge || 5) * 60 * 1000);
            }
        }
    } catch (e: any) {
        console.log('Sync with server failed:', e.message);
        setNextRefreshTime(Date.now() + (S.serverCacheAge || 5) * 60 * 1000);
    }
}

function startRefreshTimer(secondsUntilRefresh: number): void {
    setNextRefreshTime(Date.now() + (secondsUntilRefresh * 1000));
    if (!S.countdownInterval) {
        setCountdownInterval(setInterval(updateCountdown, 1000));
    }
    updateCountdown();
}

export async function checkServer(): Promise<void> {
    try {
        const resp = await fetch('/api/health');
        if (resp.ok) {
            const info = await resp.json();
            setServerCacheAge(info.cache_max_age);
            if (info.auto_refresh) {
                const secondsUntilRefresh = info.next_refresh_in || (info.cache_max_age * 60);
                startRefreshTimer(secondsUntilRefresh);
            }
        }
    } catch (e) {
        document.getElementById('cache-info')!.innerHTML = 'Direct mode (no caching)';
    }
}
