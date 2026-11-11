import { ThemeService } from './theme.service';

describe('ThemeService', () => {
  let prefersLight: boolean;
  let mediaListeners: (() => void)[];
  let services: ThemeService[];

  function create(): ThemeService {
    const s = new ThemeService();
    services.push(s);

    return s;
  }

  function attr(): string | null {
    return document.documentElement.getAttribute('data-theme');
  }

  beforeEach(() => {
    localStorage.removeItem(ThemeService.storageKey);
    prefersLight = false;
    mediaListeners = [];
    services = [];
    spyOn(window, 'matchMedia').and.callFake(() => ({
      get matches() {
        return prefersLight;
      },
      addEventListener: (_: string, fn: () => void) => mediaListeners.push(fn),
      removeEventListener: () => {},
    } as unknown as MediaQueryList));
  });

  afterEach(() => {
    services.forEach(s => s.ngOnDestroy());
    localStorage.removeItem(ThemeService.storageKey);
    document.documentElement.removeAttribute('data-theme');
  });

  it('follows the system by default', () => {
    const s = create();
    expect(s.choice).toBe('system');
    expect(attr()).toBe('dark');

    prefersLight = true;
    mediaListeners.forEach(fn => fn());
    expect(s.theme).toBe('light');
    expect(attr()).toBe('light');
  });

  it('starts light when the system prefers light', () => {
    prefersLight = true;
    expect(create().theme).toBe('light');
    expect(attr()).toBe('light');
  });

  it('switches with the toggle and ignores the system while a theme is picked', () => {
    const s = create();
    s.setChoice('light');
    expect(attr()).toBe('light');
    s.setChoice('dark');
    prefersLight = true;
    mediaListeners.forEach(fn => fn());
    expect(attr()).toBe('dark');
    s.setChoice('system');
    expect(attr()).toBe('light');
  });

  it('persists the choice for the next page load', () => {
    create().setChoice('light');
    expect(localStorage.getItem(ThemeService.storageKey)).toBe('light');
    expect(create().choice).toBe('light');

    services[1].setChoice('system');
    expect(localStorage.getItem(ThemeService.storageKey)).toBeNull();
    expect(create().choice).toBe('system');
  });

  it('ignores an unknown saved value', () => {
    localStorage.setItem(ThemeService.storageKey, 'sepia');
    expect(create().choice).toBe('system');
  });

  it('still works when storage throws', () => {
    spyOn(Storage.prototype, 'getItem').and.throwError('blocked');
    spyOn(Storage.prototype, 'setItem').and.throwError('blocked');
    const s = create();
    expect(s.choice).toBe('system');
    s.setChoice('light');
    expect(s.choice).toBe('light');
    expect(attr()).toBe('light');
  });
});
