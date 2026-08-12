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
import { colors, LOCAL_EDGE_COLOR, ROUTE_DEST_COLOR } from './constants';
import { handleGraphNodeClick } from './node-click';
import { getCountryColor, getIPGroupColor, countryToFlag } from './utils';
// (grouping geometry is computed locally below — packCircles / radiusForCount)

export interface CosmosNode {
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
export interface CosmosLink {
  source: string;
  target: string;
  color: string;
  type: string;
  live: boolean;
}

// Cosmos simulation-space extent. Nodes with explicit x/y (grouping mode) must
// live inside [0, COSMOS_SPACE]; keep this in sync with the graph's spaceSize.
const COSMOS_SPACE = 4096;

// Cosmos renders nodes at a CONSTANT pixel size (scaleNodesOnZoom:false), while
// the group boundary circles are drawn in scaled space. So at a zoomed-out fit a
// snug ring clips the fixed-size dots sitting near its edge — nodes appear to
// spill outside their country circle. Pad the DRAWN circle radius by (a bit more
// than) the largest node pixel radius so the ring always encloses its dots. This
// is purely a rendering pad; the packed layout stays tight.
const BOUNDARY_NODE_PAD = 10;

let graph: Graph<CosmosNode, CosmosLink> | null = null;
let active = false;
// PK of the node kept highlighted by a click (persistent selection, parity with Flat).
let selectedNodeId: string | null = null;

// cosmosSetPhysics starts/pauses the GPU force simulation (parity with the Flat
// view's physics toggle). No-op in grouped mode, where positions are fixed.
export function cosmosSetPhysics(on: boolean): void {
  if (!graph) { return; }
  if (on) { graph.setConfig({ disableSimulation: false }); graph.start(); } else { graph.pause(); }
}
let tooltip: HTMLDivElement | null = null;

// Group-boundary overlay (grouping mode): the packed group circles + labels,
// drawn on a canvas above the cosmos canvas and converted space→screen each frame
// so they track pan/zoom — the WebGL analogue of the flat view's group boundaries.
let boundaryCircles: { x: number; y: number; r: number; color: string; label: string; flag: string }[] = [];
let boundaryCanvas: HTMLCanvasElement | null = null;
let boundaryRAF: number | null = null;
// Fingerprint of the last-drawn view transform (zoom + pan). The boundary rAF
// stays alive to track pan/zoom, but skips the actual canvas redraw when the
// view hasn't moved — otherwise it burns ~60fps redrawing a static overlay.
let lastBoundaryKey = '';

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
  // Honor the "Show Tooltips on Hover" toggle (parity with the Flat view).
  if ((document.getElementById('toggle-tooltips') as HTMLInputElement)?.checked === false) { return; }
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
  // Suspended (visualizer not visible): keep the loop alive but skip the redraw.
  if (S.renderPaused) { boundaryRAF = requestAnimationFrame(drawBoundaries); return; }
  // Idle-skip: if the view transform is unchanged since the last drawn frame,
  // reschedule without touching the canvas. Keeps the overlay glued to the graph
  // during pan/zoom while cutting idle CPU (the grouped layout is static).
  const [r0x, r0y] = graph.spaceToScreenPosition([0, 0]);
  const key = graph.getZoomLevel() + ':' + Math.round(r0x) + ':' + Math.round(r0y);
  if (key === lastBoundaryKey) { boundaryRAF = requestAnimationFrame(drawBoundaries); return; }
  lastBoundaryKey = key;
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
  ctx.font = '13px system-ui, sans-serif';
  for (const b of boundaryCircles) {
    const [sx, sy] = graph.spaceToScreenPosition([b.x, b.y]);
    // Derive the screen radius from spaceToScreenPosition of a point on the
    // circle's edge — NOT graph.spaceToScreenRadius(), which in this cosmos
    // version returns a value ~3x smaller than the node rendering scale, so the
    // ring was drawn far too small and its own nodes rendered outside it. Using
    // the same projection as the nodes keeps circle and nodes consistent at any
    // zoom. Pad by the node pixel radius so fixed-size dots at the edge stay in.
    const [ex, ey] = graph.spaceToScreenPosition([b.x + b.r, b.y]);
    const sr = Math.hypot(ex - sx, ey - sy) + BOUNDARY_NODE_PAD;
    if (!isFinite(sx) || !isFinite(sy) || sr < 2) { continue; }
    ctx.beginPath();
    ctx.arc(sx, sy, sr, 0, 2 * Math.PI);
    ctx.globalAlpha = 0.07; ctx.fillStyle = b.color; ctx.fill();
    ctx.globalAlpha = 0.55; ctx.lineWidth = 1.2; ctx.strokeStyle = b.color; ctx.stroke();
    ctx.globalAlpha = 1;
    const label = (b.flag ? b.flag + ' ' : '') + b.label;
    ctx.fillStyle = b.color;
    ctx.fillText(label, sx, sy - sr - 5);
  }
  boundaryRAF = requestAnimationFrame(drawBoundaries);
}

