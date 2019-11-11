import { Component, ViewChild, AfterViewInit } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { ConfigurationComponent } from './configuration/configuration.component';
import { Router } from '@angular/router';
import { UpdateNodeComponent } from './update-node/update-node.component';
import { ButtonComponent } from '../../../layout/button/button.component';
import { BasicTerminalComponent } from './basic-terminal/basic-terminal.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { NodeComponent } from '../node.component';

@Component({
  selector: 'app-actions',
  templateUrl: './actions.component.html',
  styleUrls: ['./actions.component.scss']
})
export class ActionsComponent implements AfterViewInit {
  @ViewChild('updateButton', { static: false }) updateButton: ButtonComponent;

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private snackbarService: SnackbarService,
  ) { }

  ngAfterViewInit() {
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
        pk: NodeComponent.getCurrentNodeKey(),
      },
    });
  }

  back() {
    this.router.navigate(['nodes']);
  }
}
