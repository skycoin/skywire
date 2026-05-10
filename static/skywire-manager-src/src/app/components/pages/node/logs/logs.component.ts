import { Component, OnDestroy, OnInit, ViewChild, ElementRef, NgZone, ChangeDetectorRef } from '@angular/core';
import { Subscription, delay, mergeMap, of } from 'rxjs';

import { Node } from '../../../../app.datatypes';
import { NodeComponent } from '../node.component';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { AppConfig } from 'src/app/app.config';
import { NodeService } from 'src/app/services/node.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { environment } from 'src/environments/environment';

/**
 * Per-visor "Logs" tab — the runtime-log viewer that used to be a
 * MatDialog opened from the actions menu. Promoted to a real tab so
 * it lives next to the other diagnostics surfaces (Resources,
 * Terminal, Bandwidth) instead of hiding behind the destructive-
 * action menu by the shut-down button.
 *
 * Reuses the existing diff-streaming endpoint
 *   GET /api/visors/<pk>/runtime-logs/since=<cursor>
 * which returns:
 *   { entries: string[]  // each is a JSON-encoded log line
 *     latest:  number,   // new cursor value
 *     dropped: number }  // entries we missed (ring wrapped)
 */

enum Level {
  Panic = 8,
  Fatal = 7,
  Error = 6,
  Warn = 5,
  Info = 4,
  Debug = 3,
  Trace = 2,
  Unknown = 1,
}

interface LogEntry {
  time: string;
  level: Level;
  msg: string;
  func?: string;
  module?: string;
  extra: { name: string, value: string }[];
}

@Component({
  selector: 'app-logs',
  templateUrl: './logs.component.html',
  styleUrls: ['./logs.component.scss'],
  standalone: false,
})
export class LogsComponent extends PageBaseComponent implements OnInit, OnDestroy {
  @ViewChild('content') content: ElementRef;

  node: Node;
  loading = true;
  liveTail = true;
  livePollMs = 2000;
  totalDropped = 0;
  totalLogs = 0;
  hasMoreLogMessages = false;

  // Importance threshold for the visible filter. Lower = more verbose.
  minLevel = Level.Unknown;
  levels = [
    { level: Level.Unknown, label: 'All', cls: '' },
    { level: Level.Debug, label: 'Debug+', cls: '' },
    { level: Level.Info, label: 'Info+', cls: '' },
    { level: Level.Warn, label: 'Warn+', cls: '' },
    { level: Level.Error, label: 'Error+', cls: '' },
    { level: Level.Fatal, label: 'Fatal+', cls: '' },
    { level: Level.Panic, label: 'Panic', cls: '' },
  ];

  logEntries: LogEntry[] = [];
  filteredLogEntries: LogEntry[] = [];
  maxBufferEntries = 1000;

  // Module regex + free-text search filters, both empty by default.
  // The compiled regex is rebuilt on each setModuleFilter() so an
  // invalid pattern leaves the prior valid filter in place and
  // surfaces moduleFilterError to the template for inline feedback.
  moduleFilter = '';
  moduleFilterError: string | null = null;
  private moduleRe: RegExp | null = null;
  textFilter = '';

  private logCursor = 0;
  private wasAtBottom = true;
  private subscription: Subscription;
  private nodeSub: Subscription;
  private shouldShowError = true;

