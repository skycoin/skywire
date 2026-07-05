// Color and configuration constants

export const API_BASE = window.location.hostname === '' || window.location.protocol === 'file:'
    ? 'https://tpd.skywire.skycoin.com'
    : '';

export const WS_MAX_RECONNECT_DELAY = 30000;

export const colors: Record<string, string> = {
    stcpr: '#00d9a5',
    sudph: '#00b4d8',
    dmsg: '#ffd166',
    // Newer transport types: skywire-QUIC (squicr, now the default), WebTransport
    // (swtr) and skywire-WebSocket (swsr). Without these the fleet's QUIC edges
    // fell through to the gray '#888888' fallback and had no legend/filter.
    squicr: '#8b7cff',
    swtr: '#ff7eb6',
    swsr: '#c3e88d',
    webrtc: '#ff9f43',
    route: '#ff00ff',
};

export const LOCAL_VISOR_COLOR = {
    background: '#00ffff',
    border: '#ff00ff',
    highlight: { background: '#00ffff', border: '#ffffff' },
};

export const LOCAL_EDGE_COLOR = '#00ffff';

export const ROUTE_DEST_COLOR = {
    background: '#ff00ff',
    border: '#ffffff',
    highlight: { background: '#ff66ff', border: '#ffffff' },
};

export const SATELLITE_EMOJI = '🛰️';
export const ORBIT_LANES = 3;
export const ORBIT_LANE_SPACING = 60;
export const ORBIT_ANGULAR_VELOCITY = 0.0008;
