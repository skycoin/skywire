// Pure utility functions — no DOM access, no state mutation

import * as S from './state';

export function countryToFlag(countryCode: string): string {
    if (!countryCode || countryCode.length !== 2) return '';
    const code = countryCode.toUpperCase();
    const first = 0x1F1E6 + (code.charCodeAt(0) - 65);
    const second = 0x1F1E6 + (code.charCodeAt(1) - 65);
    return String.fromCodePoint(first, second);
}

export function getBaseVersion(version: string): string {
    if (!version) return '';
    const idx = version.indexOf('-');
    return idx > 0 ? version.substring(0, idx) : version;
}

export function parseVersion(version: string): [number, number, number] {
    const base = getBaseVersion(version);
    const match = base.match(/v?(\d+)\.(\d+)\.(\d+)/);
    if (!match) return [0, 0, 0];
    return [parseInt(match[1]), parseInt(match[2]), parseInt(match[3])];
}

export function compareVersions(a: string, b: string): number {
    const pa = parseVersion(a);
    const pb = parseVersion(b);
    for (let i = 0; i < 3; i++) {
        if (pa[i] !== pb[i]) return pa[i] - pb[i];
    }
    return 0;
}

export function getUniqueVersions(): string[] {
    const versions = new Set<string>();
    for (const v of Object.values(S.visorVersions)) {
        const base = getBaseVersion(v);
        if (base) versions.add(base);
    }
    return Array.from(versions).sort((a, b) => compareVersions(b, a));
}

export function getVersionMass(version: string): number {
    if (!version) return 1;
    const versions = getUniqueVersions();
    if (versions.length === 0) return 1;
    const base = getBaseVersion(version);
    const idx = versions.indexOf(base);
    if (idx === -1) return 1;
    const ratio = 1 - (idx / versions.length);
    return 1 + ratio * 2;
}

export function getVisorStatus(pk: string): 'online' | 'offline' | 'unknown' {
    if (S.onlineVisors.has(pk)) return 'online';
    if (S.offlineVisors.has(pk)) return 'offline';
    return 'unknown';
}

export function formatBytes(bytes: number): string {
    if (!bytes || bytes === 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + units[i];
}

export function isLocalVisor(pk: string): boolean {
    return !!(S.localVisorData && S.localVisorData.connected && S.localVisorData.pub_key === pk);
}

export function isLocalTransport(pk1: string, pk2: string): boolean {
    if (!S.localVisorData || !S.localVisorData.connected || !S.localVisorData.transports) return false;
    const localPK = S.localVisorData.pub_key;
    return S.localVisorData.transports.some(t =>
        (t.remote_pk === pk1 && localPK === pk2) ||
        (t.remote_pk === pk2 && localPK === pk1) ||
        (localPK === pk1 && t.remote_pk === pk2) ||
        (localPK === pk2 && t.remote_pk === pk1)
    );
}

export function getLocalTransportStats(remotePK: string) {
    if (!S.localVisorData || !S.localVisorData.transports) return null;
    return S.localVisorData.transports.find(t => t.remote_pk === remotePK) || null;
}

export function fetchWithTimeout(url: string, timeoutMs = 25000): Promise<Response> {
    return Promise.race([
        fetch(url),
        new Promise<never>((_, reject) =>
            setTimeout(() => reject(new Error('Request timeout')), timeoutMs)
        ),
    ]);
}

export function getIPGroupColor(groupId: number): string {
    if (!S.ipGroupColors[groupId]) {
        const goldenRatio = 0.618033988749895;
        const hue = (groupId * goldenRatio * 360) % 360;
        const saturation = 70 + (groupId % 3) * 10;
        const lightness = 50 + (groupId % 2) * 10;
        S.ipGroupColors[groupId] = `hsl(${hue}, ${saturation}%, ${lightness}%)`;
    }
    return S.ipGroupColors[groupId];
}

export function getIPGroupBorder(groupId: number): string {
    const goldenRatio = 0.618033988749895;
    const hue = (groupId * goldenRatio * 360) % 360;
    return `hsl(${hue}, 60%, 35%)`;
}

export function getCountryColor(countryCode: string): { background: string; border: string } {
    if (!countryCode) return { background: 'rgba(102, 102, 102, 0.3)', border: '#666' };
    let hash = 0;
    for (let i = 0; i < countryCode.length; i++) {
        hash = countryCode.charCodeAt(i) + ((hash << 5) - hash);
    }
    const hue = Math.abs(hash) % 360;
    return {
        background: `hsla(${hue}, 70%, 50%, 0.15)`,
        border: `hsl(${hue}, 70%, 50%)`,
    };
}

export function formatCountdown(seconds: number): string {
    const mins = Math.floor(seconds / 60);
    const secs = seconds % 60;
    return mins + ':' + (secs < 10 ? '0' : '') + secs;
}
