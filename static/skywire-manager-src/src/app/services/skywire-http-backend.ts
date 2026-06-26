import { Injectable } from '@angular/core';
import {
  HttpBackend,
  HttpRequest,
  HttpEvent,
  HttpResponse,
  HttpErrorResponse,
  HttpHeaders,
  HttpXhrBackend,
} from '@angular/common/http';
import { Observable, from, of, throwError } from 'rxjs';
import { switchMap } from 'rxjs/operators';

/**
 * Response shape returned by the in-browser visor gateway
 * (globalThis.skywireVisor.hvApi / skywireDmsg.hvApi / skywireDmsg.fetch).
 */
interface GatewayResponse {
  status: number;
  body: Uint8Array;
  headers?: { [k: string]: string };
}

/**
 * SkywireHttpBackend is the bottom of the Angular HttpClient pipeline (below
 * interceptors). It lets the SAME hypervisor UI build talk to either backend:
 *
 *   - native (visor-served): a transparent pass-through to the real XHR backend —
 *     byte-for-byte the previous behavior. This is the default whenever no
 *     hypervisor mode is configured, so the ordinary visor-served build is
 *     unaffected.
 *   - in-tab wasm-visor (window.__SKYWIRE_HV__.visor): route /api/* to the
 *     in-wasm core via globalThis.skywireVisor.hvApi(method, path, body).
 *   - standalone hypervisor (.standalone): route /api/* to skywireDmsg.hvApi.
 *   - remote viewer (.pk): fetch /api/* over dmsg from a remote hypervisor PK.
 *
 * This replaces the override.js fetch/XHR monkey-patch's *routing* with an
 * Angular-native, DI-injected, unit-testable seam. (override.js's other duties —
 * booting the wasm client and serving inlined assets for the single-file build —
 * remain generator concerns and are out of scope here.)
 *
 * ApiService and every feature service stay unchanged; only the transport swaps.
 */
@Injectable()
export class SkywireHttpBackend implements HttpBackend {
  private readonly cfg: any =
    (typeof window !== 'undefined' && (window as any).__SKYWIRE_HV__) || {};

  // active = "is there a wasm/dmsg mode to take over for?" When false (the
  // visor-served build, or an explicit `served` flag), this backend is a
  // transparent delegate to the real XHR transport.
  private readonly active =
    !this.cfg.served &&
    Boolean(this.cfg.pk || this.cfg.standalone || this.cfg.visor);

  constructor(private xhr: HttpXhrBackend) {}

  handle(req: HttpRequest<any>): Observable<HttpEvent<any>> {
    // Native mode, or a non-backend request (assets/i18n/images): delegate to the
    // real XHR backend. The wasm-visor serves the bundle + assets normally, so no
    // asset-inlining path is needed here.
    if (!this.active || !this.isApi(req.urlWithParams)) {
      return this.xhr.handle(req);
    }
    return from(this.dispatch(req)).pipe(switchMap(r => this.toEvent(req, r)));
  }

  private isApi(url: string): boolean {
    const p = this.pathOf(url);
    return p === '/api' || p.indexOf('/api/') === 0;
  }

  private pathOf(url: string): string {
    try {
      const u = new URL(url, location.href);
      return u.pathname + u.search;
    } catch (e) {
      return url;
    }
  }

  private headersObj(req: HttpRequest<any>): { [k: string]: string } {
    const out: { [k: string]: string } = {};
    for (const k of req.headers.keys()) {
      const v = req.headers.get(k);
      if (v != null) {
        out[k] = v;
      }
    }
    return out;
  }

  /** dispatch routes one /api request to the configured gateway. */
  private async dispatch(req: HttpRequest<any>): Promise<GatewayResponse> {
    const w = window as any;
    // Wait for the in-tab visor/dmsg client to finish booting before the first
    // /api call. The boot bootstrap exposes its boot promise as
    // window.__SKYWIRE_HV__.ready; absent it, assume the client is already up.
    if (this.cfg.ready && typeof this.cfg.ready.then === 'function') {
      try {
        await this.cfg.ready;
      } catch (e) {
        // boot failed — fall through; the dispatch below surfaces a clear error.
      }
    }

    const method = req.method;
    const path = this.pathOf(req.urlWithParams);
    const raw = req.serializeBody();
    const body = raw == null ? null : String(raw);

    const synth = (r: any): GatewayResponse => ({
      status: r.status,
      body: r.body,
      // hvApi carries no response headers of its own; synthesize JSON.
      headers: r.headers || { 'Content-Type': 'application/json' },
    });

    if (this.cfg.visor && w.skywireVisor && w.skywireVisor.hvApi) {
      return w.skywireVisor.hvApi(method, path, body).then(synth);
    }
    if (this.cfg.standalone && w.skywireDmsg && w.skywireDmsg.hvApi) {
      return w.skywireDmsg.hvApi(method, path, body).then(synth);
    }
    if (w.skywireDmsg && w.skywireDmsg.fetch) {
      // viewer: /api over dmsg to a remote hypervisor PK.
      return w.skywireDmsg.fetch(this.cfg.pk, method, path, body, this.headersObj(req));
    }
    throw new Error('SkywireHttpBackend: no in-tab gateway available');
  }

  /** toEvent converts a GatewayResponse into the HttpResponse/HttpErrorResponse Angular expects. */
  private toEvent(req: HttpRequest<any>, r: GatewayResponse): Observable<HttpEvent<any>> {
    const text = new TextDecoder().decode(r.body || new Uint8Array());
    let body: any = text;
    if (req.responseType === 'json') {
      try {
        body = text === '' ? null : JSON.parse(text);
      } catch (e) {
        // Non-2xx with a non-JSON body is common (plain error strings); keep text.
        body = text;
      }
    }
    const headers = new HttpHeaders(r.headers || { 'Content-Type': 'application/json' });
    const url = req.urlWithParams;
    if (r.status >= 200 && r.status < 300) {
      return of(new HttpResponse({ body, status: r.status, statusText: 'OK', headers, url }));
    }
    return throwError(
      () => new HttpErrorResponse({ error: body, status: r.status, statusText: 'Error', headers, url }),
    );
  }
}
