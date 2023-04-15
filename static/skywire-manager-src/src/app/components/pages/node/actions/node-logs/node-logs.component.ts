import { Component, OnInit, OnDestroy, ViewChild, ElementRef, NgZone } from '@angular/core';
import { MatDialogConfig, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Subscription, of, timer } from 'rxjs';
import { delay, mergeMap } from 'rxjs/operators';

import { AppConfig } from 'src/app/app.config';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { NodeService } from 'src/app/services/node.service';
import { NodeComponent } from '../../node.component';
import { environment } from 'src/environments/environment';
import TimeUtils, { ElapsedTime } from 'src/app/utils/timeUtils';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { SelectOptionComponent, SelectableOption } from 'src/app/components/layout/select-option/select-option.component';

/**
 * Importance levels of the log entries.
 */
enum Level {
  PanicLevel,
	FatalLevel,
	ErrorLevel,
	WarnLevel,
	InfoLevel,
	DebugLevel,
	TraceLevel,
  Unknown,
}

/**
 * Properties of the importance levels.
 */
class LevelDetails {
  // Name to show on the log entries list for the importance level.
  name: string;
  // CSS class for showing the name of the level.
  colorClass: string;
  // Translatable var for showing the name of a filter which shows entries of this level or more.
  levelFilterName: string;
  // Numeric importance of the leve.
  importance: number;
}

/**
 * Represents a log entry.
 */
class LogEntry {
  // Date and hour.
  time: string;
  // Importance level.
  level: Level;
  // Log msg.
  msg: string;
  // Function that originated the msg.
  func: string;
  // Module that originated the msg.
  _module: string;
  // Collection of extra key value pairs that form part of the log entry.
  extra: LogEntryExtraValue[] = [];
}

/**
 * Unknown key value pairs that can be part of an log entry.
 */
class LogEntryExtraValue {
  name: string;
  value: string;
}

/**
 * Modal window for showing the runtime logs of a node.
 */
@Component({
  selector: 'app-node-logs',
  templateUrl: './node-logs.component.html',
  styleUrls: ['./node-logs.component.scss'],
})
export class NodeLogsComponent implements OnInit, OnDestroy {
  @ViewChild('content') content: ElementRef;

  // Map with the properties of each possible log entry importance level.
  levelDetails: Map<Level, LevelDetails> = new Map([
    [Level.PanicLevel,
      {name: 'PANIC', colorClass: 'panic-level-color', levelFilterName: 'filter-panic', importance: 8 }
    ],
    [Level.FatalLevel,
      {name: 'FATAL', colorClass: 'fatal-level-color', levelFilterName: 'filter-faltal', importance: 7 }
    ],
    [Level.ErrorLevel,
      {name: 'ERROR', colorClass: 'error-level-color', levelFilterName: 'filter-error', importance: 6 }
    ],
    [Level.WarnLevel,
      {name: 'WARNING', colorClass: 'warning-level-color', levelFilterName: 'filter-warning', importance: 5 }
    ],
    [Level.InfoLevel,
      {name: 'INFO', colorClass: 'info-level-color', levelFilterName: 'filter-info', importance: 4 }
    ],
    [Level.DebugLevel,
      {name: 'DEBUG', colorClass: 'debug-level-color', levelFilterName: 'filter-debug', importance: 3 }
    ],
    [Level.TraceLevel,
      {name: 'TRACE', colorClass: 'trace-level-color', levelFilterName: 'filter-all', importance: 2 }
    ],
    [Level.Unknown,
      {name: 'UNKNOWN LOG', colorClass: 'unknown-level-color', levelFilterName: 'filter-all', importance: 1 }
    ]
  ]);

  // Current minimum importanmce level used as filter.
  currentMinimumLevel = Level.Unknown;

  loading = true;
  // Moment in which the data was loaded.
  LoadingMoment = 0;
  // How much time has passed since the data was loaded.
  elapsedTime: ElapsedTime;

  // How many entries the modal window can show, to avoid performance problems.
  maxElementsPerPage = 1000;

  // All logs entries obtained from the back-end.
  logEntries: LogEntry[] = [];
  // Logs entries shown on the UI.
  filteredLogEntries: LogEntry[] = [];
  // If not all logs entries ontained from the backend are being shown.
  hasMoreLogMessages = false;
  // How many log entries were obtained from the backend.
  totalLogs = 0;

  /**
   * Allows to show an error msg in the snack bar only the first time there is an error
   * getting the data, and not all the automatic attemps.
   */
  private shouldShowError = true;

  private subscription: Subscription;
  private timeUpdateSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<NodeLogsComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(NodeLogsComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<NodeLogsComponent>,
    private nodeService: NodeService,
    private snackbarService: SnackbarService,
    private ngZone: NgZone,
    private dialog: MatDialog
  ) { }

  ngOnInit() {
    this.loadData(0);
  }

  ngOnDestroy(): void {
    this.removeSubscription();
    this.removeTimeSubscription();
  }

