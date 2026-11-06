import {
  versionLabel
} from "./chunk-H3STL4JE.js";
import {
  ApiService
} from "./chunk-ZMZW6HXB.js";
import {
  Injectable,
  Observable,
  catchError,
  defer,
  map,
  of,
  throwError,
  timer
} from "./chunk-ZNLGPAUZ.js";

// src/app/services/app-mode.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AppModeService = class AppModeService2 {
  /**
   * The mark for this UI: the Skycoin cloud for a wasm visor, the Skywire icon
   * for anything served by a native visor. Used for BOTH the tab favicon and
   * the menu button, so the two never disagree about which UI you are looking
   * at — telling them apart at a glance is the whole point of having two.
   */
  get icon() {
    return this.mode === "wasm visor" ? "assets/img/wasm-visor-icon.png" : "assets/img/skywire-icon.png";
  }
  constructor(api) {
    this.version = "";
    this.localPk = "";
    const cfg = typeof window !== "undefined" && window.__SKYWIRE_HV__ || {};
    if (cfg.visor) {
      this.mode = "wasm visor";
    } else if (cfg.standalone) {
      this.mode = "Standalone hypervisor";
    } else if (cfg.pk) {
      this.mode = "Remote viewer";
    } else {
      this.mode = "Native hypervisor";
    }
    this.serverless = !!(cfg.visor || cfg.standalone || cfg.pk);
    this.harness = typeof window !== "undefined" && !!window.__SKYWIRE_HARNESS__;
    if (this.serverless && typeof document !== "undefined" && document.body) {
      document.body.classList.add("wasm-visor-ui");
    }
    if (cfg.visor && typeof document !== "undefined") {
      const link = document.querySelector("link[rel~='icon']") || document.createElement("link");
      link.rel = "icon";
      link.type = "image/png";
      link.href = this.icon;
      if (!link.parentNode) {
        document.head.appendChild(link);
      }
    }
    api.get("about").subscribe({
      next: (a) => {
        this.version = a && a.build && a.build.version || a?.version || "";
        this.localPk = a && a.public_key || "";
        if (this.mode === "Native hypervisor") {
          const os = a?.build?.os || "";
          const arch = a?.build?.arch || "";
          const label = os === "js" ? "wasm" : arch;
          if (label) {
            this.mode = label + " hypervisor";
          }
        }
      },
      error: () => {
      }
    });
  }
  /**
   * "wasm visor · v1.3.78 · harness" — the badge shown where the home title was.
   *
   * The harness suffix is not decoration. With `--harness` the page mounts a
   * control surface that lets anything reaching /ctl/* drive this visor, which
   * is a development-only posture and indistinguishable from a normal build by
   * looking at the UI. Someone who leaves it on should be able to see that they
   * did.
   */
  get badge() {
    return this.badgeWith(versionLabel(this.version));
  }
  /** The badge with the version exactly as the visor reports it. */
  get fullBadge() {
    return this.badgeWith(this.version);
  }
  badgeWith(version) {
    const parts = [this.mode];
    if (version) {
      parts.push(version);
    }
    if (this.harness) {
      parts.push("harness");
    }
    return parts.join(" \xB7 ");
  }
  /** The home/start page's title key — replaced by the mode badge in the top bar. */
  isStartTitle(titleParts) {
    return !!titleParts && titleParts.length > 0 && titleParts[titleParts.length - 1] === "start.title";
  }
  static {
    this.ctorParameters = () => [
      { type: ApiService }
    ];
  }
};
AppModeService = __decorate([
  Injectable({ providedIn: "root" })
], AppModeService);

// src/app/services/live.service.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var LiveUnavailableError = class extends Error {
  constructor() {
    super("live socket unavailable");
  }
};
var maxFailedOpens = 2;
var LiveService = class LiveService2 {
  constructor(appMode) {
    this.appMode = appMode;
    this.socket = null;
    this.open = false;
    this.failedOpens = 0;
    this.everOpened = false;
    this.gaveUp = false;
    this.nextId = 1;
    this.subs = /* @__PURE__ */ new Map();
    this.reconnectTimer = null;
  }
  /** Whether subscriptions can be tried. A serverless or in-tab UI has no /ws. */
  get usable() {
    return !this.appMode.serverless && !this.gaveUp && typeof WebSocket !== "undefined";
  }
  /**
   * Pushes the result of GET path now and whenever it changes, checking every
   * everyMs. Errors with LiveUnavailableError when the socket cannot be used.
   */
  subscribe(path, everyMs) {
    return new Observable((subscriber) => {
      if (!this.usable) {
        subscriber.error(new LiveUnavailableError());
        return void 0;
      }
      const id = this.nextId++;
      this.subs.set(id, { path, everyMs, subscriber });
      if (this.open) {
        this.sendSub(id);
      } else {
        this.connect();
      }
      return () => {
        this.subs.delete(id);
        if (this.open) {
          this.send({ type: "unsub", id });
        }
      };
    });
  }
  /**
   * Follows GET /api/<url> like interval(everyMs).pipe(startWith(0)) would, but
   * pushed over the socket when it can be. Each emission is one result to
   * subscribe to: the parsed pushed body, or request() when polling. An error
   * status also goes through request(), so callers keep their HTTP error handling.
   */
  watch(url, everyMs, request, parse = (b) => b) {
    const poll = timer(0, everyMs).pipe(map(() => request()));
    const path = "/api/" + url.replace(/^\/+/, "");
    return this.subscribe(path, everyMs).pipe(map((ev) => {
      if (ev.status === 429) {
        throw new LiveUnavailableError();
      }
      if (ev.status < 200 || ev.status >= 300) {
        return request();
      }
      return defer(() => of(parse(decodeBody(ev.body))));
    }), catchError((err) => err instanceof LiveUnavailableError ? poll : throwError(() => err)));
  }
  connect() {
    if (this.socket || this.reconnectTimer) {
      return;
    }
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${proto}//${location.host}/ws`);
    this.socket = socket;
    socket.onopen = () => {
      this.open = true;
      this.everOpened = true;
      this.failedOpens = 0;
      this.subs.forEach((_, id) => this.sendSub(id));
    };
    socket.onmessage = (ev) => {
      let frame;
      try {
        frame = JSON.parse(ev.data);
      } catch (e) {
        return;
      }
      if (frame.type === "res" && this.subs.has(frame.id) && frame.status >= 400) {
        this.giveUp();
        return;
      }
      if (frame.type !== "event") {
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
        }, Math.min(1e3 * Math.pow(2, this.failedOpens), 3e4));
      }
    };
  }
  giveUp() {
    this.gaveUp = true;
    const subs = Array.from(this.subs.values());
    this.subs.clear();
    subs.forEach((s) => s.subscriber.error(new LiveUnavailableError()));
  }
  sendSub(id) {
    const sub = this.subs.get(id);
    if (sub) {
      this.send({ type: "sub", id, path: sub.path, every_ms: sub.everyMs });
    }
  }
  send(frame) {
    try {
      this.socket?.send(JSON.stringify(frame));
    } catch (e) {
    }
  }
  static {
    this.ctorParameters = () => [
      { type: AppModeService }
    ];
  }
};
LiveService = __decorate2([
  Injectable({
    providedIn: "root"
  })
], LiveService);
function decodeBody(body) {
  try {
    return JSON.parse(body);
  } catch (e) {
    return body;
  }
}

export {
  LiveUnavailableError,
  LiveService
};
//# debugId=e92b35ab-bf51-55d9-8636-debf2818b1ab
//# sourceMappingURL=chunk-GPHL7TNH.js.map
