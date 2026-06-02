import { ErrorHandler, Injectable } from '@angular/core';

import { environment } from '../../environments/environment';

/**
 * Forwards browser-side errors to the hypervisor so they land in the
 * visor log (POST /api/client-log → "hvui_client" logger). This lets
 * an operator debug a broken UI page by reading ./local/log/skywire.log
 * instead of copy-pasting the dev-tools console.
 *
 * Sources captured: uncaught errors (window.onerror), unhandled promise
 * rejections, Angular's ErrorHandler, and console.error / console.warn.
 *
 * Everything here is best-effort and MUST NOT throw or recurse — a
 * reporter that breaks while reporting an error would be worse than
 * useless. Hence: a re-entrancy guard, a short dedupe window (a dead
 * page can loop the same error), and swallowed failures.
 */

// Mirror ApiService.apiPrefix so the beacon hits the same backend the
// rest of the app uses (dev http proxy vs the production 'api/' path).
const apiPrefix =
  !environment.production && location.protocol.indexOf('http:') !== -1
    ? 'http-api/'
    : 'api/';
const CLIENT_LOG_ENDPOINT = apiPrefix + 'client-log';

const MAX_FIELD = 4000;
const DEDUPE_MS = 3000;

let reporting = false;
const recent = new Map<string, number>();

function postReport(level: string, message: string, stack?: string, line?: number): void {
  if (reporting) {
    return; // never report an error raised while reporting
  }
  const now = Date.now();
  const key = level + '|' + message;
  const last = recent.get(key);
  if (last && now - last < DEDUPE_MS) {
    return; // drop the same message hammering within the window
  }
  recent.set(key, now);
  if (recent.size > 200) {
    recent.clear();
  }

  reporting = true;
  try {
    const body = JSON.stringify({
      level: level,
      message: String(message).slice(0, MAX_FIELD),
      stack: stack ? String(stack).slice(0, MAX_FIELD) : '',
      url: location.href,
      line: line || 0,
    });
    // sendBeacon is fire-and-forget and survives page unload; it also
    // never surfaces a response that could re-trigger the reporter.
    if (navigator.sendBeacon) {
      navigator.sendBeacon(CLIENT_LOG_ENDPOINT, new Blob([body], { type: 'application/json' }));
    } else {
      // keepalive so an in-flight report isn't cancelled on navigation.
      void fetch(CLIENT_LOG_ENDPOINT, {
        method: 'POST',
        body: body,
        headers: { 'Content-Type': 'application/json' },
        keepalive: true,
      }).catch(() => { /* swallow */ });
    }
  } catch {
    // never let the reporter throw
  } finally {
    reporting = false;
  }
}

function formatArg(a: any): string {
  if (a instanceof Error) {
    return a.message;
  }
  if (typeof a === 'string') {
    return a;
  }
  try {
    return JSON.stringify(a);
  } catch {
    return String(a);
  }
}

function pickStack(args: any[]): string | undefined {
  for (const a of args) {
    if (a instanceof Error && a.stack) {
      return a.stack;
    }
  }
  return undefined;
}

/**
 * Installs the non-Angular global hooks. Call once, as early as
 * possible (main.ts, before bootstrap) so it catches bootstrap-time
 * failures too.
 */
export function installClientErrorReporter(): void {
  window.addEventListener('error', (ev: ErrorEvent) => {
    postReport('error', ev.message || 'window.onerror', ev.error ? ev.error.stack : undefined, ev.lineno);
  });

  window.addEventListener('unhandledrejection', (ev: PromiseRejectionEvent) => {
    const reason: any = ev.reason;
    const msg = reason && reason.message ? reason.message : String(reason);
    postReport('error', 'unhandledrejection: ' + msg, reason ? reason.stack : undefined);
  });

  const wrap = (level: 'error' | 'warn', orig: (...a: any[]) => void) => {
    return (...args: any[]) => {
      try {
        postReport(level, args.map(formatArg).join(' '), pickStack(args));
      } catch {
        /* never break the console */
      }
      orig.apply(console, args);
    };
  };
  // Bind to keep the originals so devtools still shows everything.
  console.error = wrap('error', console.error.bind(console));
  console.warn = wrap('warn', console.warn.bind(console));
}

/**
 * Angular global error handler. Forwards uncaught Angular errors
 * (template / change-detection / DI failures) and preserves the
 * default console behavior.
 */
@Injectable()
export class ClientErrorHandler implements ErrorHandler {
  handleError(error: any): void {
    try {
      const msg = error && error.message ? error.message : String(error);
      postReport('error', 'angular: ' + msg, error ? error.stack : undefined);
    } catch {
      /* never break error handling */
    }
    // Default behavior — also forwarded via the console.error wrap,
    // deduped against the report above.
    console.error(error);
  }
}
