import { Component, OnDestroy, OnInit, ChangeDetectionStrategy, ChangeDetectorRef, inject } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';

/**
 * Visor "Resources" tab — host + process resource monitor on its
 * own page. Wraps app-resource-monitor with [expanded]=true so the
 * panel polls immediately when the user navigates here.
 */
@Component({
  selector: 'app-node-resources',
  templateUrl: './node-resources.component.html',
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class NodeResourcesComponent extends PageBaseComponent implements OnInit, OnDestroy {
  private changeDetectorRef = inject(ChangeDetectorRef);

  node!: Node;
  private dataSubscription!: Subscription;

  override ngOnInit() {
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
      this.changeDetectorRef.markForCheck();
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.dataSubscription) {
 this.dataSubscription.unsubscribe(); 
}
  }
}
