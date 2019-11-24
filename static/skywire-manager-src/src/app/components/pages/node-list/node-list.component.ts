import { Component, OnDestroy, OnInit, NgZone } from '@angular/core';
import { NodeService } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { Subscription, of, timer } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';
import { AuthService } from '../../../services/auth.service';
import { EditLabelComponent } from '../../layout/edit-label/edit-label.component';
import { StorageService } from '../../../services/storage.service';
import { delay, flatMap, tap } from 'rxjs/operators';
import { TabButtonData } from '../../layout/tab-bar/tab-bar.component';
import { SnackbarService } from '../../../services/snackbar.service';
import { SidenavService } from 'src/app/services/sidenav.service';
import { SelectColumnComponent, SelectedColumn } from '../../layout/select-column/select-column.component';
import GeneralUtils from 'src/app/utils/generalUtils';
import { SelectOptionComponent, SelectableOption } from '../../layout/select-option/select-option.component';

enum SortableColumns {
  Label = 'nodes.label',
  Key = 'common.key',
}

@Component({
  selector: 'app-node-list',
  templateUrl: './node-list.component.html',
  styleUrls: ['./node-list.component.scss'],
})
export class NodeListComponent implements OnInit, OnDestroy {
  sortableColumns = SortableColumns;
  sortBy = SortableColumns.Key;
  sortReverse = false;
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

  loading = true;
  dataSource: Node[];
  tabsData: TabButtonData[] = [];

  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;
  private menuSubscription: Subscription;

  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;

  constructor(
    private nodeService: NodeService,
    private router: Router,
    private dialog: MatDialog,
    private authService: AuthService,
    public storageService: StorageService,
    private ngZone: NgZone,
    private snackbarService: SnackbarService,
    private sidenavService: SidenavService,
  ) {
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'nodes.title',
        linkParts: ['/nodes'],
      },
      {
        icon: 'settings',
        label: 'settings.title',
        linkParts: ['/settings'],
      }
    ];
  }

  ngOnInit() {
    this.refresh(0);

    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription =
        timer(5000, 5000).subscribe(() => this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1000);
        }));
    });

    setTimeout(() => {
      this.menuSubscription = this.sidenavService.setContents([
        {
          name: 'nodes.logout',
          actionName: 'logout',
          icon: 'power_settings_new'
        }], null).subscribe(actionName => {
          if (actionName === 'logout') {
            this.logout();
          }
        }
      );
    });
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();

    if (this.menuSubscription) {
      this.menuSubscription.unsubscribe();
    }
  }

  nodeStatusClass(node: Node, forTooltip: boolean): string {
    switch (node.online) {
      case true:
        return forTooltip ? 'dot-green' : 'green-text';
      default:
        return forTooltip ? 'dot-red' : 'red-text';
    }
  }

  nodeStatusText(node: Node, forTooltip: boolean): string {
    switch (node.online) {
      case true:
        return 'node.statuses.online' + (forTooltip ? '-tooltip' : '');
      default:
        return 'node.statuses.offline' + (forTooltip ? '-tooltip' : '');
    }
  }

  changeSortingOrder(column: SortableColumns) {
    if (this.sortBy !== column) {
      this.sortBy = column;
      this.sortReverse = false;
    } else {
      this.sortReverse = !this.sortReverse;
    }

    this.sortList();
  }

  openSortingOrderModal() {
    const enumKeys = Object.keys(SortableColumns);
    const columnsMap = new Map<string, SortableColumns>();
    const columns = enumKeys.map(key => {
      const val = SortableColumns[key as any];
      columnsMap.set(val, SortableColumns[key]);

      return val;
    });

    SelectColumnComponent.openDialog(this.dialog, columns).afterClosed().subscribe((result: SelectedColumn) => {
      if (result) {
        if (columnsMap.has(result.label) && (result.sortReverse !== this.sortReverse || columnsMap.get(result.label) !== this.sortBy)) {
          this.sortBy = columnsMap.get(result.label);
          this.sortReverse = result.sortReverse;

          this.sortList();
        }
      }
    });
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
            this.dataSource = nodes;
            this.sortList();
            this.loading = false;
            this.snackbarService.closeCurrentIfTemporalError();

            this.lastUpdate = Date.now();
            this.secondsSinceLastUpdate = 0;
            this.updating = false;
            this.errorsUpdating = false;

            this.refresh(this.storageService.getRefreshTime() * 1000);
          });
        }, error => {
          this.ngZone.run(() => {
            if (!this.errorsUpdating) {
              if (this.loading) {
                this.snackbarService.showError('common.loading-error', null, true);
              } else {
                this.snackbarService.showError('nodes.error-load', null, true);
              }
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

  private sortList() {
    this.dataSource = this.dataSource.sort((a, b) => {
      const defaultOrder = a.local_pk.localeCompare(b.local_pk);

      let response: number;
      if (this.sortBy === SortableColumns.Key) {
        response = !this.sortReverse ? a.local_pk.localeCompare(b.local_pk) : b.local_pk.localeCompare(a.local_pk);
      } else if (this.sortBy === SortableColumns.Label) {
        response = !this.sortReverse ? a.label.localeCompare(b.label) : b.label.localeCompare(a.label);
      } else {
        response = defaultOrder;
      }

      return response !== 0 ? response : defaultOrder;
    });
  }

  logout() {
    this.authService.logout().subscribe(
      () => this.router.navigate(['login']),
      () => this.snackbarService.showError('nodes.logout-error')
    );
  }

  showOptionsDialog(node: Node) {
    const options: SelectableOption[] = [
      {
        icon: 'short_text',
        label: 'edit-label.title',
      },
      {
        icon: 'close',
        label: 'nodes.delete-node',
      }
    ];

    SelectOptionComponent.openDialog(this.dialog, options).afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.showEditLabelDialog(node);
      } else if (selectedOption === 2) {
        this.deleteNode(node);
      }
    });
  }

  showEditLabelDialog(node: Node) {
    EditLabelComponent.openDialog(this.dialog, node).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        this.refresh(0);
      }
    });
  }

  deleteNode(node: Node) {
    const confirmationDialog = GeneralUtils.createDeleteConfirmation(this.dialog, 'nodes.delete-node-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.storageService.changeNodeState(node.local_pk, true);
      this.refresh(0);
      this.snackbarService.showDone('nodes.deleted');
    });
  }

  open(node: Node) {
    if (node.online) {
      this.router.navigate(['nodes', node.local_pk]);
    }
  }
}
