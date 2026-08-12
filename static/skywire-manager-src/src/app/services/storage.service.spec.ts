import { StorageService, LabeledElementTypes } from './storage.service';

// StorageService has no Angular dependencies — it wraps the browser's
// localStorage, which karma provides for real — so we instantiate it directly
// and clear storage around every test for isolation. initialize() binds the
// service to a hypervisor PK (used as the key prefix) and runs the legacy
// migration, so every test calls it.
describe('StorageService', () => {
  let service: StorageService;
  const HV = 'test-hv-pk';

  beforeEach(() => {
    localStorage.clear();
    service = new StorageService();
    service.initialize(HV);
  });

  afterEach(() => localStorage.clear());

  describe('refresh time', () => {
    it('defaults to 10 seconds when nothing is saved', () => {
      expect(service.getRefreshTime()).toBe(10);
    });

    it('persists an updated refresh time and replays it to a new instance', (done) => {
      const emissions: number[] = [];
      service.getRefreshTimeObservable().subscribe((v) => {
        emissions.push(v);
        if (emissions.length === 2) {
          expect(emissions).toEqual([10, 25]);
          expect(service.getRefreshTime()).toBe(25);

          // A freshly initialized service reads the persisted value.
          const reopened = new StorageService();
          reopened.initialize(HV);
          expect(reopened.getRefreshTime()).toBe(25);
          done();
        }
      });

      service.setRefreshTime(25);
    });
  });

  describe('hypervisor-scoped storage', () => {
    it('prefixes keys with the hypervisor PK and isolates other hypervisors', () => {
      service.setDataForHv('color', 'blue');

      expect(service.getDataForHv('color')).toBe('blue');
      expect(localStorage.getItem(HV + 'color')).toBe('blue');

      const other = new StorageService();
      other.initialize('other-hv');
      expect(other.getDataForHv('color')).toBeNull();
    });
  });

  describe('local nodes visibility', () => {
    it('marks nodes visible, then hidden, updating the set and persistent storage', () => {
      service.includeVisibleLocalNodes(['pk1', 'pk2'], ['1.1.1.1', '2.2.2.2']);
      expect(service.getSavedVisibleLocalNodes().has('pk1')).toBeTrue();
      expect(service.getSavedVisibleLocalNodes().has('pk2')).toBeTrue();
      expect(service.getSavedLocalNodes().length).toBe(2);

      service.setLocalNodesAsHidden(['pk1'], ['1.1.1.1']);
      expect(service.getSavedVisibleLocalNodes().has('pk1')).toBeFalse();
      expect(service.getSavedVisibleLocalNodes().has('pk2')).toBeTrue();

      const stored = service.getSavedLocalNodes().find((n) => n.publicKey === 'pk1');
      expect(stored?.hidden).toBeTrue();
    });

    it('throws when the public-key and ip arrays differ in length', () => {
      expect(() => service.includeVisibleLocalNodes(['pk1'], [])).toThrowError('Invalid params');
    });
  });

  describe('labels', () => {
    it('saves, updates and removes a label', () => {
      service.saveLabel('id1', 'First', LabeledElementTypes.Node);
      expect(service.getLabelInfo('id1')!.label).toBe('First');

      service.saveLabel('id1', 'Renamed', LabeledElementTypes.Node);
      expect(service.getLabelInfo('id1')!.label).toBe('Renamed');

      service.saveLabel('id1', '', LabeledElementTypes.Node);
      expect(service.getLabelInfo('id1')).toBeNull();
    });

    it('returns null for an unknown id', () => {
      expect(service.getLabelInfo('unknown')).toBeNull();
    });
  });

  describe('getDefaultLabel', () => {
    it('prefers hostname, then ip, then public key', () => {
      expect(service.getDefaultLabel({ hostname: 'raspi', ip: '10.0.0.1', localPk: 'PK' } as any)).toBe('raspi');
      expect(service.getDefaultLabel({ hostname: '', ip: '10.0.0.1', localPk: 'PK' } as any)).toBe('10.0.0.1');
      expect(service.getDefaultLabel({ hostname: '', ip: '', localPk: 'PK' } as any)).toBe('PK');
    });

    it('returns an empty string for a null node', () => {
      expect(service.getDefaultLabel(null)).toBe('');
    });
  });

  describe('legacy migration', () => {
    it('migrates a pre-hypervisor refresh time into hypervisor-scoped storage', () => {
      localStorage.clear();
      localStorage.setItem('refreshSeconds', '42'); // legacy, non-HV key

      const migrated = new StorageService();
      migrated.initialize(HV);

      expect(migrated.getRefreshTime()).toBe(42);
      expect(localStorage.getItem('refreshSeconds')).toBeNull(); // legacy key consumed
    });
  });
});
