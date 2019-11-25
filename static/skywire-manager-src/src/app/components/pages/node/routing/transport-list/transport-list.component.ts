import { Component, Input, OnDestroy } from '@angular/core';
import { Transport } from '../../../../../app.datatypes';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { CreateTransportComponent } from './create-transport/create-transport.component';
import { TransportService } from '../../../../../services/transport.service';
import { NodeComponent } from '../../node.component';
import { Observable, Subscription } from 'rxjs';
import { AppConfig } from '../../../../../app.config';
import { ActivatedRoute } from '@angular/router';
import { ConfirmationComponent } from '../../../../layout/confirmation/confirmation.component';
import GeneralUtils from '../../../../../utils/generalUtils';
import { TransportDetailsComponent } from './transport-details/transport-details.component';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectColumnComponent, SelectedColumn } from 'src/app/components/layout/select-column/select-column.component';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';

enum SortableColumns {
  Id = 'transports.id',
  RemotePk = 'transports.remote',
  Type = 'transports.type',
  Uploaded = 'common.uploaded',
  Downloaded = 'common.downloaded',
}

@Component({
  selector: 'app-transport-list',
  templateUrl: './transport-list.component.html',
  styleUrls: ['./transport-list.component.scss']
})
export class TransportListComponent implements OnDestroy {
  @Input() nodePK: string;
  sortableColumns = SortableColumns;

  sortBy = SortableColumns.Id;
  sortReverse = false;
  get sortingArrow(): string {
    return this.sortReverse ? 'keyboard_arrow_up' : 'keyboard_arrow_down';
  }

  dataSource: Transport[];
  selections = new Map<string, boolean>();

  showShortList_: boolean;
  @Input() set showShortList(val: boolean) {
    this.showShortList_ = val;
    this.recalculateElementsToShow();
  }

  allTransports: Transport[];
  transportsToShow: Transport[];
  numberOfPages = 1;
  currentPage = 1;
  currentPageInUrl = 1;
  @Input() set transports(val: Transport[]) {
    this.allTransports = val;
    this.recalculateElementsToShow();
  }

  private navigationsSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private transportService: TransportService,
    private route: ActivatedRoute,
    private snackbarService: SnackbarService,
  ) {
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (selectedPage === NaN || selectedPage < 0) {
          selectedPage = 0;
        }

        this.currentPageInUrl = selectedPage;

        this.recalculateElementsToShow();
      }
    });
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
  }

  changeSelection(transport: Transport) {
    if (this.selections.get(transport.id)) {
      this.selections.set(transport.id, false);
    } else {
      this.selections.set(transport.id, true);
    }
  }

  hasSelectedElements(): boolean {
    if (!this.selections) {
      return false;
    }

    let found = false;
    this.selections.forEach((val) => {
      if (val) {
        found = true;
      }
    });

    return found;
  }

  changeAllSelections(setSelected: boolean) {
    this.selections.forEach((val, key) => {
      this.selections.set(key, setSelected);
    });
  }

  deleteSelected() {
    const confirmationDialog = GeneralUtils.createDeleteConfirmation(this.dialog, 'transports.delete-selected-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      const elementsToRemove: string[] = [];
      this.selections.forEach((val, key) => {
        if (val) {
          elementsToRemove.push(key);
        }
      });

      this.deleteRecursively(elementsToRemove, confirmationDialog);
    });
  }

  create() {
    CreateTransportComponent.openDialog(this.dialog);
  }

  showOptionsDialog(transport: Transport) {
    const options: SelectableOption[] = [
      {
        icon: 'visibility',
        label: 'transports.details.title',
      },
      {
        icon: 'close',
        label: 'transports.delete',
      }
    ];

    SelectOptionComponent.openDialog(this.dialog, options).afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.details(transport);
      } else if (selectedOption === 2) {
        this.delete(transport.id);
      }
    });
  }

  details(transport: Transport) {
    TransportDetailsComponent.openDialog(this.dialog, transport);
  }

  delete(id: string) {
    const confirmationDialog = GeneralUtils.createDeleteConfirmation(this.dialog, 'transports.delete-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.startDeleting(id).subscribe(() => {
        confirmationDialog.close();
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('transports.deleted');
      }, () => {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'transports.error-deleting');
      });
    });
  }

  changeSortingOrder(column: SortableColumns) {
    if (this.sortBy !== column) {
      this.sortBy = column;
      this.sortReverse = false;
    } else {
      this.sortReverse = !this.sortReverse;
    }

    this.recalculateElementsToShow();
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

          this.recalculateElementsToShow();
        }
      }
    });
  }

  private recalculateElementsToShow() {
    this.currentPage = this.currentPageInUrl;

    if (this.allTransports) {
      this.allTransports.sort((a, b) => {
        const defaultOrder = a.id.localeCompare(b.id);

        let response: number;
        if (this.sortBy === SortableColumns.Id) {
          response = !this.sortReverse ? a.id.localeCompare(b.id) : b.id.localeCompare(a.id);
        } else if (this.sortBy === SortableColumns.RemotePk) {
          response = !this.sortReverse ? a.remote_pk.localeCompare(b.remote_pk) : b.remote_pk.localeCompare(a.remote_pk);
        } else if (this.sortBy === SortableColumns.Type) {
          response = !this.sortReverse ? a.type.localeCompare(b.type) : b.type.localeCompare(a.type);
        } else if (this.sortBy === SortableColumns.Uploaded) {
          response = !this.sortReverse ? b.log.sent - a.log.sent : a.log.sent - b.log.sent;
        } else if (this.sortBy === SortableColumns.Downloaded) {
          response = !this.sortReverse ? b.log.recv - a.log.recv : a.log.recv - b.log.recv;
        } else {
          response = defaultOrder;
        }

        return response !== 0 ? response : defaultOrder;
      });

      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;

      this.numberOfPages = Math.ceil(this.allTransports.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.transportsToShow = this.allTransports.slice(start, end);

      const currentElementsMap = new Map<string, boolean>();
      this.transportsToShow.forEach(transport => {
        currentElementsMap.set(transport.id, true);

        if (!this.selections.has(transport.id)) {
          this.selections.set(transport.id, false);
        }
      });

      const keysToRemove: string[] = [];
      this.selections.forEach((value, key) => {
        if (!currentElementsMap.has(key)) {
          keysToRemove.push(key);
        }
      });

      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });
    } else {
      this.transportsToShow = null;
      this.selections = new Map<string, boolean>();
    }

    this.dataSource = this.transportsToShow;
  }

  private startDeleting(id: string): Observable<any> {
    return this.transportService.delete(NodeComponent.getCurrentNodeKey(), id);
  }

  deleteRecursively(ids: string[], confirmationDialog: MatDialogRef<ConfirmationComponent, any>) {
    this.startDeleting(ids[ids.length - 1]).subscribe(() => {
      ids.pop();
      if (ids.length === 0) {
        confirmationDialog.close();
        NodeComponent.refreshCurrentDisplayedData();
        this.snackbarService.showDone('transports.deleted');
      } else {
        this.deleteRecursively(ids, confirmationDialog);
      }
    }, () => {
      NodeComponent.refreshCurrentDisplayedData();
      confirmationDialog.componentInstance.showDone('confirmation.error-header-text', 'transports.error-deleting');
    });
  }
}
