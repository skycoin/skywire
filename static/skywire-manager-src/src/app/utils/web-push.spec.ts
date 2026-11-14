import { PushEnv, pushHint, vapidKeyBytes, deviceLabel, crossOriginFrame, askDesk } from './web-push';

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

  it('tells a cross-origin frame from a top page and a same-origin frame', () => {
    const top: any = {};
    top.parent = top;
    expect(crossOriginFrame(top)).toBe(false);
    expect(crossOriginFrame({ parent: { location: { href: 'https://desk/' } } } as any)).toBe(false);
    const foreign = {
      get location(): any {
        throw new Error('cross-origin');
      },
    };
    expect(crossOriginFrame({ parent: foreign } as any)).toBe(true);
  });

  it('takes the subscription the desk makes for a frame', async () => {
    spyOn(window.parent, 'postMessage').and.callFake((m: any) => {
      expect(m.type).toBe('skywire-push');
      expect(m.op).toBe('subscribe');
      expect(m.key).toBe('KEY');
      const say = (data: any) => window.dispatchEvent(new MessageEvent('message', { data: data, source: window.parent }));
      setTimeout(() => {
        say({ type: 'skywire-push-reply', id: m.id, ack: true });
        say({ type: 'skywire-push-reply', id: 'other', sub: { endpoint: 'wrong' } });
        say({ type: 'skywire-push-reply', id: m.id, sub: { endpoint: 'https://push.example/1' } });
      });
    });
    const r = await askDesk('subscribe', 'KEY', 50);
    expect(r.sub.endpoint).toBe('https://push.example/1');
  });

  it('passes on the error the desk reports', async () => {
    spyOn(window.parent, 'postMessage').and.callFake((m: any) => {
      setTimeout(() => window.dispatchEvent(new MessageEvent('message',
        { data: { type: 'skywire-push-reply', id: m.id, error: 'blocked' }, source: window.parent })));
    });
    await expectAsync(askDesk('subscribe', 'KEY', 50)).toBeRejectedWithError('blocked');
  });

  it('gives up when no desk answers', async () => {
    spyOn(window.parent, 'postMessage');
    await expectAsync(askDesk('endpoint', '', 20)).toBeRejectedWithError('no desk answered');
  });
});
