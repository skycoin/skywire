import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { NodeComponent } from '../../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { ApiService } from 'src/app/services/api.service';
import { TransportService } from 'src/app/services/transport.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

/**
 * Transports tab content for a single visor: a summary block with
 * total count + per-type breakdown + autoconnect / public-visor
 * toggles, followed by the full transport list. Replaces both the
 * old "preview on Routing tab" embed and the paginated full-list
 * page (showShortList=false renders the same table without slicing).
 */
@Component({
    selector: 'app-all-transports',
    templateUrl: './all-transports.component.html',
    styleUrls: ['./all-transports.component.scss'],
    standalone: false
})
export class AllTransportsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  transportStats: { total: number, byType: { type: string, count: number }[] } = { total: 0, byType: [] };
  isPublic = false;

  private dataSubscription: Subscription;
  private autoconnectSubscription: Subscription;
  private publicToggleSubscription: Subscription;

  constructor(
    private apiService: ApiService,
    private transportService: TransportService,
    private snackbarService: SnackbarService,
  ) {
    super();
  }

  ngOnInit() {
    this.dataSubscription = NodeComponent.currentNode.subscribe((node: Node) => {
      this.node = node;
      this.transportStats = this.computeTransportStats(node);
      if (node) {
        this.fetchPublicStatus(node.localPk);
      }
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.dataSubscription?.unsubscribe();
    this.autoconnectSubscription?.unsubscribe();
    this.publicToggleSubscription?.unsubscribe();
  }

  private computeTransportStats(node: Node): { total: number, byType: { type: string, count: number }[] } {
    if (!node || !node.transports) { return { total: 0, byType: [] }; }
    const counts: { [k: string]: number } = {};
    for (const tp of node.transports) { counts[tp.type] = (counts[tp.type] || 0) + 1; }
    const byType = Object.entries(counts)
      .map(([type, count]) => ({ type, count }))
      .sort((a, b) => b.count - a.count);
    return { total: node.transports.length, byType };
  }

  private fetchPublicStatus(pk: string) {
    this.apiService.get(`visors/${pk}/public`).subscribe((result: any) => {
      this.isPublic = !!(result && result.is_public === true);
    }, () => { this.isPublic = false; });
  }

  changeTransportsConfig() {
    if (!this.node) { return; }
    const next = !this.node.autoconnectTransports;
    this.autoconnectSubscription = this.transportService.changeAutoconnectSetting(
      this.node.localPk, next,
    ).subscribe(() => {
      this.snackbarService.showDone(
        next ? 'node.details.transports-info.enable-done' : 'node.details.transports-info.disable-done'
      );
      NodeComponent.refreshCurrentDisplayedData();
    }, (err: OperationError) => {
      err = processServiceError(err);
      this.snackbarService.showError(err);
    });
  }

  changePublicConfig() {
    if (!this.node) { return; }
    const next = !this.isPublic;
    this.publicToggleSubscription = this.apiService.put(`visors/${this.node.localPk}/public`, { is_public: next }).subscribe(() => {
      this.isPublic = next;
      this.snackbarService.showDone(
        next ? 'node.details.transports-info.public-enable-done' : 'node.details.transports-info.public-disable-done'
      );
    }, (err: OperationError) => {
      err = processServiceError(err);
      this.snackbarService.showError(err);
    });
  }
}
