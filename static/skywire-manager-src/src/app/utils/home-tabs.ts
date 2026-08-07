import { TabButtonData } from '../components/layout/top-bar/top-bar.component';

/**
 * Single source of truth for the home top-bar's tab strip.
 * Used by every home-level page (node-list, network-view,
 * network-transports, multi-visor-resources, multi-visor-uptime,
 * services-health, settings) so the order and grouping stay
 * consistent.
 *
 * The `group` field drives the visual separator in the top-bar
 * between local-hypervisor tabs and network-wide tabs.
 *
 * Indices (kept in sync with selectedTabIndex on each page):
 *   0 Visor list
 *   1 Local Visor (redirects to the hypervisor's own visor detail)
 *   2 Rewards
 *   3 Resources
 *   4 Transports
 *   5 Network
 *   6 Network Visualizer
 *   7 Services Health
 *   8 Uptime
 *   9 Settings
 */
export const HOME_TAB_INDEX = {
  visorList: 0,
  localVisor: 1,
  rewards: 2,
  resources: 3,
  transports: 4,
  network: 5,
  tpviz: 6,
  servicesHealth: 7,
  uptime: 8,
  settings: 9,
};

/**
 * True when the UI is served BY a browser wasm hypervisor core (an in-tab
 * serverless visor or a standalone wasm hypervisor), rather than a native
 * visor-served build or a remote-viewer of a native hypervisor. hv-boot.js
 * sets window.__SKYWIRE_HV__ before Angular boots; .visor / .standalone are the
 * two modes whose /api requests are answered by the in-wasm core (see
 * SkywireHttpBackend). A native build leaves __SKYWIRE_HV__ unset, so this is
 * false there and the native hypervisor is unaffected. A remote viewer (.pk)
 * proxies to a real native hypervisor, so it is NOT treated as a wasm core.
 */
export function isWasmHvCore(): boolean {
  const cfg: any = (typeof window !== 'undefined' && (window as any).__SKYWIRE_HV__) || {};

  return !!(cfg.visor || cfg.standalone);
}

export function homeTabsData(): TabButtonData[] {
  // On a browser wasm hypervisor the Rewards (fleet reward system) and
  // Resources (host CPU/RAM/disk of the serving machine) tabs have no
  // meaningful backing — a browser tab has no host to measure and can't reach
  // the reward system — so they are hidden there. They stay visible on the
  // native hypervisor. `hidden` (not removal) keeps the tab array indices
  // stable so each page's fixed selectedTabIndex still lines up.
  const wasm = isWasmHvCore();

  return [
    // --- local: this hypervisor ---
    { icon: 'view_headline', label: 'nodes.title', linkParts: ['/nodes'], group: 'local' },
    // "Local Visor" — jump straight to THIS hypervisor's own visor detail page
    // (the /nodes/local route resolves the local PK and redirects). Surfaces the
    // serving visor's controls without hunting for it in the list; for a
    // serverless wasm tab this is the in-browser visor itself.
    { icon: 'router', label: 'nodes.local-visor-title', linkParts: ['/nodes', 'local'], group: 'local' },
    { icon: 'monetization_on', label: 'nodes.rewards-title', linkParts: ['/nodes', 'rewards'], group: 'local', hidden: wasm },
    { icon: 'memory', label: 'nodes.resources-title', linkParts: ['/nodes', 'resources'], group: 'local', hidden: wasm },
    // --- network-wide ---
    { icon: 'swap_horiz', label: 'nodes.transports-title', linkParts: ['/nodes', 'transports'], group: 'network' },
    { icon: 'public', label: 'nodes.network-title', linkParts: ['/nodes', 'network'], group: 'network' },
    { icon: 'bubble_chart', label: 'node.details.tpviz.title', linkParts: ['/nodes', 'visualizer'], group: 'network' },
    { icon: 'check_circle', label: 'nodes.services-health-title', linkParts: ['/nodes', 'services-health'], group: 'network' },
    { icon: 'schedule', label: 'nodes.uptime-title', linkParts: ['/nodes', 'uptime'], group: 'network' },
    // --- meta ---
    { icon: 'settings', label: 'settings.title', linkParts: ['/settings'] },
  ];
}
