import { fakeAsync, tick } from '@angular/core/testing';

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
});
