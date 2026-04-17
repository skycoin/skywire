import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { UntypedFormControl, UntypedFormGroup } from '@angular/forms';

import { NodeService } from '../../../../../services/node.service';
import { SnackbarService } from '../../../../../services/snackbar.service';

@Component({
  selector: 'app-proxy-settings',
  templateUrl: './proxy-settings.component.html',
  styleUrls: ['./proxy-settings.component.scss'],
  standalone: false
})
export class ProxySettingsComponent {
  form: UntypedFormGroup;
  loading = false;
  proxyStatus: any = null;

  constructor(
    private dialogRef: MatDialogRef<ProxySettingsComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { nodeKey: string },
    private nodeService: NodeService,
    private snackbarService: SnackbarService,
  ) {
    this.form = new UntypedFormGroup({
      skynetEnabled: new UntypedFormControl(false),
      upstream: new UntypedFormControl(''),
    });
    this.loadStatus();
  }

  loadStatus() {
    this.loading = true;
    this.nodeService.getProxies(this.data.nodeKey).subscribe(
      (status: any) => {
        this.proxyStatus = status;
        this.loading = false;
        // Set form values from current state
        const skynetRunning = status?.skynet_web?.running || false;
        const upstream = status?.skynet_web?.upstream_socks || '';
        this.form.get('skynetEnabled').setValue(skynetRunning);
        this.form.get('upstream').setValue(upstream);
      },
      () => {
        this.loading = false;
        this.snackbarService.showError('Failed to load proxy status');
      }
    );
  }

  toggleProxy() {
    const enable = this.form.get('skynetEnabled').value;
    this.loading = true;

    // Enable skynet first, then dmsg (dmsg chains through skynet)
    this.nodeService.setProxyEnabled(this.data.nodeKey, 'skynet', enable).subscribe(
      () => {
        this.nodeService.setProxyEnabled(this.data.nodeKey, 'dmsg', enable).subscribe(
          () => {
            this.loading = false;
            this.snackbarService.showDone(enable ? 'Resolving proxy enabled' : 'Resolving proxy disabled');
            this.loadStatus();
          },
          () => {
            this.loading = false;
            this.snackbarService.showError('Failed to toggle DMSG proxy');
          }
        );
      },
      () => {
        this.loading = false;
        this.snackbarService.showError('Failed to toggle Skynet proxy');
      }
    );
  }

  setUpstream() {
    const addr = this.form.get('upstream').value.trim();
    this.loading = true;
    this.nodeService.setProxyUpstream(this.data.nodeKey, 'skynet', addr).subscribe(
      () => {
        this.loading = false;
        this.snackbarService.showDone(addr ? `Upstream set to ${addr}` : 'Upstream cleared');
        this.loadStatus();
      },
      () => {
        this.loading = false;
        this.snackbarService.showError('Failed to set upstream');
      }
    );
  }

  close() {
    this.dialogRef.close();
  }
}
