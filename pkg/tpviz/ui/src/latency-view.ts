// Latency view: visors positioned by measured round-trip time, with a
// spherical Voronoi cell each.
//
// The sphere is NOT the Earth. A visor's position is a function of the
// latencies on its transports and nothing else, so two visors sit
// together when the network puts them together, whichever continents
// they are on. Country stays available as a colour, which is what makes
// the interesting question askable: where does network distance agree
// with physical distance, and where does it not.
//
// All of the numeric work is Go (pkg/tpviz/latency, called through
// tpvizGL): the embedding is stress majorization on the sphere and the
// cells come from a spherical Delaunay triangulation. cosmos-go draws
// the points with its simulation OFF, because the positions are solved
// rather than simulated — its link distance is one global scalar, so a
// force layout could not honour per-edge latency even in principle.

import * as S from './state';
import { API_BASE } from './constants';
import { fetchWithTimeout } from './utils';

function gl(): any { return (window as any).tpvizGL; }

interface LatencyEdge { a: string; b: string; ms: number; n: number; type: string; }
interface LatencyGraph { edges: LatencyEdge[]; visors: string[]; days: number; }

let loaded: LatencyGraph | null = null;
let active = false;

// Drag state for rotating the sphere.
let dragging = false;
let lastX = 0, lastY = 0;

/** Fetches the latency graph. CXO-backed; 503 means the feed is cold. */
export async function loadLatencyGraph(): Promise<LatencyGraph | null> {
    try {
        const r = await fetchWithTimeout(API_BASE + '/api/tp-latency');
        if (!r.ok) {
            const body = await r.json().catch(() => ({}));
            setStatus(body.error || `latency feed unavailable (${r.status})`, body.hint || '');
            return null;
        }
        loaded = await r.json();
        return loaded;
    } catch (e: any) {
        setStatus('latency feed unavailable', e?.message || '');
        return null;
    }
}

/** Solves the embedding in Go and pushes the result into cosmos-go. */
export function renderLatency(): void {
    const g = loaded;
    const api = gl();
    if (!g || !api || !api.setLatencyGraph) { return; }

    const index = new Map<string, number>();
    g.visors.forEach((pk, i) => index.set(pk, i));
    const edges = g.edges.map(e => ({
        a: index.get(e.a) ?? 0,
        b: index.get(e.b) ?? 0,
        ms: e.ms,
    }));

    const out = api.setLatencyGraph({ visors: g.visors, edges });
    applyProjection(out, g);
}

// applyProjection feeds one solved/rotated frame to the renderer.
function applyProjection(out: any, g: LatencyGraph): void {
    const api = gl();
    if (!out || !api) { return; }

    // Positions arrive as a flat [x,y,...] in unit-sphere space; cosmos-go
    // wants a Float32Array. Scale to the simulation space so the sphere
    // fills the view rather than sitting in one pixel at the origin.
    const scale = 2000;
    const pts = out.points as number[];
    const pos = new Float32Array(pts.length);
    for (let i = 0; i < pts.length; i++) { pos[i] = pts[i] * scale; }
    api.setPointPositions?.(pos);

    // One color per visor, by country, so geography reads as color on a
    // sphere whose geometry owes nothing to it. That is the point of the
    // view: where the colors cluster, network distance and physical
    // distance agree, and where they scatter, they do not.
    const colors: string[] = g.visors.map(colorForVisor);

    const cells = (out.cells as any[]).map(c => ({
        site: c.site,
        ring: (c.ring as number[]).map(v => v * scale),
    }));
    api.setLatencyCells?.(cells, colors);

    setStatus(
        `${g.visors.length} visors · ${g.edges.length} measured pairs · ${g.days}d window`,
        `fit: stress ${Number(out.stress).toFixed(3)} — positions show relative structure, not readable RTT`,
    );
}

/** Rotates the sphere by a drag, reprojecting without re-solving. */
function rotate(dx: number, dy: number): void {
    const api = gl();
    if (!api?.rotateLatency || !loaded) { return; }
    applyProjection(api.rotateLatency(dx, dy), loaded);
}

function onDown(e: PointerEvent) { dragging = true; lastX = e.clientX; lastY = e.clientY; }
function onUp() { dragging = false; }
function onMove(e: PointerEvent) {
    if (!dragging) { return; }
    const dx = (e.clientX - lastX) * 0.005;
    const dy = (e.clientY - lastY) * 0.005;
    lastX = e.clientX; lastY = e.clientY;
    rotate(dx, dy);
}

function container(): HTMLElement | null {
    return document.getElementById('cosmosgo-container');
}

/** Enters the latency view. */
export async function showLatency(): Promise<void> {
    if (active) { return; }
    active = true;
    const el = container();
    if (el) {
        el.style.display = 'block';
        el.addEventListener('pointerdown', onDown);
        window.addEventListener('pointerup', onUp);
        window.addEventListener('pointermove', onMove);
    }
    // The solved layout must not be perturbed by a running simulation.
    gl()?.setPhysics?.(false);
    if (!loaded) { await loadLatencyGraph(); }
    renderLatency();
}

/** Leaves the latency view, clearing the cells so they cannot bleed
 *  into another view that shares the canvas. */
export function hideLatency(): void {
    if (!active) { return; }
    active = false;
    gl()?.setLatencyCells?.([], []);
    const el = container();
    if (el) {
        el.removeEventListener('pointerdown', onDown);
    }
    window.removeEventListener('pointerup', onUp);
    window.removeEventListener('pointermove', onMove);
    setStatus('', '');
}

export function latencyActive(): boolean { return active; }

// setStatus writes the two-line readout. The second line carries the fit,
// deliberately: a sphere gives each visor two degrees of freedom and
// latency space is not spherical, so the picture is honest about who is
// near whom and must not be read as a measurement.
function setStatus(main: string, detail: string): void {
    const el = document.getElementById('latency-status');
    if (!el) { return; }
    el.style.display = main ? 'block' : 'none';
    el.innerHTML = main
        ? `<div>${main}</div><div style="color:#888;font-size:11px;margin-top:2px">${detail}</div>`
        : '';
}

// Re-exported for the state module's benefit.
export { S };

// colorForVisor maps a visor to a stable color by country, falling back
// to a hash of its public key when the country is unknown. Countries get
// hues off the golden ratio so neighbouring codes stay distinguishable.
function colorForVisor(pk: string): string {
    const country = S.servicesData?.[pk]?.country || '';
    const key = country || pk;
    let h = 0;
    for (let i = 0; i < key.length; i++) { h = (h * 31 + key.charCodeAt(i)) >>> 0; }
    const hue = (h * 0.618033988749895 * 360) % 360;
    // Unknown-country visors are desaturated so they read as "no data"
    // rather than as another country.
    return country ? `hsl(${hue.toFixed(0)}, 70%, 60%)` : `hsl(${hue.toFixed(0)}, 15%, 45%)`;
}
