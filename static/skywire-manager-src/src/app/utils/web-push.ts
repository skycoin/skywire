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

/** Whether this page sits in a frame from another origin, such as a desk's browser. */
export function crossOriginFrame(w: Window = window): boolean {
  if (w.parent === w) {
    return false;
  }
  try {
    return !w.parent.location.href;
  } catch (e) {
    return true;
  }
}

/** Raised when no desk around the frame answers. */
class NoDesk extends Error {}

/**
 * Asks the desk around a cross-origin frame to do op for it. Such a frame cannot
 * ask for notification permission, so the desk subscribes its own worker with
 * this hypervisor's key (pkg/wasmhv/browse-transport.js). A frame with no desk
 * around it gets no ack and fails with NoDesk.
 */
export function askDesk(op: 'subscribe' | 'endpoint', key = '', ackMs = 1500): Promise<any> {
  const id = Math.random().toString(36).slice(2);

  return new Promise((resolve, reject) => {
    const done = (f: () => void) => {
      clearTimeout(timer);
      window.removeEventListener('message', onMessage);
      f();
    };
    const timer = setTimeout(() => done(() => reject(new NoDesk('no desk answered'))), ackMs);
    const onMessage = (e: MessageEvent) => {
      const d = e.data || {};
      if (e.source !== window.parent || d.type !== 'skywire-push-reply' || d.id !== id) {
        return;
      }
      if (d.ack) {
        clearTimeout(timer);
      } else if (d.error) {
        done(() => reject(new Error(d.error)));
      } else {
        done(() => resolve(d));
      }
    };
    window.addEventListener('message', onMessage);
    window.parent.postMessage({ type: 'skywire-push', id: id, op: op, key: key }, '*');
  });
}

function sameKey(a: ArrayBuffer | null, b: Uint8Array): boolean {
  if (!a) {
    return false;
  }
  const x = new Uint8Array(a);

  return x.length === b.length && x.every((v, i) => v === b[i]);
}

/** The desk's answer to op, or null outside a desk. */
async function viaDesk(op: 'subscribe' | 'endpoint', key = ''): Promise<any> {
  if (!crossOriginFrame()) {
    return null;
  }
  try {
    return await askDesk(op, key);
  } catch (e) {
    if (e instanceof NoDesk) {
      return null;
    }
    throw e;
  }
}

/**
 * Asks for permission and subscribes this browser with key, replacing a
 * subscription made with another key. Returns PushSubscription.toJSON().
 */
export async function subscribePush(publicKey: string): Promise<any> {
  const viaD = await viaDesk('subscribe', publicKey);
  if (viaD) {
    return viaD.sub;
  }
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
  const viaD = await viaDesk('endpoint').catch(() => null);
  if (viaD) {
    return viaD.endpoint || '';
  }
  if (!('serviceWorker' in navigator)) {
    return '';
  }
  const reg = await navigator.serviceWorker.getRegistration('/');
  const sub = reg && reg.pushManager ? await reg.pushManager.getSubscription() : null;

  return sub ? sub.endpoint : '';
}
