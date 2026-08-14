// The Go/wasm WebGL view — an alternative to cosmos-graph.ts, selectable from
// the view toggle so the two can be compared on the same data.
//
// The engine is third_party/0magnet/cosmos-go (a Go port of cosmos.gl 2.6.3,
// MIT) compiled to wasm, and it owns the group-boundary overlay too, so the
// only per-frame work on this side is none: everything below runs on a user
// action or a data refresh.
//
// The JavaScript view is built on @cosmograph/cosmos 1.6.1, which is
// CC-BY-NC-4.0 — the sole non-permissive dependency in tpviz, bundled into
// legacy/bundle.js and embedded in the binary. When this view is shown to
// match, that dependency goes away with it.
//
// Data comes from cosmos-graph.ts's buildData, deliberately: a difference
// between the two views should be the engine, not the input.

import { buildData, currentBoundaries, CosmosNode } from './cosmos-graph';
import { handleGraphNodeClick } from './node-click';

// wasmBase resolves the module + its glue against where THIS bundle.js was
// loaded from, not the document. On the standalone /tp-viz/ page both are the
// same, but when the Angular HV UI mounts the bundle (base href '/', route
// #/nodes/visualizer via BundleMountComponent) a bare 'tpviz-gl.wasm' resolves
// to '/tpviz-gl.wasm' → 404, and tpviz-gl-exec.js never defines window.Go, so
// the view fails to load. The bundle is always injected as a <script
// src=".../tp-viz/bundle.js"> (id "tpviz-bundle-script" when embedded), so its
// resolved .src gives the correct '.../tp-viz/' prefix on every surface.
function wasmBase(): string {
  const s = (document.currentScript
    || document.getElementById('tpviz-bundle-script')
    || document.querySelector('script[src*="tp-viz/bundle.js"]')) as HTMLScriptElement | null;
  const src = s?.src || '';
  // strip query/hash, then the filename, leaving the trailing-slash base
  return src.replace(/[?#].*$/, '').replace(/[^/]*$/, '');
}
const WASM_BASE = wasmBase();
const WASM_MODULE = WASM_BASE + 'tpviz-gl.wasm';
const WASM_EXEC = WASM_BASE + 'tpviz-gl-exec.js';

let active = false;
let loading: Promise<boolean> | null = null;
let nodes: CosmosNode[] = [];
let indexOfId = new Map<string, number>();
let settleTimer: ReturnType<typeof setTimeout> | null = null;
let selectedNodeId: string | null = null;

function gl(): any { return (window as any).tpvizGL; }

// loadModule fetches and instantiates the wasm module the first time this view
// is opened, so a user who never selects it pays nothing for it.
function loadModule(): Promise<boolean> {
  if (loading) { return loading; }
  loading = new Promise<boolean>((resolve) => {
    if (gl()) { resolve(true); return; }
    // The module served at WASM_MODULE is the one wasm-visor blob, not a
    // separate tpviz-gl build. Run it in its "netview" role so its main()
    // installs only the WebGL view (globalThis.tpvizGL) and never boots a
    // visor — the same one-binary-many-roles trick as the websh terminal.
    // Must be set before go.run() executes the module's main().
    (window as any).__SKYWIRE_WASM_ROLE__ = 'netview';
    const script = document.createElement('script');
    script.src = WASM_EXEC;
    script.onerror = () => resolve(false);
    script.onload = () => {
      const GoCtor = (window as any).Go;
      if (!GoCtor) { resolve(false); return; }
      const go = new GoCtor();
      // TinyGo's glue expects a crypto shim under some hosts; harmless when
      // the standard-Go build is served instead.
      WebAssembly.instantiateStreaming(fetch(WASM_MODULE), go.importObject)
        .then((res: any) => {
          go.run(res.instance);
          // go.run returns once the module blocks in select{}; tpvizGL is set
          // by then, but poll briefly rather than assume the ordering.
          let tries = 0;
          const wait = () => {
            if (gl()) { resolve(true); return; }
            if (++tries > 100) { resolve(false); return; }
            setTimeout(wait, 20);
          };
          wait();
        })
        .catch(() => resolve(false));
    };
    document.head.appendChild(script);
  });
  return loading;
}

// onEvent receives clicks, hovers and simulation-end from the Go side. The
// wasm module works in point indices; the names the rest of the UI speaks are
// public keys, so this is where the two meet.
function onEvent(kind: string, index: number, x: number, y: number): void {
  const node = index >= 0 && index < nodes.length ? nodes[index] : null;
  switch (kind) {
    case 'click':
      if (node) { selectedNodeId = node.id; handleGraphNodeClick(node.id); }
      break;
    case 'bgclick':
      selectedNodeId = null;
      break;
    case 'over':
      if (node) { showTooltip(node, x, y); }
      break;
    case 'out':
      hideTooltip();
      // Restore the persistent click-selection rather than clearing it.
      gl()?.selectIdx(selectedNodeId !== null ? (indexOfId.get(selectedNodeId) ?? -1) : -1);
      break;
    default:
      break;
  }
}

let tooltipEl: HTMLDivElement | null = null;

function showTooltip(node: CosmosNode, clientX: number, clientY: number): void {
  if (!tooltipEl) {
    tooltipEl = document.createElement('div');
    tooltipEl.id = 'cosmosgo-tooltip';
    tooltipEl.style.cssText =
      'position:fixed;z-index:10000;pointer-events:none;background:rgba(22,33,62,0.97);' +
      'border:1px solid #00d9a5;border-radius:4px;padding:6px 9px;color:#e6e6e6;' +
      'font:12px system-ui,sans-serif;max-width:320px;white-space:pre-line;display:none';
    document.body.appendChild(tooltipEl);
  }
  tooltipEl.textContent = node.title || node.id;
  tooltipEl.style.display = 'block';
  tooltipEl.style.left = Math.min(clientX + 14, window.innerWidth - 330) + 'px';
  tooltipEl.style.top = (clientY + 14) + 'px';
}

function hideTooltip(): void {
  if (tooltipEl) { tooltipEl.style.display = 'none'; }
}

// toPayload converts buildData's objects into the flat arrays cosmos 2.x
// takes. This is the shape difference between the two engine versions: v1 took
// accessor callbacks over node objects, v2 takes typed arrays.
function toPayload(data: ReturnType<typeof buildData>) {
  const n = data.nodes.length;
  const positions = new Float32Array(n * 2);
  const pointSizes = new Float32Array(n);
  const pointColors: string[] = new Array(n);
  let havePositions = false;

  indexOfId = new Map<string, number>();
  for (let i = 0; i < n; i++) {
    const node = data.nodes[i];
    indexOfId.set(node.id, i);
    pointColors[i] = node.color;
    pointSizes[i] = node.size;
    if (typeof node.x === 'number' && typeof node.y === 'number') {
      positions[i * 2] = node.x; positions[i * 2 + 1] = node.y; havePositions = true;
    }
  }

  const links = new Float32Array(data.links.length * 2);
  const linkWidths = new Float32Array(data.links.length);
  const linkColors: string[] = new Array(data.links.length);
  let written = 0;
  for (const l of data.links) {
    const s = indexOfId.get(l.source); const t = indexOfId.get(l.target);
    if (s === undefined || t === undefined) { continue; }
    links[written * 2] = s; links[written * 2 + 1] = t;
    linkColors[written] = l.color;
    linkWidths[written] = l.live ? 1 : 0.5;
    written++;
  }

  return {
    positions: havePositions ? positions : new Float32Array(0),
    pointColors, pointSizes,
    links: links.subarray(0, written * 2),
    linkColors: linkColors.slice(0, written),
    linkWidths: linkWidths.subarray(0, written),
    grouped: data.grouped,
    boundaries: currentBoundaries(),
  };
}

function connectedIndices(ids: string[]): number[] {
  const out: number[] = [];
  for (const id of ids) { const i = indexOfId.get(id); if (i !== undefined) { out.push(i); } }
  return out;
}

export function isCosmosGoActive(): boolean { return active; }

export function showCosmosGo(): void {
  const container = document.getElementById('cosmosgo-container');
  if (container) { container.style.display = 'block'; }
  active = true;
  loadModule().then((ok) => {
    if (!ok) {
      if (container) {
        container.innerHTML =
          '<div style="color:#e94560;padding:24px;font:13px system-ui">' +
          'the Go WebGL view failed to load (tpviz-gl.wasm)</div>';
      }
      return;
    }
    if (!active) { return; }
    if (!gl().init('cosmosgo-container', onEvent)) { return; }
    updateCosmosGoData();
  });
}

export function hideCosmosGo(): void {
  active = false;
  if (settleTimer) { clearTimeout(settleTimer); settleTimer = null; }
  hideTooltip();
  gl()?.pause();
  const container = document.getElementById('cosmosgo-container');
  if (container) { container.style.display = 'none'; }
}

export function cosmosGoTeardown(): void {
  active = false;
  if (settleTimer) { clearTimeout(settleTimer); settleTimer = null; }
  hideTooltip();
  gl()?.teardown();
}

export function cosmosGoPause(): void { if (active) { gl()?.pause(); } }
export function cosmosGoResume(): void { if (active) { gl()?.resume(); } }
export function cosmosGoFit(): void { gl()?.fit(); }
export function cosmosGoZoomBy(factor: number): void { gl()?.zoomBy(factor); }

export function cosmosGoFocusNode(pk: string): void {
  const i = indexOfId.get(pk);
  if (i !== undefined) { gl()?.focusIndex(i); }
}

export function cosmosGoSetPhysics(on: boolean): void { gl()?.setPhysics(on); }

// updateCosmosGoData mirrors updateCosmosData's policy exactly — including
// letting the force simulation run a few seconds on a ~20k-edge graph and then
// pausing rather than waiting for a cool that never comes — so that what is
// being compared is the engine and not the settling strategy.
export function updateCosmosGoData(): void {
  if (!active || !gl()) { return; }
  const data = buildData();
  if (settleTimer) { clearTimeout(settleTimer); settleTimer = null; }
  gl().setData(toPayload(data));
  const connected = connectedIndices(data.connectedIds);
  if (data.grouped) {
    gl().fitIndices(connected);
    return;
  }
  settleTimer = setTimeout(() => {
    if (!active) { return; }
    gl().pause();
    gl().fitIndices(connected);
  }, 5000);
}
