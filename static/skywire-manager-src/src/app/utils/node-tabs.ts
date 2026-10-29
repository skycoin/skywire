import { TabButtonData } from '../components/layout/top-bar/top-bar.component';

/**
 * The tabs of a visor's detail page, shared by the node page and the command
 * palette. A null key gives tabs without links.
 */
export function nodeTabsData(key: string | undefined, wasm: boolean): TabButtonData[] {
  const link = (seg: string) => key ? ['/nodes', key, seg] : null;
  let tabs: TabButtonData[] = [
    {
      icon: 'info',
      label: 'node.tabs.info',
      // Info is now a real tab on every screen size — the right
      // bar split-view that previously surfaced this content
      // has been removed. DMSG settings folded into Info as a
      // collapsible section to keep the tab row compact.
      linkParts: link('info'),
    },
    {
      icon: 'shuffle',
      label: 'node.tabs.routing',
      linkParts: link('routing'),
    },
    {
      icon: 'swap_horiz',
      label: 'node.tabs.transports',
      linkParts: link('transports'),
    },
    {
      icon: 'equalizer',
      label: 'node.tabs.bandwidth',
      linkParts: link('bandwidth'),
    },
    // DMSG + Reachability tabs were dropped: DMSG is folded into
    // Info as a collapsible section (the standalone /dmsg route
    // redirects to /info for back-compat) and Reachability moved
    // back to a future home — the per-node tab row was overflowing
    // and these were the lowest-priority entries. Their routes
    // still resolve so old bookmarks don't 404.
    {
      icon: 'schedule',
      label: 'node.tabs.uptime',
      linkParts: link('uptime'),
    },
    {
      // Apps tab: list view + sub-tabs for VPN / Skysocks. (Skychat
      // was pulled out to its own top-level Chat tab below.)
      icon: 'apps',
      label: 'node.tabs.apps',
      linkParts: link('apps'),
    },
    {
      // Chat tab: the visor's skychat client, promoted from an Apps
      // sub-tab to its own top-level tab (parity with the wasm visor's
      // ☰ Chat entry).
      icon: 'forum',
      label: 'node.tabs.chat',
      linkParts: link('chat'),
    },
    {
      // The visor's mailbox, the same one the desk's mail window shows.
      icon: 'mail',
      label: 'node.tabs.mail',
      linkParts: link('mail'),
    },
    {
      // The visor's contact book, which skychat also reads names from.
      icon: 'contacts',
      label: 'node.tabs.contacts',
      linkParts: link('contacts'),
    },
    {
      icon: 'monetization_on',
      label: 'node.tabs.rewards',
      linkParts: link('rewards'),
    },
    {
      icon: 'public',
      label: 'node.tabs.skynet',
      linkParts: link('skynet'),
    },
    {
      icon: 'language',
      label: 'node.tabs.web-proxy',
      linkParts: link('web-proxy'),
    },
    {
      // VPN tab: the full VPN UI, iframed in-place here (like the terminal
      // tab) instead of only launchable in a separate window.
      icon: 'vpn_lock',
      label: 'node.tabs.vpn',
      linkParts: link('vpn'),
    },
    {
      icon: 'memory',
      label: 'node.tabs.resources',
      linkParts: link('resources'),
    },
    {
      // 'terminal' was added to Material Icons in 2020 but the
      // font we ship locally predates that. 'code' is in the
      // bundled glyph map and reads as terminal-ish.
      icon: 'code',
      label: 'node.tabs.terminal',
      linkParts: link('terminal'),
    },
    {
      icon: 'account_balance_wallet',
      label: 'node.tabs.wallet',
      linkParts: link('wallet'),
    },
    {
      icon: 'description',
      label: 'node.tabs.logs',
      linkParts: link('logs'),
    },
    {
      icon: 'tune',
      label: 'node.tabs.settings',
      linkParts: link('settings'),
    },
  ];

  // A browser/wasm visor can't back several tabs with anything usable, so
  // drop them from the tab row (the flagship in-browser UI should show only
  // what actually works in the tab runtime):
  //   bandwidth / uptime / rewards / web-proxy — no host data source (404).
  //   resources — host CPU/mem/disk/net; a tab has none (renders all-zeros).
  // The terminal tab stays: the tab's visor hosts a websh session, and the
  // page's WebSocket reaches it through vnet.webSocket.
  //   settings  — the desk's own settings window edits the tab's conf, and
  //               knows which variables the desk sets on every load.
  // The wallet tab IS shown on wasm: the browser-tab visor serves /wallet/
  // (client-side wallets, node/backend proxied over dmsg) just like the
  // native HV-served wallet — the daemon-instance controls simply hide there.
  // The host-only features (VPN, real skysocks, port forwarding)
  // are the invitation to INSTALL the visor on the host. The node page picks
  // its selected tab by route, so a shorter array stays correct.
  if (wasm) {
    const wasmHiddenTabs = new Set(['bandwidth', 'uptime', 'rewards', 'web-proxy',
      'vpn', 'resources', 'settings']);
    tabs = tabs.filter(t => {
      const seg = t.label.replace('node.tabs.', '');

      return !wasmHiddenTabs.has(seg);
    });
    // The host bridge page this visor serves beside the dashboard: it hands
    // out the bridge for the user's computer and connects to it.
    tabs.push({
      icon: 'settings_ethernet',
      label: 'node.tabs.host-bridge',
      linkParts: null,
      externalUrl: 'hostbridge/',
    });
  }

  return tabs;
}
