import {
  TestBed,
  fakeAsync,
  tick
} from "./chunk-UQCSELO5.js";
import {
  ApiService,
  HttpBackend,
  HttpClientModule,
  HttpErrorResponse,
  HttpEventType,
  HttpHeaders,
  HttpResponse,
  HttpStatusCode,
  REQUESTS_CONTRIBUTE_TO_STABILITY,
  RequestOptions,
  RequestTypes,
  ResponseTypes,
  Router,
  SnackbarService,
  provideHttpClient,
  withXhr
} from "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import {
  FactoryTarget,
  Injectable,
  NgModule,
  Observable,
  core_exports,
  ɵɵngDeclareClassMetadata,
  ɵɵngDeclareFactory,
  ɵɵngDeclareInjectable,
  ɵɵngDeclareInjector,
  ɵɵngDeclareNgModule
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// node_modules/@angular/common/fesm2022/http-testing.mjs
/**
 * @license Angular v22.2.1
 * (c) 2010-2026 Google LLC. https://angular.dev/
 * License: MIT
 */
var HttpTestingController = class {
};
var TestRequest = class {
  request;
  observer;
  get cancelled() {
    return this._cancelled;
  }
  _cancelled = false;
  constructor(request, observer) {
    this.request = request;
    this.observer = observer;
  }
  flush(body, opts = {}) {
    if (this.cancelled) {
      throw new Error(`Cannot flush a cancelled request.`);
    }
    const url = this.request.urlWithParams;
    const headers = opts.headers instanceof HttpHeaders ? opts.headers : new HttpHeaders(opts.headers);
    body = _maybeConvertBody(this.request.responseType, body);
    let statusText = opts.statusText;
    let status = opts.status !== void 0 ? opts.status : HttpStatusCode.Ok;
    if (opts.status === void 0) {
      if (body === null) {
        status = HttpStatusCode.NoContent;
        statusText ||= "No Content";
      } else {
        statusText ||= "OK";
      }
    }
    if (statusText === void 0) {
      throw new Error("statusText is required when setting a custom status.");
    }
    if (status >= 200 && status < 300) {
      this.observer.next(new HttpResponse({
        body,
        headers,
        status,
        statusText,
        url
      }));
      this.observer.complete();
    } else {
      this.observer.error(new HttpErrorResponse({
        error: body,
        headers,
        status,
        statusText,
        url
      }));
    }
  }
  error(error, opts = {}) {
    if (this.cancelled) {
      throw new Error(`Cannot return an error for a cancelled request.`);
    }
    const headers = opts.headers instanceof HttpHeaders ? opts.headers : new HttpHeaders(opts.headers);
    this.observer.error(new HttpErrorResponse({
      error,
      headers,
      status: opts.status || 0,
      statusText: opts.statusText || "",
      url: this.request.urlWithParams
    }));
  }
  event(event) {
    if (this.cancelled) {
      throw new Error(`Cannot send events to a cancelled request.`);
    }
    this.observer.next(event);
  }
};
function _toArrayBufferBody(body) {
  if (typeof ArrayBuffer === "undefined") {
    throw new Error("ArrayBuffer responses are not supported on this platform.");
  }
  if (body instanceof ArrayBuffer) {
    return body;
  }
  throw new Error("Automatic conversion to ArrayBuffer is not supported for response type.");
}
function _toBlob(body) {
  if (typeof Blob === "undefined") {
    throw new Error("Blob responses are not supported on this platform.");
  }
  if (body instanceof Blob) {
    return body;
  }
  if (ArrayBuffer && body instanceof ArrayBuffer) {
    return new Blob([body]);
  }
  throw new Error("Automatic conversion to Blob is not supported for response type.");
}
function _toJsonBody(body, format = "JSON") {
  if (typeof ArrayBuffer !== "undefined" && body instanceof ArrayBuffer) {
    throw new Error(`Automatic conversion to ${format} is not supported for ArrayBuffers.`);
  }
  if (typeof Blob !== "undefined" && body instanceof Blob) {
    throw new Error(`Automatic conversion to ${format} is not supported for Blobs.`);
  }
  if (typeof body === "string" || typeof body === "number" || typeof body === "object" || typeof body === "boolean" || Array.isArray(body)) {
    return body;
  }
  throw new Error(`Automatic conversion to ${format} is not supported for response type.`);
}
function _toTextBody(body) {
  if (typeof body === "string") {
    return body;
  }
  if (typeof ArrayBuffer !== "undefined" && body instanceof ArrayBuffer) {
    throw new Error("Automatic conversion to text is not supported for ArrayBuffers.");
  }
  if (typeof Blob !== "undefined" && body instanceof Blob) {
    throw new Error("Automatic conversion to text is not supported for Blobs.");
  }
  return JSON.stringify(_toJsonBody(body, "text"));
}
function _maybeConvertBody(responseType, body) {
  if (body === null) {
    return null;
  }
  switch (responseType) {
    case "arraybuffer":
      return _toArrayBufferBody(body);
    case "blob":
      return _toBlob(body);
    case "json":
      return _toJsonBody(body);
    case "text":
      return _toTextBody(body);
    default:
      throw new Error(`Unsupported responseType: ${responseType}`);
  }
}
var HttpClientTestingBackend = class _HttpClientTestingBackend {
  open = [];
  isTestingBackend = true;
  handle(req) {
    return new Observable((observer) => {
      const testReq = new TestRequest(req, observer);
      this.open.push(testReq);
      observer.next({
        type: HttpEventType.Sent
      });
      return () => {
        testReq._cancelled = true;
      };
    });
  }
  _match(match) {
    if (typeof match === "string") {
      return this.open.filter((testReq) => testReq.request.urlWithParams === match);
    } else if (typeof match === "function") {
      return this.open.filter((testReq) => match(testReq.request));
    } else {
      return this.open.filter((testReq) => (!match.method || testReq.request.method === match.method.toUpperCase()) && (!match.url || testReq.request.urlWithParams === match.url));
    }
  }
  match(match) {
    const results = this._match(match);
    results.forEach((result) => {
      const index = this.open.indexOf(result);
      if (index !== -1) {
        this.open.splice(index, 1);
      }
    });
    return results;
  }
  expectOne(match, description) {
    description ||= this.descriptionFromMatcher(match);
    const matches = this.match(match);
    if (matches.length > 1) {
      throw new Error(`Expected one matching request for criteria "${description}", found ${matches.length} requests.`);
    }
    if (matches.length === 0) {
      let message = `Expected one matching request for criteria "${description}", found none.`;
      if (this.open.length > 0) {
        const requests = this.open.map(describeRequest).join(", ");
        message += ` Requests received are: ${requests}.`;
      }
      throw new Error(message);
    }
    return matches[0];
  }
  expectNone(match, description) {
    description ||= this.descriptionFromMatcher(match);
    const matches = this.match(match);
    if (matches.length > 0) {
      throw new Error(`Expected zero matching requests for criteria "${description}", found ${matches.length}.`);
    }
  }
  verify(opts = {}) {
    let open = this.open;
    if (opts.ignoreCancelled) {
      open = open.filter((testReq) => !testReq.cancelled);
    }
    if (open.length > 0) {
      const requests = open.map(describeRequest).join(", ");
      throw new Error(`Expected no open requests, found ${open.length}: ${requests}`);
    }
  }
  descriptionFromMatcher(matcher) {
    if (typeof matcher === "string") {
      return `Match URL: ${matcher}`;
    } else if (typeof matcher === "object") {
      const method = matcher.method || "(any)";
      const url = matcher.url || "(any)";
      return `Match method: ${method}, URL: ${url}`;
    } else {
      return `Match by function: ${matcher.name}`;
    }
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _HttpClientTestingBackend,
    deps: [],
    target: FactoryTarget.Injectable
  });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _HttpClientTestingBackend
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: HttpClientTestingBackend,
  decorators: [{
    type: Injectable
  }]
});
function describeRequest(testRequest) {
  const url = testRequest.request.urlWithParams;
  const method = testRequest.request.method;
  return `${method} ${url}`;
}
function provideHttpClientTesting() {
  return [HttpClientTestingBackend, {
    provide: HttpBackend,
    useExisting: HttpClientTestingBackend
  }, {
    provide: HttpTestingController,
    useExisting: HttpClientTestingBackend
  }, {
    provide: REQUESTS_CONTRIBUTE_TO_STABILITY,
    useValue: false
  }];
}
var HttpClientTestingModule = class _HttpClientTestingModule {
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _HttpClientTestingModule,
    deps: [],
    target: FactoryTarget.NgModule
  });
  static \u0275mod = \u0275\u0275ngDeclareNgModule({
    minVersion: "14.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _HttpClientTestingModule,
    imports: [HttpClientModule]
  });
  static \u0275inj = \u0275\u0275ngDeclareInjector({
    minVersion: "12.0.0",
    version: "22.2.1",
    ngImport: core_exports,
    type: _HttpClientTestingModule,
    providers: [provideHttpClientTesting()],
    imports: [HttpClientModule]
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.1",
  ngImport: core_exports,
  type: HttpClientTestingModule,
  decorators: [{
    type: NgModule,
    args: [{
      imports: [HttpClientModule],
      providers: [provideHttpClientTesting()]
    }]
  }]
});

