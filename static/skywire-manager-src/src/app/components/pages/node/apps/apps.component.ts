import { Component, OnInit, OnDestroy, ChangeDetectionStrategy, ChangeDetectorRef, inject } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs';

import { Application, Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

export type AppsSub = 'list' | 'catalog' | 'chat' | 'vpn' | 'skysocks';

/**
 * Apps tab — list view + sub-tabs for the per-app management
 * surfaces (Catalog, VPN, Skysocks). The catalog has its own route
 * (apps/catalog) and the others a ?sub= query, so every sub-tab has a link.
 */
@Component({
    selector: 'app-apps',
    templateUrl: './apps.component.html',
    styleUrls: ['./apps.component.scss'],
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush
})
export class AppsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  private changeDetectorRef = inject(ChangeDetectorRef);
  private route = inject(ActivatedRoute);

  apps!: Application[];
  nodePK!: string;
  nodeIp!: string | null;

  // Active sub-tab. 'list' (default) is the apps table.
  sub: AppsSub = 'list';
  private requestedSub: AppsSub = 'list';

  // True when the current node is an in-browser wasm visor, which can't run the
  // native VPN (TUN) or skysocks-server apps — so those sub-tabs are hidden for
  // it (Skychat runs in-process and stays). A REMOTE native visor managed from
  // the wasm hypervisor is NOT wasm, so it keeps all sub-tabs. Gated on the
  // node's arch, not the UI mode, for exactly that reason.
  wasmNode = false;

  private dataSubscription!: Subscription;
  private routeSubscription!: Subscription;

  override ngOnInit() {
    this.routeSubscription = this.route.queryParamMap.subscribe(q => {
      const fromQuery = q.get('sub');
      if (this.route.snapshot.data['sub'] === 'catalog') {
        this.requestedSub = 'catalog';
      } else if (fromQuery === 'vpn' || fromQuery === 'skysocks') {
        this.requestedSub = fromQuery;
      } else {
        this.requestedSub = 'list';
      }
      this.pickSub();
      this.changeDetectorRef.markForCheck();
    });

    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.apps = node.apps;
      this.nodeIp = node.ip;
      this.wasmNode = (node as any).arch === 'wasm';
      this.pickSub();
      this.changeDetectorRef.markForCheck();
    });

    return super.ngOnInit();
  }

  private pickSub() {
    const hidden = this.wasmNode && (this.requestedSub === 'vpn' || this.requestedSub === 'skysocks');
    this.sub = hidden ? 'list' : this.requestedSub;
  }

  /** The router link of a sub-tab. */
  subLink(sub: AppsSub): string[] {
    const base = ['/nodes', this.nodePK || NodeComponent.getCurrentNodeKey(), 'apps'];

    return sub === 'catalog' ? base.concat('catalog') : base;
  }

  subQuery(sub: AppsSub): { [k: string]: string } | null {
    return sub === 'vpn' || sub === 'skysocks' ? { sub: sub } : null;
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.routeSubscription.unsubscribe();
  }
}
