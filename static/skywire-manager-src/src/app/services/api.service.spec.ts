import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Router } from '@angular/router';

import { ApiService, RequestOptions, RequestTypes, ResponseTypes } from './api.service';

// ApiService is exercised through Angular's HttpTestingController so the real
// request pipeline (URL prefixing, CSRF pre-fetch, header/body encoding, the
// success/error handlers) runs, while no network is hit. Router is a spy so we
// can assert the auth redirects; NgZone comes from TestBed.
describe('ApiService', () => {
  let service: ApiService;
  let httpMock: HttpTestingController;
  let routerSpy: jasmine.SpyObj<Router>;

  beforeEach(() => {
    routerSpy = jasmine.createSpyObj('Router', ['navigate']);
    TestBed.configureTestingModule({
      providers: [
        ApiService,
        { provide: Router, useValue: routerSpy },
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    });
    service = TestBed.inject(ApiService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => httpMock.verify());

  describe('get', () => {
    it('issues a GET to the API prefix and returns the body', () => {
      let result: any;
      service.get('users').subscribe((r) => (result = r));

      const req = httpMock.expectOne(service.apiPrefix + 'users');
      expect(req.request.method).toBe('GET');
      expect(req.request.withCredentials).toBeTrue();
      req.flush({ ok: 1 });

      expect(result).toEqual({ ok: 1 });
    });

    it('strips a leading slash from the URL', () => {
      service.get('/nodes').subscribe();
      const req = httpMock.expectOne(service.apiPrefix + 'nodes');
      expect(req.request.url).toBe(service.apiPrefix + 'nodes');
      req.flush([]);
    });
  });

  describe('post', () => {
    it('pre-fetches a CSRF token, then POSTs with the X-CSRF-Token header and JSON body', () => {
      let result: any;
      service.post('add', { a: 1 }).subscribe((r) => (result = r));

      const csrfReq = httpMock.expectOne(service.apiPrefix + 'csrf');
      expect(csrfReq.request.method).toBe('GET');
      csrfReq.flush({ csrf_token: 'tok-123' });

      const postReq = httpMock.expectOne(service.apiPrefix + 'add');
      expect(postReq.request.method).toBe('POST');
      expect(postReq.request.headers.get('X-CSRF-Token')).toBe('tok-123');
      expect(postReq.request.headers.get('Content-Type')).toBe('application/json');
      expect(postReq.request.body).toBe(JSON.stringify({ a: 1 }));
      postReq.flush(true);

      expect(result).toBeTrue();
    });

    it('sends a RawJson body verbatim (no re-stringify)', () => {
      const raw = '{"keep":"exact-bytes"}';
      service.post('cfg', raw, new RequestOptions({ requestType: RequestTypes.RawJson })).subscribe();

      httpMock.expectOne(service.apiPrefix + 'csrf').flush({ csrf_token: 't' });
      const req = httpMock.expectOne(service.apiPrefix + 'cfg');
      expect(req.request.body).toBe(raw);
      req.flush(true);
    });
  });

  describe('successHandler', () => {
    it('throws when the body is the "manager token is null" sentinel', () => {
      let errored = false;
      service
        .get('x', new RequestOptions({ responseType: ResponseTypes.Text }))
        .subscribe({ error: () => (errored = true) });

      httpMock.expectOne(service.apiPrefix + 'x').flush('manager token is null');
      expect(errored).toBeTrue();
    });
  });

  describe('errorHandler', () => {
    it('redirects to login and errors on a 401 when auth is not ignored', () => {
      let errored = false;
      service.get('secure').subscribe({ error: () => (errored = true) });

      httpMock
        .expectOne(service.apiPrefix + 'secure')
        .flush('nope', { status: 401, statusText: 'Unauthorized' });

      expect(routerSpy.navigate).toHaveBeenCalledWith(['login'], { replaceUrl: true });
      expect(errored).toBeTrue();
    });

    it('does not redirect on a 401 when auth is ignored', () => {
      service
        .get('secure', new RequestOptions({ ignoreAuth: true }))
        .subscribe({ error: () => undefined });

      httpMock
        .expectOne(service.apiPrefix + 'secure')
        .flush('nope', { status: 401, statusText: 'Unauthorized' });

      expect(routerSpy.navigate).not.toHaveBeenCalled();
    });

    it('redirects to the vpn login when a vpn key is supplied on a 401', () => {
      service
        .get('secure', new RequestOptions({ vpnKeyForAuth: 'VPNPK' }))
        .subscribe({ error: () => undefined });

      httpMock
        .expectOne(service.apiPrefix + 'secure')
        .flush('nope', { status: 401, statusText: 'Unauthorized' });

      expect(routerSpy.navigate).toHaveBeenCalledWith(['vpnlogin', 'VPNPK'], { replaceUrl: true });
    });
  });
});
