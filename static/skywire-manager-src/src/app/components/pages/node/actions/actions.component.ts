import { Component, Input, OnInit, ViewChild } from '@angular/core';
import { Node } from '../../../../app.datatypes';
import { MatDialog } from '@angular/material';
import { ConfigurationComponent } from './configuration/configuration.component';
import { Router } from '@angular/router';
import {UpdateNodeComponent} from './update-node/update-node.component';
import { ButtonComponent } from '../../../layout/button/button.component';
import { BasicTerminalComponent } from './basic-terminal/basic-terminal.component';
import { SnackbarService } from '../../../../services/snackbar.service';

@Component({
  selector: 'app-actions',
  templateUrl: './actions.component.html',
  styleUrls: ['./actions.component.scss']
})
export class ActionsComponent implements OnInit {
  @Input() node: Node;
  @ViewChild('updateButton') updateButton: ButtonComponent;

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    // if (environment.production) {
    //   this.updateButton.loading();
    //
    //   this.nodeService.checkUpdate().subscribe(hasUpdate => {
    //     this.updateButton.reset();
    //     this.updateButton.notify(hasUpdate);
    //   });
    // }
  }

  reboot() {
    // this.nodeService.reboot().subscribe(
    //   () => {
    //     this.snackbarService.showDone('actions.config.success');
    //     this.router.navigate(['nodes']);
    //   },
    //   (e) => this.snackbarService.showError(e.message),
    // );
  }

  update() {
    this.dialog.open(UpdateNodeComponent).afterClosed().subscribe((updated) => {
      if (updated) {
        this.snackbarService.showDone('actions.update.update-success');
      }
    });
  }

  configuration() {
    this.dialog.open(ConfigurationComponent, {data: {}});
  }

  terminal() {
    this.dialog.open(BasicTerminalComponent, {
      width: '1000px',
      data: {
        addr: this.node.tcp_addr,
        pk: this.node.local_pk,
      },
    });
  }

  back() {
    this.router.navigate(['nodes']);
  }
}
