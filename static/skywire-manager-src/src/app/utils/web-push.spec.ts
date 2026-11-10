import { PushEnv, pushHint, vapidKeyBytes, deviceLabel } from './web-push';

describe('web push helpers', () => {
  const ok: PushEnv = { secure: true, serviceWorker: true, pushManager: true, ios: false, standalone: false, host: 'localhost:8000' };

  it('has no hint where push works', () => {
    expect(pushHint(ok)).toBe('');
  });

  it('asks iOS to add the page to the Home Screen first', () => {
    expect(pushHint({ ...ok, ios: true, pushManager: false })).toContain('Home Screen');
    expect(pushHint({ ...ok, ios: true, standalone: true })).toBe('');
  });

  it('explains a LAN http page is not secure', () => {
    expect(pushHint({ ...ok, secure: false, host: '192.168.1.5:8000' })).toContain('http://192.168.1.5:8000');
  });

  it('says when the browser has no push', () => {
    expect(pushHint({ ...ok, pushManager: false })).toContain('does not support');
  });

  it('decodes an unpadded base64url key', () => {
    const k = vapidKeyBytes('BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8');
    expect(k.length).toBe(65);
    expect(k[0]).toBe(4);
    expect(k[64]).toBe(0x0f);
  });

  it('names devices', () => {
    expect(deviceLabel('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1'))
      .toBe('iPhone Safari');
    expect(deviceLabel('Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120.0 Mobile Safari/537.36')).toBe('Android Chrome');
    expect(deviceLabel('Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0')).toBe('Linux Firefox');
  });
});
