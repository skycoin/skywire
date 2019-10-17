import { Component, OnDestroy, OnInit, ViewChild, NgZone } from '@angular/core';
import { NodeService } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { Subscription, of, timer } from 'rxjs';
import { MatDialog, MatTableDataSource } from '@angular/material';
import { Router } from '@angular/router';
import { ButtonComponent } from '../../layout/button/button.component';
import { TranslateService } from '@ngx-translate/core';
import { ErrorsnackbarService } from '../../../services/errorsnackbar.service';
import { AuthService } from '../../../services/auth.service';
import { EditLabelComponent } from '../../layout/edit-label/edit-label.component';
import { StorageService } from '../../../services/storage.service';
import { delay, flatMap, tap } from 'rxjs/operators';

@Component({
  selector: 'app-node-list',
  templateUrl: './node-list.component.html',
  styleUrls: ['./node-list.component.scss'],
})
export class NodeListComponent implements OnInit, OnDestroy {
  loading = true;
  dataSource = new MatTableDataSource<Node>();
  displayedColumns: string[] = ['enabled', 'index', 'label', 'key', 'actions'];

  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;

  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;

  constructor(
    private nodeService: NodeService,
    private router: Router,
    private errorSnackBar: ErrorsnackbarService,
    private dialog: MatDialog,
    private translate: TranslateService,
    private authService: AuthService,
    public storageService: StorageService,
    private ngZone: NgZone,
  ) { }

  ngOnInit() {
    this.refresh(0);

    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription =
        timer(5000, 5000).subscribe(() => this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1000);
        }));
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();
  }

  nodeStatusClass(node: Node): string {
    switch (node.online) {
      case true:
        return 'dot-green';
      default:
        return 'dot-red';
    }
  }

  nodeStatusTooltip(node: Node): string {
    switch (node.online) {
      case true:
        return 'node.statuses.online-tooltip';
      default:
        return 'node.statuses.offline-tooltip';
    }
  }

  private refresh(delayMilliseconds: number) {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.ngZone.runOutsideAngular(() => {
      this.dataSubscription = of(1).pipe(
        delay(delayMilliseconds),
        tap(() => this.ngZone.run(() => this.updating = true)),
        delay(120),
        flatMap(() => this.nodeService.getNodes())
      ).subscribe(
        (nodes: Node[]) => {
          this.ngZone.run(() => {
            this.dataSource.data = nodes;
            this.loading = false;

            this.lastUpdate = Date.now();
            this.secondsSinceLastUpdate = 0;
            this.updating = false;
            this.errorsUpdating = false;

            this.refresh(this.storageService.getRefreshTime() * 1000);
          });
        }, error => {
          this.ngZone.run(() => {
            if (!this.errorsUpdating) {
              this.errorSnackBar.open(this.translate.instant('nodes.error-load', { error }));
            }

            this.updating = false;
            this.errorsUpdating = true;

            if (this.loading) {
              this.refresh(3000);
            } else {
              this.refresh(this.storageService.getRefreshTime() * 1000);
            }
          });
        }
      );
    });
  }

  settings() {
    this.router.navigate(['settings']);
  }

  logout() {
    this.authService.logout().subscribe(
      () => this.router.navigate(['login']),
      () => this.errorSnackBar.open(this.translate.instant('nodes.logout-error'))
    );
  }

  showEditLabelDialog(node: Node) {
    this.dialog.open(EditLabelComponent, {
      data: { label: node.label },
    }).afterClosed().subscribe((label: string) => {
      label = label.trim();
      if (label) {
        this.storageService.setNodeLabel(node.local_pk, label);
      } else if (label === '') {
        const addressParts = node.tcp_addr.split(':');
        let defaultLabel = node.tcp_addr;
        if (addressParts && addressParts.length === 2) {
          defaultLabel = ':' + addressParts[1];
        }

        this.storageService.setNodeLabel(node.local_pk, defaultLabel);
      }

      this.refresh(0);
    });
  }

  deleteNode(node: Node) {
    this.storageService.removeNode(node.local_pk);
    this.refresh(0);
  }

  open(node: Node) {
    if (node.online) {
      this.router.navigate(['nodes', node.local_pk]);
    }
  }
}