function startBoundaries(): void {
  lastBoundaryKey = ''; // force one redraw with the (possibly new) circles
  if (boundaryRAF == null) { boundaryRAF = requestAnimationFrame(drawBoundaries); }
}

function stopBoundaries(): void {
  if (boundaryRAF != null) { cancelAnimationFrame(boundaryRAF); boundaryRAF = null; }
  lastBoundaryKey = '';
  clearBoundaryCanvas();
}

// cosmosPause / cosmosResume suspend + restore the WebGL render loop when the
// visualizer isn't visible. Unlike the settle-pause (which frees the GPU once the
// layout is framed), this is a reversible visibility gate: pause halts rendering
// while hidden, resume restarts the sim so it keeps running while visible. No-op
// unless the cosmos view is active + built.
export function cosmosPause(): void {
  if (graph && active) { try { graph.pause(); } catch { /* noop */ } }
}
export function cosmosResume(): void {
  if (graph && active) { try { graph.start(); } catch { /* noop */ } }
}

// cosmosTeardown stops the WebGL renderer + the boundary-overlay rAF and releases
// the GPU graph, so unmounting the tab doesn't leave the cosmos render loop /
// force simulation running against a detached canvas. Idempotent + null-safe.
export function cosmosTeardown(): void {
  stopBoundaries();
  if (graph) {
    // Prefer a full destroy() if the cosmos build exposes one; otherwise pause()
    // (which stops scheduling frames) is enough to halt the render loop.
    try { (graph as unknown as { destroy?: () => void }).destroy?.(); } catch { /* noop */ }
    try { graph.pause(); } catch { /* noop */ }
    graph = null;
  }
  active = false;
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
// dmsgServersVisible follows the show-dmsg-servers toggle. DMSG-server nodes are a
// distinct class (Flat adds them via addDMSGGraphElements and hides them + their
// client edges unless this is checked); default off.
function dmsgServersVisible(): boolean {
  return (document.getElementById('show-dmsg-servers') as HTMLInputElement)?.checked === true;
}
function statusVisible(status: string): boolean {
  const on = (id: string) => (document.getElementById(id) as HTMLInputElement)?.checked === true;
  if (status === 'online') return on('show-online');
  if (status === 'offline') return on('show-offline');
  return on('show-unknown');
}

// versionVisible honors the sidebar version-filter <select> (parity with the Flat
// view, which hid non-matching nodes). '' / 'all' = show everything.
function versionVisible(version: string | undefined): boolean {
  const sel = document.getElementById('version-filter') as HTMLSelectElement | null;
  const want = sel?.value || '';
  if (!want || want === 'all') return true;
  return (version || '') === want;
}

// serviceRouteColorOf mirrors the flat view's getNodeColor service/route recolor:
// with show-routes on, route destinations turn magenta; with show-services on, VPN
// nodes turn purple and proxy nodes orange (routes take precedence, as in Flat).
// Returns null when no override applies so the node keeps its status/group color.
// The local visor is never recolored (stays cyan) — callers guard on isLocal.
function serviceRouteColorOf(id: string): string | null {
  const showRoutes = (document.getElementById('show-routes') as HTMLInputElement)?.checked === true;
  if (showRoutes && S.routeDestinations.has(id)) { return ROUTE_DEST_COLOR.background; }
  const showServices = (document.getElementById('show-services') as HTMLInputElement)?.checked === true;
  if (showServices) {
    const svc = S.visorServices[id];
    if (svc && svc.services && svc.services.length) {
      if (svc.services.includes('vpn')) { return '#9f6efc'; }
      if (svc.services.includes('proxy')) { return '#ffa500'; }
    }
  }
  return null;
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

interface GroupCircle { cx: number; cy: number; r: number; color: string; label: string; flag: string }

// computeGroupedLayout packs the given nodes into per-group circles using the SAME
// geometry as the flat view's arrangeNodesIntoGroups (calculateGroupRadius +
// findNonOverlappingPosition + single / ring(≤6) / Fibonacci-spiral placement) and
// returns each node's fixed position + its group color. Cosmos renders at these
// positions with the simulation disabled, so WebGL groups by country/IP like Flat.
function computeGroupedLayout(
  nodeIds: string[], mode: 'country' | 'ip', weightOf: (id: string) => number,
): { pos: Map<string, { x: number; y: number }>; color: Map<string, string>; groups: GroupCircle[] } {
  const buckets = new Map<string, string[]>();
  for (const id of nodeIds) {
    const k = groupKeyOf(id, mode);
    let arr = buckets.get(k);
    if (!arr) { arr = []; buckets.set(k, arr); }
    arr.push(id);
  }
  // For each group, place the nodes in LOCAL coordinates first, then size the
  // circle from the ACTUAL furthest node — so the ring is guaranteed to enclose
  // every node in the country (no node ever spills out) without an oversized
  // empty margin. Nodes are ordered by transport count descending so the
  // best-connected sit at the centre and the fill spirals out to the least-
  // connected (parity with the flat view's mass-weighted placement).
  const grouped = Array.from(buckets.entries())
    .map(([key, ids]) => {
      const sorted = ids.slice().sort((a, b) => weightOf(b) - weightOf(a));
      const n = sorted.length;
      const f = fillRadiusForCount(n);
      const offsets = sorted.map((_, i) => {
        if (n === 1 || i === 0) { return { x: 0, y: 0 }; } // most-connected at centre
        if (n <= 6) {
          const a = ((i - 1) / (n - 1)) * 2 * Math.PI; // remaining nodes on an even ring
          return { x: f * Math.cos(a), y: f * Math.sin(a) };
        }
        const ga = Math.PI * (3 - Math.sqrt(5)); const a = i * ga;
        const r = f * Math.sqrt(i / n);
        return { x: r * Math.cos(a), y: r * Math.sin(a) };
      });
      let maxDist = 0;
      for (const o of offsets) { const d = Math.hypot(o.x, o.y); if (d > maxDist) { maxDist = d; } }
      const radius = maxDist + GROUP_RING_MARGIN;
      return { key, ids: sorted, offsets, radius };
    })
    .sort((a, b) => b.radius - a.radius);

  // Guaranteed non-overlapping placement of the (real-extent) circles, largest first.
  const centers = packCircles(grouped.map(g => g.radius), GROUP_PACK_PADDING);
  const pos = new Map<string, { x: number; y: number }>();
  const color = new Map<string, string>();
  const groups: GroupCircle[] = [];
  grouped.forEach((g, gi) => {
    const c = centers[gi];
    const col = groupColorOf(g.key, mode);
    // Label includes the node count, matching the flat view's "DE (15)".
    groups.push({ cx: c.x, cy: c.y, r: g.radius, color: col, label: groupLabelOf(g.key, mode) + ' (' + g.ids.length + ')', flag: groupFlagOf(g.key, mode) });
    g.ids.forEach((id, i) => {
      const o = g.offsets[i];
      pos.set(id, { x: c.x + o.x, y: c.y + o.y });
      color.set(id, col);
    });
  });
  return { pos, color, groups };
}

// Layout tunables for the grouped view. NODE_SPACING is the approximate gap (in
// layout units) between adjacent nodes in a group; the whole layout is later
// uniformly scaled to fit COSMOS_SPACE, so these are relative, not pixels.
const NODE_SPACING = 9;
const GROUP_RING_MARGIN = 12; // gap from the outermost node to the circle edge
const GROUP_PACK_PADDING = 18; // gap between neighbouring group circles

// fillRadiusForCount sizes the radius the nodes OCCUPY so a Fibonacci/ring fill
// at ~NODE_SPACING between neighbours lands n nodes in a disk of this radius:
// for n points spread over a disk of radius R the nearest-neighbour spacing is
// ~R/sqrt(n), so R = NODE_SPACING*sqrt(n). Area ∝ n, so a 1-visor country reads
// as a small dot and a 200-visor country as proportionally large — and the
// circle (fill + margin) always hugs its contents instead of dwarfing them.
function fillRadiusForCount(n: number): number {
  return Math.max(6, NODE_SPACING * Math.sqrt(Math.max(1, n)));
}

// packCircles lays out circles (largest first) at the first slot on an outward
// spiral that clears every already-placed circle — a guaranteed non-overlapping
// pack (the shared ring-search fell back to an overlapping hex grid at scale).
function packCircles(radii: number[], padding: number): { x: number; y: number }[] {
  const placed: { x: number; y: number; r: number }[] = [];
  const out: { x: number; y: number }[] = [];
  for (const r of radii) {
    let best = { x: 0, y: 0 };
    if (placed.length) {
      const step = Math.max(10, r * 0.2);
      let found = false;
      for (let rad = 0; rad < 200000 && !found; rad += step) {
        const count = Math.max(1, Math.floor((2 * Math.PI * rad) / step));
        for (let k = 0; k < count; k++) {
          const a = (k / count) * 2 * Math.PI;
          const x = Math.cos(a) * rad; const y = Math.sin(a) * rad;
          if (placed.every(p => Math.hypot(p.x - x, p.y - y) >= p.r + r + padding)) {
            best = { x, y }; found = true; break;
          }
        }
      }
    }
    placed.push({ x: best.x, y: best.y, r });
    out.push(best);
  }
  return out;
}

// buildData projects the vis-network datasets into cosmos node/link arrays,
// applying the active type/status filters, then — when country/IP grouping is on —
// assigns fixed group-packed positions + per-group colors.
// buildData is exported so the Go/wasm WebGL view (cosmos-go-graph.ts) is fed
// from exactly this function. The two views exist side by side to be compared,
// and that comparison is only about the engine if the data reaching them is
// identical.
// currentBoundaries exposes the group circles buildData last computed, in graph
// space, for whichever view is drawing the overlay.
export function currentBoundaries(): { x: number; y: number; r: number; color: string; label: string; flag: string }[] {
  return boundaryCircles;
}

export function buildData(): { nodes: CosmosNode[]; links: CosmosLink[]; grouped: boolean; connectedIds: string[] } {
  const nodesRaw: any[] = S.nodesDataset ? S.nodesDataset.get() : [];
  const edgesRaw: any[] = S.edgesDataset ? S.edgesDataset.get() : [];

  const visibleNode = new Set<string>();
  const nodes: CosmosNode[] = [];
  for (const n of nodesRaw) {
    if (n.isDMSGServer) {
      // DMSG servers are a distinct node class. Flat adds them via
      // addDMSGGraphElements and shows them only when show-dmsg-servers is on;
      // previously cosmos leaked them in through the show-unknown status filter
      // and drew them like visors. Gate on the toggle, colour them a distinct
      // dmsg purple, and size by client count (encoded in n.size = 5..30). They
      // keep their country in visorServices so country grouping places them.
      if (!dmsgServersVisible()) { continue; }
      visibleNode.add(n.id);
      nodes.push({
        id: n.id,
        color: '#c9a8ff',
        size: Math.max(3, Math.min(8, (n.size || 5) * 0.28)),
        status: 'dmsg',
        isLocal: false,
        title: n.title || n.id,
        label: n.id.replace('dmsg-srv-', '').substring(0, 8),
      });
      continue;
    }
    if (!statusVisible(n.status)) { continue; }
    if (!versionVisible((S.visorVersions && S.visorVersions[n.id]) || n.version)) { continue; }
    visibleNode.add(n.id);
    const bg = (n.color && n.color.background) || '#888888';
    const isLocal = !!(S.localVisorData && n.id === S.localVisorData.pub_key);
    const svcRouteColor = isLocal ? null : serviceRouteColorOf(n.id);
    nodes.push({
      id: n.id,
      color: isLocal ? '#00ffff' : (svcRouteColor || bg),
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
    if (e.isDMSGConnection || e.type === 'dmsg-connection') {
      // Client↔dmsg-server edges follow show-dmsg-servers (Flat sets
      // hidden:!showDMSGServers on them), not the transport-type filters.
      if (!dmsgServersVisible()) { continue; }
      if (!visibleNode.has(e.from) || !visibleNode.has(e.to)) { continue; }
      links.push({ source: e.from, target: e.to, color: '#e94560', type: 'dmsg-connection', live: true });
      connected.add(e.from); connected.add(e.to);
      continue;
    }
    if (!edgeTypeVisible(e.type)) { continue; }
    if (!visibleNode.has(e.from) || !visibleNode.has(e.to)) { continue; }
    const c = e.isLocal || e.isLocalOnly ? LOCAL_EDGE_COLOR : (colors[e.type] || '#9aa0a6');
    links.push({ source: e.from, target: e.to, color: c, type: e.type, live: e.live !== false });
    connected.add(e.from); connected.add(e.to);
  }
  // Parity with the Flat view: filtering a transport type hides EDGES, not
  // visors (Flat keeps every node and just toggles edge visibility). So keep
  // every status-visible node here too. `connectedIds` (nodes with ≥1 visible
  // edge) is used ONLY to frame the view via fitViewByNodeIds — isolated nodes
  // get pulled into an outer ring by gravity (like Flat's orphan ring) instead
  // of being dropped, and no longer stretch the fit so the cluster stays large.
  const connectedIds = nodes.filter(n => connected.has(n.id)).map(n => n.id);

  const mode = cosmosGroupMode();
  if (mode) {
    // Connection count per node drives the in-circle placement: best-connected
    // toward the centre, like the flat view. Counts both transports AND dmsg
    // connections (so a dmsg server, which links to many clients, sits near its
    // country's centre alongside high-transport visors). Only route overlays —
    // which aren't connectivity — are excluded.
    const degree = new Map<string, number>();
    for (const e of edgesRaw) {
      if (e.type === 'route') { continue; }
      degree.set(e.from, (degree.get(e.from) || 0) + 1);
      degree.set(e.to, (degree.get(e.to) || 0) + 1);
    }
    const { pos, color, groups } = computeGroupedLayout(nodes.map(n => n.id), mode, (id) => degree.get(id) || 0);
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
    for (const n of nodes) {
      const p = pos.get(n.id);
      if (p) { const s = toSpace(p.x, p.y); n.x = s.x; n.y = s.y; }
      // Recolor non-local nodes (the local visor stays cyan so "YOU" is findable;
      // DMSG servers keep their distinct purple so they stay identifiable inside
      // the country bubble).
      if (!n.isLocal && n.status !== 'dmsg') {
        // Service/route recolor takes precedence over the group color (parity
        // with Flat, where getNodeColor's service/route branch wins regardless
        // of grouping); otherwise colour by group so the clusters read.
        const svcRouteColor = serviceRouteColorOf(n.id);
        if (svcRouteColor) { n.color = svcRouteColor; }
        else { const col = color.get(n.id); if (col) { n.color = col; } }
      }
      if (!n.isLocal) {
        // Gap #4: node positions get compressed by `scale`, but the dot pixel
        // size is fixed (scaleNodesOnZoom:false) — so in a dense group the dots
        // overlap and bleed past the (also-scaled) boundary ring. Shrink the dot
        // with the layout so the Fibonacci fill stays inside its bubble.
        n.size = Math.max(1.5, n.size * scale);
      }
    }
    // Group boundary circles in the SAME normalized space (drawn on the overlay).
    boundaryCircles = groups.map(g => {
      const s = toSpace(g.cx, g.cy);
      return { x: s.x, y: s.y, r: g.r * scale, color: g.color, label: g.label, flag: g.flag };
    });
  } else {
    boundaryCircles = [];
  }
  return { nodes, links, grouped: !!mode, connectedIds };
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
    // GPU force simulation — light gravity + repulsion settle a ~20k-edge
    // hairball into a readable ball in a couple seconds, off the main thread.
    spaceSize: COSMOS_SPACE,
    simulation: {
      gravity: 0.9,
      repulsion: 0.5,
      linkSpring: 1.3,
      linkDistance: 3,
      friction: 0.85,
      decay: 2000,
      // Re-frame once the layout settles (the initial fit is too tight while
      // every node still sits clustered near the center).
      onEnd: () => { if (graph) { graph.fitView(400, 0.1); } },
    },
    events: {
      onClick: (node?: CosmosNode) => {
        if (node && node.id) {
          // Persistent selection (parity with Flat's click-to-isolate): keep this
          // node's neighborhood highlighted after the pointer leaves, until another
          // node or empty space is clicked.
          selectedNodeId = node.id;
          handleGraphNodeClick(node.id);
          if (graph) { graph.selectNodeById(node.id, true); }
        } else {
          selectedNodeId = null;
          if (graph) { graph.unselectNodes(); }
        }
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
        // Restore the persistent click-selection instead of clearing everything.
        if (graph) {
          if (selectedNodeId) { graph.selectNodeById(selectedNodeId, true); } else { graph.unselectNodes(); }
        }
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
  const cosmosContainer = document.getElementById('cosmos-container');
  if (cosmosContainer) { cosmosContainer.style.display = 'none'; }
}

let settleTimer: ReturnType<typeof setTimeout> | null = null;

// updateCosmosData reprojects + repushes the data (on refresh or filter change),
// runs the GPU simulation briefly, then pauses + fits. The force sim never fully
// "cools" on a ~20k-edge graph, so rather than wait for it we let it run a few
// seconds (enough to untangle the hairball), then pause to free the GPU and
// frame the result.
export function updateCosmosData(): void {
  if (!active) { return; }
  const g = ensureGraph();
  if (!g) { return; }
  const { nodes, links, grouped, connectedIds } = buildData();
  if (settleTimer) { clearTimeout(settleTimer); settleTimer = null; }
  if (grouped) {
    // Fixed group-packed positions. cosmos reads node x/y as the initial point
    // positions; disableSimulation then keeps them there (no force layout) while
    // leaving the render loop alive for pan/zoom. runSimulation stays default
    // (true) — that's what makes cosmos initialise FROM the provided x/y; passing
    // false skips that init and leaves points at cosmos' random grid.
    g.setConfig({ disableSimulation: true });
    g.setData(nodes, links);
    fitFrame(g, connectedIds);
    startBoundaries();
    return;
  }
  stopBoundaries();
  g.setConfig({ disableSimulation: false });
  g.setData(nodes, links);
  settleTimer = setTimeout(() => {
    if (graph && active) { graph.pause(); fitFrame(graph, connectedIds); }
  }, 5000);
}

// fitFrame frames the connected subset when there is one (so a ring of isolated
// visors — kept for Flat-parity — doesn't shrink the main cluster), else all nodes.
function fitFrame(g: Graph<CosmosNode, CosmosLink>, connectedIds: string[]): void {
  if (connectedIds.length > 0) { g.fitViewByNodeIds(connectedIds, 500, 0.1); } else { g.fitView(500, 0.1); }
}
