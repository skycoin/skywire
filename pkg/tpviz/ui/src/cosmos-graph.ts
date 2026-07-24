// WebGL graph renderer (GPU force-layout + rendering via @cosmograph/cosmos).
//
// The classic flat view uses vis-network (Canvas2D), which stabilizes its force
// layout on the main thread and re-tessellates every edge per redraw. At the
// full all-transports scale (~20k edges) that blocks the main thread for
// seconds — the "hang". Cosmos runs BOTH the force simulation and the rendering
// on the GPU, so it draws the entire routable topology (all-transports, not just
// the QoS-reporting /metrics subset) without freezing the UI.
//
// This is a third view alongside Flat (vis-network) and Globe (three.js). It
// reads the SAME S.nodesDataset / S.edgesDataset the other views are built from,
// so it can't drift from them, and honors the same per-type / per-status filters.

import { Graph } from '@cosmograph/cosmos';
import * as S from './state';
import { colors, LOCAL_EDGE_COLOR, ORBIT_LANES, ORBIT_LANE_SPACING } from './constants';
import { handleGraphNodeClick } from './node-click';
import { getCountryColor, getIPGroupColor, countryToFlag } from './utils';
import { calculateGroupRadius, findNonOverlappingPosition } from './grouping';

interface CosmosNode {
  id: string;
  // Fixed positions used when grouping is active (cosmos renders at these with the
  // simulation disabled). Absent → the GPU force layout places the node.
  x?: number;
  y?: number;
  color: string;
  size: number;
  status: string;
  isLocal: boolean;
  title: string;
  label: string;
}
interface CosmosLink {
  source: string;
  target: string;
  color: string;
  type: string;
  live: boolean;
}

// Cosmos simulation-space extent. Nodes with explicit x/y (grouping mode) must
// live inside [0, COSMOS_SPACE]; keep this in sync with the graph's spaceSize.
const COSMOS_SPACE = 4096;

// Dim colour for satellite (country-less) visors that orbit the country clusters.
const SATELLITE_COLOR = '#8a94a6';

let graph: Graph<CosmosNode, CosmosLink> | null = null;
let active = false;
let tooltip: HTMLDivElement | null = null;

// Group-boundary overlay (grouping mode): the packed group circles + labels,
// drawn on a canvas above the cosmos canvas and converted space→screen each frame
// so they track pan/zoom — the WebGL analogue of the flat view's group boundaries.
let boundaryCircles: { x: number; y: number; r: number; color: string; label: string; flag: string; count: number }[] = [];
let boundaryCanvas: HTMLCanvasElement | null = null;
let boundaryRAF: number | null = null;

// ensureTooltip creates the floating hover panel (cosmos renders points only —
// no text — so node detail lives in a hover tooltip, the same info the flat
// view shows in its vis-network node title).
function ensureTooltip(): HTMLDivElement | null {
  if (tooltip) { return tooltip; }
  const container = document.getElementById('cosmos-container');
  if (!container) { return null; }
  tooltip = document.createElement('div');
  tooltip.id = 'cosmos-tooltip';
  tooltip.style.cssText =
    'position:absolute;z-index:10000;display:none;pointer-events:none;max-width:340px;' +
    'padding:8px 10px;background:rgba(11,16,32,0.96);border:1px solid #00d9a5;border-radius:4px;' +
    'color:#cdd2da;font:11px/1.5 monospace;white-space:pre-wrap;word-break:break-all;';
  container.appendChild(tooltip);
  return tooltip;
}

function showTooltip(node: CosmosNode, event: MouseEvent): void {
  const t = ensureTooltip();
  const container = document.getElementById('cosmos-container');
  if (!t || !container) { return; }
  t.textContent = node.title || node.id;
  const rect = container.getBoundingClientRect();
  let x = event.clientX - rect.left + 14;
  let y = event.clientY - rect.top + 14;
  if (x + 350 > rect.width) { x = rect.width - 350; }
  if (y + 120 > rect.height) { y = event.clientY - rect.top - 120; }
  t.style.left = Math.max(0, x) + 'px';
  t.style.top = Math.max(0, y) + 'px';
  t.style.display = 'block';
}

