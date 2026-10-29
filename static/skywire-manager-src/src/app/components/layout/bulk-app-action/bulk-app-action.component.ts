import { Component, Inject, OnDestroy, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Observable, Subscription, catchError, concat, defer, from, map, mergeMap, of, toArray } from 'rxjs';

import { AppConfig } from 'src/app/app.config';
import { ApiService } from 'src/app/services/api.service';
import { AppsService } from 'src/app/services/apps.service';
import { processServiceError } from 'src/app/utils/errors';
import { CatalogEntry } from '../../pages/node/apps/app-catalog/app-catalog.component';

export type BulkAppMode = 'add' | 'start' | 'stop';

/** A ticked visor, as the visor list knows it. */
export interface BulkAppNode {
  key: string;
  label: string;
  online: boolean;
  viaSubHypervisor: boolean;
  apps: string[];
}

export interface BulkAppParams {
  mode: BulkAppMode;
  nodes: BulkAppNode[];
}

export interface BulkAppRow {
  node: BulkAppNode;
  state: 'waiting' | 'working' | 'ok' | 'failed' | 'skipped';
  message: string;
}

// How many visors are asked at once.
export const bulkAppLimit = 4;

/** Runs op for every item, limit at a time. A failure is reported for its item and the rest go on. */
export function runLimited<T>(items: T[], limit: number, op: (item: T) => Observable<unknown>):
  Observable<{ item: T; error: string | null }> {
  return from(items).pipe(mergeMap(item => op(item).pipe(
    toArray(),
    map(() => {
      return { item: item, error: null as string | null };
    }),
    catchError(err => {
      const e = processServiceError(err);

      return of({ item: item, error: e.translatableErrorMsg || e.originalServerErrorMsg || String(err) });
    }),
  ), limit));
}

/**
 * Adds, starts or stops one app on each of the visors ticked in the visor
 * list, showing every visor's result.
 */
@Component({
  selector: 'app-bulk-app-action',
  templateUrl: './bulk-app-action.component.html',
  styleUrls: ['./bulk-app-action.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class BulkAppActionComponent implements OnDestroy {
  appOptions: string[] = [];
  descriptions = new Map<string, string>();
  app = '';
  startAfterAdd = false;
  loadingCatalog = false;
  catalogError = '';
  rows: BulkAppRow[];
  started = false;
  finished = false;

  private changed = false;
  private sub?: Subscription;

  public static openDialog(dialog: MatDialog, params: BulkAppParams): MatDialogRef<BulkAppActionComponent, boolean> {
    const config = new MatDialogConfig();
    config.data = params;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(BulkAppActionComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<BulkAppActionComponent, boolean>,
    @Inject(MAT_DIALOG_DATA) public data: BulkAppParams,
    private api: ApiService,
    private appsService: AppsService,
    private cdr: ChangeDetectorRef,
  ) {
    this.rows = data.nodes.map((n): BulkAppRow => {
      return { node: n, state: 'waiting', message: '' };
    });
    if (data.mode === 'add') {
      this.loadCatalog();
    } else {
      this.appOptions = this.configuredApps();
    }
  }

  private configuredApps(): string[] {
    return [...new Set(this.data.nodes.flatMap(n => n.apps))].sort();
  }

  get headline(): string {
    return 'nodes.bulk-apps.title-' + this.data.mode;
  }

  // The catalog is the same on every visor of a version, so one online visor's is enough.
  private loadCatalog() {
    const source = this.data.nodes.find(n => n.online && !n.viaSubHypervisor) || this.data.nodes.find(n => n.online);
    if (!source) {
      this.catalogError = 'nodes.bulk.none-online';

      return;
    }
    this.loadingCatalog = true;
    this.sub = this.api.get(`visors/${source.key}/apps/catalog`).subscribe({
      next: (entries: CatalogEntry[]) => {
        (entries || []).forEach(e => this.descriptions.set(e.name, e.description));
        this.appOptions = (entries || []).map(e => e.name).sort();
        this.loadingCatalog = false;
        this.cdr.markForCheck();
      },
      // Visors older than the catalog route still offer their configured apps.
      error: () => {
        this.catalogError = 'nodes.bulk-apps.catalog-fallback';
        this.appOptions = this.configuredApps();
        this.loadingCatalog = false;
        this.cdr.markForCheck();
      },
    });
  }

  /** Why a visor is left out before anything is sent, or '' when it is not. */
  skipReason(n: BulkAppNode): string {
    if (!n.online) {
      return 'nodes.bulk-apps.offline';
    }
    const has = n.apps.includes(this.app);
    if (this.data.mode === 'add') {
      return has && !this.startAfterAdd ? 'nodes.bulk-apps.already-added' : '';
    }

    return has ? '' : 'nodes.bulk-apps.not-configured';
  }

  private operation(n: BulkAppNode): Observable<unknown> {
    if (this.data.mode !== 'add') {
      return this.appsService.changeAppState(n.key, this.app, this.data.mode === 'start');
    }
    const start = defer(() => this.appsService.changeAppState(n.key, this.app, true));
    if (n.apps.includes(this.app)) {
      return start;
    }
    const add = this.appsService.addApp(n.key, this.app, this.app);

    return this.startAfterAdd ? concat(add, start) : add;
  }

  run() {
    if (!this.app || this.started) {
      return;
    }
    this.started = true;
    const todo: BulkAppRow[] = [];
    this.rows.forEach(r => {
      const reason = this.skipReason(r.node);
      r.state = reason ? 'skipped' : 'waiting';
      r.message = reason;
      if (!reason) {
        todo.push(r);
      }
    });
    this.sub?.unsubscribe();
    this.sub = runLimited(todo, bulkAppLimit, r => {
      r.state = 'working';
      this.cdr.markForCheck();

      return this.operation(r.node);
    }).subscribe({
      next: res => {
        res.item.state = res.error ? 'failed' : 'ok';
        res.item.message = res.error || '';
        this.changed = this.changed || !res.error;
        this.cdr.markForCheck();
      },
      complete: () => {
        this.finished = true;
        this.cdr.markForCheck();
      },
    });
    this.cdr.markForCheck();
  }

  get counts(): { ok: number; failed: number; skipped: number } {
    return {
      ok: this.rows.filter(r => r.state === 'ok').length,
      failed: this.rows.filter(r => r.state === 'failed').length,
      skipped: this.rows.filter(r => r.state === 'skipped').length,
    };
  }

  close() {
    this.dialogRef.close(this.changed);
  }

  ngOnDestroy() {
    this.sub?.unsubscribe();
  }
}
