import { TabButtonData } from '../layout/top-bar/top-bar.component';

/**
 * Data for configuring the tab-bar shown in the header of the vpn client pages.
 */
export let vpnTabsData: TabButtonData[] = [
  {
    icon: 'power_settings_new',
    label: 'vpn.start',
    linkParts: ['/vpn'],
  },
  {
    icon: 'list',
    label: 'vpn.servers',
    linkParts: ['/vpn/servers'],
  },
  {
    icon: 'flag',
    label: 'vpn.countries',
    linkParts: ['/vpn'],
  },
  {
    icon: 'settings',
    label: 'vpn.settings',
    linkParts: ['/vpn'],
  },
];
