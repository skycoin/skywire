import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { UntypedFormControl, UntypedFormGroup } from '@angular/forms';
import { Subscription } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { NodeService } from 'src/app/services/node.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

/**
 * Per-visor "Web Proxy" tab — controls the visor's embedded
 * resolving proxy (.skynet + .dmsg domain resolution). Was a
 * MatDialog opened from the actions menu next to the shut-down
 * button; promoted to a tab so it sits with the other per-visor
 * configuration surfaces and isn't gated behind a destructive-
 * action menu.
 *
 * Mirrors the previous ProxySettingsComponent feature set: enable
 * toggle (which flips both .skynet and .dmsg resolvers in lock-
 * step) + an upstream SOCKS5 address for non-resolved traffic.
 */
@Component({
  selector: 'app-web-proxy',
  templateUrl: './web-proxy.component.html',
  styleUrls: ['./web-proxy.component.scss'],
  standalone: false,
})
export class WebProxyComponent extends PageBaseComponent implements OnInit, OnDestroy {
  node: Node;
  form: UntypedFormGroup;
  loading = false;
  proxyStatus: any = null;

  private nodeSub: Subscription;

  constructor(
    private nodeService: NodeService,
    private snackbar: SnackbarService,
    private cdr: ChangeDetectorRef,
  ) {
    super();
    this.form = new UntypedFormGroup({
      skynetEnabled: new UntypedFormControl(false),
      upstream: new UntypedFormControl(''),
    });
  }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const wasUnset = !this.node;
      this.node = node;
      if (wasUnset && node) { this.loadStatus(); }
    });
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
  }

  loadStatus() {
    if (!this.node) { return; }
    this.loading = true;
    this.nodeService.getProxies(this.node.localPk).subscribe(
      (status: any) => {
        this.proxyStatus = status;
        this.loading = false;
        const skynetRunning = status?.skynet_web?.running || false;
        const upstream = status?.skynet_web?.upstream_socks || '';
        this.form.get('skynetEnabled').setValue(skynetRunning);
        this.form.get('upstream').setValue(upstream);
        this.cdr.markForCheck();
      },
      () => { this.loading = false; this.cdr.markForCheck(); },
    );
  }

  toggleProxy() {
    if (!this.node) { return; }
    const enable = this.form.get('skynetEnabled').value;
    this.loading = true;
    this.nodeService.setProxyEnabled(this.node.localPk, 'skynet', enable).subscribe(
      () => {
        this.nodeService.setProxyEnabled(this.node.localPk, 'dmsg', enable).subscribe(
          () => {
            this.loading = false;
            this.snackbar.showDone(enable ? 'Resolving proxy enabled' : 'Resolving proxy disabled');
            this.loadStatus();
          },
          () => { this.loading = false; },
        );
      },
      () => { this.loading = false; this.snackbar.showError('Failed to toggle proxy'); },
    );
  }

  setUpstream() {
    if (!this.node) { return; }
    const addr = (this.form.get('upstream').value || '').trim();
    this.loading = true;
    this.nodeService.setProxyUpstream(this.node.localPk, 'skynet', addr).subscribe(
      () => {
        this.loading = false;
        this.snackbar.showDone(addr ? `Upstream set to ${addr}` : 'Upstream cleared');
        this.loadStatus();
      },
      () => { this.loading = false; this.snackbar.showError('Failed to set upstream'); },
    );
  }

  refresh() { this.loadStatus(); }
}