function hideTooltip(): void {
  if (tooltip) { tooltip.style.display = 'none'; }
}

// ensureBoundaryCanvas creates the overlay canvas (above the cosmos WebGL canvas)
// that the group boundaries are drawn on.
function ensureBoundaryCanvas(): HTMLCanvasElement | null {
  if (boundaryCanvas) { return boundaryCanvas; }
  const container = document.getElementById('cosmos-container');
  if (!container) { return null; }
  const c = document.createElement('canvas');
  c.id = 'cosmos-boundary';
  c.style.cssText = 'position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none;z-index:5;';
  container.appendChild(c);
  boundaryCanvas = c;
  return c;
}

function clearBoundaryCanvas(): void {
  if (boundaryCanvas) {
    const ctx = boundaryCanvas.getContext('2d');
    if (ctx) { ctx.clearRect(0, 0, boundaryCanvas.width, boundaryCanvas.height); }
  }
}

// drawBoundaries paints the group circles + labels each frame, converting the
// space-coord circles to screen via cosmos' spaceToScreenPosition / spaceToScreenRadius
// so they stay glued to the clusters as the user pans/zooms. Self-schedules while
// the grouped WebGL view is active.
function drawBoundaries(): void {
  boundaryRAF = null;
  if (!active || !graph || boundaryCircles.length === 0) { clearBoundaryCanvas(); return; }
  const canvas = ensureBoundaryCanvas();
  if (!canvas) { return; }
  const rect = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  const w = Math.max(1, Math.round(rect.width * dpr));
  const h = Math.max(1, Math.round(rect.height * dpr));
  if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
  const ctx = canvas.getContext('2d');
  if (!ctx) { return; }
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, rect.width, rect.height);
  ctx.textAlign = 'center';
  ctx.font = 'bold 15px system-ui, sans-serif';
  for (const b of boundaryCircles) {
    const [sx, sy] = graph.spaceToScreenPosition([b.x, b.y]);
    const sr = graph.spaceToScreenRadius(b.r);
    if (!isFinite(sx) || !isFinite(sy) || sr < 3) { continue; }
    ctx.beginPath();
    ctx.arc(sx, sy, sr, 0, 2 * Math.PI);
    ctx.globalAlpha = 0.07; ctx.fillStyle = b.color; ctx.fill();
    // Dashed boundary ring — matches the classic Flat view's group circles.
    ctx.globalAlpha = 0.6; ctx.lineWidth = 2; ctx.strokeStyle = b.color;
    ctx.setLineDash([8, 4]); ctx.stroke(); ctx.setLineDash([]);
    ctx.globalAlpha = 1;
    // "🇺🇸 US (123)" on a translucent black plate above the circle (as in Flat).
    const label = (b.flag ? b.flag + ' ' : '') + b.label + ' (' + b.count + ')';
    const tw = ctx.measureText(label).width;
    const ly = sy - sr - 12;
    ctx.globalAlpha = 0.6; ctx.fillStyle = '#000';
    ctx.fillRect(sx - tw / 2 - 6, ly - 13, tw + 12, 20);
    ctx.globalAlpha = 1; ctx.fillStyle = b.color;
    ctx.fillText(label, sx, ly + 2);
  }
  boundaryRAF = requestAnimationFrame(drawBoundaries);
}

function startBoundaries(): void {
  if (boundaryRAF == null) { boundaryRAF = requestAnimationFrame(drawBoundaries); }
}

function stopBoundaries(): void {
  if (boundaryRAF != null) { cancelAnimationFrame(boundaryRAF); boundaryRAF = null; }
  clearBoundaryCanvas();
}

export function isCosmosActive(): boolean {
  return active;
}

// filterState reads the same edge-type + node-status checkboxes the flat/globe
// views use, so the WebGL view filters identically.
function edgeTypeVisible(type: string): boolean {
  const on = (id: string) => (document.getElementById(id) as HTMLInputElement)?.checked !== false;
  switch ((type || '').toLowerCase()) {
    case 'stcpr': return on('show-stcpr');
    case 'sudph': return on('show-sudph');
    case 'squicr': return on('show-squicr');
    case 'swtr': return on('show-swtr');
    case 'swsr': return on('show-swsr');
    case 'webrtc': return on('show-webrtc');
    case 'dmsg': return on('show-dmsg');
    default: return true;
  }
}
function statusVisible(status: string): boolean {
  const on = (id: string) => (document.getElementById(id) as HTMLInputElement)?.checked === true;
  if (status === 'online') return on('show-online');
  if (status === 'offline') return on('show-offline');
  return on('show-unknown');
}

