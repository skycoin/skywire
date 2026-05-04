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
 *   1 Rewards
 *   2 Resources
 *   3 Transports
 *   4 Network
 *   5 Network Visualizer
 *   6 Services Health
 *   7 Uptime
 *   8 Settings
 */
export const HOME_TAB_INDEX = {
  visorList: 0,
  rewards: 1,
  resources: 2,
  transports: 3,
  network: 4,
  tpviz: 5,
  servicesHealth: 6,
  uptime: 7,
  settings: 8,
};

export function homeTabsData(): TabButtonData[] {
  return [
    // --- local: this hypervisor ---
    { icon: 'view_headline', label: 'nodes.title', linkParts: ['/nodes'], group: 'local' },
    { icon: 'monetization_on', label: 'nodes.rewards-title', linkParts: ['/nodes', 'rewards'], group: 'local' },
    { icon: 'memory', label: 'nodes.resources-title', linkParts: ['/nodes', 'resources'], group: 'local' },
    // --- network-wide ---
    { icon: 'swap_horiz', label: 'nodes.transports-title', linkParts: ['/nodes', 'transports'], group: 'network' },
    { icon: 'public', label: 'nodes.network-title', linkParts: ['/nodes', 'network'], group: 'network' },
    { icon: 'bubble_chart', label: 'node.details.tpviz.title', linkParts: [], externalUrl: '/tp-viz/', group: 'network' },
    { icon: 'check_circle', label: 'nodes.services-health-title', linkParts: ['/nodes', 'services-health'], group: 'network' },
    { icon: 'schedule', label: 'nodes.uptime-title', linkParts: ['/nodes', 'uptime'], group: 'network' },
    // --- meta ---
    { icon: 'settings', label: 'settings.title', linkParts: ['/settings'] },
  ];
}
