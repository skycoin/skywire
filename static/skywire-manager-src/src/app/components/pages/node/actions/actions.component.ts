import { Component, AfterViewInit, OnDestroy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { ConfigurationComponent } from './configuration/configuration.component';
import { Router } from '@angular/router';
import { UpdateNodeComponent } from './update-node/update-node.component';
import { BasicTerminalComponent } from './basic-terminal/basic-terminal.component';
import { SnackbarService } from '../../../../services/snackbar.service';
import { NodeComponent } from '../node.component';
import { Subscription } from 'rxjs';
import { SidenavService } from 'src/app/services/sidenav.service';

@Component({
  selector: 'app-actions',
  templateUrl: './actions.component.html',
  styleUrls: ['./actions.component.scss']
})
export class ActionsComponent implements AfterViewInit, OnDestroy {
  // @ViewChild('updateButton', { static: false }) updateButton: ButtonComponent;

  private menuSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private snackbarService: SnackbarService,
    private sidenavService: SidenavService,
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

    setTimeout(() => {
      this.menuSubscription = this.sidenavService.setContents([
        {
          name: 'actions.menu.terminal',
          actionName: 'terminal',
          icon: 'laptop'
        },
        {
          name: 'actions.menu.config',
          actionName: 'config',
          icon: 'settings'
        },
        {
          name: 'actions.menu.update',
          actionName: 'update',
          icon: 'get_app',
          disabled: true
        },
        {
          name: 'actions.menu.reboot',
          actionName: 'reboot',
          icon: 'rotate_right',
          disabled: true
        }], [
        {
          name: 'nodes.title',
          actionName: 'back',
          icon: 'chevron_left'
        }]).subscribe(actionName => {
          if (actionName === 'terminal') {
            this.terminal();
          } else if (actionName === 'config') {
            this.configuration();
          } else if (actionName === 'update') {
            this.update();
          } else if (actionName === 'reboot') {
            this.reboot();
          } else if (actionName === 'back') {
            this.back();
          }
        }
      );
    });
  }

  ngOnDestroy() {
    if (this.menuSubscription) {
      this.menuSubscription.unsubscribe();
    }
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
    UpdateNodeComponent.openDialog(this.dialog).afterClosed().subscribe((updated) => {
      if (updated) {
        this.snackbarService.showDone('actions.update.update-success');
      }
    });
  }

  configuration() {
    ConfigurationComponent.openDialog(this.dialog, {});
  }

  terminal() {
    BasicTerminalComponent.openDialog(this.dialog, {
      pk: NodeComponent.getCurrentNodeKey(),
    });
  }

  back() {
    this.router.navigate(['nodes']);
  }
}