  constructor(
    private nodeService: NodeService,
    private snackbar: SnackbarService,
    private ngZone: NgZone,
    private cdr: ChangeDetectorRef,
  ) { super(); }

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const wasUnset = !this.node;
      this.node = node;
      if (wasUnset && node) { this.loadData(0); }
    });
    return super.ngOnInit();
  }

  ngOnDestroy(): void {
    this.nodeSub?.unsubscribe();
    this.subscription?.unsubscribe();
  }

  setLevel(l: Level) {
    this.minLevel = l;
    this.applyFilter();
  }

  setModuleFilter(value: string) {
    this.moduleFilter = value || '';
    if (!this.moduleFilter) {
      this.moduleRe = null;
      this.moduleFilterError = null;
    } else {
      try {
        this.moduleRe = new RegExp(this.moduleFilter, 'i');
        this.moduleFilterError = null;
      } catch (e: any) {
        // Keep the prior regex active rather than dropping all output;
        // surface the error so the user knows the pattern was rejected.
        this.moduleFilterError = e?.message || 'invalid regex';
        return;
      }
    }
    this.applyFilter();
  }

  setTextFilter(value: string) {
    this.textFilter = value || '';
    this.applyFilter();
  }

  toggleLiveTail() {
    this.liveTail = !this.liveTail;
    if (this.liveTail) { this.loadData(0); } else { this.subscription?.unsubscribe(); }
  }

  fullLogsUrl(): string {
    if (!this.node) { return ''; }
    const apiPrefix = !environment.production && location.protocol.indexOf('http:') !== -1 ? 'http-api' : 'api';
    return window.location.origin + '/' + apiPrefix + '/visors/' + this.node.localPk + '/runtime-logs';
  }

  private loadData(delayMs: number) {
    if (!this.node) { return; }
    this.subscription?.unsubscribe();
    this.captureScrollTailState();
    this.loading = this.logEntries.length === 0;
    const cursor = this.logCursor;

    this.subscription = of(1).pipe(
      delay(delayMs),
      mergeMap(() => this.nodeService.getRuntimeLogsSince(this.node.localPk, cursor)),
    ).subscribe(
      (delta: any) => this.onDelta(delta),
      (err: OperationError) => this.onError(err),
    );
  }

  private onDelta(delta: any) {
    if (!delta) { this.loading = false; this.scheduleNext(); return; }
    const isInitial = this.logCursor === 0;
    this.logCursor = (typeof delta.latest === 'number') ? delta.latest : this.logCursor;
    if (typeof delta.dropped === 'number' && delta.dropped > 0) { this.totalDropped += delta.dropped; }

    const raws: string[] = Array.isArray(delta.entries) ? delta.entries : [];
    const parsed: any[] = [];
    for (const r of raws) {
      try { parsed.push(JSON.parse(r)); } catch { /* skip malformed */ }
    }
    if (isInitial) { this.logEntries = []; }
    this.appendParsed(parsed);

    if (this.logEntries.length > this.maxBufferEntries) {
      this.logEntries = this.logEntries.slice(this.logEntries.length - this.maxBufferEntries);
      this.hasMoreLogMessages = true;
    }
    this.totalLogs = this.logCursor;
    this.loading = false;
    this.shouldShowError = true;
    this.applyFilter();
    this.cdr.markForCheck();
    this.scheduleNext();
  }

  private appendParsed(rows: any[]) {
    for (const e of rows) {
      const entry: LogEntry = {
        time: e.time,
        level: this.parseLevel(e.level),
        msg: e.msg,
        func: e.func,
        module: e._module,
        extra: [],
      };
      if (e.error) { entry.extra.push({ name: 'error', value: e.error }); }
      const known = new Set(['time', '_module', 'msg', 'func', 'level', 'log_line', 'error']);
      for (const k in e) {
        if (!known.has(k)) { entry.extra.push({ name: k, value: e[k] }); }
      }
      this.logEntries.push(entry);
    }
  }

  private parseLevel(s: string): Level {
    const lc = (s || '').toLowerCase();
    if (lc.includes('panic')) { return Level.Panic; }
    if (lc.includes('fatal')) { return Level.Fatal; }
    if (lc.includes('error')) { return Level.Error; }
    if (lc.includes('warn')) { return Level.Warn; }
    if (lc.includes('info')) { return Level.Info; }
    if (lc.includes('debug')) { return Level.Debug; }
    if (lc.includes('trace')) { return Level.Trace; }
    return Level.Unknown;
  }

  private applyFilter() {
    const moduleRe = this.moduleRe;
    const textLC = this.textFilter.toLowerCase();
    this.filteredLogEntries = this.logEntries.filter((e) => {
      if (e.level < this.minLevel) { return false; }
      if (moduleRe && !moduleRe.test(e.module || '')) { return false; }
      if (textLC) {
        // Match against msg, module, and all extra k=v pairs so the
        // search box behaves like ad-hoc grep over the full record.
        if (!(e.msg || '').toLowerCase().includes(textLC) &&
            !(e.module || '').toLowerCase().includes(textLC) &&
            !e.extra.some((kv) => (kv.value || '').toLowerCase().includes(textLC) ||
                                  (kv.name || '').toLowerCase().includes(textLC))) {
          return false;
        }
      }
      return true;
    });
    if (this.wasAtBottom) {
      setTimeout(() => {
        if (this.content) {
          const el = this.content.nativeElement as HTMLElement;
          el.scrollTop = el.scrollHeight;
        }
      });
    }
  }

  private captureScrollTailState() {
    if (!this.content) { this.wasAtBottom = true; return; }
    const el = this.content.nativeElement as HTMLElement;
    this.wasAtBottom = (el.scrollHeight - el.scrollTop - el.clientHeight) < 40;
  }

  private scheduleNext() {
    if (this.liveTail) { this.ngZone.run(() => this.loadData(this.livePollMs)); }
  }

  private onError(err: OperationError) {
    err = processServiceError(err);
    if (this.shouldShowError) {
      this.snackbar.showError('common.loading-error', null, true, err);
      this.shouldShowError = false;
    }
    this.loadData(AppConfig.connectionRetryDelay);
  }

  levelClass(l: Level): string {
    switch (l) {
      case Level.Panic:
      case Level.Fatal:
        return 'lvl-fatal';
      case Level.Error: return 'lvl-error';
      case Level.Warn: return 'lvl-warn';
      case Level.Info: return 'lvl-info';
      case Level.Debug: return 'lvl-debug';
      case Level.Trace: return 'lvl-trace';
      default: return 'lvl-unknown';
    }
  }

  levelName(l: Level): string {
    switch (l) {
      case Level.Panic: return 'PANIC';
      case Level.Fatal: return 'FATAL';
      case Level.Error: return 'ERROR';
      case Level.Warn: return 'WARN';
      case Level.Info: return 'INFO';
      case Level.Debug: return 'DEBUG';
      case Level.Trace: return 'TRACE';
      default: return 'LOG';
    }
  }

  trackEntry(_: number, e: LogEntry) { return e; }
}
