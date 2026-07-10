import { of } from 'rxjs';

import { NodeService, UpdaterStorageKeys } from './node.service';
import { ResponseTypes } from './api.service';

// NodeService is mostly a thin, typed facade over ApiService: each method maps
// to one endpoint + verb + body. We mock ApiService with spies and assert those
// mappings (the class of bug — wrong URL, verb or body shape — that a facade is
// most prone to), plus the few methods with real logic: the getNetworkView
// refresh flag and the localStorage-driven updater channel/body building.
// StorageService is unused by these methods, so a bare stub suffices.
describe('NodeService', () => {
  let apiService: jasmine.SpyObj<any>;
  let service: NodeService;

  beforeEach(() => {
    localStorage.clear();
    apiService = jasmine.createSpyObj('ApiService', ['get', 'post', 'put', 'delete', 'ws']);
    ['get', 'post', 'put', 'delete', 'ws'].forEach((m) => apiService[m].and.returnValue(of({})));
    service = new NodeService(apiService, {} as any);
  });

  afterEach(() => localStorage.clear());

  describe('endpoint wrappers', () => {
    interface WrapperCase {
      desc: string;
      run: () => void;
      verb: 'get' | 'post' | 'put' | 'delete';
      args: any[];
    }

    const cases: WrapperCase[] = [
      { desc: 'getRewardsAddress', run: () => service.getRewardsAddress('NK'), verb: 'get', args: ['visors/NK/reward'] },
      { desc: 'getRuntimeLogs', run: () => service.getRuntimeLogs('NK'), verb: 'get', args: ['visors/NK/runtime-logs'] },
      { desc: 'getRuntimeLogsSince', run: () => service.getRuntimeLogsSince('NK', 5), verb: 'get', args: ['visors/NK/runtime-logs?since=5'] },
      { desc: 'getRuntimeStats', run: () => service.getRuntimeStats('NK'), verb: 'get', args: ['visors/NK/runtime-stats'] },
      { desc: 'getHostStats', run: () => service.getHostStats('NK'), verb: 'get', args: ['visors/NK/host-stats'] },
      { desc: 'getProxies', run: () => service.getProxies('NK'), verb: 'get', args: ['visors/NK/proxies'] },
      { desc: 'getSkynetPorts', run: () => service.getSkynetPorts('NK'), verb: 'get', args: ['visors/NK/skynet-ports'] },
      { desc: 'getForwardedPorts', run: () => service.getForwardedPorts('NK'), verb: 'get', args: ['visors/NK/forwarded-ports'] },
      { desc: 'getSkynetForwards', run: () => service.getSkynetForwards('NK'), verb: 'get', args: ['visors/NK/skynet-forwards'] },
      { desc: 'checkIfUpdating', run: () => service.checkIfUpdating('NK'), verb: 'get', args: ['visors/NK/update/ws/running'] },
      { desc: 'setRewardsAddress', run: () => service.setRewardsAddress('NK', 'ADDR'), verb: 'put', args: ['visors/NK/reward', { reward_address: 'ADDR' }] },
      { desc: 'deleteRewardsAddress', run: () => service.deleteRewardsAddress('NK'), verb: 'delete', args: ['visors/NK/reward'] },
      { desc: 'setProxyEnabled', run: () => service.setProxyEnabled('NK', 'socks', true), verb: 'post', args: ['visors/NK/proxies/set', { kind: 'socks', enable: true }] },
      { desc: 'setProxyUpstream', run: () => service.setProxyUpstream('NK', 'socks', '1.2.3.4'), verb: 'post', args: ['visors/NK/proxies/upstream', { kind: 'socks', addr: '1.2.3.4' }] },
      { desc: 'registerSkynetPort', run: () => service.registerSkynetPort('NK', 80), verb: 'post', args: ['visors/NK/skynet-ports/register', { port: 80 }] },
      { desc: 'deregisterSkynetPort', run: () => service.deregisterSkynetPort('NK', 80), verb: 'post', args: ['visors/NK/skynet-ports/deregister', { port: 80 }] },
      { desc: 'registerForwardedPort', run: () => service.registerForwardedPort('NK', { p: 1 }), verb: 'post', args: ['visors/NK/forwarded-ports/register', { p: 1 }] },
      { desc: 'updateForwardedPort', run: () => service.updateForwardedPort('NK', { p: 1 }), verb: 'post', args: ['visors/NK/forwarded-ports/update', { p: 1 }] },
      {
        desc: 'skynetConnect',
        run: () => service.skynetConnect('NK', 'dmsg', 'RPK', 80, 90),
        verb: 'post',
        args: ['visors/NK/skynet-forwards/connect', { network: 'dmsg', remote_pk: 'RPK', remote_port: 80, local_port: 90 }],
      },
      { desc: 'skynetDisconnect', run: () => service.skynetDisconnect('NK', 'UUID'), verb: 'post', args: ['visors/NK/skynet-forwards/disconnect', { id: 'UUID' }] },
      { desc: 'shutdown', run: () => service.shutdown('NK'), verb: 'post', args: ['visors/NK/shutdown'] },
    ];

    cases.forEach((c) => {
      it(`${c.desc} calls apiService.${c.verb} with the expected URL and body`, () => {
        c.run();
        expect(apiService[c.verb]).toHaveBeenCalledWith(...c.args);
      });
    });
  });

  describe('getNetworkView', () => {
    it('omits the refresh query by default', () => {
      service.getNetworkView();
      expect(apiService.get).toHaveBeenCalledWith('network-view');
    });

    it('adds ?refresh=true when asked to refresh', () => {
      service.getNetworkView(true);
      expect(apiService.get).toHaveBeenCalledWith('network-view?refresh=true');
    });
  });

  describe('getRewardRules', () => {
    it('requests reward-rules as text', () => {
      service.getRewardRules();
      expect(apiService.get).toHaveBeenCalledWith(
        'reward-rules', jasmine.objectContaining({ responseType: ResponseTypes.Text }));
    });
  });

  describe('checkUpdate', () => {
    it('uses the stable channel by default', () => {
      service.checkUpdate('NK');
      expect(apiService.get).toHaveBeenCalledWith('visors/NK/update/available/stable');
    });

    it('uses a custom channel saved in localStorage', () => {
      localStorage.setItem(UpdaterStorageKeys.Channel, 'beta');
      service.checkUpdate('NK');
      expect(apiService.get).toHaveBeenCalledWith('visors/NK/update/available/beta');
    });
  });

  describe('update', () => {
    it('sends only the stable channel when no custom settings are stored', () => {
      service.update('NK');
      expect(apiService.ws).toHaveBeenCalledWith('visors/NK/update/ws', { channel: 'stable' });
    });

    it('builds the body from stored custom updater settings', () => {
      localStorage.setItem(UpdaterStorageKeys.UseCustomSettings, 'true');
      localStorage.setItem(UpdaterStorageKeys.Channel, 'testing');
      localStorage.setItem(UpdaterStorageKeys.Version, '1.2.3');
      localStorage.setItem(UpdaterStorageKeys.ArchiveURL, 'http://a');
      localStorage.setItem(UpdaterStorageKeys.ChecksumsURL, 'http://c');

      service.update('NK');

      expect(apiService.ws).toHaveBeenCalledWith('visors/NK/update/ws', {
        channel: 'testing',
        version: '1.2.3',
        archive_url: 'http://a',
        checksums_url: 'http://c',
      });
    });
  });
});
