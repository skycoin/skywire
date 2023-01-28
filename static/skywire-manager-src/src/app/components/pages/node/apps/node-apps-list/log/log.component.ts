import { Component, Inject, OnInit, OnDestroy, ViewChild, ElementRef } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogConfig, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Subscription, of } from 'rxjs';
import { delay, flatMap } from 'rxjs/operators';

import { AppsService } from '../../../../../../services/apps.service';
import { Application } from '../../../../../../app.datatypes';
import { NodeComponent } from '../../../node.component';
import { LogFilterComponent, LogsFilter } from './log-filter/log-filter.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

/**
 * Represents a log entry.
 */
interface LogMessage {
  /**
   * String with the date of the entry.
   */
  time: string;
  /**
   * The log msg itself.
   */
  msg: string;
}

/**
 * Modal window for showing the logs of an app. It allow to filter the initial date
 * of the log messages that are shown.
 */
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

  /**
   * Allows to show an error msg in the snack bar only the first time there is an error
   * getting the data, and not all the automatic attemps.
   */
  private shouldShowError = true;
  private subscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<LogComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(LogComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    public dialogRef: MatDialogRef<LogComponent>,
    private appsService: AppsService,
    private dialog: MatDialog,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.loadData(0);
  }

  ngOnDestroy(): void {
    this.removeSubscription();
  }

  filter() {
    LogFilterComponent.openDialog(this.dialog, this.currentFilter).afterClosed().subscribe(result => {
      if (result) {
        // Change the filter and reload the data.
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
      // Wait the delay.
      delay(delayMilliseconds),
      // Load the data. The node pk is obtained from the currently openned node page.
      flatMap(() => this.appsService.getLogMessages(NodeComponent.getCurrentNodeKey(), this.data.name, this.currentFilter.days))
    ).subscribe(
      (log) => this.onLogsReceived(log),
      (err: OperationError) => this.onLogsError(err)
    );
  }

  private removeSubscription() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }
  }

  private onLogsReceived(logs: string[] = []) {
    // Reset the indicators related to the loading operation.
    this.loading = false;
    this.shouldShowError = true;
    this.snackbarService.closeCurrentIfTemporaryError();

    // Separate the date from the actual log msg and add the entry to the array that will populate the UI.
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

    // Scroll to the bottom. Use a timer to wait for the UI to be updated.
    setTimeout(() => {
      (this.content.nativeElement as HTMLElement).scrollTop = (this.content.nativeElement as HTMLElement).scrollHeight;
    });
  }

  private onLogsError(err: OperationError) {
    err = processServiceError(err);

    // Show an error msg if it has not be done before during the current attempt to obtain the data.
    if (this.shouldShowError) {
      this.snackbarService.showError('common.loading-error', null, true, err);
      this.shouldShowError = false;
    }

    // Retry after a small delay.
    this.loadData(AppConfig.connectionRetryDelay);
  }
}