// cosmosGroupMode reads the same cluster-country / cluster-ip checkboxes the flat
// view uses, so the WebGL view groups identically. Returns the active single-level
// grouping, or null for the free force layout. (Dual country+IP nesting is a
// flat-view refinement; when both are checked here we group by country.)
function cosmosGroupMode(): 'country' | 'ip' | null {
  const country = (document.getElementById('cluster-country') as HTMLInputElement)?.checked === true;
  const ip = (document.getElementById('cluster-ip') as HTMLInputElement)?.checked === true
    && S.ipGroupsEnabled && !!S.ipGroupsData;
  if (country) { return 'country'; }
  if (ip) { return 'ip'; }
  return null;
}

function groupKeyOf(id: string, mode: 'country' | 'ip'): string {
  if (mode === 'ip') {
    const g = S.ipGroupsData ? S.ipGroupsData.groups[id] : undefined;
    return g !== undefined ? 'ip_' + g : '_no_ip';
  }
  const svc = S.visorServices[id];
  return (svc && svc.country) || '_unknown';
}

function groupColorOf(key: string, mode: 'country' | 'ip'): string {
  if (mode === 'ip') {
    if (key === '_no_ip') { return '#666'; }
    return getIPGroupColor(parseInt(key.replace('ip_', ''), 10));
  }
  return getCountryColor(key === '_unknown' ? '' : key).border;
}

function groupLabelOf(key: string, mode: 'country' | 'ip'): string {
  if (mode === 'ip') { return key === '_no_ip' ? 'No IP' : 'IP ' + key.replace('ip_', ''); }
  return key === '_unknown' ? 'Unknown' : key;
}

function groupFlagOf(key: string, mode: 'country' | 'ip'): string {
  if (mode === 'ip' || key === '_unknown') { return ''; }
  return countryToFlag(key);
}

interface GroupCircle { cx: number; cy: number; r: number; color: string; label: string; flag: string; count: number }

