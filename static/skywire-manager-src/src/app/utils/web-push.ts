/** What the page offers for Web Push, as pushHint reads it. */
export interface PushEnv {
  secure: boolean;
  serviceWorker: boolean;
  pushManager: boolean;
  ios: boolean;
  standalone: boolean;
  host: string;
}

export function pushEnv(): PushEnv {
  const nav: any = navigator;
  const ios = /iPad|iPhone|iPod/.test(nav.userAgent) || (nav.platform === 'MacIntel' && nav.maxTouchPoints > 1);

  return {
    secure: window.isSecureContext,
    serviceWorker: 'serviceWorker' in nav,
    pushManager: 'PushManager' in window,
    ios: ios,
    standalone: nav.standalone === true || window.matchMedia('(display-mode: standalone)').matches,
    host: location.host,
  };
}

/** Why this page cannot subscribe, or '' when it can. */
export function pushHint(e: PushEnv): string {
  if (e.ios && !e.standalone) {
    return 'On iPhone and iPad, first add this page to the Home Screen (Share, then Add to Home Screen), ' +
      'open it from there and press Notify this device again. Needs iOS 16.4 or later.';
  }
  if (!e.secure) {
    return `Push needs a secure page, and http://${e.host} is not one. Open the UI at http://localhost ` +
      '(for example through an SSH tunnel) or over https on this device.';
  }
  if (!e.serviceWorker || !e.pushManager) {
    return 'This browser does not support push notifications.';
  }

  return '';
}

/** The applicationServerKey for a base64url VAPID public key. */
export function vapidKeyBytes(b64url: string): Uint8Array {
  const b64 = b64url.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - b64url.length % 4) % 4);
  const raw = atob(b64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) {
    out[i] = raw.charCodeAt(i);
  }

  return out;
}

/** A short name for this device, such as "iPhone Safari". */
export function deviceLabel(ua: string): string {
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' :
    /Windows/.test(ua) ? 'Windows' : /Mac OS X|Macintosh/.test(ua) ? 'Mac' : /Linux/.test(ua) ? 'Linux' : 'Device';
  const br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /(Chrome|CriOS)\//.test(ua) ? 'Chrome' :
    /Safari\//.test(ua) ? 'Safari' : '';

  return br ? os + ' ' + br : os;
}

/**
 * The worker that receives the pushes. A page under a desk already has a root
 * worker that carries the push handler (pkg/wasmhv/sw.js), and registering
 * another at that scope would replace it, so it is used. Otherwise the
 * dashboard registers push-sw.js, which handles nothing else.
 */
export async function pushRegistration(): Promise<ServiceWorkerRegistration> {
  const sw = navigator.serviceWorker;
  const existing = await sw.getRegistration('/');
  if (existing) {
    return existing;
  }
  await sw.register('../push-sw.js');

  return sw.ready;
}

function sameKey(a: ArrayBuffer | null, b: Uint8Array): boolean {
  if (!a) {
    return false;
  }
  const x = new Uint8Array(a);

  return x.length === b.length && x.every((v, i) => v === b[i]);
}

/**
 * Asks for permission and subscribes this browser with key, replacing a
 * subscription made with another key. Returns PushSubscription.toJSON().
 */
export async function subscribePush(publicKey: string): Promise<any> {
  const perm = await Notification.requestPermission();
  if (perm !== 'granted') {
    throw new Error('Notifications are blocked for this page. Allow them in the browser settings and try again.');
  }
  const reg = await pushRegistration();
  const key = vapidKeyBytes(publicKey);
  let sub = await reg.pushManager.getSubscription();
  if (sub && !sameKey(sub.options.applicationServerKey, key)) {
    await sub.unsubscribe();
    sub = null;
  }
  if (!sub) {
    sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key as BufferSource });
  }

  return sub.toJSON();
}

/** This browser's current push endpoint, or '' when it has none. */
export async function currentPushEndpoint(): Promise<string> {
  if (!('serviceWorker' in navigator)) {
    return '';
  }
  const reg = await navigator.serviceWorker.getRegistration('/');
  const sub = reg && reg.pushManager ? await reg.pushManager.getSubscription() : null;

  return sub ? sub.endpoint : '';
}
