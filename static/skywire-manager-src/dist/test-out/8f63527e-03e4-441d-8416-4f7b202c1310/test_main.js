import "./chunk-BYWCQDWW.js";
import {
  FakeNavigation,
  TestComponentRenderer,
  getTestBed
} from "./chunk-UQCSELO5.js";
import {
  BrowserModule,
  platformBrowser
} from "./chunk-CJYNOHF2.js";
import {
  LocationStrategy,
  PRECOMMIT_HANDLER_SUPPORTED,
  PlatformLocation,
  PlatformNavigation,
  getDOM,
  normalizeQueryParams
} from "./chunk-VGD7AO6Y.js";
import {
  APP_ID,
  DOCUMENT,
  FactoryTarget,
  Inject,
  Injectable,
  InjectionToken,
  NgModule,
  Optional,
  Subject,
  core_exports,
  createPlatformFactory,
  inject,
  ɵɵngDeclareClassMetadata,
  ɵɵngDeclareFactory,
  ɵɵngDeclareInjectable,
  ɵɵngDeclareInjector,
  ɵɵngDeclareNgModule
} from "./chunk-ZNLGPAUZ.js";
import {
  __spreadProps,
  __spreadValues
} from "./chunk-ARXMZVRT.js";

// node_modules/@angular/common/fesm2022/testing.mjs
/**
 * @license Angular v22.2.1
 * (c) 2010-2026 Google LLC. https://angular.dev/
 * License: MIT
 */