// computeGroupedLayout packs the given nodes into per-group circles using the SAME
// geometry as the flat view's arrangeNodesIntoGroups (calculateGroupRadius +
// findNonOverlappingPosition + single / ring(≤6) / Fibonacci-spiral placement) and
// returns each node's fixed position + its group color. Cosmos renders at these
// positions with the simulation disabled, so WebGL groups by country/IP like Flat.
function computeGroupedLayout(
  nodeIds: string[], mode: 'country' | 'ip',
): { pos: Map<string, { x: number; y: number }>; color: Map<string, string>; groups: GroupCircle[]; satellites: Set<string> } {
  const unknownKey = mode === 'ip' ? '_no_ip' : '_unknown';
  const buckets = new Map<string, string[]>();
  for (const id of nodeIds) {
    const k = groupKeyOf(id, mode);
    let arr = buckets.get(k);
    if (!arr) { arr = []; buckets.set(k, arr); }
    arr.push(id);
  }
  // Country-less visors don't get a circle — they become satellites orbiting the
  // whole cluster (matching the classic view), so pull them out before packing.
  const satIds = buckets.get(unknownKey) || [];
  buckets.delete(unknownKey);

  const grouped = Array.from(buckets.entries())
    .map(([key, ids]) => ({ key, ids, radius: calculateGroupRadius(ids.length) }))
    .sort((a, b) => b.radius - a.radius);

  const placed: { x: number; y: number; radius: number }[] = [];
  const pos = new Map<string, { x: number; y: number }>();
  const color = new Map<string, string>();
  const groups: GroupCircle[] = [];
  for (const g of grouped) {
    const c = findNonOverlappingPosition(g.radius, placed, 60);
    placed.push({ x: c.x, y: c.y, radius: g.radius });
    const col = groupColorOf(g.key, mode);
    groups.push({ cx: c.x, cy: c.y, r: g.radius, color: col, label: groupLabelOf(g.key, mode), flag: groupFlagOf(g.key, mode), count: g.ids.length });
    // Fibonacci/sunflower placement — identical to the flat view's
    // placeNodesInCircle: innerRadius = r - 40, golden-angle spiral, 0.9 fill.
    // The 0.9 keeps the furthest node inside the circle, so it always encloses
    // every visor of the country.
    const inner = g.radius - 40;
    const n = g.ids.length;
    g.ids.forEach((id, i) => {
      let x: number; let y: number;
      if (n === 1) {
        x = c.x; y = c.y;
      } else if (n <= 6) {
        const a = (i / n) * 2 * Math.PI; const r = inner * 0.5;
        x = c.x + r * Math.cos(a); y = c.y + r * Math.sin(a);
      } else {
        const ga = Math.PI * (3 - Math.sqrt(5)); const a = i * ga;
        const r = inner * Math.sqrt(i / n) * 0.9;
        x = c.x + r * Math.cos(a); y = c.y + r * Math.sin(a);
      }
      pos.set(id, { x, y });
      color.set(id, col);
    });
  }

  // Satellites: country-less visors orbit the country clusters on concentric
  // lanes, evenly spaced by angle — the classic view's satellite ring. Placed
  // OUTSIDE every country circle (base = furthest circle edge + 150), matching
  // calculateOrbitParams / initializeSatelliteOrbits.
  const satellites = new Set(satIds);
  if (satIds.length > 0) {
    let ox = 0; let oy = 0;
    for (const p of placed) { ox += p.x; oy += p.y; }
    ox = placed.length ? ox / placed.length : 0;
    oy = placed.length ? oy / placed.length : 0;
    let maxExtent = 0;
    for (const p of placed) {
      const d = Math.hypot(p.x - ox, p.y - oy) + p.radius;
      if (d > maxExtent) { maxExtent = d; }
    }
    const base = (maxExtent || 350) + 150;
    const step = (2 * Math.PI) / satIds.length;
    satIds.forEach((id, i) => {
      const r = base + (i % ORBIT_LANES) * ORBIT_LANE_SPACING;
      const a = i * step;
      pos.set(id, { x: ox + r * Math.cos(a), y: oy + r * Math.sin(a) });
      color.set(id, SATELLITE_COLOR);
    });
  }

  return { pos, color, groups, satellites };
}