  // Shows the modal window for selecting the minimum importance level to use as filter.
  showFilters() {
    const options: SelectableOption[] = [
      { icon: '', label: 'node.logs.filter-all' },
      { icon: '', label: 'node.logs.filter-debug' },
      { icon: '', label: 'node.logs.filter-info' },
      { icon: '', label: 'node.logs.filter-warning' },
      { icon: '', label: 'node.logs.filter-error' },
      { icon: '', label: 'node.logs.filter-faltal' },
      { icon: '', label: 'node.logs.filter-panic' }
    ];

    const optionTypes: Level[] = [
      Level.Unknown,
      Level.DebugLevel,
      Level.InfoLevel,
      Level.WarnLevel,
      Level.ErrorLevel,
      Level.FatalLevel,
      Level.PanicLevel
    ];

    // Put the check mark on the currently selected option.
    for (let i = 0; i <= optionTypes.length; i++) {
      if (this.currentMinimumLevel === optionTypes[i]) {
        options[i].icon = 'check';
      }
    }

    SelectOptionComponent.openDialog(this.dialog, options, 'node.logs.filter-title').afterClosed().subscribe((selectedOption: number) => {
      // Use the selected option and update the filtered entries list.
      this.currentMinimumLevel = optionTypes[selectedOption - 1];
      this.filter();
    });
  }

  /**
   * Gets the logs from the back-end.
   * @param delayMilliseconds Delay before getting the data, for retries after errors..
   */
  loadData(delayMilliseconds: number) {
    this.removeSubscription();

    this.loading = true;
    this.subscription = of(1).pipe(
      // Wait the delay.
      delay(delayMilliseconds),
      // Load the data. The node pk is obtained from the currently openned node page.
      mergeMap(() => this.nodeService.getRuntimeLogs(NodeComponent.getCurrentNodeKey()))
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

  private removeTimeSubscription() {
    if (this.timeUpdateSubscription) {
      this.timeUpdateSubscription.unsubscribe();
    }
  }

  private onLogsReceived(logs: any[]) {
    let amount = 0;
    this.totalLogs = logs.length;
    // Check if the modal window can show all the entries.
    this.hasMoreLogMessages = this.totalLogs - this.maxElementsPerPage > 0;

    logs.forEach(e => {
      // Save all the basic data.
      const entry = new LogEntry();
      entry.time = e.time;
      entry._module = e._module;
      entry.msg = e.msg;
      entry.func = e.func;

      // Save the importance level.
      const receivewdLevel = e.level ? (e.level as string).toLowerCase() : '';
      if (receivewdLevel.includes('panic')) {
        entry.level = Level.PanicLevel;
      } else if (receivewdLevel.includes('fatal')) {
        entry.level = Level.FatalLevel;
      } else if (receivewdLevel.includes('error')) {
        entry.level = Level.ErrorLevel;
      } else if (receivewdLevel.includes('warn')) {
        entry.level = Level.WarnLevel;
      } else if (receivewdLevel.includes('info')) {
        entry.level = Level.InfoLevel;
      } else if (receivewdLevel.includes('debug')) {
        entry.level = Level.DebugLevel;
      } else if (receivewdLevel.includes('trace')) {
        entry.level = Level.TraceLevel;
      } else {
        entry.level = Level.Unknown;
      }

      // Format the current_backoff value, if any.
      if (e.current_backoff) {
        const seg = Math.floor(e.current_backoff / 1000000000);
        const min = Math.floor(seg / 60);
        const segs = Math.floor(seg % 60);
        if (min ) {
          entry.extra.push({name: 'current_backoff', value: min + 'm' + segs + 's'});
        } else {
          entry.extra.push({name: 'current_backoff', value: segs + 's'});
        }
      }

      // Save the error msg, is any.
      if (e.error) {
        entry.extra.push({name: 'error', value: e.error});
      }

      // List with the properties that should not be considered as unknown extra properties.
      const knownProperties = new Set<string>();
      knownProperties.add('time');
      knownProperties.add('_module');
      knownProperties.add('msg');
      knownProperties.add('func');
      knownProperties.add('level');
      knownProperties.add('current_backoff');
      knownProperties.add('error');
      knownProperties.add('log_line');

      // Save the unknow extra properties.
      for(const key in e) {
        if (!knownProperties.has(key)) {
          entry.extra.push({name: key, value: e[key]});
        }
      }

      // Add to the list.
      if (this.totalLogs - amount <= this.maxElementsPerPage) {
        this.logEntries.push(entry);
      }

      amount += 1;
    });

    this.loading = false;
    this.LoadingMoment = Date.now();

    this.startUpdatingTime();

    this.filter();
  }

  // Removes all the entries that do not meet the filter criteria.
  private filter() {
    this.filteredLogEntries = [];

    const minimumimportance = this.levelDetails.get(this.currentMinimumLevel).importance;

    this.logEntries.forEach(e => {
      const importance = this.levelDetails.get(e.level).importance;
      if (minimumimportance <= importance) {
        this.filteredLogEntries.push(e);
      }
    });

    // Scroll to the bottom. Use a timer to wait for the UI to be updated.
    setTimeout(() => {
      (this.content.nativeElement as HTMLElement).scrollTop = (this.content.nativeElement as HTMLElement).scrollHeight;
    });
  }

  // Updates the text which says how much time has passed since the data was loaded. It does it
  // periodically.
  startUpdatingTime() {
    this.elapsedTime = TimeUtils.getElapsedTime(Math.floor((Date.now() - this.LoadingMoment) / 1000));

    this.removeTimeSubscription();
    this.timeUpdateSubscription = timer(5000, 5000).subscribe(() => this.ngZone.run(() => {
      this.elapsedTime = TimeUtils.getElapsedTime(Math.floor((Date.now() - this.LoadingMoment) / 1000));
    }));
  }

  // Returns the URL with the raw log data.
  getFullLogsUrl(): string {
    const apiPrefix = !environment.production && location.protocol.indexOf('http:') !== -1 ? 'http-api' : 'api';

    return window.location.origin + '/' + apiPrefix + '/visors/' + NodeComponent.getCurrentNodeKey() + '/runtime-logs';;
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
