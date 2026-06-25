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
  version = '';
  localPk = '';

  constructor(api: ApiService) {
    const cfg: any = (typeof window !== 'undefined' && (window as any).__SKYWIRE_HV__) || {};
    if (cfg.visor) {
      this.mode = 'Serverless visor';
    } else if (cfg.standalone) {
      this.mode = 'Standalone hypervisor';
    } else if (cfg.pk) {
      this.mode = 'Remote viewer';
    } else {
      this.mode = 'Native hypervisor';
    }
    this.serverless = !!(cfg.visor || cfg.standalone || cfg.pk);

    api.get('about').subscribe({
      next: (a: any) => {
        this.version = (a && a.build && a.build.version) || a?.version || '';
        this.localPk = (a && a.public_key) || '';
      },
      error: () => {},
    });
  }

  /** "Serverless visor · v1.3.78" — the badge shown where the home title was. */
  get badge(): string {
    return this.version ? `${this.mode} · ${this.version}` : this.mode;
  }

  /** The home/start page's title key — replaced by the mode badge in the top bar. */
  isStartTitle(titleParts: string[] | undefined): boolean {
    return !!titleParts && titleParts.length > 0 && titleParts[titleParts.length - 1] === 'start.title';
  }
}