// buildData projects the vis-network datasets into cosmos node/link arrays,
// applying the active type/status filters, then — when country/IP grouping is on —
// assigns fixed group-packed positions + per-group colors.
function buildData(): { nodes: CosmosNode[]; links: CosmosLink[]; grouped: boolean } {
  const nodesRaw: any[] = S.nodesDataset ? S.nodesDataset.get() : [];
  const edgesRaw: any[] = S.edgesDataset ? S.edgesDataset.get() : [];

  const visibleNode = new Set<string>();
  const nodes: CosmosNode[] = [];
  for (const n of nodesRaw) {
    if (!statusVisible(n.status)) { continue; }
    visibleNode.add(n.id);
    const bg = (n.color && n.color.background) || '#888888';
    const isLocal = !!(S.localVisorData && n.id === S.localVisorData.pub_key);
    nodes.push({
      id: n.id,
      color: isLocal ? '#00ffff' : bg,
      // vis-network sizes (5..30) are far too big as cosmos pixel dots; map down.
      size: isLocal ? 8 : Math.max(2.5, Math.min(5, (n.size || 4) * 0.4)),
      status: n.status,
      isLocal,
      title: n.title || n.id,
      label: isLocal ? '⬢ YOU' : n.id.substring(0, 8),
    });
  }

  const links: CosmosLink[] = [];
  const connected = new Set<string>();
  for (const e of edgesRaw) {
    if (e.type === 'route') { continue; } // route overlays are a flat-view affordance
    if (!edgeTypeVisible(e.type)) { continue; }
    if (!visibleNode.has(e.from) || !visibleNode.has(e.to)) { continue; }
    const c = e.isLocal || e.isLocalOnly ? LOCAL_EDGE_COLOR : (colors[e.type] || '#9aa0a6');
    links.push({ source: e.from, target: e.to, color: c, type: e.type, live: e.live !== false });
    connected.add(e.from); connected.add(e.to);
  }
  // Drop isolated nodes (no visible edge). Repulsion flings them far out, which
  // stretches fitView so the main cluster ends up tiny and off-center.
  const linkedNodes = nodes.filter(n => connected.has(n.id));

  const mode = cosmosGroupMode();
  if (mode) {
    const { pos, color, groups, satellites } = computeGroupedLayout(linkedNodes.map(n => n.id), mode);
    // No country/IP circles at all (e.g. no survey data → everything is a
    // satellite): a lone satellite ring around empty space is worse than the free
    // force layout, so fall back to it instead.
    if (groups.length === 0) {
      boundaryCircles = [];
      return { nodes: linkedNodes, links, grouped: false };
    }
    // The packed layout is centered on the origin (negative → positive). Cosmos
    // random-inits nodes in a [0, spaceSize] box and renders provided x/y in that
    // same space, so negative coords land off-screen. Translate + (only if needed)
    // uniformly scale the layout to fit centered in the space with a margin.
    let minX = Infinity; let minY = Infinity; let maxX = -Infinity; let maxY = -Infinity;
    pos.forEach(p => {
      if (p.x < minX) { minX = p.x; } if (p.x > maxX) { maxX = p.x; }
      if (p.y < minY) { minY = p.y; } if (p.y > maxY) { maxY = p.y; }
    });
    const margin = 80;
    const usable = COSMOS_SPACE - 2 * margin;
    const spanX = (maxX - minX) || 1; const spanY = (maxY - minY) || 1;
    const scale = Math.min(usable / spanX, usable / spanY, 1); // don't upscale small layouts
    const cx = (minX + maxX) / 2; const cy = (minY + maxY) / 2;
    const toSpace = (x: number, y: number) => ({
      x: COSMOS_SPACE / 2 + (x - cx) * scale,
      y: COSMOS_SPACE / 2 + (y - cy) * scale,
    });
    for (const n of linkedNodes) {
      const p = pos.get(n.id);
      if (p) { const s = toSpace(p.x, p.y); n.x = s.x; n.y = s.y; }
      // Colour by group so the clusters read at a glance (keep the local visor
      // cyan so "YOU" stays findable). Satellites keep their dim colour + a
      // smaller dot so they read as orbiting country-less visors.
      if (!n.isLocal) { const col = color.get(n.id); if (col) { n.color = col; } }
      if (satellites.has(n.id) && !n.isLocal) { n.size = Math.min(n.size, 2.5); }
    }
    // Group boundary circles in the SAME normalized space (drawn on the overlay).
    boundaryCircles = groups.map(g => {
      const s = toSpace(g.cx, g.cy);
      return { x: s.x, y: s.y, r: g.r * scale, color: g.color, label: g.label, flag: g.flag, count: g.count };
    });
  } else {
    boundaryCircles = [];
  }
  return { nodes: linkedNodes, links, grouped: !!mode };
}

