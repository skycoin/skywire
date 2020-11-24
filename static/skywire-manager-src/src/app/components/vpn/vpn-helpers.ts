import { TabButtonData } from '../layout/top-bar/top-bar.component';

export class VpnHelpers {
  private static currentPk = '';

  static changeCurrentPk(pk: string): void {
    this.currentPk = pk;
  }

  /**
   * Data for configuring the tab-bar shown in the header of the vpn client pages.
   */
  static get vpnTabsData(): TabButtonData[] {
    return [
      {
        icon: 'power_settings_new',
        label: 'vpn.start',
        linkParts: ['/vpn', this.currentPk, 'status'],
      },
      {
        icon: 'list',
        label: 'vpn.servers',
        linkParts: ['/vpn', this.currentPk, 'servers'],
      },
      {
        icon: 'flag',
        label: 'vpn.countries',
        linkParts: ['/vpn', this.currentPk, 'status'],
      },
      {
        icon: 'settings',
        label: 'vpn.settings',
        linkParts: ['/vpn', this.currentPk, 'status'],
      },
    ];
  }

  /**
   * Gets the name of the translatable var that must be used for showing a latency value. This
   * allows to add the correct measure suffix.
   */
  static getLatencyValueString(latency: number): string {
    if (latency < 1000) {
      return 'time-in-ms';
    }

    return 'time-in-segs';
  }

  /**
   * Gets the string value to show in the UI a latency value with an adecuate number of decimals.
   * This function converts the value from ms to segs, if appropriate, so the value must be shown
   * using the var returned by getLatencyValueString.
   */
  static getPrintableLatency(latency: number): string {
    if (latency < 1000) {
      return latency + '';
    }

    return (latency / 1000).toFixed(1);
  }
}
