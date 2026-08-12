import { Component, Inject, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { UntypedFormControl, UntypedFormGroup } from '@angular/forms';

import { NodeService } from '../../../../../services/node.service';
import { SnackbarService } from '../../../../../services/snackbar.service';

@Component({
  selector: 'app-proxy-settings',
  templateUrl: './proxy-settings.component.html',
  styleUrls: ['./proxy-settings.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
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
    private changeDetectorRef: ChangeDetectorRef,
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
        this.form.get('skynetEnabled')!.setValue(skynetRunning);
        this.form.get('upstream')!.setValue(upstream);
        this.changeDetectorRef.markForCheck();
      },
      () => {
 this.loading = false; 
        this.changeDetectorRef.markForCheck();
      }
    );
  }

  loadPorts() {
    this.nodeService.getSkynetPorts(this.data.nodeKey).subscribe(
      (ports: number[]) => {
        this.skynetPorts = (ports || []).sort((a, b) => a - b);
        this.changeDetectorRef.markForCheck();
      },
      () => {
        this.changeDetectorRef.markForCheck();
      }
    );
  }

  toggleProxy() {
    const enable = this.form.get('skynetEnabled')!.value;
    this.loading = true;
    this.nodeService.setProxyEnabled(this.data.nodeKey, 'skynet', enable).subscribe(
      () => {
        this.nodeService.setProxyEnabled(this.data.nodeKey, 'dmsg', enable).subscribe(
          () => {
            this.loading = false;
            this.snackbarService.showDone(enable ? 'Resolving proxy enabled' : 'Resolving proxy disabled');
            this.loadStatus();
            this.changeDetectorRef.markForCheck();
          },
          () => {
 this.loading = false; 
            this.changeDetectorRef.markForCheck();
          }
        );
        this.changeDetectorRef.markForCheck();
      },
      () => {
 this.loading = false; this.snackbarService.showError('Failed to toggle proxy'); 
        this.changeDetectorRef.markForCheck();
      }
    );
  }

  setUpstream() {
    const addr = this.form.get('upstream')!.value.trim();
    this.loading = true;
    this.nodeService.setProxyUpstream(this.data.nodeKey, 'skynet', addr).subscribe(
      () => {
        this.loading = false;
        this.snackbarService.showDone(addr ? `Upstream set to ${addr}` : 'Upstream cleared');
        this.loadStatus();
        this.changeDetectorRef.markForCheck();
      },
      () => {
 this.loading = false; this.snackbarService.showError('Failed to set upstream'); 
        this.changeDetectorRef.markForCheck();
      }
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
        this.changeDetectorRef.markForCheck();
      },
      (err) => {
        this.snackbarService.showError(err?.error?.error || 'Failed to register port');
        this.changeDetectorRef.markForCheck();
      }
    );
  }

  removePort(port: number) {
    this.nodeService.deregisterSkynetPort(this.data.nodeKey, port).subscribe(
      () => {
        this.snackbarService.showDone(`Port ${port} removed`);
        this.loadPorts();
        this.changeDetectorRef.markForCheck();
      },
      () => {
 this.snackbarService.showError('Failed to deregister port'); 
        this.changeDetectorRef.markForCheck();
      }
    );
  }

  close() {
    this.dialogRef.close();
  }
}