function ensureGraph(): Graph<CosmosNode, CosmosLink> | null {
  if (graph) { return graph; }
  const container = document.getElementById('cosmos-container');
  if (!container) { return null; }
  const canvas = document.getElementById('cosmos-canvas') as HTMLCanvasElement | null;
  if (!canvas) { return null; }

  graph = new Graph<CosmosNode, CosmosLink>(canvas, {
    backgroundColor: '#0b1020',
    nodeColor: (n: CosmosNode) => n.color,
    nodeSize: (n: CosmosNode) => n.size,
    nodeGreyoutOpacity: 0.1,
    linkColor: (l: CosmosLink) => l.color,
    linkWidth: (l: CosmosLink) => (l.live ? 1 : 0.5),
    linkArrows: false,
    linkGreyoutOpacity: 0,
    // Fade long links so inter-group edges recede and the country/IP clusters +
    // their boundaries read at a glance; short intra-group links stay opaque. (Also
    // declutters the free force layout.)
    linkVisibilityDistanceRange: [40, 250],
    linkVisibilityMinTransparency: 0.08,
    renderLinks: true,
    curvedLinks: false,
    // Small pixel dots that don't grow on zoom — at ~1k nodes, big dots overlap
    // into solid blobs.
    scaleNodesOnZoom: false,
    fitViewOnInit: true,
    // GPU force simulation — light gravity + repulsion settle a ~15k-edge
    // hairball into a readable ball in a couple seconds, off the main thread.
    // Like the classic Flat view (which stabilizes then disables physics), we
    // run it briefly, follow it with the camera, then pause + frame — a
    // continuously-running sim doesn't reach true stationarity on this graph and
    // slowly translates the (unfollowed) frame off-screen.
    spaceSize: COSMOS_SPACE,
    simulation: {
      gravity: 0.9,
      repulsion: 0.5,
      linkSpring: 1.3,
      linkDistance: 3,
      friction: 0.85,
      decay: 2000,
      // Re-frame once the layout settles (the initial fit is too tight while
      // every node still sits clustered near the center) — but never yank the
      // camera after we've settled + paused (the user may have panned/zoomed).
      onEnd: () => { if (graph && !settled) { graph.fitView(400, 0.1); } },
    },
    events: {
      onClick: (node?: CosmosNode) => {
        if (node && node.id) { handleGraphNodeClick(node.id); }
      },
      onNodeMouseOver: (node: CosmosNode, _i: number, _pos: [number, number], event: any) => {
        if (!node) { return; }
        // Highlight the hovered node's neighborhood (greys out the rest), the
        // WebGL analogue of the flat view's showNodeEdgesOnly-on-hover.
        if (graph) { graph.selectNodeById(node.id, true); }
        if (event && typeof event.clientX === 'number') { showTooltip(node, event as MouseEvent); }
      },
      onNodeMouseOut: () => {
        hideTooltip();
        if (graph) { graph.unselectNodes(); }
      },
    },
  });
  return graph;
}

// cosmosFit frames the whole graph (wired to the "Fit to Screen" + zoom-fit
// buttons when the WebGL view is active).
export function cosmosFit(): void {
  if (graph) { graph.fitView(400, 0.1); }
}

// cosmosZoomBy multiplies the current zoom level (wired to the +/- buttons).
export function cosmosZoomBy(factor: number): void {
  if (graph) { graph.setZoomLevel(graph.getZoomLevel() * factor, 200); }
}

// cosmosFocusNode centers + zooms to a node and selects its neighborhood
// (wired to focusNode / the sidebar visor list / search).
export function cosmosFocusNode(pk: string): void {
  if (!graph) { return; }
  graph.zoomToNodeById(pk, 500, 5);
  graph.selectNodeById(pk, true);
}

// showCosmos hides the flat/globe containers, shows the WebGL canvas and renders.
export function showCosmos(): void {
  active = true;
  const networkEl = document.getElementById('network');
  const globeContainer = document.getElementById('globe-container');
  const cosmosContainer = document.getElementById('cosmos-container');
  if (networkEl) { networkEl.style.display = 'none'; }
  if (globeContainer) { globeContainer.style.display = 'none'; }
  if (cosmosContainer) { cosmosContainer.style.display = 'block'; }
  updateCosmosData();
}

export function hideCosmos(): void {
  active = false;
  hideTooltip();
  stopBoundaries();
  stopSettle();
  const cosmosContainer = document.getElementById('cosmos-container');
  if (cosmosContainer) { cosmosContainer.style.display = 'none'; }
}

// settled tracks whether the force layout has finished its settle window and been
// paused + framed; lastSig fingerprints the currently-rendered graph (mode + node
// + link counts). Together they let a periodic refresh of an UNCHANGED graph
// update the data WITHOUT restarting the simulation — the fix for the view
// "drifting out of view within moments": the 1s countdown refresh (min 5s, see
// api.ts) used to re-run setData every cycle, restarting the force sim before its
// own settle completed, so the graph perpetually re-expanded past the viewport.
let settled = false;
let lastSig = '';
let settleInterval: ReturnType<typeof setInterval> | null = null;

function stopSettle(): void {
  if (settleInterval != null) { clearInterval(settleInterval); settleInterval = null; }
}

