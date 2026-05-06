import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Application, Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Apps tab — list view + sub-tabs for the per-app management
 * surfaces (Skychat, VPN, Skysocks). Used to be three separate
 * top-level tabs in the per-visor tab bar; consolidated here so
 * the row stops overflowing horizontally on default zoom.
 */
@Component({
    selector: 'app-apps',
    templateUrl: './apps.component.html',
    styleUrls: ['./apps.component.scss'],
    standalone: false
})
export class AppsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  apps: Application[];
  nodePK: string;
  nodeIp: string;

  // Active sub-tab. 'list' (default) is the apps table; the other
  // three render the per-app management surfaces inline.
  sub: 'list' | 'chat' | 'vpn' | 'skysocks' = 'list';

  private dataSubscription: Subscription;

  ngOnInit() {
    // Get the node data from the parent page.
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.apps = node.apps;
      this.nodeIp = node.ip;
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
  }
}
