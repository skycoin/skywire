// persist.ts — filter/view persistence + deep-linkable view mode.
//
// The sidebar filters and the active view (Globe / Flat=legacy / WebGL) survive a
// reload via localStorage, the view is also deep-linkable via a `?view=` param,
// and a reset clears everything back to defaults (view → WebGL). Works both
// standalone (/tp-viz/) and embedded (host passes opts through mount()).

export type ViewMode = 'globe' | 'flat' | 'cosmos' | 'cosmosgo';

const FILTER_KEY = 'tpviz.filters.v1';
const VIEW_KEY = 'tpviz.view.v1';

// Control ids whose state is persisted. Checkboxes save .checked; selects/inputs
// save .value. Missing ids are skipped (older/newer markup tolerated).
const CONTROL_IDS = [
  'show-stcpr', 'show-sudph', 'show-stcp', 'show-squicr', 'show-swtr', 'show-swsr',
  'show-webrtc', 'show-dmsg', 'show-dmsg-servers', 'show-online', 'show-offline',
  'show-unknown', 'show-services', 'show-flags', 'show-dataflow', 'show-routes',
  'toggle-tooltips', 'toggle-physics', 'cluster-country', 'cluster-ip',
  'show-voronoi-overlay', 'version-filter', 'search',
];

// Host (Angular tab / WinBox) options, set by mount(). view seeds the initial
// view; onViewChange lets the host reflect view switches into its own URL.
interface HostOpts { view?: string; onViewChange?: (v: ViewMode) => void }
let host: HostOpts = {};
export function setHostOpts(o: HostOpts | undefined): void { host = o || {}; }

export function normalizeView(v: string | null | undefined): ViewMode | null {
  switch ((v || '').toLowerCase()) {
    case 'globe': return 'globe';
    case 'flat': case 'legacy': return 'flat';
    case 'webgl': case 'cosmos': case 'gl': return 'cosmos';
    // The Go/wasm WebGL view, which runs alongside the JavaScript one
    // so the two can be compared.
    case 'webgl-go': case 'cosmosgo': case 'go': return 'cosmosgo';
    default: return null;
  }
}

function viewFromURL(): ViewMode | null {
  try {
    const search = normalizeView(new URLSearchParams(location.search).get('view'));
    if (search) { return search; }
    const hashQ = location.hash.includes('?') ? location.hash.split('?')[1] : '';
    return normalizeView(new URLSearchParams(hashQ).get('view'));
  } catch { return null; }
}

// getInitialView resolves the view to show on load: host opt → URL ?view= →
// localStorage → cosmos-go (Go/WebGL2) default. The @cosmograph/cosmos ('cosmos')
// view needs WebGL1 + OES_texture_float, which fails under software WebGL (e.g.
// Brave with HW accel off) — the Go port renders on plain WebGL2, so it's the
// safe default.
export function getInitialView(): ViewMode {
  const explicit = normalizeView(host.view) || viewFromURL() || normalizeView(safeGet(VIEW_KEY));
  if (explicit) { return explicit; }
  // Embedded in the browser (wasm) visor's HV page, the Go/WebGL2 view routinely
  // comes up BLANK — it fetches an ~11 MB wasm module and needs a working WebGL2
  // context, which the browser-visor page (software WebGL, a second wasm runtime,
  // the tab already busy) frequently can't give it, so the tour lands on an empty
  // canvas that looks broken. The Flat (vis-network, canvas-2D) view renders the
  // full graph with no GPU and no extra module, so default to it there. Standalone
  // tp-viz (no skywireVisor bridge on the page) keeps the WebGL-Go default; an
  // explicit host/URL/localStorage choice always wins over both.
  try { if ((globalThis as { skywireVisor?: unknown }).skywireVisor) { return 'flat'; } } catch { /* no bridge */ }
  return 'cosmosgo';
}

// saveView records the active view (localStorage) and reflects it to the URL:
// the host's onViewChange for embedded, or the standalone query string.
export function saveView(v: ViewMode): void {
  safeSet(VIEW_KEY, v);
  if (host.onViewChange) { host.onViewChange(v); return; }
  try {
    const u = new URL(location.href);
    u.searchParams.set('view', v);
    history.replaceState(history.state, '', u.toString());
  } catch { /* ignore */ }
}

export function saveFilters(): void {
  const checks: Record<string, boolean> = {};
  const vals: Record<string, string> = {};
  for (const id of CONTROL_IDS) {
    const el = document.getElementById(id) as HTMLInputElement | null;
    if (!el) { continue; }
    if (el.type === 'checkbox') { checks[id] = el.checked; } else { vals[id] = el.value; }
  }
  safeSet(FILTER_KEY, JSON.stringify({ checks, vals }));
}

// restoreFilters applies persisted control state to the DOM (call before the
// first render so the initial view honours the saved filters).
export function restoreFilters(): void {
  let s: any;
  try { s = JSON.parse(safeGet(FILTER_KEY) || 'null'); } catch { s = null; }
  if (!s) { return; }
  for (const id of CONTROL_IDS) {
    const el = document.getElementById(id) as HTMLInputElement | null;
    if (!el) { continue; }
    if (el.type === 'checkbox') {
      if (s.checks && id in s.checks) { el.checked = !!s.checks[id]; }
    } else if (s.vals && id in s.vals) { el.value = s.vals[id]; }
  }
}

// resetControls clears persisted state and returns every control to its markup
// default (checkboxes → defaultChecked, inputs → defaultValue).
export function resetControls(): void {
  try { localStorage.removeItem(FILTER_KEY); localStorage.removeItem(VIEW_KEY); } catch { /* ignore */ }
  for (const id of CONTROL_IDS) {
    const el = document.getElementById(id) as HTMLInputElement | null;
    if (!el) { continue; }
    if (el.type === 'checkbox') { el.checked = el.defaultChecked; } else { el.value = el.defaultValue; }
  }
}

function safeGet(k: string): string | null { try { return localStorage.getItem(k); } catch { return null; } }
function safeSet(k: string, v: string): void { try { localStorage.setItem(k, v); } catch { /* ignore */ } }
