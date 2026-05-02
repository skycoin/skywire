import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { NodeService } from '../../../../services/node.service';
import { NodeComponent } from '../node.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { PageBaseComponent } from 'src/app/utils/page-base';

interface ForwardedPort {
  port: number;
  local_port?: number;
  proxy_addr?: string;
  label: string;
  description: string;
  show_on_landing: boolean;
  skynet: boolean;
  dmsg: boolean;
  whitelist: string[];
}

interface ForwardEntry {
  id: string;
  network: string;
  remotePK: string;
  remotePort: number;
  localPort: number;
}

@Component({
  selector: 'app-skynet',
  templateUrl: './skynet.component.html',
  styleUrls: ['./skynet.component.scss'],
  standalone: false
})
export class SkynetComponent extends PageBaseComponent implements OnInit, OnDestroy {
  ports: ForwardedPort[] = [];
  portsLoading = true;

  // New port form
  newPort = '';
  newLocalPort = '';
  newProxyAddr = '';
  newLabel = '';
  newDesc = '';
  newWhitelist = '';
  newSkynet = true;
  newDmsg = true;
  newShowLanding = true;

  // Whitelist editor (inline, one row at a time)
  editingWhitelistPort: number | null = null;
  whitelistInput = '';

  // Reverse proxy
  forwards: ForwardEntry[] = [];
  forwardsLoading = true;
  connectNetwork = 'skynet';
  connectPK = '';
  connectRemotePort = '';
  connectLocalPort = '';

  nodeKey = '';

  private portsSub: Subscription;
  private fwdsSub: Subscription;

  constructor(
    private nodeService: NodeService,
    private snackbarService: SnackbarService,
  ) {
    super();
  }

