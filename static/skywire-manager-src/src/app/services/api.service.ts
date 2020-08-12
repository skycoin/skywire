import { Injectable, NgZone } from '@angular/core';
import { HttpClient, HttpErrorResponse, HttpHeaders } from '@angular/common/http';
import { Observable, throwError } from 'rxjs';
import { catchError, map } from 'rxjs/operators';
import { webSocket } from 'rxjs/webSocket';
import { Router } from '@angular/router';

import { processServiceError } from '../utils/errors';
import { environment } from 'src/environments/environment';

export enum ResponseTypes {
  Json = 'json',
  Text = 'text',
}

export enum RequestTypes {
  Json = 'json',
}

export class RequestOptions {
  responseType = ResponseTypes.Json;
  requestType = RequestTypes.Json;
  ignoreAuth = false;

  public constructor(init?: Partial<RequestOptions>) {
    Object.assign(this, init);
  }
}

/**
 * Allows to make requests to the backend API.
 */
@Injectable({
  providedIn: 'root'
})
export class ApiService {
  /**
   * URL prefix for the API routes. The 'http-api/' prefix is used if the app is running
   * with the dev server using the http protocol, because the dev server proxy uses it to
   * route the request to the appropiate url.
   */
  private readonly apiPrefix = !environment.production && location.protocol.indexOf('http:') !== -1 ?
    'http-api/' : 'api/';

  /**
   * Similar to apiPrefix, but for web sockets.
   */
  private readonly wsApiPrefix = !environment.production ?
    (location.protocol.indexOf('http:') !== -1 ? 'ws-api/' : 'wss-api/') :
    'api/';

  constructor(
    private http: HttpClient,
    private router: Router,
    private ngZone: NgZone,
  ) { }

  /**
   * Makes a request to a GET endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  get(url: string, options: RequestOptions = null): Observable<any> {
    return this.request('GET', url, {}, options);
  }

  /**
   * Makes a request to a POST endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  post(url: string, body: any = {}, options: RequestOptions = null): Observable<any> {
    return this.request('POST', url, body, options);
  }

  /**
   * Makes a request to a PUT endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  put(url: string, body: any = {}, options: RequestOptions = null): Observable<any> {
    return this.request('PUT', url, body, options);
  }

  /**
   * Makes a request to a DELETE endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  delete(url: string, options: RequestOptions = null): Observable<any> {
    return this.request('DELETE', url, {}, options);
  }

  /**
   * Makes a request to a WebSocket endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  ws(url: string, body: any = {}): Observable<any> {
    const wsProtocol = (location.protocol.startsWith('https')) ? 'wss://' : 'ws://';
    const wsUrl = wsProtocol + location.host + '/' + this.wsApiPrefix + url;
    const ws = webSocket(wsUrl);

    ws.next(body);
    return ws;
  }

  /**
   * Makes the actual call to the API.
   */
  private request(method: string, url: string, body: any, options: RequestOptions) {
    body = body ? body : {};
    options = options ? options : new RequestOptions();

    // Sanitize the URL.
    if (url.startsWith('/')) {
      url = url.substr(1, url.length - 1);
    }

    return this.http.request(method, this.apiPrefix + url, {
      ...this.getRequestOptions(options),
      responseType: options.responseType,
      // Use the session cookies.
      withCredentials: true,
      body: this.getPostBody(body, options),
    }).pipe(
      map(result => this.successHandler(result)),
      catchError(error => this.errorHandler(error, options)),
    );
  }

  /**
   * Process the options to use them whem making the reques.
   */
  private getRequestOptions(options: RequestOptions) {
    const requestOptions: any = {};

    requestOptions.headers = new HttpHeaders();

    if (options.requestType === RequestTypes.Json) {
      requestOptions.headers = requestOptions.headers.append('Content-Type', 'application/json');
    }

    return requestOptions;
  }

  /**
   * Encode the content to send it to the backend.
   */
  private getPostBody(body: any, options: RequestOptions) {
    if (options.requestType === RequestTypes.Json) {
      return JSON.stringify(body);
    }

    const formData = new FormData();

    Object.keys(body).forEach(key => formData.append(key, body[key]));

    return formData;
  }

  private successHandler(result: any) {
    if (typeof result === 'string' && result === 'manager token is null') {
      throw new Error(result);
    }

    return result;
  }

  private errorHandler(error: HttpErrorResponse, options: RequestOptions) {
    // Normally, if the problem was due to the session cookie being invalid, the
    // user is redirected to the login page.
    if (!options.ignoreAuth) {
      if (error.status === 401) {
        this.ngZone.run(() => this.router.navigate(['login'], { replaceUrl: true }));
      }

      if (error.error && typeof error.error === 'string' && error.error.includes('change password')) {
        this.ngZone.run(() => this.router.navigate(['login'], { replaceUrl: true }));
      }
    }

    return throwError(processServiceError(error));
  }
}
