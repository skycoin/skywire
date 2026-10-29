import { fakeAsync, tick } from '@angular/core/testing';
import { of, switchMap } from 'rxjs';

import { LiveService, LiveUnavailableError } from './live.service';

// A stand-in for the browser WebSocket that records what the service sends and
// lets a test open, message and close it.
class FakeSocket {
  static all: FakeSocket[] = [];
  sent: any[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;

  constructor(public url: string) {
    FakeSocket.all.push(this);
  }

  send(data: string) {
    this.sent.push(JSON.parse(data));
  }

  open() {
    this.onopen?.();
  }

  push(frame: any) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }

  close() {
    this.onclose?.();
  }
}

describe('LiveService', () => {
  const realSocket = (window as any).WebSocket;
  let service: LiveService;

  beforeEach(() => {
    FakeSocket.all = [];
    (window as any).WebSocket = FakeSocket;
    service = new LiveService({ serverless: false } as any);
  });

  afterEach(() => {
    (window as any).WebSocket = realSocket;
  });

  it('subscribes once open, delivers events and unsubscribes', () => {
    const got: string[] = [];
    const sub = service.subscribe('/api/visors/02aa/summary', 5000).subscribe(ev => got.push(ev.body));
    const socket = FakeSocket.all[0];
    expect(socket.url).toMatch(/\/ws$/);

    socket.open();
    expect(socket.sent).toEqual([{ type: 'sub', id: 1, path: '/api/visors/02aa/summary', every_ms: 5000 }]);

    socket.push({ type: 'event', id: 1, status: 200, body: 'a' });
    socket.push({ type: 'res', id: 1, status: 200, body: 'ignored' });
    expect(got).toEqual(['a']);

    sub.unsubscribe();
    expect(socket.sent[1]).toEqual({ type: 'unsub', id: 1 });
  });

  it('resubscribes after the socket drops', fakeAsync(() => {
    service.subscribe('/api/x', 5000).subscribe();
    FakeSocket.all[0].open();
    FakeSocket.all[0].close();
    tick(30000);
    const again = FakeSocket.all[1];
    again.open();
    expect(again.sent).toEqual([{ type: 'sub', id: 1, path: '/api/x', every_ms: 5000 }]);
  }));

  it('gives up when the socket never opens, so callers poll instead', fakeAsync(() => {
    let failed: any = null;
    service.subscribe('/api/x', 5000).subscribe({ error: e => failed = e });
    FakeSocket.all[0].close();
    tick(30000);
    FakeSocket.all[1].close();
    expect(failed instanceof LiveUnavailableError).toBeTrue();
    expect(service.usable).toBeFalse();
  }));

  it('gives up on a hypervisor that answers sub as an unknown request', () => {
    let failed: any = null;
    service.subscribe('/api/x', 5000).subscribe({ error: e => failed = e });
    FakeSocket.all[0].open();
    FakeSocket.all[0].push({ type: 'res', id: 1, status: 405, body: 'method not allowed on this transport' });
    expect(failed instanceof LiveUnavailableError).toBeTrue();
  });

  it('is not used by a serverless UI', () => {
    const serverless = new LiveService({ serverless: true } as any);
    let failed: any = null;
    serverless.subscribe('/api/x', 5000).subscribe({ error: e => failed = e });
    expect(failed instanceof LiveUnavailableError).toBeTrue();
    expect(FakeSocket.all.length).toBe(0);
  });

  describe('watch', () => {
    // Collects what each emitted result yields, as a caller's switchMap would.
    const collect = (src: any, got: any[]) => src.pipe(switchMap((res: any) => res)).subscribe((v: any) => got.push(v));

    it('subscribes to the /api path and yields the parsed pushed body', () => {
      let requests = 0;
      const got: any[] = [];
      collect(service.watch('visors/02aa/dmsg/sessions', 5000, () => {
        requests++;

        return of('http');
      }), got);
      const socket = FakeSocket.all[0];
      socket.open();
      expect(socket.sent[0]).toEqual({ type: 'sub', id: 1, path: '/api/visors/02aa/dmsg/sessions', every_ms: 5000 });

      socket.push({ type: 'event', id: 1, status: 200, body: '{"clients":[1]}' });
      expect(got).toEqual([{ clients: [1] }]);
      expect(requests).toBe(0);
    });

    it('applies parse to a pushed body', () => {
      const got: any[] = [];
      collect(service.watch('x', 5000, () => of(0), (b: any) => b.n * 2), got);
      FakeSocket.all[0].open();
      FakeSocket.all[0].push({ type: 'event', id: 1, status: 200, body: '{"n":21}' });
      expect(got).toEqual([42]);
    });

    it('asks over HTTP when the pushed status is an error', () => {
      const got: any[] = [];
      collect(service.watch('x', 5000, () => of('from http')), got);
      FakeSocket.all[0].open();
      FakeSocket.all[0].push({ type: 'event', id: 1, status: 500, body: 'boom' });
      expect(got).toEqual(['from http']);
    });

    it('polls when the socket cannot be used', fakeAsync(() => {
      const serverless = new LiveService({ serverless: true } as any);
      let requests = 0;
      const sub = collect(serverless.watch('x', 5000, () => of(++requests)), []);
      tick(0);
      expect(requests).toBe(1);
      tick(10000);
      expect(requests).toBe(3);
      sub.unsubscribe();
    }));

    it('polls past the per-socket subscription cap', fakeAsync(() => {
      let requests = 0;
      const sub = collect(service.watch('x', 5000, () => of(++requests)), []);
      const socket = FakeSocket.all[0];
      socket.open();
      socket.push({ type: 'event', id: 1, status: 429, body: 'too many subscriptions' });
      tick(0);
      expect(requests).toBe(1);
      expect(socket.sent[1]).toEqual({ type: 'unsub', id: 1 });
      tick(5000);
      expect(requests).toBe(2);
      sub.unsubscribe();
    }));
  });
});
