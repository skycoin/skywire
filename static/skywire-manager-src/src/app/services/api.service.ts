import { Injectable, NgZone } from '@angular/core';
import { HttpClient, HttpErrorResponse, HttpHeaders } from '@angular/common/http';
import { Observable, throwError } from 'rxjs';
import { catchError, map } from 'rxjs/operators';
import { Router } from '@angular/router';

/**
 * Allows to make requests to the backend API.
 */
@Injectable({
  providedIn: 'root'
})
export class ApiService {
  constructor(
    private http: HttpClient,
    private router: Router,
    private ngZone: NgZone,
  ) { }

  /**
   * Makes a request to a GET endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  get(url: string, options: any = {}): Observable<any> {
    return this.request('GET', url, {}, options);
  }

  /**
   * Makes a request to a POST endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  post(url: string, body: any = {}, options: any = {}): Observable<any> {
    return this.request('POST', url, body, options);
  }

  /**
   * Makes a request to a PUT endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  put(url: string, body: any = {}, options: any = {}): Observable<any> {
    return this.request('PUT', url, body, options);
  }

  /**
   * Makes a request to a DELETE endpoint.
   * @param url Endpoint URL, after the "/api/" part.
   */
  delete(url: string, options: any = {}): Observable<any> {
    return this.request('DELETE', url, {}, options);
  }

  /**
   * Makes the actual call to the API.
   */
  private request(method: string, url: string, body: any, options: any) {
    body = body ? body : {};
    options = options ? options : {};

    return this.http.request(method, `api/${url}`, {
      ...this.getRequestOptions(options),
      responseType: options.responseType ? options.responseType : 'json',
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
  private getRequestOptions(options: any) {
    const requestOptions: any = {};

    requestOptions.headers = new HttpHeaders();

    if (options.type === 'json') {
      requestOptions.headers = requestOptions.headers.append('Content-Type', 'application/json');
    }

    if (options.params) {
      requestOptions.params = options.params;
    }

    return requestOptions;
  }

  /**
   * Encode the content to send it to the backend.
   */
  private getPostBody(body: any, options: any) {
    if (options.type === 'json') {
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

  private errorHandler(error: HttpErrorResponse, options: any) {
    // Normally, if the problem was due to the session cookie being invalid, the
    // user is redirected to the login page.
    if (!options.ignoreAuth) {
      if (error.status === 401) {
        this.ngZone.run(() => this.router.navigate(['login']));
      }

      if (error.error && typeof error.error === 'string' && error.error.includes('change password')) {
        this.ngZone.run(() => this.router.navigate(['login']));
      }
    }

    return throwError(error);
  }
}
