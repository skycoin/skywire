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
  skynetPorts: number[] = [];
  newPort = '';

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
    this.loadPorts();
  }

  loadStatus() {
    this.loading = true;
    this.nodeService.getProxies(this.data.nodeKey).subscribe(
      (status: any) => {
        this.proxyStatus = status;
        this.loading = false;
        const skynetRunning = status?.skynet_web?.running || false;
        const upstream = status?.skynet_web?.upstream_socks || '';
        this.form.get('skynetEnabled').setValue(skynetRunning);
        this.form.get('upstream').setValue(upstream);
      },
      () => { this.loading = false; }
    );
  }

  loadPorts() {
    this.nodeService.getSkynetPorts(this.data.nodeKey).subscribe(
      (ports: number[]) => {
        this.skynetPorts = (ports || []).sort((a, b) => a - b);
      },
      () => {}
    );
  }

  toggleProxy() {
    const enable = this.form.get('skynetEnabled').value;
    this.loading = true;
    this.nodeService.setProxyEnabled(this.data.nodeKey, 'skynet', enable).subscribe(
      () => {
        this.nodeService.setProxyEnabled(this.data.nodeKey, 'dmsg', enable).subscribe(
          () => {
            this.loading = false;
            this.snackbarService.showDone(enable ? 'Resolving proxy enabled' : 'Resolving proxy disabled');
            this.loadStatus();
          },
          () => { this.loading = false; }
        );
      },
      () => { this.loading = false; this.snackbarService.showError('Failed to toggle proxy'); }
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
      () => { this.loading = false; this.snackbarService.showError('Failed to set upstream'); }
    );
  }

  addPort() {
    const port = parseInt(this.newPort, 10);
    if (isNaN(port) || port < 1 || port > 65535) {
      this.snackbarService.showError('Invalid port number');
      return;
    }
    this.nodeService.registerSkynetPort(this.data.nodeKey, port).subscribe(
      () => {
        this.newPort = '';
        this.snackbarService.showDone(`Port ${port} forwarded`);
        this.loadPorts();
      },
      (err) => {
        this.snackbarService.showError(err?.error?.error || 'Failed to register port');
      }
    );
  }

  removePort(port: number) {
    this.nodeService.deregisterSkynetPort(this.data.nodeKey, port).subscribe(
      () => {
        this.snackbarService.showDone(`Port ${port} removed`);
        this.loadPorts();
      },
      () => { this.snackbarService.showError('Failed to deregister port'); }
    );
  }

  close() {
    this.dialogRef.close();
  }
}
