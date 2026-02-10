// Apps: visor application management functionality

import { fetchWithTimeout } from './utils';
import { API_BASE } from './constants';
import type { AppState } from './types';

// App status constants
const APP_STATUS = {
    STOPPED: 0,
    RUNNING: 1,
    ERRORED: 2,
    STARTING: 3,
} as const;

// Status display info
const STATUS_INFO: Record<number, { label: string; color: string; bg: string }> = {
    [APP_STATUS.STOPPED]: { label: 'Stopped', color: '#888', bg: 'rgba(136,136,136,0.2)' },
    [APP_STATUS.RUNNING]: { label: 'Running', color: '#00d9a5', bg: 'rgba(0,217,165,0.2)' },
    [APP_STATUS.ERRORED]: { label: 'Error', color: '#e94560', bg: 'rgba(233,69,96,0.2)' },
    [APP_STATUS.STARTING]: { label: 'Starting', color: '#ffd166', bg: 'rgba(255,209,102,0.2)' },
};

let appsData: AppState[] = [];
let appsRefreshInterval: ReturnType<typeof setInterval> | null = null;
let appsInitialized = false;

/**
 * Fetches the list of apps from the visor.
 */
export async function fetchApps(): Promise<AppState[]> {
    try {
        const resp = await fetchWithTimeout(`${API_BASE}/api/apps`, 10000);
        const data = await resp.json();
        if (data.error) {
            console.error('Apps fetch error:', data.error);
            return [];
        }
        appsData = data;
        return data;
    } catch (err) {
        console.error('Failed to fetch apps:', err);
        return [];
    }
}

/**
 * Starts an application.
 */
export async function startApp(name: string): Promise<boolean> {
    try {
        const resp = await fetchWithTimeout(`${API_BASE}/api/apps/start`, 30000, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name }),
        });
        const data = await resp.json();
        if (data.error) {
            showAppMessage(`Failed to start ${name}: ${data.error}`, 'error');
            return false;
        }
        showAppMessage(`${name} started`, 'success');
        await refreshAppsUI();
        return true;
    } catch (err: any) {
        showAppMessage(`Failed to start ${name}: ${err.message}`, 'error');
        return false;
    }
}

/**
 * Stops an application.
 */
export async function stopApp(name: string): Promise<boolean> {
    try {
        const resp = await fetchWithTimeout(`${API_BASE}/api/apps/stop`, 10000, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name }),
        });
        const data = await resp.json();
        if (data.error) {
            showAppMessage(`Failed to stop ${name}: ${data.error}`, 'error');
            return false;
        }
        showAppMessage(`${name} stopped`, 'success');
        await refreshAppsUI();
        return true;
    } catch (err: any) {
        showAppMessage(`Failed to stop ${name}: ${err.message}`, 'error');
        return false;
    }
}

/**
 * Toggles the auto-start flag for an app.
 */
export async function toggleAutoStart(name: string, autoStart: boolean): Promise<boolean> {
    try {
        const resp = await fetchWithTimeout(`${API_BASE}/api/apps/autostart`, 10000, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, auto_start: autoStart }),
        });
        const data = await resp.json();
        if (data.error) {
            showAppMessage(`Failed to update auto-start: ${data.error}`, 'error');
            return false;
        }
        showAppMessage(`${name} auto-start ${autoStart ? 'enabled' : 'disabled'}`, 'success');
        await refreshAppsUI();
        return true;
    } catch (err: any) {
        showAppMessage(`Failed to update auto-start: ${err.message}`, 'error');
        return false;
    }
}

/**
 * Sets the server PK for an app (vpn-client, skysocks-client).
 */
export async function setAppPK(name: string, pk: string): Promise<boolean> {
    try {
        const resp = await fetchWithTimeout(`${API_BASE}/api/apps/set-pk`, 10000, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, pk }),
        });
        const data = await resp.json();
        if (data.error) {
            showAppMessage(`Failed to set PK: ${data.error}`, 'error');
            return false;
        }
        showAppMessage(`${name} server PK updated`, 'success');
        await refreshAppsUI();
        return true;
    } catch (err: any) {
        showAppMessage(`Failed to set PK: ${err.message}`, 'error');
        return false;
    }
}

/**
 * Shows a message in the apps section.
 */
