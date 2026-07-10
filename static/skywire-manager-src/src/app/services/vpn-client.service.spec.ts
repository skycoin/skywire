import { AppState, CheckPkResults, VpnClientService, VpnServiceStates } from './vpn-client.service';

// VpnClientService is large and stateful, but two pieces carry the interesting
// logic and are pure enough to test in isolation: checkNewPk (the "can I switch
// server" state machine) and processAppData (raw visor app data → typed state).
// We build the service with stub dependencies and drive its private state
// through casts — the constructor itself only sets initial fields.
describe('VpnClientService', () => {
  let vpnSavedDataService: { currentServer: { pk: string } | null };
  let translateService: { instant: (key: string) => string };
  let service: VpnClientService;

  const build = () =>
    new VpnClientService(
      {} as any, // apiService
      {} as any, // appsService
      {} as any, // router
      vpnSavedDataService as any,
      {} as any, // http
      {} as any, // snackbarService
      translateService as any,
    );

  beforeEach(() => {
    vpnSavedDataService = { currentServer: { pk: 'SERVER_PK' } };
    translateService = { instant: (key: string) => key };
    service = build();
  });

  describe('checkNewPk', () => {
    // checkNewPk is guarded by `working`, which the constructor leaves true;
    // clear it for the non-busy branches.
    const setState = (working: boolean, state: VpnServiceStates) => {
      (service as any).working = working;
      (service as any).lastServiceState = state;
    };

    it('returns Busy while the service is working', () => {
      setState(true, VpnServiceStates.Off);
      expect(service.checkNewPk('ANY')).toBe(CheckPkResults.Busy);
    });

    it('returns SamePkRunning when running against the current server', () => {
      setState(false, VpnServiceStates.Running);
      expect(service.checkNewPk('SERVER_PK')).toBe(CheckPkResults.SamePkRunning);
    });

    it('returns MustStop when running against a different server', () => {
      setState(false, VpnServiceStates.Running);
      expect(service.checkNewPk('OTHER_PK')).toBe(CheckPkResults.MustStop);
    });

    it('returns SamePkStopped when stopped on the already-selected server', () => {
      setState(false, VpnServiceStates.Off);
      expect(service.checkNewPk('SERVER_PK')).toBe(CheckPkResults.SamePkStopped);
    });

    it('returns Ok when stopped and a new server is selected', () => {
      setState(false, VpnServiceStates.Off);
      expect(service.checkNewPk('OTHER_PK')).toBe(CheckPkResults.Ok);
    });
  });

  describe('processAppData', () => {
    const process = (appData: any) => (service as any).processAppData(appData);

    it('treats status 0 (stopped) as not running', () => {
      const data = process({ status: 0, detailed_status: '' });
      expect(data.running).toBeFalse();
      expect(data.appState).toBe(AppState.Stopped);
    });

    it('maps a running app with detailed_status Running', () => {
      const data = process({ status: 1, detailed_status: AppState.Running });
      expect(data.running).toBeTrue();
      expect(data.appState).toBe(AppState.Running);
    });

    it('maps status 3 to Connecting', () => {
      const data = process({ status: 3, detailed_status: '' });
      expect(data.running).toBeTrue();
      expect(data.appState).toBe(AppState.Connecting);
    });

    it('captures the error message from a status 2 (error) app', () => {
      const data = process({ status: 2, detailed_status: 'handshake failed' });
      expect(data.running).toBeFalse();
      expect(data.lastErrorMsg).toBe('handshake failed');
    });

    it('falls back to a translated message when a status 2 app has no detail', () => {
      const data = process({ status: 2, detailed_status: '' });
      expect(data.lastErrorMsg).toBe('vpn.status-page.unknown-error');
    });

    it('parses server pk, killswitch and dns from the app args', () => {
      // -srv/-dns take the following arg; -killswitch is a single combined token.
      const data = process({
        status: 1,
        detailed_status: AppState.Running,
        connection_duration: 120,
        args: ['-srv', 'ARG_PK', '-killswitch=true', '-dns', '1.1.1.1'],
      });
      expect(data.serverPk).toBe('ARG_PK');
      expect(data.killswitch).toBeTrue();
      expect(data.dns).toBe('1.1.1.1');
      expect(data.connectionDuration).toBe(120);
    });

    it('defaults killswitch to false when the arg is absent', () => {
      const data = process({ status: 1, detailed_status: AppState.Running, args: ['-srv', 'ARG_PK'] });
      expect(data.killswitch).toBeFalse();
    });
  });
});