var urlParse = /^(([^:\/?#]+):)?(\/\/([^\/?#]*))?([^?#]*)(\?([^#]*))?(#(.*))?/;
function parseUrl(urlStr, baseHref) {
  const verifyProtocol = /^((http[s]?|ftp):\/\/)/;
  let serverBase;
  if (!verifyProtocol.test(urlStr)) {
    serverBase = "http://empty.com/";
  }
  let parsedUrl;
  try {
    parsedUrl = new URL(urlStr, serverBase);
  } catch (e) {
    const result = urlParse.exec(serverBase || "" + urlStr);
    if (!result) {
      throw new Error(`Invalid URL: ${urlStr} with base: ${baseHref}`);
    }
    const hostSplit = result[4].split(":");
    parsedUrl = {
      protocol: result[1],
      hostname: hostSplit[0],
      port: hostSplit[1] || "",
      pathname: result[5],
      search: result[6],
      hash: result[8]
    };
  }
  if (parsedUrl.pathname && parsedUrl.pathname.indexOf(baseHref) === 0) {
    parsedUrl.pathname = parsedUrl.pathname.substring(baseHref.length);
  }
  return {
    hostname: !serverBase && parsedUrl.hostname || "",
    protocol: !serverBase && parsedUrl.protocol || "",
    port: !serverBase && parsedUrl.port || "",
    pathname: parsedUrl.pathname || "/",
    search: parsedUrl.search || "",
    hash: parsedUrl.hash || ""
  };
}
var MOCK_PLATFORM_LOCATION_CONFIG = new InjectionToken("MOCK_PLATFORM_LOCATION_CONFIG");
var MockPlatformLocation = class _MockPlatformLocation {
  baseHref = "";
  hashUpdate = new Subject();
  popStateSubject = new Subject();
  urlChangeIndex = 0;
  urlChanges = [{
    hostname: "",
    protocol: "",
    port: "",
    pathname: "/",
    search: "",
    hash: "",
    state: null
  }];
  constructor(config) {
    if (config) {
      this.baseHref = config.appBaseHref || "";
      const parsedChanges = this.parseChanges(null, config.startUrl || "http://_empty_/", this.baseHref);
      this.urlChanges[0] = __spreadValues({}, parsedChanges);
    }
  }
  get hostname() {
    return this.urlChanges[this.urlChangeIndex].hostname;
  }
  get protocol() {
    return this.urlChanges[this.urlChangeIndex].protocol;
  }
  get port() {
    return this.urlChanges[this.urlChangeIndex].port;
  }
  get pathname() {
    return this.urlChanges[this.urlChangeIndex].pathname;
  }
  get search() {
    return this.urlChanges[this.urlChangeIndex].search;
  }
  get hash() {
    return this.urlChanges[this.urlChangeIndex].hash;
  }
  get state() {
    return this.urlChanges[this.urlChangeIndex].state;
  }
  getBaseHrefFromDOM() {
    return this.baseHref;
  }
  onPopState(fn) {
    const subscription = this.popStateSubject.subscribe(fn);
    return () => subscription.unsubscribe();
  }
  onHashChange(fn) {
    const subscription = this.hashUpdate.subscribe(fn);
    return () => subscription.unsubscribe();
  }
  get href() {
    let url = `${this.protocol}//${this.hostname}${this.port ? ":" + this.port : ""}`;
    url += `${this.pathname === "/" ? "" : this.pathname}${this.search}${this.hash}`;
    return url;
  }
  get url() {
    return `${this.pathname}${this.search}${this.hash}`;
  }
  parseChanges(state, url, baseHref = "") {
    state = JSON.parse(JSON.stringify(state));
    return __spreadProps(__spreadValues({}, parseUrl(url, baseHref)), {
      state
    });
  }
  replaceState(state, title, newUrl) {
    const {
      pathname,
      search,
      state: parsedState,
      hash
    } = this.parseChanges(state, newUrl);
    this.urlChanges[this.urlChangeIndex] = __spreadProps(__spreadValues({}, this.urlChanges[this.urlChangeIndex]), {
      pathname,
      search,
      hash,
      state: parsedState
    });
  }
  pushState(state, title, newUrl) {
    const {
      pathname,
      search,
      state: parsedState,
      hash
    } = this.parseChanges(state, newUrl);
    if (this.urlChangeIndex > 0) {
      this.urlChanges.splice(this.urlChangeIndex + 1);
    }
    this.urlChanges.push(__spreadProps(__spreadValues({}, this.urlChanges[this.urlChangeIndex]), {
      pathname,
      search,
      hash,
      state: parsedState
    }));
    this.urlChangeIndex = this.urlChanges.length - 1;
  }
  forward() {
    const oldUrl = this.url;
    const oldHash = this.hash;
    if (this.urlChangeIndex < this.urlChanges.length) {
      this.urlChangeIndex++;
    }
    this.emitEvents(oldHash, oldUrl);
  }
  back() {
    const oldUrl = this.url;
    const oldHash = this.hash;
    if (this.urlChangeIndex > 0) {
      this.urlChangeIndex--;
    }
    this.emitEvents(oldHash, oldUrl);
  }
  historyGo(relativePosition = 0) {
    const oldUrl = this.url;
    const oldHash = this.hash;
    const nextPageIndex = this.urlChangeIndex + relativePosition;
    if (nextPageIndex >= 0 && nextPageIndex < this.urlChanges.length) {
      this.urlChangeIndex = nextPageIndex;
    }
    this.emitEvents(oldHash, oldUrl);
  }
  getState() {
    return this.state;
  }
  emitEvents(oldHash, oldUrl) {
    this.popStateSubject.next({
      type: "popstate",
      state: this.getState(),
      oldUrl,
      newUrl: this.url
    });
    if (oldHash !== this.hash) {
      this.hashUpdate.next({
        type: "hashchange",
        state: null,
        oldUrl,
        newUrl: this.url
      });
    }
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _MockPlatformLocation,
    deps: [{
      token: MOCK_PLATFORM_LOCATION_CONFIG,
      optional: true
    }],
    target: FactoryTarget.Injectable
  });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _MockPlatformLocation
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: MockPlatformLocation,
  decorators: [{
    type: Injectable
  }],
  ctorParameters: () => [{
    type: void 0,
    decorators: [{
      type: Inject,
      args: [MOCK_PLATFORM_LOCATION_CONFIG]
    }, {
      type: Optional
    }]
  }]
});
var FakeNavigationPlatformLocation = class _FakeNavigationPlatformLocation {
  _platformNavigation;
  constructor() {
    const platformNavigation = inject(PlatformNavigation);
    if (!(platformNavigation instanceof FakeNavigation)) {
      throw new Error("FakePlatformNavigation cannot be used without FakeNavigation. Use `provideFakeNavigation` to have all these services provided together.");
    }
    this._platformNavigation = platformNavigation;
  }
  config = inject(MOCK_PLATFORM_LOCATION_CONFIG, {
    optional: true
  });
  getBaseHrefFromDOM() {
    return this.config?.appBaseHref ?? "";
  }
  onPopState(fn) {
    this._platformNavigation.window?.addEventListener?.("popstate", fn);
    return () => this._platformNavigation.window?.removeEventListener?.("popstate", fn);
  }
  onHashChange(fn) {
    this._platformNavigation.window?.addEventListener?.("hashchange", fn);
    return () => this._platformNavigation.window?.removeEventListener?.("hashchange", fn);
  }
  get href() {
    return this._platformNavigation.currentEntry.url;
  }
  get protocol() {
    return new URL(this._platformNavigation.currentEntry.url).protocol;
  }
  get hostname() {
    return new URL(this._platformNavigation.currentEntry.url).hostname;
  }
  get port() {
    return new URL(this._platformNavigation.currentEntry.url).port;
  }
  get pathname() {
    return new URL(this._platformNavigation.currentEntry.url).pathname;
  }
  get search() {
    return new URL(this._platformNavigation.currentEntry.url).search;
  }
  get hash() {
    return new URL(this._platformNavigation.currentEntry.url).hash;
  }
  pushState(state, title, url) {
    this._platformNavigation.pushState(state, title, url);
  }
  replaceState(state, title, url) {
    this._platformNavigation.replaceState(state, title, url);
  }
  forward() {
    this._platformNavigation.forward();
  }
  back() {
    this._platformNavigation.back();
  }
  historyGo(relativePosition = 0) {
    this._platformNavigation.go(relativePosition);
  }
  getState() {
    return this._platformNavigation.currentEntry.getHistoryState();
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _FakeNavigationPlatformLocation,
    deps: [],
    target: FactoryTarget.Injectable
  });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _FakeNavigationPlatformLocation
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: FakeNavigationPlatformLocation,
  decorators: [{
    type: Injectable
  }],
  ctorParameters: () => []
});
var FAKE_NAVIGATION = new InjectionToken("fakeNavigation", {
  factory: () => {
    const config = inject(MOCK_PLATFORM_LOCATION_CONFIG, {
      optional: true
    });
    const baseFallback = "http://_empty_/";
    const startUrl = new URL(config?.startUrl || baseFallback, baseFallback);
    const fakeNavigation = new FakeNavigation(inject(DOCUMENT), startUrl.href);
    fakeNavigation.setSynchronousTraversalsForTesting(true);
    return fakeNavigation;
  }
});
function provideFakePlatformNavigation() {
  return [{
    provide: PlatformNavigation,
    useFactory: () => inject(FAKE_NAVIGATION)
  }, {
    provide: PlatformLocation,
    useClass: FakeNavigationPlatformLocation
  }, {
    provide: PRECOMMIT_HANDLER_SUPPORTED,
    useValue: true
  }];
}
var SpyLocation = class _SpyLocation {
  urlChanges = [];
  _history = [new LocationState("", "", null)];
  _historyIndex = 0;
  _subject = new Subject();
  _basePath = "";
  _locationStrategy = null;
  _urlChangeListeners = [];
  _urlChangeSubscription = null;
  ngOnDestroy() {
    this._urlChangeSubscription?.unsubscribe();
    this._urlChangeListeners = [];
  }
  setInitialPath(url) {
    this._history[this._historyIndex].path = url;
  }
  setBaseHref(url) {
    this._basePath = url;
  }
  path() {
    return this._history[this._historyIndex].path;
  }
  getState() {
    return this._history[this._historyIndex].state;
  }
  isCurrentPathEqualTo(path, query = "") {
    const givenPath = path.endsWith("/") ? path.substring(0, path.length - 1) : path;
    const currPath = this.path().endsWith("/") ? this.path().substring(0, this.path().length - 1) : this.path();
    return currPath == givenPath + (query.length > 0 ? "?" + query : "");
  }
  simulateUrlPop(pathname) {
    this._subject.next({
      "url": pathname,
      "pop": true,
      "type": "popstate"
    });
  }
  simulateHashChange(pathname) {
    const path = this.prepareExternalUrl(pathname);
    this.pushHistory(path, "", null);
    this.urlChanges.push("hash: " + pathname);
    this._subject.next({
      "url": pathname,
      "pop": true,
      "type": "popstate"
    });
    this._subject.next({
      "url": pathname,
      "pop": true,
      "type": "hashchange"
    });
  }
  prepareExternalUrl(url) {
    if (url.length > 0 && !url.startsWith("/")) {
      url = "/" + url;
    }
    return this._basePath + url;
  }
  go(path, query = "", state = null) {
    path = this.prepareExternalUrl(path);
    this.pushHistory(path, query, state);
    const locationState = this._history[this._historyIndex - 1];
    if (locationState.path == path && locationState.query == query) {
      return;
    }
    const url = path + (query.length > 0 ? "?" + query : "");
    this.urlChanges.push(url);
    this._notifyUrlChangeListeners(path + normalizeQueryParams(query), state);
  }
  replaceState(path, query = "", state = null) {
    path = this.prepareExternalUrl(path);
    const history = this._history[this._historyIndex];
    history.state = state;
    if (history.path == path && history.query == query) {
      return;
    }
    history.path = path;
    history.query = query;
    const url = path + (query.length > 0 ? "?" + query : "");
    this.urlChanges.push("replace: " + url);
    this._notifyUrlChangeListeners(path + normalizeQueryParams(query), state);
  }
  forward() {
    if (this._historyIndex < this._history.length - 1) {
      this._historyIndex++;
      this._subject.next({
        "url": this.path(),
        "state": this.getState(),
        "pop": true,
        "type": "popstate"
      });
    }
  }
  back() {
    if (this._historyIndex > 0) {
      this._historyIndex--;
      this._subject.next({
        "url": this.path(),
        "state": this.getState(),
        "pop": true,
        "type": "popstate"
      });
    }
  }
  historyGo(relativePosition = 0) {
    const nextPageIndex = this._historyIndex + relativePosition;
    if (nextPageIndex >= 0 && nextPageIndex < this._history.length) {
      this._historyIndex = nextPageIndex;
      this._subject.next({
        "url": this.path(),
        "state": this.getState(),
        "pop": true,
        "type": "popstate"
      });
    }
  }
  onUrlChange(fn) {
    this._urlChangeListeners.push(fn);
    this._urlChangeSubscription ??= this.subscribe((v) => {
      this._notifyUrlChangeListeners(v.url, v.state);
    });
    return () => {
      const fnIndex = this._urlChangeListeners.indexOf(fn);
      this._urlChangeListeners.splice(fnIndex, 1);
      if (this._urlChangeListeners.length === 0) {
        this._urlChangeSubscription?.unsubscribe();
        this._urlChangeSubscription = null;
      }
    };
  }
  _notifyUrlChangeListeners(url = "", state) {
    this._urlChangeListeners.forEach((fn) => fn(url, state));
  }
  subscribe(onNext, onThrow, onReturn) {
    return this._subject.subscribe({
      next: onNext,
      error: onThrow ?? void 0,
      complete: onReturn ?? void 0
    });
  }
  normalize(url) {
    return null;
  }
  pushHistory(path, query, state) {
    if (this._historyIndex > 0) {
      this._history.splice(this._historyIndex + 1);
    }
    this._history.push(new LocationState(path, query, state));
    this._historyIndex = this._history.length - 1;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _SpyLocation,
    deps: [],
    target: FactoryTarget.Injectable
  });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _SpyLocation
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: SpyLocation,
  decorators: [{
    type: Injectable
  }]
});
var LocationState = class {
  path;
  query;
  state;
  constructor(path, query, state) {
    this.path = path;
    this.query = query;
    this.state = state;
  }
};
var MockLocationStrategy = class _MockLocationStrategy extends LocationStrategy {
  internalBaseHref = "/";
  internalPath = "/";
  internalTitle = "";
  urlChanges = [];
  _subject = new Subject();
  stateChanges = [];
  constructor() {
    super();
  }
  simulatePopState(url) {
    this.internalPath = url;
    this._subject.next(new _MockPopStateEvent(this.path()));
  }
  path(includeHash = false) {
    return this.internalPath;
  }
  prepareExternalUrl(internal) {
    if (internal.startsWith("/") && this.internalBaseHref.endsWith("/")) {
      return this.internalBaseHref + internal.substring(1);
    }
    return this.internalBaseHref + internal;
  }
  pushState(ctx, title, path, query) {
    this.stateChanges.push(ctx);
    this.internalTitle = title;
    const url = path + (query.length > 0 ? "?" + query : "");
    this.internalPath = url;
    const externalUrl = this.prepareExternalUrl(url);
    this.urlChanges.push(externalUrl);
  }
  replaceState(ctx, title, path, query) {
    this.stateChanges[(this.stateChanges.length || 1) - 1] = ctx;
    this.internalTitle = title;
    const url = path + (query.length > 0 ? "?" + query : "");
    this.internalPath = url;
    const externalUrl = this.prepareExternalUrl(url);
    this.urlChanges.push("replace: " + externalUrl);
  }
  onPopState(fn) {
    this._subject.subscribe({
      next: fn
    });
  }
  getBaseHref() {
    return this.internalBaseHref;
  }
  back() {
    if (this.urlChanges.length > 0) {
      this.urlChanges.pop();
      this.stateChanges.pop();
      const nextUrl = this.urlChanges.length > 0 ? this.urlChanges[this.urlChanges.length - 1] : "";
      this.simulatePopState(nextUrl);
    }
  }
  forward() {
    throw "not implemented";
  }
  getState() {
    return this.stateChanges[(this.stateChanges.length || 1) - 1];
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _MockLocationStrategy,
    deps: [],
    target: FactoryTarget.Injectable
  });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _MockLocationStrategy
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: MockLocationStrategy,
  decorators: [{
    type: Injectable
  }],
  ctorParameters: () => []
});
var _MockPopStateEvent = class {
  newUrl;
  pop = true;
  type = "popstate";
  constructor(newUrl) {
    this.newUrl = newUrl;
  }
};

