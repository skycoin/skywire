import { Component, Inject, OnInit, OnDestroy, ViewChild, ElementRef } from '@angular/core';
import { AppsService } from '../../../../../../services/apps.service';
import { LogMessage, Application } from '../../../../../../app.datatypes';
import { MAT_DIALOG_DATA, MatDialogConfig, MatDialog } from '@angular/material';
import { Subscription, of } from 'rxjs';
import { NodeComponent } from '../../../node.component';
import { delay, flatMap } from 'rxjs/operators';
import { LogFilterComponent, LogsFilter } from './log-filter/log-filter.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';

@Component({
  selector: 'app-log',
  templateUrl: './log.component.html',
  styleUrls: ['./log.component.scss'],
})
export class LogComponent implements OnInit, OnDestroy {
  @ViewChild('content') content: ElementRef;

  logMessages: LogMessage[] = [];
  loading = false;
  currentFilter: LogsFilter = {
    text: 'apps.log.filter.7-days',
    days: 7
  };

  private shouldShowError = true;
  private subscription: Subscription;

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    private appsService: AppsService,
    private dialog: MatDialog,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.loadData(0);
  }

  ngOnDestroy(): void {
    this.snackbarService.closeCurrentIfTemporalError();
    this.removeSubscription();
  }

  filter() {
    const config = new MatDialogConfig();
    config.data = this.currentFilter;
    config.autoFocus = false;
    config.width = '480px';
    this.dialog.open(LogFilterComponent, config).afterClosed().subscribe(result => {
      if (result) {
        this.currentFilter = result;
        this.logMessages = [];

        this.loadData(0);
      }
    });
  }

  private loadData(delayMilliseconds: number) {
    this.removeSubscription();

    this.loading = true;
    this.subscription = of(1).pipe(
      delay(delayMilliseconds),
      flatMap(() => this.appsService.getLogMessages(NodeComponent.getCurrentNodeKey(), this.data.name, this.currentFilter.days))
    ).subscribe(
      (log) => this.onLogsReceived(log),
      this.onLogsError.bind(this)
    );
  }

  private removeSubscription() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }
  }

  private onLogsReceived(logs: string[] = []) {
    this.loading = false;
    this.snackbarService.closeCurrentIfTemporalError();

    logs.forEach(log => {
      const dateStart = log.startsWith('[') ? 0 : -1;
      const dateEnd = dateStart !== -1 ? log.indexOf(']') : -1;

      if (dateStart !== -1 && dateEnd !== -1) {
        this.logMessages.push({
          time: log.substr(dateStart, dateEnd + 1),
          msg: log.substr(dateEnd + 1),
        });
      } else {
        this.logMessages.push({
          time: '',
          msg: log,
        });
      }
    });

    setTimeout(() => {
      (this.content.nativeElement as HTMLElement).scrollTop = (this.content.nativeElement as HTMLElement).scrollHeight;
    });
  }

  private onLogsError() {
    if (this.shouldShowError) {
      this.snackbarService.showError('common.loading-error', null, true);
      this.shouldShowError = false;
    }

    this.loadData(3000);
  }
}