// src/app/services/api.service.spec.ts
describe("ApiService", () => {
  let service;
  let httpMock;
  let routerSpy;
  let snackbarSpy;
  beforeEach(() => {
    routerSpy = jasmine.createSpyObj("Router", ["navigate"]);
    snackbarSpy = jasmine.createSpyObj("SnackbarService", ["showWarning"]);
    TestBed.configureTestingModule({
      providers: [
        ApiService,
        { provide: Router, useValue: routerSpy },
        { provide: SnackbarService, useValue: snackbarSpy },
        provideHttpClient(withXhr()),
        provideHttpClientTesting()
      ]
    });
    service = TestBed.inject(ApiService);
    httpMock = TestBed.inject(HttpTestingController);
  });
  afterEach(() => httpMock.verify());
  describe("get", () => {
    it("issues a GET to the API prefix and returns the body", () => {
      let result;
      service.get("users").subscribe((r) => result = r);
      const req = httpMock.expectOne(service.apiPrefix + "users");
      expect(req.request.method).toBe("GET");
      expect(req.request.withCredentials).toBeTrue();
      req.flush({ ok: 1 });
      expect(result).toEqual({ ok: 1 });
    });
    it("strips a leading slash from the URL", () => {
      service.get("/nodes").subscribe();
      const req = httpMock.expectOne(service.apiPrefix + "nodes");
      expect(req.request.url).toBe(service.apiPrefix + "nodes");
      req.flush([]);
    });
  });
  describe("post", () => {
    it("pre-fetches a CSRF token, then POSTs with the X-CSRF-Token header and JSON body", () => {
      let result;
      service.post("add", { a: 1 }).subscribe((r) => result = r);
      const csrfReq = httpMock.expectOne(service.apiPrefix + "csrf");
      expect(csrfReq.request.method).toBe("GET");
      csrfReq.flush({ csrf_token: "tok-123" });
      const postReq = httpMock.expectOne(service.apiPrefix + "add");
      expect(postReq.request.method).toBe("POST");
      expect(postReq.request.headers.get("X-CSRF-Token")).toBe("tok-123");
      expect(postReq.request.headers.get("Content-Type")).toBe("application/json");
      expect(postReq.request.body).toBe(JSON.stringify({ a: 1 }));
      postReq.flush(true);
      expect(result).toBeTrue();
    });
    it("sends a RawJson body verbatim (no re-stringify)", () => {
      const raw = '{"keep":"exact-bytes"}';
      service.post("cfg", raw, new RequestOptions({ requestType: RequestTypes.RawJson })).subscribe();
      httpMock.expectOne(service.apiPrefix + "csrf").flush({ csrf_token: "t" });
      const req = httpMock.expectOne(service.apiPrefix + "cfg");
      expect(req.request.body).toBe(raw);
      req.flush(true);
    });
  });
  describe("successHandler", () => {
    it('throws when the body is the "manager token is null" sentinel', () => {
      let errored = false;
      service.get("x", new RequestOptions({ responseType: ResponseTypes.Text })).subscribe({ error: () => errored = true });
      httpMock.expectOne(service.apiPrefix + "x").flush("manager token is null");
      expect(errored).toBeTrue();
    });
  });
  describe("errorHandler", () => {
    it("redirects to login and errors on a 401 when auth is not ignored", () => {
      let errored = false;
      service.get("secure").subscribe({ error: () => errored = true });
      httpMock.expectOne(service.apiPrefix + "secure").flush("nope", { status: 401, statusText: "Unauthorized" });
      expect(routerSpy.navigate).toHaveBeenCalledWith(["login"], { replaceUrl: true });
      expect(errored).toBeTrue();
    });
    it("does not redirect on a 401 when auth is ignored", () => {
      service.get("secure", new RequestOptions({ ignoreAuth: true })).subscribe({ error: () => void 0 });
      httpMock.expectOne(service.apiPrefix + "secure").flush("nope", { status: 401, statusText: "Unauthorized" });
      expect(routerSpy.navigate).not.toHaveBeenCalled();
    });
    it("redirects to the vpn login when a vpn key is supplied on a 401", () => {
      service.get("secure", new RequestOptions({ vpnKeyForAuth: "VPNPK" })).subscribe({ error: () => void 0 });
      httpMock.expectOne(service.apiPrefix + "secure").flush("nope", { status: 401, statusText: "Unauthorized" });
      expect(routerSpy.navigate).toHaveBeenCalledWith(["vpnlogin", "VPNPK"], { replaceUrl: true });
    });
  });
});
describe("ApiService parked changes", () => {
  let service;
  let httpMock;
  let snackbarSpy;
  beforeEach(() => {
    snackbarSpy = jasmine.createSpyObj("SnackbarService", ["showWarning"]);
    TestBed.configureTestingModule({
      providers: [
        ApiService,
        { provide: Router, useValue: jasmine.createSpyObj("Router", ["navigate"]) },
        { provide: SnackbarService, useValue: snackbarSpy },
        provideHttpClient(withXhr()),
        provideHttpClientTesting()
      ]
    });
    service = TestBed.inject(ApiService);
    httpMock = TestBed.inject(HttpTestingController);
  });
  afterEach(() => httpMock.verify());
  it("says a parked change applies when the visor is back", fakeAsync(() => {
    let result;
    service.put("visors/pk/apps/x", { autostart: true }).subscribe((r) => result = r);
    httpMock.expectOne(service.apiPrefix + "csrf").flush({ csrf_token: "t" });
    httpMock.expectOne(service.apiPrefix + "visors/pk/apps/x").flush({ name: "x" }, { status: 202, statusText: "Accepted", headers: { "X-Skywire-Parked": "host" } });
    tick();
    expect(result).toEqual({ name: "x" });
    expect(snackbarSpy.showWarning).toHaveBeenCalledWith("common.change-parked");
  }));
  it("says nothing for an ordinary 202", fakeAsync(() => {
    service.post("visors/pk/restart").subscribe();
    httpMock.expectOne(service.apiPrefix + "csrf").flush({ csrf_token: "t" });
    httpMock.expectOne(service.apiPrefix + "visors/pk/restart").flush({ restarting: true }, { status: 202, statusText: "Accepted" });
    tick();
    expect(snackbarSpy.showWarning).not.toHaveBeenCalled();
  }));
});
//# debugId=0d7f7857-827c-51a0-99ec-9224a924058f
//# sourceMappingURL=spec-app-services-api.service.spec.js.map
