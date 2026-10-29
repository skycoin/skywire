import { Node } from '../../../../../app.datatypes';
import { overviewAlerts, sameRelease } from './visor-overview.component';

// The overview's alerts come only from the visor summary, so each rule can be
// checked against a hand-made node.
describe('visor overview alerts', () => {
  const base = (): Node => ({
    localPk: '02aa', label: 'x', version: 'v1.3.103', configVersion: 'v1.3.103',
    rewardsAddress: 'xpub', secondsOnline: 3600,
    health: { servicesHealth: 'healthy', uptimeTrackerHealth: 'healthy', autoconnectHealth: 'healthy', transportabilityHealth: 'healthy' },
    apps: [], transports: [{ type: 'stcpr', initiator: true }], dmsgServers: [{}],
  } as unknown as Node);
  const texts = (n: Node) => overviewAlerts(n).map(a => a.text);

  it('reports nothing for a healthy visor', () => {
    expect(overviewAlerts(base())).toEqual([]);
  });

  it('warns about a check still connecting only after the grace time', () => {
    const n = base();
    n.health!.uptimeTrackerHealth = 'connecting';
    expect(overviewAlerts(n)[0].level).toBe('warn');
    n.secondsOnline = 60;
    expect(overviewAlerts(n)[0].level).toBe('info');
  });

  it('names failed apps and autostart apps that are not running', () => {
    const n = base();
    n.apps = [
      { name: 'vpn-client', status: 2, autostart: false },
      { name: 'skychat', status: 0, autostart: true },
      { name: 'skysocks', status: 1, autostart: true },
    ] as any;
    const alerts = overviewAlerts(n);
    expect(alerts.map(a => a.params?.['app'])).toEqual(['vpn-client', 'skychat']);
    expect(alerts[1].text).toBe('node.overview.alert-app-stopped');
  });

  it('says why a stopped autostart app is not running when it reports a reason', () => {
    const n = base();
    const why = 'port 25 is served by the host\'s mail server (`serve add 25`)';
    n.apps = [{ name: 'skymail', status: 0, autostart: true, detailedStatus: why }] as any;
    const alerts = overviewAlerts(n);
    expect(alerts.length).toBe(1);
    expect(alerts[0].text).toBe('node.overview.alert-app-stopped-why');
    expect(alerts[0].params).toEqual({ app: 'skymail', why: why });
  });

  it('flags a missing reward address, no transports and no dmsg', () => {
    const n = base();
    n.rewardsAddress = '';
    n.transports = [];
    n.dmsgServers = [];
    expect(texts(n)).toEqual(jasmine.arrayContaining([
      'node.overview.alert-no-reward', 'node.overview.alert-no-transports', 'node.overview.alert-no-dmsg',
    ]));
  });
});

describe('visor overview config version', () => {
  it('treats a commit suffix as the same release', () => {
    expect(sameRelease('v1.3.103-122-g7845bbc81-7845bbc81358', 'v1.3.103-122-g7845bbc81')).toBeTrue();
    expect(sameRelease('develop-latest-tinygo-cores-7845bbc81358', 'develop-latest-tinygo-cores')).toBeTrue();
  });

  it('flags a config from another release', () => {
    expect(sameRelease('v1.3.104', 'v1.3.99')).toBeFalse();
    expect(sameRelease('develop-a', 'develop-b')).toBeFalse();
  });
});