function showAppMessage(msg: string, type: 'success' | 'error'): void {
    const msgEl = document.getElementById('apps-message');
    if (!msgEl) return;

    msgEl.textContent = msg;
    msgEl.style.display = 'block';
    msgEl.style.color = type === 'success' ? '#00d9a5' : '#e94560';
    msgEl.style.background = type === 'success' ? 'rgba(0,217,165,0.1)' : 'rgba(233,69,96,0.1)';

    setTimeout(() => {
        msgEl.style.display = 'none';
    }, 3000);
}

/**
 * Refreshes the apps UI display.
 */
export async function refreshAppsUI(): Promise<void> {
    const apps = await fetchApps();
    renderAppsUI(apps);
}

/**
 * Renders the apps list in the UI.
 */
export function renderAppsUI(apps: AppState[]): void {
    const container = document.getElementById('apps-list');
    if (!container) return;

    if (apps.length === 0) {
        container.innerHTML = '<div style="color:#666;font-size:11px;padding:8px;">No apps available</div>';
        return;
    }

    container.innerHTML = apps.map(app => {
        const statusInfo = STATUS_INFO[app.status] || STATUS_INFO[APP_STATUS.STOPPED];
        const isRunning = app.status === APP_STATUS.RUNNING;
        const isClient = app.name.includes('-client');
        const needsPK = isClient && (app.name === 'vpn-client' || app.name === 'skysocks-client');

        // Extract current server PK from args if present
        let currentPK = '';
        if (app.args) {
            const srvIdx = app.args.indexOf('--srv');
            if (srvIdx !== -1 && app.args[srvIdx + 1]) {
                currentPK = app.args[srvIdx + 1];
            }
        }

        return `
            <div class="app-item" data-app="${app.name}">
                <div class="app-header">
                    <span class="app-name">${app.name}</span>
                    <span class="app-status" style="color:${statusInfo.color};background:${statusInfo.bg}">
                        ${statusInfo.label}
                    </span>
                </div>
                <div class="app-details">
                    <span class="app-port">Port: ${app.port}</span>
                    <label class="app-autostart">
                        <input type="checkbox" ${app.auto_start ? 'checked' : ''}
                               onchange="window.appToggleAutoStart('${app.name}', this.checked)">
                        Auto-start
                    </label>
                </div>
                ${needsPK ? `
                <div class="app-pk-row">
                    <input type="text" class="app-pk-input" id="pk-${app.name}"
                           placeholder="Server PK" value="${currentPK}"
                           style="flex:1;min-width:0;padding:3px 6px;font-size:10px;font-family:monospace;background:#1a1a2e;border:1px solid #0f3460;color:#eee;border-radius:2px;">
                    <button class="app-pk-btn" onclick="window.appSetPK('${app.name}')"
                            style="padding:3px 6px;font-size:9px;background:#0f3460;color:#eee;border:none;border-radius:2px;cursor:pointer;">Set</button>
                </div>
                ` : ''}
                <div class="app-actions">
                    ${isRunning
                        ? `<button class="app-btn app-btn-stop" onclick="window.appStop('${app.name}')">Stop</button>`
                        : `<button class="app-btn app-btn-start" onclick="window.appStart('${app.name}')">Start</button>`
                    }
                </div>
            </div>
        `;
    }).join('');
}

/**
 * Initializes apps management - sets up global handlers and starts refresh interval.
 */
export function initApps(): void {
    // Only initialize once
    if (appsInitialized) {
        return;
    }
    appsInitialized = true;

    // Expose functions globally for onclick handlers
    (window as any).appStart = startApp;
    (window as any).appStop = stopApp;
    (window as any).appToggleAutoStart = toggleAutoStart;
    (window as any).appSetPK = (name: string) => {
        const input = document.getElementById(`pk-${name}`) as HTMLInputElement;
        if (input && input.value.trim()) {
            setAppPK(name, input.value.trim());
        }
    };

    // Initial fetch
    refreshAppsUI();

    // Auto-refresh every 5 seconds
    if (appsRefreshInterval) {
        clearInterval(appsRefreshInterval);
    }
    appsRefreshInterval = setInterval(refreshAppsUI, 5000);
}

/**
 * Shows or hides the apps section based on visor connection.
 */
export function showAppsSection(show: boolean): void {
    const section = document.getElementById('section-apps');
    if (section) {
        section.style.display = show ? 'block' : 'none';
        if (show) {
            initApps();
        } else {
            if (appsRefreshInterval) {
                clearInterval(appsRefreshInterval);
                appsRefreshInterval = null;
            }
            appsInitialized = false; // Reset so it can be initialized again on reconnect
        }
    }
}
