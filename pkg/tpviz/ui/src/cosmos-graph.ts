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
import { colors, LOCAL_EDGE_COLOR } from './constants';
import { handleGraphNodeClick } from './node-click';

interface CosmosNode {
  id: string;
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

let graph: Graph<CosmosNode, CosmosLink> | null = null;
let active = false;
let tooltip: HTMLDivElement | null = null;

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

// buildData projects the vis-network datasets into cosmos node/link arrays,
// applying the active type/status filters.
function buildData(): { nodes: CosmosNode[]; links: CosmosLink[] } {
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
  return { nodes: linkedNodes, links };
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
    renderLinks: true,
    curvedLinks: false,
    // Small pixel dots that don't grow on zoom — at ~1k nodes, big dots overlap
    // into solid blobs.
    scaleNodesOnZoom: false,
    fitViewOnInit: true,
    // GPU force simulation — light gravity + repulsion settle a ~20k-edge
    // hairball into a readable ball in a couple seconds, off the main thread.
    spaceSize: 4096,
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
  const { nodes, links } = buildData();
  g.setData(nodes, links);
  if (settleTimer) { clearTimeout(settleTimer); }
  settleTimer = setTimeout(() => {
    if (graph && active) { graph.pause(); graph.fitView(500, 0.1); }
  }, 5000);
}
