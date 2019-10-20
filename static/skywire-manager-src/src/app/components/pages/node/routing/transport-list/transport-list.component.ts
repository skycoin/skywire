import { Component, Input, OnDestroy } from '@angular/core';
import { Transport } from '../../../../../app.datatypes';
import { MatDialog, MatTableDataSource } from '@angular/material';
import { CreateTransportComponent } from './create-transport/create-transport.component';
import { TransportService } from '../../../../../services/transport.service';
import { NodeComponent } from '../../node.component';
import { ErrorsnackbarService } from '../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';
import { Observable, Subscription } from 'rxjs';
import { AppConfig } from '../../../../../app.config';
import { ActivatedRoute } from '@angular/router';

@Component({
  selector: 'app-transport-list',
  templateUrl: './transport-list.component.html',
  styleUrls: ['./transport-list.component.scss']
})
export class TransportListComponent implements OnDestroy {
  @Input() nodePK: string;

  displayedColumns: string[] = ['selection', 'remote', 'type', 'upload_total', 'download_total', 'x'];
  dataSource = new MatTableDataSource<Transport>();
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
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
    private route: ActivatedRoute,
  ) {
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'));
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
    const elementsToRemove: string[] = [];
    this.selections.forEach((val, key) => {
      if (val) {
        elementsToRemove.push(key);
      }
    });

    this.deleteRecursively(elementsToRemove);
  }

  create() {
    this.dialog.open(CreateTransportComponent);
  }

  delete(id: string) {
    this.startDeleting(id).subscribe(() => {
      NodeComponent.refreshCurrentDisplayedData();
      this.errorSnackBar.open(this.translate.instant('transports.deleted'));
    }, () => {
      this.errorSnackBar.open(this.translate.instant('transports.error-deleting'));
    });
  }

  private recalculateElementsToShow() {
    this.currentPage = this.currentPageInUrl;

    if (this.allTransports) {
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

    this.dataSource.data = this.transportsToShow;
  }

  private startDeleting(id: string): Observable<any> {
    return this.transportService.delete(NodeComponent.getCurrentNodeKey(), id);
  }

  deleteRecursively(ids: string[]) {
    this.startDeleting(ids[ids.length - 1]).subscribe(() => {
      ids.pop();
      if (ids.length === 0) {
        NodeComponent.refreshCurrentDisplayedData();
        this.errorSnackBar.open(this.translate.instant('transports.deleted'));
      } else {
        this.deleteRecursively(ids);
      }
    }, () => {
      NodeComponent.refreshCurrentDisplayedData();
      this.errorSnackBar.open(this.translate.instant('transports.error-deleting'));
    });
  }
}
