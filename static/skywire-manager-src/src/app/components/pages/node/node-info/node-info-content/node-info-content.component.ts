import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { EditLabelComponent } from 'src/app/components/layout/edit-label/edit-label.component';
import { NodeComponent } from '../../node.component';
import TimeUtils, { ElapsedTime } from 'src/app/utils/timeUtils';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { KnownHealthStatuses } from 'src/app/services/node.service';
import { RouterConfigComponent, RouterConfigParams } from './router-config/router-config.component';
import GeneralUtils from 'src/app/utils/generalUtils';
import { TransportService } from 'src/app/services/transport.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

/**
 * Shows the basic info of a node.
 */
@Component({
  selector: 'app-node-info-content',
  templateUrl: './node-info-content.component.html',
  styleUrls: ['./node-info-content.component.scss']
})
export class NodeInfoContentComponent implements OnDestroy {
  @Input() set nodeInfo(val: Node) {
    this.node = val;
    this.timeOnline = TimeUtils.getElapsedTime(val.secondsOnline);

    if (val.health && val.health.servicesHealth === KnownHealthStatuses.Healthy) {
      this.nodeHealthText = 'node.statuses.online';
      this.nodeHealthClass = 'dot-green';
    } else if (val.health && val.health.servicesHealth === KnownHealthStatuses.Unhealthy) {
      this.nodeHealthText = 'node.statuses.partially-online';
      this.nodeHealthClass = 'dot-yellow blinking';
    } else if (val.health && val.health.servicesHealth === KnownHealthStatuses.Connecting) {
      this.nodeHealthText = 'node.statuses.connecting';
      this.nodeHealthClass = 'dot-outline-gray';
    } else {
      this.nodeHealthText = 'node.statuses.unknown';
      this.nodeHealthClass = 'dot-outline-gray';
    }
  }

  node: Node;
  timeOnline: ElapsedTime;
  nodeHealthClass: string;
  nodeHealthText: string;

  private autoconnectSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    public storageService: StorageService,
    private transportService: TransportService,
    private snackbarService: SnackbarService,
  ) { }

  ngOnDestroy() {
    if (this.autoconnectSubscription) {
      this.autoconnectSubscription.unsubscribe();
    }
  }

  showEditLabelDialog() {
    let labelInfo =  this.storageService.getLabelInfo(this.node.localPk);
    if (!labelInfo) {
      labelInfo = {
        id: this.node.localPk,
        label: '',
        identifiedElementType: LabeledElementTypes.Node,
      };
    }

    EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        NodeComponent.refreshCurrentDisplayedData();
      }
    });
  }

  changeRouterConfig() {
    const params: RouterConfigParams = {nodePk: this.node.localPk, minHops: this.node.minHops};
    RouterConfigComponent.openDialog(this.dialog, params).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        NodeComponent.refreshCurrentDisplayedData();
      }
    });
  }

  /**
   * Enables or disables the transport.public_autoconnect setting.
   */
  changeTransportsConfig() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(
      this.dialog,
      this.node.autoconnectTransports ? 'node.details.transports-info.disable-confirmation' : 'node.details.transports-info.enable-confirmation'
    );

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      const operation = this.transportService.changeAutoconnectSetting(this.node.localPk, !this.node.autoconnectTransports);
      this.autoconnectSubscription = operation.subscribe(() => {
        confirmationDialog.close();
        this.snackbarService.showDone(
          this.node.autoconnectTransports ? 'node.details.transports-info.disable-done' : 'node.details.transports-info.enable-done'
        );

        NodeComponent.refreshCurrentDisplayedData();
      }, (err: OperationError) => {
        err = processServiceError(err);

        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      });
    });
  }
}
