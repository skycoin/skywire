import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { Subscription } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { EditLabelComponent } from 'src/app/components/layout/edit-label/edit-label.component';
import { NodeComponent } from '../../node.component';
import TimeUtils, { ElapsedTime } from 'src/app/utils/timeUtils';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { NodeService } from 'src/app/services/node.service';
import { TransportService } from 'src/app/services/transport.service';
import { RouteService } from 'src/app/services/route.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { ApiService } from 'src/app/services/api.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
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
  ports: { name: string, value: string }[] = [];
  showPorts = false;
  isPublic = false;
  rawConfig = '';

  // Inline reward-address editor (replaces RewardsAddressComponent dialog).
  showRewardForm = false;
  rewardForm: UntypedFormGroup;
  showRewardRules = false;
  rewardRules: string | null = null;

  // Inline router-config editor (replaces RouterConfigComponent dialog).
  showRouterForm = false;
  routerForm: UntypedFormGroup;

  // Collapsible Runtime Configuration section (matches Ports pattern).
  showConfigSection = false;

  private autoconnectSubscription: Subscription;
  private publicToggleSubscription: Subscription;
  private saveRewardsSubscription: Subscription;
  private saveRouterSubscription: Subscription;
  private rewardRulesSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    public storageService: StorageService,
    private transportService: TransportService,
    private snackbarService: SnackbarService,
    private apiService: ApiService,
    private formBuilder: UntypedFormBuilder,
    private nodeService: NodeService,
    private routeService: RouteService,
  ) {
    this.rewardForm = this.formBuilder.group({
      address: ['', Validators.compose([Validators.minLength(20), Validators.maxLength(112)])],
    });
    this.routerForm = this.formBuilder.group({
      min: [1, Validators.compose([
        Validators.required,
        Validators.maxLength(3),
        Validators.pattern('^[0-9]+$'),
      ])],
    });
  }

  ngOnDestroy() {
    if (this.autoconnectSubscription) {
      this.autoconnectSubscription.unsubscribe();
    }
    if (this.publicToggleSubscription) {
      this.publicToggleSubscription.unsubscribe();
    }
    if (this.saveRewardsSubscription) {
      this.saveRewardsSubscription.unsubscribe();
    }
    if (this.saveRouterSubscription) {
      this.saveRouterSubscription.unsubscribe();
    }
    if (this.rewardRulesSubscription) {
      this.rewardRulesSubscription.unsubscribe();
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

  /** Toggle the inline reward-address editor; pre-fill with current value. */
  toggleRewardForm() {
    this.showRewardForm = !this.showRewardForm;
    if (this.showRewardForm) {
      this.rewardForm.get('address').setValue(this.node.rewardsAddress || '');
    }
  }

  /** Submit the inline reward-address form. Empty address removes
   *  the registration (replicates the dialog's empty-warning path
   *  with a friendlier inline confirm prompt). */
  submitRewardAddress() {
    if (!this.rewardForm.valid) {
      return;
    }
    const newAddr = (this.rewardForm.get('address').value || '').trim();
    const op = newAddr
      ? this.nodeService.setRewardsAddress(this.node.localPk, newAddr)
      : this.nodeService.deleteRewardsAddress(this.node.localPk);
    this.saveRewardsSubscription = op.subscribe({
      next: () => {
        this.snackbarService.showDone('rewards-address-config.done');
        this.showRewardForm = false;
        NodeComponent.refreshCurrentDisplayedData();
      },
      error: (err: OperationError) => {
        err = processServiceError(err);
        this.snackbarService.showError(err);
      },
    });
  }

  /** Lazy-load the embedded mainnet rules markdown the first time
   *  the user expands the rules block; subsequent toggles just hide/
   *  show the cached text. */
  toggleRewardRules() {
    this.showRewardRules = !this.showRewardRules;
    if (this.showRewardRules && this.rewardRules === null) {
      this.rewardRulesSubscription = this.nodeService.getRewardRules().subscribe(
        (text: string) => { this.rewardRules = text || ''; },
        () => { this.rewardRules = ''; this.snackbarService.showError('common.loading-error'); },
      );
    }
  }

  /** Toggle the inline router-config editor; pre-fill with current value. */
  toggleRouterForm() {
    this.showRouterForm = !this.showRouterForm;
    if (this.showRouterForm) {
      this.routerForm.get('min').setValue(this.node.minHops);
    }
  }

  submitRouterConfig() {
    if (!this.routerForm.valid) {
      return;
    }
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

  /** Collapsible config section toggle. Fetches the runtime config
   *  the first time the section is opened, then caches it. */
  onConfigToggle() {
    this.showConfigSection = !this.showConfigSection;
    if (this.showConfigSection && !this.rawConfig) {
      this.apiService.get(`visors/${this.node.localPk}/runtime-config`).subscribe(
        (result: any) => { this.rawConfig = JSON.stringify(result, null, 2); },
        () => { this.snackbarService.showError('common.loading-error'); },
      );
    }
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
