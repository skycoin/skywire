import { Component, OnInit, OnDestroy } from '@angular/core';
import { Subscription } from 'rxjs';

import { NodeService } from '../../../../services/node.service';
import { NodeComponent } from '../node.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { PageBaseComponent } from 'src/app/utils/page-base';

@Component({
  selector: 'app-skynet',
  templateUrl: './skynet.component.html',
  styleUrls: ['./skynet.component.scss'],
  standalone: false
})
export class SkynetComponent extends PageBaseComponent implements OnInit, OnDestroy {
  ports: number[] = [];
  newPort = '';
  loading = true;
  nodeKey = '';

  private portsSub: Subscription;

  constructor(
    private nodeService: NodeService,
    private snackbarService: SnackbarService,
  ) {
    super();
  }

  ngOnInit() {
    this.nodeKey = NodeComponent.getCurrentNodeKey();
    this.loadPorts();
    return super.ngOnInit();
  }

  ngOnDestroy() {
    if (this.portsSub) {
      this.portsSub.unsubscribe();
    }
  }

  loadPorts() {
    this.loading = true;
    this.portsSub = this.nodeService.getSkynetPorts(this.nodeKey).subscribe(
      (ports: number[]) => {
        this.ports = (ports || []).sort((a, b) => a - b);
        this.loading = false;
      },
      () => {
        this.ports = [];
        this.loading = false;
      }
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
        this.snackbarService.showDone(`Port ${port} forwarded over skynet`);
        this.loadPorts();
      },
      (err) => {
        const msg = err?.error?.error || 'Failed to forward port';
        this.snackbarService.showError(msg);
      }
    );
  }

  removePort(port: number) {
    this.nodeService.deregisterSkynetPort(this.nodeKey, port).subscribe(
      () => {
        this.snackbarService.showDone(`Port ${port} removed`);
        this.loadPorts();
      },
      () => {
        this.snackbarService.showError('Failed to remove port');
      }
    );
  }
}
