import { Component, OnDestroy, Type, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';
import { Subscription } from 'rxjs';

import { SkychatComponent } from '../node/skychat/skychat.component';
import { LogsComponent } from '../node/logs/logs.component';

/**
 * How a given app name is mounted full-page:
 *  - `component`: a native Angular component rendered via ngComponentOutlet with
 *    its `embeddedNodeKey` input set to the visor PK (skychat, logs, …).
 *  - `iframe`: apps that are their own router sub-tree or a separately-served SPA
 *    are hosted in an iframe (vpn → the /#/vpn/<pk>/status route; wallet → the
 *    served skycoin-web app). The shared shell (top bar + hamburger) wraps either.
 */
interface AppMount {
  title: string;
  component?: Type<unknown>;
  iframe?: (pk: string) => string;
}

const APP_MOUNTS: Record<string, AppMount> = {
  skychat: { title: 'Skychat', component: SkychatComponent },
  logs: { title: 'Logs', component: LogsComponent },
  vpn: { title: 'VPN', iframe: (pk) => '/#/vpn/' + pk + '/status' },
  wallet: { title: 'Wallet', iframe: () => '/wallet/' },
};

/**
 * Unified full-page host for an app GUI, mounted at the top-level route
 * `#/app/<name>/<pk>`. Every app's "open full UI" navigates here (in-SPA, never a
 * new window / never the separately-served app URL), so the full-page views are
 * congruent: one shared shell with a hamburger whose Home item returns to the
 * visor list.
 */
@Component({
  selector: 'app-full-app-host',
  templateUrl: './full-app-host.component.html',
  styleUrls: ['./full-app-host.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class FullAppHostComponent implements OnDestroy {
  name = '';
  key = '';
  title = '';
  comp: Type<unknown> | null = null;
  compInputs: Record<string, unknown> = {};
  iframeUrl: SafeResourceUrl | null = null;
  unknownApp = false;

  private sub: Subscription;

  constructor(
    route: ActivatedRoute,
    private router: Router,
    private sanitizer: DomSanitizer,
    private changeDetectorRef: ChangeDetectorRef,
  ) {
    this.sub = route.paramMap.subscribe((p) => {
      this.name = (p.get('name') || '').toLowerCase();
      this.key = p.get('key') || '';
      const m = APP_MOUNTS[this.name];
      this.unknownApp = !m;
      this.title = m ? m.title : this.name;
      this.comp = null;
      this.iframeUrl = null;
      if (!m) {
        return;
      }
      if (m.component) {
        this.compInputs = { embeddedNodeKey: this.key };
        this.comp = m.component;
      } else if (m.iframe) {
        this.iframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(m.iframe(this.key));
      }
      this.changeDetectorRef.markForCheck();
    });
  }

  goHome(): void {
    this.router.navigate(['/nodes', 'list', '1']);
  }

  goNode(): void {
    if (this.key) {
      this.router.navigate(['/nodes', this.key, 'info']);
    }
  }

  ngOnDestroy(): void {
    this.sub.unsubscribe();
  }
}
