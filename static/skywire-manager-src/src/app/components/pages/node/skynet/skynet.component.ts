import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { NodeService } from '../../../../services/node.service';
import { NodeComponent } from '../node.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { PageBaseComponent } from 'src/app/utils/page-base';

interface ForwardEntry {
  id: string;
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
  // Port forwarding (expose local ports)
  ports: number[] = [];
  newPort = '';
  portsLoading = true;

  // Reverse proxy (map remote ports to local)
  forwards: ForwardEntry[] = [];
  forwardsLoading = true;
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

  // --- Port Forwarding ---

  loadPorts() {
    this.portsLoading = true;
    this.portsSub = this.nodeService.getSkynetPorts(this.nodeKey).subscribe(
      (ports: number[]) => {
        this.ports = (ports || []).sort((a, b) => a - b);
        this.portsLoading = false;
      },
      () => { this.ports = []; this.portsLoading = false; }
    );
  }

  addPort() {
    const port = parseInt(this.newPort, 10);
    if (isNaN(port) || port < 1 || port > 65535) {
      this.snackbarService.showError('Enter a valid port number (1-65535)');
      return;
    }
    this.nodeService.registerSkynetPort(this.nodeKey, port).subscribe(
      () => {
        this.newPort = '';
        this.snackbarService.showDone(`Port ${port} forwarded`);
        this.loadPorts();
      },
      (err) => { this.snackbarService.showError(err?.error?.error || 'Failed to forward port'); }
    );
  }

  removePort(port: number) {
    this.nodeService.deregisterSkynetPort(this.nodeKey, port).subscribe(
      () => { this.snackbarService.showDone(`Port ${port} removed`); this.loadPorts(); },
      () => { this.snackbarService.showError('Failed to remove port'); }
    );
  }

  // --- Reverse Proxy ---

  loadForwards() {
    this.forwardsLoading = true;
    this.fwdsSub = this.nodeService.getSkynetForwards(this.nodeKey).subscribe(
      (data: any) => {
        this.forwards = [];
        if (data) {
          for (const [id, fwd] of Object.entries(data as Record<string, any>)) {
            this.forwards.push({
              id,
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
    if (!this.connectPK || this.connectPK.length !== 66) {
      this.snackbarService.showError('Enter a valid public key (66 hex chars)');
      return;
    }
    if (isNaN(rPort) || rPort < 1 || rPort > 65535) {
      this.snackbarService.showError('Enter a valid remote port');
      return;
    }
    if (isNaN(lPort) || lPort < 1 || lPort > 65535) {
      this.snackbarService.showError('Enter a valid local port');
      return;
    }
    this.nodeService.skynetConnect(this.nodeKey, this.connectPK, rPort, lPort).subscribe(
      () => {
        this.connectPK = '';
        this.connectRemotePort = '';
        this.connectLocalPort = '';
        this.snackbarService.showDone(`Connected: remote ${rPort} → localhost:${lPort}`);
        this.loadForwards();
      },
      (err) => { this.snackbarService.showError(err?.error?.error || 'Failed to connect'); }
    );
  }

  disconnect(id: string) {
    this.nodeService.skynetDisconnect(this.nodeKey, id).subscribe(
      () => { this.snackbarService.showDone('Disconnected'); this.loadForwards(); },
      () => { this.snackbarService.showError('Failed to disconnect'); }
    );
  }
}
