import { Component, OnDestroy, OnInit, ViewChild, NgZone } from '@angular/core';
import { NodeService } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { Subscription, of } from 'rxjs';
import { MatDialog, MatTableDataSource } from '@angular/material';
import { Router } from '@angular/router';
import { ButtonComponent } from '../../layout/button/button.component';
import { TranslateService } from '@ngx-translate/core';
import { ErrorsnackbarService } from '../../../services/errorsnackbar.service';
import { AuthService } from '../../../services/auth.service';
import { EditLabelComponent } from '../../layout/edit-label/edit-label.component';
import { StorageService } from '../../../services/storage.service';
import { delay, flatMap } from 'rxjs/operators';

@Component({
  selector: 'app-node-list',
  templateUrl: './node-list.component.html',
  styleUrls: ['./node-list.component.scss'],
})
export class NodeListComponent implements OnInit, OnDestroy {
  @ViewChild('refreshButton') refreshButton: ButtonComponent;
  dataSource = new MatTableDataSource<Node>();
  displayedColumns: string[] = ['enabled', 'index', 'label', 'key', 'actions'];

  private dataSubscription: Subscription;

  constructor(
    private nodeService: NodeService,
    private router: Router,
    private errorSnackBar: ErrorsnackbarService,
    private dialog: MatDialog,
    private translate: TranslateService,
    private authService: AuthService,
    private storageService: StorageService,
    private ngZone: NgZone,
  ) { }

  ngOnInit() {
    this.refresh(0);
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
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
      this.dataSubscription = of(1).pipe(delay(delayMilliseconds), flatMap(() => this.nodeService.getNodes())).subscribe(
        (nodes: Node[]) => {
          this.ngZone.run(() => {
            this.refreshButton.reset();
            this.dataSource.data = nodes;

            this.refresh(this.storageService.getRefreshTime() * 1000);
          });
        }, error => {
          this.ngZone.run(() => {
            this.translate.get('nodes.error-load', { error }).subscribe(str => {
              this.errorSnackBar.open(str);
            });

            this.refreshButton.error(error);

            this.refresh(this.storageService.getRefreshTime() * 1000);
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
