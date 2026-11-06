import {
  fakeAsync,
  tick
} from "./chunk-UQCSELO5.js";
import {
  LiveService,
  LiveUnavailableError
} from "./chunk-GPHL7TNH.js";
import "./chunk-H3STL4JE.js";
import "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import {
  of,
  switchMap
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/services/live.service.spec.ts
var FakeSocket = class _FakeSocket {
  static {
    this.all = [];
  }
  constructor(url) {
    this.url = url;
    this.sent = [];
    this.onopen = null;
    this.onmessage = null;
    this.onclose = null;
    _FakeSocket.all.push(this);
  }
  send(data) {
    this.sent.push(JSON.parse(data));
  }
  open() {
    this.onopen?.();
  }
  push(frame) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
  close() {
    this.onclose?.();
  }
};
describe("LiveService", () => {
  const realSocket = window.WebSocket;
  let service;
  beforeEach(() => {
    FakeSocket.all = [];
    window.WebSocket = FakeSocket;
    service = new LiveService({ serverless: false });
  });
  afterEach(() => {
    window.WebSocket = realSocket;
  });
  it("subscribes once open, delivers events and unsubscribes", () => {
    const got = [];
    const sub = service.subscribe("/api/visors/02aa/summary", 5e3).subscribe((ev) => got.push(ev.body));
    const socket = FakeSocket.all[0];
    expect(socket.url).toMatch(/\/ws$/);
    socket.open();
    expect(socket.sent).toEqual([{ type: "sub", id: 1, path: "/api/visors/02aa/summary", every_ms: 5e3 }]);
    socket.push({ type: "event", id: 1, status: 200, body: "a" });
    socket.push({ type: "res", id: 1, status: 200, body: "ignored" });
    expect(got).toEqual(["a"]);
    sub.unsubscribe();
    expect(socket.sent[1]).toEqual({ type: "unsub", id: 1 });
  });
  it("resubscribes after the socket drops", fakeAsync(() => {
    service.subscribe("/api/x", 5e3).subscribe();
    FakeSocket.all[0].open();
    FakeSocket.all[0].close();
    tick(3e4);
    const again = FakeSocket.all[1];
    again.open();
    expect(again.sent).toEqual([{ type: "sub", id: 1, path: "/api/x", every_ms: 5e3 }]);
  }));
  it("gives up when the socket never opens, so callers poll instead", fakeAsync(() => {
    let failed = null;
    service.subscribe("/api/x", 5e3).subscribe({ error: (e) => failed = e });
    FakeSocket.all[0].close();
    tick(3e4);
    FakeSocket.all[1].close();
    expect(failed instanceof LiveUnavailableError).toBeTrue();
    expect(service.usable).toBeFalse();
  }));
  it("gives up on a hypervisor that answers sub as an unknown request", () => {
    let failed = null;
    service.subscribe("/api/x", 5e3).subscribe({ error: (e) => failed = e });
    FakeSocket.all[0].open();
    FakeSocket.all[0].push({ type: "res", id: 1, status: 405, body: "method not allowed on this transport" });
    expect(failed instanceof LiveUnavailableError).toBeTrue();
  });
  it("is not used by a serverless UI", () => {
    const serverless = new LiveService({ serverless: true });
    let failed = null;
    serverless.subscribe("/api/x", 5e3).subscribe({ error: (e) => failed = e });
    expect(failed instanceof LiveUnavailableError).toBeTrue();
    expect(FakeSocket.all.length).toBe(0);
  });
  describe("watch", () => {
    const collect = (src, got) => src.pipe(switchMap((res) => res)).subscribe((v) => got.push(v));
    it("subscribes to the /api path and yields the parsed pushed body", () => {
      let requests = 0;
      const got = [];
      collect(service.watch("visors/02aa/dmsg/sessions", 5e3, () => {
        requests++;
        return of("http");
      }), got);
      const socket = FakeSocket.all[0];
      socket.open();
      expect(socket.sent[0]).toEqual({ type: "sub", id: 1, path: "/api/visors/02aa/dmsg/sessions", every_ms: 5e3 });
      socket.push({ type: "event", id: 1, status: 200, body: '{"clients":[1]}' });
      expect(got).toEqual([{ clients: [1] }]);
      expect(requests).toBe(0);
    });
    it("applies parse to a pushed body", () => {
      const got = [];
      collect(service.watch("x", 5e3, () => of(0), (b) => b.n * 2), got);
      FakeSocket.all[0].open();
      FakeSocket.all[0].push({ type: "event", id: 1, status: 200, body: '{"n":21}' });
      expect(got).toEqual([42]);
    });
    it("asks over HTTP when the pushed status is an error", () => {
      const got = [];
      collect(service.watch("x", 5e3, () => of("from http")), got);
      FakeSocket.all[0].open();
      FakeSocket.all[0].push({ type: "event", id: 1, status: 500, body: "boom" });
      expect(got).toEqual(["from http"]);
    });
    it("polls when the socket cannot be used", fakeAsync(() => {
      const serverless = new LiveService({ serverless: true });
      let requests = 0;
      const sub = collect(serverless.watch("x", 5e3, () => of(++requests)), []);
      tick(0);
      expect(requests).toBe(1);
      tick(1e4);
      expect(requests).toBe(3);
      sub.unsubscribe();
    }));
    it("polls past the per-socket subscription cap", fakeAsync(() => {
      let requests = 0;
      const sub = collect(service.watch("x", 5e3, () => of(++requests)), []);
      const socket = FakeSocket.all[0];
      socket.open();
      socket.push({ type: "event", id: 1, status: 429, body: "too many subscriptions" });
      tick(0);
      expect(requests).toBe(1);
      expect(socket.sent[1]).toEqual({ type: "unsub", id: 1 });
      tick(5e3);
      expect(requests).toBe(2);
      sub.unsubscribe();
    }));
  });
});
//# debugId=6cab5b1d-4454-5383-951e-4ce6aaa5fb83
//# sourceMappingURL=spec-app-services-live.service.spec.js.map
