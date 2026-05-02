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
import { ApiService } from 'src/app/services/api.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { RewardsAddressComponent, RewardsAddressConfigParams } from './rewards-address-config/rewards-address-config.component';
import { TrafficData } from 'src/app/services/single-node-data.service';

/**
 * Shows the basic info of a node.
 */
@Component({
    selector: 'app-node-info-content',
    templateUrl: './node-info-content.component.html',
    styleUrls: ['./node-info-content.component.scss'],
    standalone: false
})
export class NodeInfoContentComponent implements OnDestroy {
  @Input() set nodeInfo(val: Node) {
    this.node = val;
    this.timeOnline = TimeUtils.getElapsedTime(val.secondsOnline);
    this.transportStats = this.computeTransportStats();

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

    // Fetch ports for this visor.
    this.fetchPorts(val.localPk);
    // Fetch public visor status.
    this.fetchPublicStatus(val.localPk);
  }

  @Input() trafficData: TrafficData;

  // True when the configured reward "address" is actually a BIP44
  // extended public key (xpub...) rather than a plain Skycoin
  // address. The Skycoin block explorer doesn't take xpubs, so the
  // template suppresses the explorer link in that case.
  get rewardsAddressIsXpub(): boolean {
    const addr = (this.node && this.node.rewardsAddress) || '';
    return addr.startsWith('xpub');
  }

  node: Node;
  timeOnline: ElapsedTime;
  transportStats: { total: number, byType: { type: string, count: number }[] } = { total: 0, byType: [] };
  nodeHealthClass: string;
  nodeHealthText: string;
  ports: { name: string, value: string }[] = [];
  showPorts = false;
  isPublic = false;
  showRawConfig = false;
  rawConfig = '';

  private autoconnectSubscription: Subscription;
  private publicToggleSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    public storageService: StorageService,
    private transportService: TransportService,
    private snackbarService: SnackbarService,
    private apiService: ApiService,
  ) { }

  ngOnDestroy() {
    if (this.autoconnectSubscription) {
      this.autoconnectSubscription.unsubscribe();
    }
    if (this.publicToggleSubscription) {
      this.publicToggleSubscription.unsubscribe();
    }
  }

  // Map a per-subsystem health value string to a CSS dot class.
  subHealthClass(v: string | undefined): string {
    if (v === KnownHealthStatuses.Healthy) {
      return 'dot-green';
    }
    if (v === KnownHealthStatuses.Unhealthy) {
      return 'dot-yellow blinking';
    }
    return 'dot-outline-gray';
  }

  // Map a per-subsystem health value string to its translation key.
  subHealthText(v: string | undefined): string {
    if (v === KnownHealthStatuses.Healthy) {
      return 'node.statuses.online';
    }
    if (v === KnownHealthStatuses.Unhealthy) {
      return 'node.statuses.partially-online';
    }
    return 'node.statuses.connecting';
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

  // Opens the modal window for changing the rewards address.
  changeRewardsAddressConfig() {
    const params: RewardsAddressConfigParams = {nodePk: this.node.localPk, currentAddress: this.node.rewardsAddress};
    RewardsAddressComponent.openDialog(this.dialog, params).afterClosed().subscribe((changed: boolean) => {
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
   * Returns if the node is connected to a valid DMSG server PK.
   */
  hasDmsgServer() {
    if (!this.node || this.node.dmsgServerPk.replace(/0/g, '').length === 0) {
      return false;
    }

    return true;
  }

  /**
   * Formats latency from nanoseconds to ms string.
   */
  formatLatency(latencyNs: number): string {
    const ms = latencyNs / 1000000;
    if (ms < 10) {
      return ms.toFixed(2) + 'ms';
    }

    return Math.round(ms) + 'ms';
  }

  /**
   * Returns transport statistics: total count and counts by type.
   */
  private computeTransportStats(): { total: number, byType: { type: string, count: number }[] } {
    if (!this.node || !this.node.transports) {
      return { total: 0, byType: [] };
    }

    const typeCounts: { [key: string]: number } = {};
    for (const transport of this.node.transports) {
      typeCounts[transport.type] = (typeCounts[transport.type] || 0) + 1;
    }

    const byType = Object.entries(typeCounts)
      .map(([type, count]) => {
return { type: type, count: count }
})
      .sort((a, b) => b.count - a.count); // Sort by count descending

    return { total: this.node.transports.length, byType: byType };
  }

  /**
   * Fetches ports for the given visor.
   */
  private fetchPorts(pk: string) {
    this.apiService.get(`visors/${pk}/ports`).subscribe((result: any) => {
      if (result && typeof result === 'object') {
        this.ports = Object.entries(result).map(([name, value]) => ({
          name: name,
          value: JSON.stringify(value),
        }));
      } else {
        this.ports = [];
      }
    }, () => {
      this.ports = [];
    });
  }

  /**
   * Fetches public visor status.
   */
  private fetchPublicStatus(pk: string) {
    this.apiService.get(`visors/${pk}/public`).subscribe((result: any) => {
      this.isPublic = result && result.is_public === true;
    }, () => {
      this.isPublic = false;
    });
  }

  /**
   * Toggles public visor status. The earlier confirmation dialog
   * was redundant — this is a reversible boolean and the snackbar
   * confirms the change.
   */
  changePublicConfig() {
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

  /**
   * Fetches and displays the runtime config.
   */
  viewConfig() {
    if (this.showRawConfig) {
      this.showRawConfig = false;
      return;
    }
    this.apiService.get(`visors/${this.node.localPk}/runtime-config`).subscribe((result: any) => {
      this.rawConfig = JSON.stringify(result, null, 2);
      this.showRawConfig = true;
    }, () => {
      this.snackbarService.showError('common.loading-error');
    });
  }

  /**
   * Opens the transport visualizer in a new window.
   */
  openTpViz() {
    window.open('/tp-viz/', '_blank');
  }

  /**
   * Enables or disables the transport.public_autoconnect setting.
   * Reversible boolean — flip + snackbar, no modal.
   */
  changeTransportsConfig() {
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
}
