import { Component, Input, OnChanges } from '@angular/core';
import { Transport } from '../../../../../app.datatypes';
import { MatDialog, MatTableDataSource } from '@angular/material';
import { CreateTransportComponent } from './create-transport/create-transport.component';
import { NodeService } from '../../../../../services/node.service';
import { TransportService } from '../../../../../services/transport.service';
import { NodeComponent } from '../../node.component';
import { ErrorsnackbarService } from '../../../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';

@Component({
  selector: 'app-transport-list',
  templateUrl: './transport-list.component.html',
  styleUrls: ['./transport-list.component.scss']
})
export class TransportListComponent implements OnChanges {
  @Input() transports: Transport[];
  displayedColumns: string[] = ['index', 'remote', 'type', 'upload_total', 'download_total', 'x'];
  dataSource = new MatTableDataSource<Transport>();

  constructor(
    private dialog: MatDialog,
    private nodeService: NodeService,
    private transportService: TransportService,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
  ) { }

  ngOnChanges(): void {
    this.dataSource.data = this.transports;
  }

  create() {
    this.dialog.open(CreateTransportComponent);
  }

  delete(transport: string) {
    this.transportService.delete(this.nodeService.getCurrentNodeKey(), transport).subscribe(() => {
      NodeComponent.refreshDisplayedData();
      this.errorSnackBar.open(this.translate.instant('transports.deleted'));
    }, () => {
      this.errorSnackBar.open(this.translate.instant('transports.error-deleting'));
    });
  }
}