// node_modules/@angular/platform-browser/fesm2022/testing.mjs
/**
 * @license Angular v22.2.1
 * (c) 2010-2026 Google LLC. https://angular.dev/
 * License: MIT
 */
var DOMTestComponentRenderer = class _DOMTestComponentRenderer extends TestComponentRenderer {
  _doc;
  constructor(_doc) {
    super();
    this._doc = _doc;
  }
  insertRootElement(rootElId, tagName = "div") {
    this.removeAllRootElementsImpl();
    const rootElement = getDOM().getDefaultDocument().createElement(tagName);
    rootElement.setAttribute("id", rootElId);
    this._doc.body.appendChild(rootElement);
  }
  removeAllRootElements() {
    if (typeof this._doc.querySelectorAll === "function") {
      this.removeAllRootElementsImpl();
    }
  }
  removeAllRootElementsImpl() {
    const oldRoots = this._doc.querySelectorAll("[id^=root]");
    for (let i = 0; i < oldRoots.length; i++) {
      getDOM().remove(oldRoots[i]);
    }
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _DOMTestComponentRenderer,
    deps: [{
      token: DOCUMENT
    }],
    target: FactoryTarget.Injectable
  });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _DOMTestComponentRenderer
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: DOMTestComponentRenderer,
  decorators: [{
    type: Injectable
  }],
  ctorParameters: () => [{
    type: void 0,
    decorators: [{
      type: Inject,
      args: [DOCUMENT]
    }]
  }]
});
var platformBrowserTesting = createPlatformFactory(platformBrowser, "browserTesting");
var BrowserTestingModule = class _BrowserTestingModule {
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _BrowserTestingModule,
    deps: [],
    target: FactoryTarget.NgModule
  });
  static \u0275mod = \u0275\u0275ngDeclareNgModule({
    minVersion: "14.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _BrowserTestingModule,
    exports: [BrowserModule]
  });
  static \u0275inj = \u0275\u0275ngDeclareInjector({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _BrowserTestingModule,
    providers: [{
      provide: APP_ID,
      useValue: "a"
    }, provideFakePlatformNavigation(), {
      provide: TestComponentRenderer,
      useClass: DOMTestComponentRenderer
    }],
    imports: [BrowserModule]
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: BrowserTestingModule,
  decorators: [{
    type: NgModule,
    args: [{
      exports: [BrowserModule],
      providers: [{
        provide: APP_ID,
        useValue: "a"
      }, provideFakePlatformNavigation(), {
        provide: TestComponentRenderer,
        useClass: DOMTestComponentRenderer
      }]
    }]
  }]
});

// src/test.ts
getTestBed().initTestEnvironment(BrowserTestingModule, platformBrowserTesting(), {
  teardown: { destroyAfterEach: false }
});
//# debugId=4f4fd382-c8b1-5860-a2e0-c60d81ac1d40
//# sourceMappingURL=test_main.js.map
