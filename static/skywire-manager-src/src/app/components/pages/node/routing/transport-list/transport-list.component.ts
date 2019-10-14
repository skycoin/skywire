import { Component, Input, OnChanges } from '@angular/core';
import { Transport } from '../../../../../app.datatypes';
import { MatDialog, MatTableDataSource } from '@angular/material';
import { CreateTransportComponent } from './create-transport/create-transport.component';
import { NodeService } from '../../../../../services/node.service';
import { TransportService } from '../../../../../services/transport.service';
import { NodeComponent } from '../../node.component';
import { ErrorsnackbarService } from '../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';
import { Observable } from 'rxjs';

@Component({
  selector: 'app-transport-list',
  templateUrl: './transport-list.component.html',
  styleUrls: ['./transport-list.component.scss']
})
export class TransportListComponent implements OnChanges {
  @Input() transports: Transport[];
  displayedColumns: string[] = ['selection', 'index', 'remote', 'type', 'upload_total', 'download_total', 'x'];
  dataSource = new MatTableDataSource<Transport>();
  selections = new Map<string, boolean>();

  constructor(
    private dialog: MatDialog,
    private nodeService: NodeService,
    private transportService: TransportService,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
  ) { }

  ngOnChanges(): void {
    this.dataSource.data = this.transports;

    if (this.transports) {
      const obtainedElementsMap = new Map<string, boolean>();
      this.transports.forEach(transport => {
        obtainedElementsMap.set(transport.id, true);

        if (!this.selections.has(transport.id)) {
          this.selections.set(transport.id, false);
        }
      });

      const keysToRemove: string[] = [];
      this.selections.forEach((val, key) => {
        if (!obtainedElementsMap.has(key)) {
          keysToRemove.push(key);
        }
      });

      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });
    }
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
      NodeComponent.refreshDisplayedData();
      this.errorSnackBar.open(this.translate.instant('transports.deleted'));
    }, () => {
      this.errorSnackBar.open(this.translate.instant('transports.error-deleting'));
    });
  }

  private startDeleting(id: string): Observable<any> {
    return this.transportService.delete(this.nodeService.getCurrentNodeKey(), id);
  }

  deleteRecursively(ids: string[]) {
    this.startDeleting(ids[ids.length - 1]).subscribe(() => {
      ids.pop();
      if (ids.length === 0) {
        NodeComponent.refreshDisplayedData();
        this.errorSnackBar.open(this.translate.instant('transports.deleted'));
      } else {
        this.deleteRecursively(ids);
      }
    }, () => {
      NodeComponent.refreshDisplayedData();
      this.errorSnackBar.open(this.translate.instant('transports.error-deleting'));
    });
  }
}