  ngOnInit() {
    this.nodeKey = NodeComponent.getCurrentNodeKey();
    this.loadPorts();
    this.loadForwards();
    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.portsSub) this.portsSub.unsubscribe();
    if (this.fwdsSub) this.fwdsSub.unsubscribe();
  }

  loadPorts() {
    this.portsLoading = true;
    this.portsSub = this.nodeService.getForwardedPorts(this.nodeKey).subscribe(
      (ports: any[]) => {
        this.ports = (ports || []).sort((a: any, b: any) => a.port - b.port);
        this.portsLoading = false;
      },
      () => { this.ports = []; this.portsLoading = false; }
    );
  }

  addPort() {
    const port = parseInt(this.newPort, 10);
    if (isNaN(port) || port < 1 || port > 65535) {
      this.snackbarService.showError('Enter a valid port (1-65535)');
      return;
    }
    const localPort = this.newLocalPort ? parseInt(this.newLocalPort, 10) : 0;
    const proxyAddr = (this.newProxyAddr || '').trim();
    let whitelist: string[] = [];
    const wlRaw = (this.newWhitelist || '').trim();
    if (wlRaw !== '') {
      whitelist = wlRaw.split(/[\s,]+/).map(s => s.trim()).filter(s => s.length > 0);
      for (const pk of whitelist) {
        if (pk.length !== 66 || !/^[0-9a-fA-F]+$/.test(pk)) {
          this.snackbarService.showError(`Invalid public key in whitelist: ${pk}`);
          return;
        }
      }
    }
    const fp: any = {
      port,
      local_port: localPort || undefined,
      proxy_addr: proxyAddr || undefined,
      label: this.newLabel,
      description: this.newDesc,
      show_on_landing: this.newShowLanding,
      skynet: this.newSkynet,
      dmsg: this.newDmsg,
      whitelist: whitelist.length > 0 ? whitelist : undefined,
    };
    this.nodeService.registerForwardedPort(this.nodeKey, fp).subscribe(
      () => {
        this.newPort = '';
        this.newLocalPort = '';
        this.newProxyAddr = '';
        this.newLabel = '';
        this.newDesc = '';
        this.newWhitelist = '';
        this.snackbarService.showDone(`Port ${port} forwarded`);
        this.loadPorts();
      },
      (err: any) => { this.snackbarService.showError(err?.error?.error || 'Failed'); }
    );
  }

  removePort(port: number) {
    this.nodeService.deregisterSkynetPort(this.nodeKey, port).subscribe(
      () => { this.snackbarService.showDone(`Port ${port} removed`); this.loadPorts(); },
      () => { this.snackbarService.showError('Failed to remove port'); }
    );
  }

  toggleLanding(fp: ForwardedPort) {
    fp.show_on_landing = !fp.show_on_landing;
    this.nodeService.updateForwardedPort(this.nodeKey, fp).subscribe(
      () => {},
      () => { this.snackbarService.showError('Failed to update'); this.loadPorts(); }
    );
  }

  toggleSkynet(fp: ForwardedPort) {
    fp.skynet = !fp.skynet;
    this.nodeService.updateForwardedPort(this.nodeKey, fp).subscribe(
      () => {},
      () => { this.snackbarService.showError('Failed to update'); this.loadPorts(); }
    );
  }

  toggleDmsg(fp: ForwardedPort) {
    fp.dmsg = !fp.dmsg;
    this.nodeService.updateForwardedPort(this.nodeKey, fp).subscribe(
      () => {},
      () => { this.snackbarService.showError('Failed to update'); this.loadPorts(); }
    );
  }

  startEditWhitelist(fp: ForwardedPort) {
    this.editingWhitelistPort = fp.port;
    this.whitelistInput = (fp.whitelist || []).join(', ');
  }

  cancelEditWhitelist() {
    this.editingWhitelistPort = null;
    this.whitelistInput = '';
  }

  saveWhitelist(fp: ForwardedPort) {
    const raw = (this.whitelistInput || '').trim();
    let pks: string[] = [];
    if (raw !== '') {
      pks = raw.split(/[\s,]+/).map(s => s.trim()).filter(s => s.length > 0);
      for (const pk of pks) {
        if (pk.length !== 66 || !/^[0-9a-fA-F]+$/.test(pk)) {
          this.snackbarService.showError(`Invalid public key: ${pk}`);
          return;
        }
      }
    }
    const updated = { ...fp, whitelist: pks };
    this.nodeService.updateForwardedPort(this.nodeKey, updated).subscribe(
      () => {
        this.snackbarService.showDone(pks.length === 0
          ? `Whitelist cleared on port ${fp.port}`
          : `Whitelist set on port ${fp.port} (${pks.length} PK${pks.length === 1 ? '' : 's'})`);
        this.cancelEditWhitelist();
        this.loadPorts();
      },
      (err: any) => { this.snackbarService.showError(err?.error?.error || 'Failed to update whitelist'); }
    );
  }

  clearWhitelist(fp: ForwardedPort) {
    const updated = { ...fp, whitelist: [] };
    this.nodeService.updateForwardedPort(this.nodeKey, updated).subscribe(
      () => {
        this.snackbarService.showDone(`Whitelist cleared on port ${fp.port}`);
        this.cancelEditWhitelist();
        this.loadPorts();
      },
      (err: any) => { this.snackbarService.showError(err?.error?.error || 'Failed to clear whitelist'); }
    );
  }

  loadForwards() {
    this.forwardsLoading = true;
    this.fwdsSub = this.nodeService.getSkynetForwards(this.nodeKey).subscribe(
      (data: any) => {
        this.forwards = [];
        if (data) {
          for (const [id, fwd] of Object.entries(data as Record<string, any>)) {
            this.forwards.push({
              id,
              network: fwd.network || 'skynet',
              remotePK: fwd.remote_pk || '',
              remotePort: fwd.remote_port || 0,
              localPort: fwd.local_port || 0,
            });
          }
        }
        this.forwardsLoading = false;
      },
      () => { this.forwards = []; this.forwardsLoading = false; }
    );
  }

  connect() {
    const rPort = parseInt(this.connectRemotePort, 10);
    const lPort = parseInt(this.connectLocalPort, 10);
    if (!this.connectPK || this.connectPK.length !== 66) { this.snackbarService.showError('Enter a valid public key'); return; }
    if (isNaN(rPort) || rPort < 1) { this.snackbarService.showError('Enter a valid remote port'); return; }
    if (isNaN(lPort) || lPort < 1) { this.snackbarService.showError('Enter a valid local port'); return; }
    if (this.connectNetwork !== 'skynet' && this.connectNetwork !== 'dmsg') {
      this.snackbarService.showError('Network must be skynet or dmsg');
      return;
    }
    this.nodeService.skynetConnect(this.nodeKey, this.connectNetwork, this.connectPK, rPort, lPort).subscribe(
      () => {
        this.connectPK = ''; this.connectRemotePort = ''; this.connectLocalPort = '';
        this.snackbarService.showDone(`Connected via ${this.connectNetwork}: remote ${rPort} → localhost:${lPort}`);
        this.loadForwards();
      },
      (err: any) => { this.snackbarService.showError(err?.error?.error || 'Failed'); }
    );
  }

  disconnect(id: string) {
    this.nodeService.skynetDisconnect(this.nodeKey, id).subscribe(
      () => { this.snackbarService.showDone('Disconnected'); this.loadForwards(); },
      () => { this.snackbarService.showError('Failed'); }
    );
  }
}
