import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { Node } from '../../../../../app.datatypes';
import { EditLabelComponent } from 'src/app/components/layout/edit-label/edit-label.component';
import { NodeComponent } from '../../node.component';
import TimeUtils, { ElapsedTime } from 'src/app/utils/timeUtils';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { ApiService } from 'src/app/services/api.service';

/**
 * Shows the basic info of a node. The reward-address management,
 * transports summary + toggles, and router/min-hops control used to
 * live here too — those moved out to their own dedicated tabs
 * (Rewards / Transports / Routing) so the Info surface stays a
 * read-only identity card.
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
    this.fetchPorts(val.localPk);
  }

  node: Node;
  timeOnline: ElapsedTime;
  ports: { name: string, value: string }[] = [];
  showPorts = false;
  rawConfig = '';

  // Collapsible Runtime Configuration section (matches Ports pattern).
  showConfigSection = false;

  constructor(
    private dialog: MatDialog,
    public storageService: StorageService,
    private snackbarService: SnackbarService,
    private apiService: ApiService,
  ) {}

  ngOnDestroy() {
    // Nothing to unsubscribe — fetchPorts/fetchConfig use one-shot HTTP.
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
}
