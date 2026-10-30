import { appStatusDot, appStatusTone, portLabel } from './app-status';

describe('app status colours', () => {
  it('uses grey for stopped and red only for failed', () => {
    expect(appStatusDot(0)).toBe('dot-grey');
    expect(appStatusDot(1)).toBe('dot-green');
    expect(appStatusDot(2)).toBe('dot-red');
    expect(appStatusDot(3)).toBe('dot-yellow');
  });

  it('treats an unknown status as stopped', () => {
    expect(appStatusTone(7)).toBe('stopped');
  });
});

describe('portLabel', () => {
  it('shows a dash for a service with no port', () => {
    expect(portLabel(0)).toBe('—');
    expect(portLabel(undefined)).toBe('—');
    expect(portLabel(44)).toBe('44');
  });
});
