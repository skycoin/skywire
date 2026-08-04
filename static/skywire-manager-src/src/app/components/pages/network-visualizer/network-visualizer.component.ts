import { AfterViewInit, Component, ElementRef, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { Subscription } from 'rxjs';

import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * NetworkVisualizerComponent — hosts the shared `pkg/tpviz` transport-graph UI
 * inside the SPA tab by MOUNTING its bundle into this component's <div> (no
 * iframe, no /tp-viz/ break-out). This is the pilot for the GUI embedding
 * standardization (docs/design/gui-embedding-standardization.md): one
 * implementation of the visualizer (the tpviz renderer with its sidebar,
 * filters, grouping and Globe/Flat/WebGL views), presented standalone at
 * /tp-viz/ AND embedded here via SkywireTpviz.mount().
 *
 * Replaces the former in-Angular vis-network reimplementation.
 */
@Component({
  selector: 'app-network-visualizer',
  templateUrl: './network-visualizer.component.html',
  styleUrls: ['./network-visualizer.component.scss'],
  standalone: false,
})
export class NetworkVisualizerComponent extends PageBaseComponent implements OnInit, AfterViewInit, OnDestroy {
  private static readonly BUNDLE_ID = 'tpviz-bundle-script';
  private static readonly BUNDLE_SRC = 'tp-viz/bundle.js';

  @ViewChild('graph', { static: false }) graphEl!: ElementRef<HTMLDivElement>;

  tabsData: TabButtonData[] = [];
  loading = true;
  error: string | null = null;
  // Set when the transport-graph bundle isn't served on this surface (e.g. the
  // in-browser wasm visor, which doesn't serve /tp-viz/). This is expected
  // degradation, not a failure, so it renders as a calm info note rather than a
  // red error.
  unavailable = false;

  private handle: { unmount(): void } | null = null;
  private routeSub?: Subscription;

  constructor(private route: ActivatedRoute, private router: Router) {
    super();
    this.tabsData = homeTabsData();
  }

  ngOnInit() {
    return super.ngOnInit();
  }

  ngAfterViewInit() {
    this.loadAndMount();
    // React to ?view= changes while the tab stays mounted (Angular reuses the
    // component, so a link to a different view wouldn't otherwise re-apply).
    this.routeSub = this.route.queryParamMap.subscribe((pm) => {
      const v = pm.get('view');
      const w = window as any;
      if (v && w.SkywireTpviz && typeof w.SkywireTpviz.setView === 'function') {
        w.SkywireTpviz.setView(v);
      }
    });
  }

  private loadAndMount(): void {
    const w = window as any;
    const doMount = () => {
      if (!w.SkywireTpviz || !this.graphEl) {
        this.unavailable = true;
        this.loading = false;

        return;
      }
      try {
        // Deep-linkable view: seed from ?view= and reflect switches back into the
        // route query so a specific mode (globe/flat/webgl) is linkable.
        const view = this.route.snapshot.queryParamMap.get('view') || undefined;
        this.handle = w.SkywireTpviz.mount(this.graphEl.nativeElement, {
          view: view,
          onViewChange: (v: string) => {
            this.router.navigate([], { relativeTo: this.route, queryParams: { view: v }, queryParamsHandling: 'merge', replaceUrl: true });
          },
        });
        this.loading = false;
      } catch (e: any) {
        this.error = e?.message || 'failed to mount transport-graph';
        this.loading = false;
      }
    };

    if (w.SkywireTpviz) {
      doMount();

      return;
    }
    const existing = document.getElementById(NetworkVisualizerComponent.BUNDLE_ID) as HTMLScriptElement | null;
    if (existing) {
      existing.addEventListener('load', doMount);
      existing.addEventListener('error', () => {
 this.unavailable = true; this.loading = false;
});

      return;
    }
    const s = document.createElement('script');
    s.id = NetworkVisualizerComponent.BUNDLE_ID;
    s.src = NetworkVisualizerComponent.BUNDLE_SRC;
    s.async = true;
    s.onload = () => doMount();
    s.onerror = () => {
 this.unavailable = true; this.loading = false;
};
    document.body.appendChild(s);
  }

  ngOnDestroy(): void {
    this.routeSub?.unsubscribe();
    if (this.handle) {
      try {
 this.handle.unmount(); 
} catch { /* ignore */ }
      this.handle = null;
    }
  }
}
