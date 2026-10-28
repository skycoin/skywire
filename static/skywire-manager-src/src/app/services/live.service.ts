import { Injectable } from '@angular/core';
import { Observable, Subscriber } from 'rxjs';

import { AppModeService } from './app-mode.service';

/** One pushed result of a subscribed GET: what the HTTP request would have returned. */
export interface LiveEvent {
  status: number;
  body: string;
}

/** Thrown to subscribers when the socket cannot be used, so they go back to polling. */
export class LiveUnavailableError extends Error {
  constructor() {
    super('live socket unavailable');
  }
}

interface LiveSub {
  path: string;
  everyMs: number;
  subscriber: Subscriber<LiveEvent>;
}

// After this many failed attempts without ever opening, the socket is given up on.
const maxFailedOpens = 2;

/**
 * Subscriptions over the hypervisor's /ws socket (pkg/visor/hypervisor_ws.go).
 * The hypervisor re-runs a subscribed GET on an interval and pushes the result
 * only when it changed, so pages stop polling over HTTP. One socket serves the
 * whole UI; it reconnects and resubscribes after a drop.
 */
@Injectable({
  providedIn: 'root'
})
export class LiveService {
  private socket: WebSocket | null = null;
  private open = false;
  private failedOpens = 0;
  private everOpened = false;
  private gaveUp = false;
  private nextId = 1;
  private subs = new Map<number, LiveSub>();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(private appMode: AppModeService) {}

  /** Whether subscriptions can be tried. A serverless or in-tab UI has no /ws. */
  get usable(): boolean {
    return !this.appMode.serverless && !this.gaveUp && typeof WebSocket !== 'undefined';
  }

  /**
   * Pushes the result of GET path now and whenever it changes, checking every
   * everyMs. Errors with LiveUnavailableError when the socket cannot be used.
   */
  subscribe(path: string, everyMs: number): Observable<LiveEvent> {
    return new Observable<LiveEvent>(subscriber => {
      if (!this.usable) {
        subscriber.error(new LiveUnavailableError());

        return undefined;
      }
      const id = this.nextId++;
      this.subs.set(id, { path: path, everyMs: everyMs, subscriber: subscriber });
      if (this.open) {
        this.sendSub(id);
      } else {
        this.connect();
      }

      return () => {
        this.subs.delete(id);
        if (this.open) {
          this.send({ type: 'unsub', id: id });
        }
      };
    });
  }

  private connect() {
    if (this.socket || this.reconnectTimer) {
      return;
    }
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(`${proto}//${location.host}/ws`);
    this.socket = socket;

    socket.onopen = () => {
      this.open = true;
      this.everOpened = true;
      this.failedOpens = 0;
      this.subs.forEach((_, id) => this.sendSub(id));
    };
    socket.onmessage = ev => {
      let frame: any;
      try {
        frame = JSON.parse(ev.data);
      } catch {
        return;
      }
      if (frame.type !== 'event') {
        return;
      }
      this.subs.get(frame.id)?.subscriber.next({ status: frame.status, body: frame.body });
    };
    socket.onclose = () => {
      const wasOpen = this.open;
      this.socket = null;
      this.open = false;
      if (!wasOpen) {
        this.failedOpens++;
      }
      if (!this.everOpened && this.failedOpens >= maxFailedOpens) {
        this.giveUp();

        return;
      }
      if (this.subs.size > 0) {
        this.reconnectTimer = setTimeout(() => {
          this.reconnectTimer = null;
          this.connect();
        }, Math.min(1000 * Math.pow(2, this.failedOpens), 30000));
      }
    };
  }

  private giveUp() {
    this.gaveUp = true;
    const subs = Array.from(this.subs.values());
    this.subs.clear();
    subs.forEach(s => s.subscriber.error(new LiveUnavailableError()));
  }

  private sendSub(id: number) {
    const sub = this.subs.get(id);
    if (sub) {
      this.send({ type: 'sub', id: id, path: sub.path, every_ms: sub.everyMs });
    }
  }

  private send(frame: any) {
    try {
      this.socket?.send(JSON.stringify(frame));
    } catch {
      // A send on a closing socket is retried by the resubscribe after reconnect.
    }
  }
}
