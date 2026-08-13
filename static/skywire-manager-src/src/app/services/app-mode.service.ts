import { Injectable } from '@angular/core';
import { ApiService } from './api.service';

/**
 * AppModeService surfaces WHICH backend the UI is talking to (so the user can
 * tell a serverless in-browser visor from a native visor-served hypervisor) plus
 * the visor version + the local hypervisor PK.
 *
 * The mode is read synchronously from window.__SKYWIRE_HV__ (set by hv-boot.js
 * before Angular boots); unset = the ordinary visor-served build = native.
 * version + localPk come from /api/about (routed to whichever backend is active
 * by SkywireHttpBackend).
 */
@Injectable({ providedIn: 'root' })
export class AppModeService {
  /** Human-readable backend mode shown in the top bar. */
  readonly mode: string;
  /** True for any in-browser/serverless mode (not the native visor-served UI). */
  readonly serverless: boolean;
  /**
   * True when the /ctl/* control surface is mounted (`hv serve --harness`, or
   * the dev harness). ctl-bridge.js is served only in that case and sets the
   * flag, so its presence is the signal rather than something inferred.
   */
  readonly harness: boolean;
  /**
   * The mark for this UI: the Skycoin cloud for a wasm visor, the Skywire icon
   * for anything served by a native visor. Used for BOTH the tab favicon and
   * the menu button, so the two never disagree about which UI you are looking
   * at — telling them apart at a glance is the whole point of having two.
   */
  get icon(): string {
    return this.mode === 'wasm visor'
      ? 'assets/img/wasm-visor-icon.png'
      : 'assets/img/skywire-icon.png';
  }
  version = '';
  localPk = '';

  constructor(api: ApiService) {
    const cfg: any = (typeof window !== 'undefined' && (window as any).__SKYWIRE_HV__) || {};
    if (cfg.visor) {
      // "wasm visor", not "serverless": serverless describes where it is NOT
      // running, which is true of the standalone hypervisor and the remote
      // viewer as well. What distinguishes this one is that the visor itself is
      // wasm executing in this tab, which is the thing an operator needs to know.
      this.mode = 'wasm visor';
    } else if (cfg.standalone) {
      this.mode = 'Standalone hypervisor';
    } else if (cfg.pk) {
      this.mode = 'Remote viewer';
    } else {
      this.mode = 'Native hypervisor';
    }
    this.serverless = !!(cfg.visor || cfg.standalone || cfg.pk);
    this.harness = typeof window !== 'undefined' && !!(window as any).__SKYWIRE_HARNESS__;

    // Tint the UI violet when it is provided BY a browser/wasm visor (the
    // serverless modes). _backgrounds.scss keys the violet gradient on this
    // class; native visor-served UIs keep the blue.
    if (this.serverless && typeof document !== 'undefined' && document.body) {
      document.body.classList.add('wasm-visor-ui');
    }

    // index.html is one file serving both UIs, so the wasm visor's favicon has
    // to be swapped at runtime. Doing it here rather than in a component keeps
    // it tied to the mode decision above — the tab icon and the menu mark are
    // then the same choice made once.
    if (cfg.visor && typeof document !== 'undefined') {
      const link: HTMLLinkElement =
        document.querySelector("link[rel~='icon']") || document.createElement('link');
      link.rel = 'icon';
      link.type = 'image/png';
      link.href = this.icon;
      if (!link.parentNode) {
        document.head.appendChild(link);
      }
    }

    api.get('about').subscribe({
      next: (a: any) => {
        this.version = (a && a.build && a.build.version) || a?.version || '';
        this.localPk = (a && a.public_key) || '';
      },
      error: () => {},
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
  get badge(): string {
    const parts = [this.mode];
    if (this.version) {
      parts.push(this.version);
    }
    if (this.harness) {
      parts.push('harness');
    }
    return parts.join(' · ');
  }

  /** The home/start page's title key — replaced by the mode badge in the top bar. */
  isStartTitle(titleParts: string[] | undefined): boolean {
    return !!titleParts && titleParts.length > 0 && titleParts[titleParts.length - 1] === 'start.title';
  }
}
