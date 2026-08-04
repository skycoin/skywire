import { AfterViewInit, Component, ElementRef, OnDestroy, OnInit, ViewChild } from '@angular/core';

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
  @ViewChild('graph', { static: false }) graphEl!: ElementRef<HTMLDivElement>;

  tabsData: TabButtonData[] = [];
  loading = true;
  error: string | null = null;

  private handle: { unmount(): void } | null = null;
  private static readonly BUNDLE_ID = 'tpviz-bundle-script';
  private static readonly BUNDLE_SRC = 'tp-viz/bundle.js';

  constructor() {
    super();
    this.tabsData = homeTabsData();
  }

  ngOnInit() {
    return super.ngOnInit();
  }

  ngAfterViewInit() {
    this.loadAndMount();
  }

  private loadAndMount(): void {
    const w = window as any;
    const doMount = () => {
      if (!w.SkywireTpviz || !this.graphEl) {
        this.error = 'transport-graph bundle unavailable';
        this.loading = false;
        return;
      }
      try {
        this.handle = w.SkywireTpviz.mount(this.graphEl.nativeElement);
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
      existing.addEventListener('error', () => { this.error = 'could not load transport-graph bundle'; this.loading = false; });
      return;
    }
    const s = document.createElement('script');
    s.id = NetworkVisualizerComponent.BUNDLE_ID;
    s.src = NetworkVisualizerComponent.BUNDLE_SRC;
    s.async = true;
    s.onload = () => doMount();
    s.onerror = () => { this.error = 'could not load transport-graph bundle'; this.loading = false; };
    document.body.appendChild(s);
  }

  ngOnDestroy(): void {
    if (this.handle) {
      try { this.handle.unmount(); } catch { /* ignore */ }
      this.handle = null;
    }
  }
}
