import { Component, OnInit, OnDestroy, ViewChild, ElementRef, NgZone } from '@angular/core';
import { MatDialogConfig, MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Subscription, delay, mergeMap, of, timer } from 'rxjs';

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
    standalone: false
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

  // Live tail polling. When true, the dialog re-fetches every
  // livePollMs and appends only the entries newer than its cursor.
  // Toggleable so the user can pause to copy lines / scroll back.
  liveTail = true;
  livePollMs = 2000;
  // Diff-streaming cursor: the highest log_line received so far.
  // Sent as ?since= on the next poll; the response carries only
  // entries with log_line > cursor (plus the new cursor value).
  private logCursor = 0;
  // Number of entries the visor reported as dropped — i.e., the
  // ring buffer wrapped past our cursor between polls. Surfaced
  // in the UI as a "skipped N entries" hint.
  totalDropped = 0;
  // Track whether the scroll viewport is pinned to the bottom so
  // we only auto-scroll on each refresh when the user was already
  // tailing — scrolling up to read history shouldn't get yanked.
  private wasAtBottom = true;

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
   * Gets the logs from the back-end. First call (cursor=0) fetches
   * the full buffer via the diff endpoint; subsequent calls receive
   * only the entries that arrived since the previous cursor.
   * @param delayMilliseconds Delay before getting the data; used both
   *   for the initial load and for the live-tail polling cadence.
   */
  loadData(delayMilliseconds: number) {
    this.removeSubscription();

    // Capture scroll position before the fetch so the post-receive
    // auto-scroll only fires when the user is tailing.
    this.captureScrollTailState();

    this.loading = this.logEntries.length === 0;
    const cursor = this.logCursor;
    this.subscription = of(1).pipe(
      delay(delayMilliseconds),
      mergeMap(() => this.nodeService.getRuntimeLogsSince(NodeComponent.getCurrentNodeKey(), cursor))
    ).subscribe(
      (delta: any) => this.onLogsDeltaReceived(delta),
      (err: OperationError) => this.onLogsError(err)
    );
  }

  /**
   * User-toggleable live tail. When off, polling stops and the
   * displayed buffer freezes at whatever was last fetched.
   */
  toggleLiveTail() {
    this.liveTail = !this.liveTail;
    if (this.liveTail) {
      this.loadData(0);
    } else {
      this.removeSubscription();
    }
  }

  private captureScrollTailState() {
    if (!this.content) {
      this.wasAtBottom = true;
      return;
    }
    const el = this.content.nativeElement as HTMLElement;
    // Treat "near the bottom" (within 40px) as still tailing — the
    // last log line's height plus a margin shouldn't break stickiness.
    this.wasAtBottom = (el.scrollHeight - el.scrollTop - el.clientHeight) < 40;
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

  /**
   * Diff-streaming receive path. The visor returns:
   *   { entries: string[]  // each is a JSON-encoded log line
   *     latest:  number,   // new cursor value
   *     dropped: number }  // entries we missed (ring wrapped past us)
   *
   * On the first call (cursor=0) entries is the entire buffer, so
   * we replace logEntries. On subsequent calls we append only the
   * new lines and trim from the head if we exceed maxElementsPerPage.
   */
  private onLogsDeltaReceived(delta: any) {
    if (!delta) {
      this.loading = false;
      this.scheduleNextPoll();
      return;
    }
    const isInitial = this.logCursor === 0;
    this.logCursor = (typeof delta.latest === 'number') ? delta.latest : this.logCursor;
    if (typeof delta.dropped === 'number' && delta.dropped > 0) {
      this.totalDropped += delta.dropped;
    }

    const entriesRaw: string[] = Array.isArray(delta.entries) ? delta.entries : [];
    if (entriesRaw.length === 0) {
      this.loading = false;
      this.LoadingMoment = Date.now();
      this.shouldShowError = true;
      this.startUpdatingTime();
      this.scheduleNextPoll();
      return;
    }

    // Each entry is a JSON-stringified object. Parse and feed
    // through the existing entry-decoding pipeline (level mapping,
    // extras, etc.). Wrap in a try so a single malformed line
    // doesn't break the whole batch.
    const parsed: any[] = [];
    for (const raw of entriesRaw) {
      try {
        parsed.push(JSON.parse(raw));
      } catch {
        // skip malformed
      }
    }

    if (isInitial) {
      this.logEntries = [];
    }
    this.appendParsedEntries(parsed);

    // Trim to keep the buffer bounded; matches the maxElementsPerPage
    // cap the old replace-buffer flow used.
    if (this.logEntries.length > this.maxElementsPerPage) {
      this.logEntries = this.logEntries.slice(this.logEntries.length - this.maxElementsPerPage);
      this.hasMoreLogMessages = true;
    }
    this.totalLogs = this.logCursor;

    this.loading = false;
    this.LoadingMoment = Date.now();
    this.shouldShowError = true;
    this.startUpdatingTime();
    this.filter();
    this.scheduleNextPoll();
  }

  private scheduleNextPoll() {
    if (this.liveTail) {
      this.loadData(this.livePollMs);
    }
  }

  private appendParsedEntries(logs: any[]) {
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

      // Append. Bound trimming is the caller's responsibility
      // (onLogsDeltaReceived caps logEntries to maxElementsPerPage).
      this.logEntries.push(entry);
    });
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

    // Auto-scroll only if the user was already tailing before this
    // refresh; otherwise leave their scroll position alone so they
    // can read history without getting yanked.
    if (this.wasAtBottom) {
      setTimeout(() => {
        if (this.content) {
          (this.content.nativeElement as HTMLElement).scrollTop = (this.content.nativeElement as HTMLElement).scrollHeight;
        }
      });
    }
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

    return window.location.origin + '/' + apiPrefix + '/visors/' + NodeComponent.getCurrentNodeKey() + '/runtime-logs';
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
