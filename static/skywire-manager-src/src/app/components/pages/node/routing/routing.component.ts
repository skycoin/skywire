import { Component, OnInit, OnDestroy } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { Subscription } from 'rxjs';

import { Node, Route } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { TrafficData } from 'src/app/services/single-node-data.service';
import { RouteService } from 'src/app/services/route.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

/**
 * Routing tab content: routes list + traffic chart + the inline
 * Min-hops editor (moved here from the Info tab so the routing
 * surface is the single place for router configuration). The
 * transport list lives on the dedicated Transports tab now.
 */
@Component({
    selector: 'app-routing',
    templateUrl: './routing.component.html',
    styleUrls: ['./routing.component.scss'],
    standalone: false
})
export class RoutingComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  routes: Route[];
  nodePK: string;
  trafficData: TrafficData;

  // Inline router-config editor (replaces the dialog flow).
  showRouterForm = false;
  routerForm: UntypedFormGroup;

  private dataSubscription: Subscription;
  private trafficSubscription: Subscription;
  private saveRouterSubscription: Subscription;

  constructor(
    private formBuilder: UntypedFormBuilder,
    private routeService: RouteService,
    private snackbarService: SnackbarService,
  ) {
    super();
    this.routerForm = this.formBuilder.group({
      min: [1, Validators.compose([
        Validators.required,
        Validators.maxLength(3),
        Validators.pattern('^[0-9]+$'),
      ])],
    });
  }

  ngOnInit() {
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.nodePK = node.localPk;
      this.node = node;
      this.routes = node.routes;
    });
    this.trafficSubscription = NodeComponent.currentTrafficData.subscribe((td: TrafficData) => {
      this.trafficData = td;
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.trafficSubscription?.unsubscribe();
    this.saveRouterSubscription?.unsubscribe();
  }

  toggleRouterForm() {
    this.showRouterForm = !this.showRouterForm;
    if (this.showRouterForm && this.node) {
      this.routerForm.get('min').setValue(this.node.minHops);
    }
  }

  submitRouterConfig() {
    if (!this.routerForm.valid || !this.node) { return; }
    const min = parseInt(this.routerForm.get('min').value, 10);
    this.saveRouterSubscription = this.routeService.setMinHops(this.node.localPk, min).subscribe({
      next: () => {
        this.snackbarService.showDone('router-config.done');
        this.showRouterForm = false;
        NodeComponent.refreshCurrentDisplayedData();
      },
      error: (err: OperationError) => {
        err = processServiceError(err);
        this.snackbarService.showError(err);
      },
    });
  }
}