// fitCore frames the DENSE CORE of the force layout, excluding the handful of
// weakly-connected nodes the GPU repulsion flings far out — those otherwise
// stretch fitView's bounding box and shove the main cluster off-center (dead
// space on one side). It keeps every node within a robust distance of the
// centroid (so legitimate spread is untouched) and only drops the far tail;
// if there's nothing to trim it falls back to the plain fitView.
function fitCore(g: Graph<CosmosNode, CosmosLink>, duration: number): void {
  const posMap = g.getNodePositionsMap();
  const ids: string[] = [];
  const dist: number[] = [];
  let cx = 0; let cy = 0;
  posMap.forEach((p) => { cx += p[0]; cy += p[1]; });
  const n = posMap.size;
  if (n < 12) { g.fitView(duration, 0.12); return; }
  cx /= n; cy /= n;
  posMap.forEach((p, id) => { ids.push(id); dist.push(Math.hypot(p[0] - cx, p[1] - cy)); });
  const sorted = [...dist].sort((a, b) => a - b);
  const median = sorted[Math.floor(n / 2)] || 1;
  const p95 = sorted[Math.floor(n * 0.95)];
  // Keep everything within 3.5x the median distance OR the 95th percentile,
  // whichever is larger — trims only the true far-flung tail (≤5% of nodes).
  const cutoff = Math.max(median * 3.5, p95);
  const kept: string[] = [];
  for (let i = 0; i < ids.length; i++) { if (dist[i] <= cutoff) { kept.push(ids[i]); } }
  if (kept.length >= 8 && kept.length < ids.length) {
    g.fitViewByNodeIds(kept, duration, 0.15);
  } else {
    g.fitView(duration, 0.12);
  }
}

// startSettle runs the GPU sim and RE-FITS the camera on a short cadence while
// the layout expands, so the view tracks it instead of being framed once (tight,
// at the center) and then left behind. After the window it pauses the sim (which
// is now stationary) and does a final fit — matching the classic Flat view, which
// stabilizes then disables physics. (Leaving the sim running slowly translates
// the frame off-screen: it never reaches true stationarity on this graph.)
function startSettle(): void {
  stopSettle();
  const start = performance.now();
  settleInterval = setInterval(() => {
    if (!graph || !active) { stopSettle(); return; }
    fitCore(graph, 280);
    if (performance.now() - start >= 4500) {
      stopSettle();
      graph.pause();
      fitCore(graph, 500);
      settled = true;
    }
  }, 350);
}

// updateCosmosData reprojects + repushes the data (on refresh, filter, or grouping
// change). It only (re)runs the force layout when the graph actually changed;
// a plain refresh of the same graph updates data in place (runSimulation=false)
// so nothing re-expands.
export function updateCosmosData(): void {
  if (!active) { return; }
  const g = ensureGraph();
  if (!g) { return; }
  const { nodes, links, grouped } = buildData();
  const sig = (grouped ? 'g:' : 'f:') + nodes.length + ':' + links.length;
  const unchanged = settled && sig === lastSig;

  if (grouped) {
    // Fixed group-packed positions. disableSimulation keeps cosmos rendering the
    // nodes at their x/y (so the country/IP clusters stay put, like the flat view)
    // WITHOUT running the force layout — and, unlike pause(), leaves the render loop
    // alive so pan/zoom still work. The boundary overlay loop draws the group
    // circles + labels, tracking pan/zoom.
    stopSettle();
    g.setConfig({ disableSimulation: true });
    g.setData(nodes, links, false);
    if (!unchanged) { g.fitView(500, 0.1); } // frame only on (re)layout, not every refresh
    lastSig = sig; settled = true;
    startBoundaries();
    return;
  }

  stopBoundaries();
  if (unchanged) {
    // Periodic refresh of an already-framed force layout: update the data without
    // restarting the sim, so the graph keeps its positions and stays in view.
    g.setData(nodes, links, false);
    return;
  }
  // Fresh or changed force layout: run the sim and follow it with the camera.
  lastSig = sig; settled = false;
  g.setConfig({ disableSimulation: false });
  g.setData(nodes, links);
  startSettle();
}
