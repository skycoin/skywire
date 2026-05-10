import { Component, OnInit, OnDestroy } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { Subscription, interval, startWith } from 'rxjs';
import { switchMap, catchError } from 'rxjs/operators';
import { of } from 'rxjs';

import { Node, Route } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { TrafficData } from 'src/app/services/single-node-data.service';
import { RouteService } from 'src/app/services/route.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

// RouteGroupInfo as exposed by the visor's /routegroups endpoint
// (pkg/visor/api_routing.go). Only the fields surfaced by the UI are
// declared; extras like FwdNextTpID are kept as `any` so backend
// additions don't break TypeScript.
interface RouteGroupHop {
  tp_id?: string;
  src_pk?: string;
  dst_pk?: string;
}
interface RouteGroupInfo {
  desc?: {
    src_pk?: string;
    dst_pk?: string;
    src_port?: number;
    dst_port?: number;
  };
  fwd_rule_id?: number;
  consume_rule_id?: number;
  fwd_next_tp_id?: string;
  initiator?: boolean;
  hops?: RouteGroupHop[];
}

type RoutingView = 'rules' | 'groups';

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

  // Active sub-view of the routing tab. Rules is the default
  // because the existing route list lives there; Groups is the
  // higher-value diagnostic view (matches `rg ls`) so the toggle
  // is prominent.
  activeView: RoutingView = 'rules';
  routeGroups: RouteGroupInfo[] = [];
  routeGroupsLoading = false;
  routeGroupsError: string | null = null;

  private dataSubscription: Subscription;
  private trafficSubscription: Subscription;
  private saveRouterSubscription: Subscription;
  private groupsSubscription: Subscription;

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
    this.groupsSubscription?.unsubscribe();
  }

  setView(v: RoutingView) {
    if (v === this.activeView) { return; }
    this.activeView = v;
    if (v === 'groups' && !this.groupsSubscription) {
      this.startGroupsPolling();
    }
  }

  private startGroupsPolling() {
    if (!this.nodePK) { return; }
    // Active route groups change on tp churn. 5s is a reasonable
    // poll cadence — frequent enough to feel live, slow enough to
    // not hammer the visor's RPC.
    this.groupsSubscription = interval(5000).pipe(
      startWith(0),
      switchMap(() => this.routeService.routeGroups(this.nodePK).pipe(
        catchError((err) => {
          this.routeGroupsError = err?.message || 'Failed to fetch route groups';
          this.routeGroupsLoading = false;
          return of(null);
        }),
      )),
    ).subscribe((rgs: any) => {
      if (rgs == null) { return; }
      this.routeGroupsError = null;
      this.routeGroupsLoading = false;
      this.routeGroups = Array.isArray(rgs) ? rgs : [];
    });
    this.routeGroupsLoading = true;
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
