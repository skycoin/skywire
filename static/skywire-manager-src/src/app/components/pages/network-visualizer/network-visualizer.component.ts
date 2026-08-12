import { Component, OnDestroy, OnInit, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { Subscription } from 'rxjs';

import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { MountableBundle } from 'src/app/shared/bundle-mount/bundle-mount.component';

/**
 * NetworkVisualizerComponent — hosts the shared `pkg/tpviz` transport-graph UI
 * inside the SPA tab by MOUNTING its bundle into an <app-bundle-mount> host (no
 * iframe, no /tp-viz/ break-out). This is the pilot for the GUI embedding
 * standardization (docs/design/gui-embedding-standardization.md): one
 * implementation of the visualizer (the tpviz renderer with its sidebar,
 * filters, grouping and Globe/Flat/WebGL views), presented standalone at
 * /tp-viz/ AND embedded here.
 *
 * The generic <app-bundle-mount> now owns script loading, the mount()/unmount()
 * lifecycle and the loading/unavailable/error states; this component keeps only
 * the tpviz-specific concern of deep-linking the active view via ?view=.
 *
 * Replaces the former in-Angular vis-network reimplementation.
 */
@Component({
  selector: 'app-network-visualizer',
  templateUrl: './network-visualizer.component.html',
  styleUrls: ['./network-visualizer.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class NetworkVisualizerComponent extends PageBaseComponent implements OnInit, OnDestroy {
  readonly bundleId = 'tpviz-bundle-script';
  readonly bundleSrc = 'tp-viz/bundle.js';

  tabsData: TabButtonData[] = [];

  // Options handed to SkywireTpviz.mount() through <app-bundle-mount>: seed the
  // view from ?view= and reflect switches back into the route so a specific mode
  // (globe/flat/webgl) stays linkable.
  mountOpts: Record<string, unknown>;

  private tpviz: MountableBundle & { setView?(v: string): void } | null = null;
  private routeSub?: Subscription;

  constructor(private route: ActivatedRoute, private router: Router, private changeDetectorRef: ChangeDetectorRef) {
    super();
    this.tabsData = homeTabsData();
    this.mountOpts = {
      view: this.route.snapshot.queryParamMap.get('view') || undefined,
      onViewChange: (v: string) => {
        this.router.navigate([], { relativeTo: this.route, queryParams: { view: v }, queryParamsHandling: 'merge', replaceUrl: true });
      },
    };
  }

  override ngOnInit() {
    // React to ?view= changes while the tab stays mounted (Angular reuses the
    // component, so a link to a different view wouldn't otherwise re-apply).
    this.routeSub = this.route.queryParamMap.subscribe((pm) => {
      const v = pm.get('view');
      if (v && this.tpviz && typeof this.tpviz.setView === 'function') {
        this.tpviz.setView(v);
      }
      this.changeDetectorRef.markForCheck();
    });

    return super.ngOnInit();
  }

  onReady(ev: { global: MountableBundle }): void {
    this.tpviz = ev.global as MountableBundle & { setView?(v: string): void };
  }

  ngOnDestroy(): void {
    this.routeSub?.unsubscribe();
  }
}
