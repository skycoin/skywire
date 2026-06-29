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

export function homeTabsData(): TabButtonData[] {
  return [
    // --- local: this hypervisor ---
    { icon: 'view_headline', label: 'nodes.title', linkParts: ['/nodes'], group: 'local' },
    // "Local Visor" — jump straight to THIS hypervisor's own visor detail page
    // (the /nodes/local route resolves the local PK and redirects). Surfaces the
    // serving visor's controls without hunting for it in the list; for a
    // serverless wasm tab this is the in-browser visor itself.
    { icon: 'router', label: 'nodes.local-visor-title', linkParts: ['/nodes', 'local'], group: 'local' },
    { icon: 'monetization_on', label: 'nodes.rewards-title', linkParts: ['/nodes', 'rewards'], group: 'local' },
    { icon: 'memory', label: 'nodes.resources-title', linkParts: ['/nodes', 'resources'], group: 'local' },
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
