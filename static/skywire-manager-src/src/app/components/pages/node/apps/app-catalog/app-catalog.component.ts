import { Component, OnInit, OnDestroy, ChangeDetectionStrategy, ChangeDetectorRef, inject } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';
import { Observable, Subscription, catchError, of, switchMap } from 'rxjs';

import { Application, Node } from '../../../../../app.datatypes';
import { ApiService } from '../../../../../services/api.service';
import { AppsService } from '../../../../../services/apps.service';
import { LiveService } from '../../../../../services/live.service';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { NodeComponent } from '../../node.component';
import { LogComponent } from '../node-apps-list/log/log.component';
import GeneralUtils from '../../../../../utils/generalUtils';
import { processServiceError } from '../../../../../utils/errors';
import { serverTypeFor } from '../app-flags-form/app-flags';
import { appStatusTone } from '../../../../../utils/app-status';

/** One configured instance of a catalog app (visorapi.CatalogInstance). */
export interface CatalogInstance {
  name: string;
  port: number;
  auto_start: boolean;
  status: string;
  detailed_status?: string;
}

/** One app the visor can run (visorapi.CatalogEntry). */
export interface CatalogEntry {
  name: string;
  kind: string;
  description: string;
  multi_instance: boolean;
  instances: CatalogInstance[];
}

export interface CatalogGroup {
  kind: string;
  entries: CatalogEntry[];
}

export const catalogKinds = ['server', 'client', 'service', 'external'];

/** Groups the entries by kind in catalogKinds order, dropping empty groups. */
export function groupCatalog(entries: CatalogEntry[]): CatalogGroup[] {
  const kindOf = (e: CatalogEntry) => catalogKinds.includes(e.kind) ? e.kind : 'external';

  return catalogKinds
    .map(kind => {
      return { kind: kind, entries: entries.filter(e => kindOf(e) === kind) };
    })
    .filter(g => g.entries.length > 0);
}

/** The first free name of the form <app>-<n>, n from 2. */
export function nextInstanceName(entry: CatalogEntry, taken: string[] = []): string {
  const used = new Set(entry.instances.map(i => i.name).concat(taken));
  let n = 2;
  while (used.has(`${entry.name}-${n}`)) {
    n++;
  }

  return `${entry.name}-${n}`;
}

/** The numeric Application status for a catalog status string. */
export function statusCode(status: string): number {
  switch (status) {
    case 'running': return 1;
    case 'errored': return 2;
    case 'starting': return 3;
    default: return 0;
  }
}

/** Whether a detailed status says more than the status badge beside it. */
export function detailAddsInfo(detail: string, ...shown: string[]): boolean {
  const norm = (s: string) => (s || '').trim().toLowerCase();

  return norm(detail) !== '' && !shown.some(s => norm(s) === norm(detail));
}

/**
 * Every app the visor can run, grouped by kind, with add, start, stop and
 * autostart actions. Follows the catalog over the live socket.
 */
@Component({
  selector: 'app-app-catalog',
  templateUrl: './app-catalog.component.html',
  styleUrls: ['./app-catalog.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class AppCatalogComponent implements OnInit, OnDestroy {
  private cdr = inject(ChangeDetectorRef);
  private api = inject(ApiService);
  private appsService = inject(AppsService);
  private live = inject(LiveService);
  private snackbar = inject(SnackbarService);
  private dialog = inject(MatDialog);
  private translate = inject(TranslateService);

  groups: CatalogGroup[] = [];
  loading = true;
  error = '';
  nodePK = '';
  busy = new Set<string>();
  openSettings = new Set<string>();
  // Server picker requests per instance, passed to its settings panel.
  pickRequests = new Map<string, number>();

  private apps = new Map<string, Application>();
  private nodeSub?: Subscription;
  private liveSub?: Subscription;
  private refreshSub?: Subscription;

  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node: Node) => {
      const known = new Set(this.apps.keys());
      this.apps = new Map((node.apps || []).map(a => [a.name, a]));
      const changed = known.size !== this.apps.size || [...this.apps.keys()].some(n => !known.has(n));
      if (node.localPk !== this.nodePK) {
        this.nodePK = node.localPk;
        this.follow();
      } else if (changed) {
        this.refresh();
      }
      this.cdr.markForCheck();
    });
  }

  ngOnDestroy() {
    this.nodeSub?.unsubscribe();
    this.liveSub?.unsubscribe();
    this.refreshSub?.unsubscribe();
  }

  private get url(): string {
    return `visors/${this.nodePK}/apps/catalog`;
  }

  private follow() {
    this.liveSub?.unsubscribe();
    this.loading = true;
    this.liveSub = this.live.watch<CatalogEntry[]>(this.url, 5000, () => this.api.get(this.url)).pipe(
      switchMap(res => this.load(res)),
    ).subscribe();
  }

  /** Fetches the catalog once, after an action or an apps change. */
  refresh() {
    if (!this.nodePK) {
      return;
    }
    this.refreshSub?.unsubscribe();
    this.refreshSub = this.load(this.api.get(this.url)).subscribe();
  }

  private load(source: Observable<CatalogEntry[]>): Observable<null> {
    return source.pipe(
      switchMap(entries => {
        this.groups = groupCatalog(entries || []);
        this.error = '';
        this.loading = false;
        this.cdr.markForCheck();

        return of(null);
      }),
      catchError(err => {
        this.error = processServiceError(err).translatableErrorMsg;
        this.loading = false;
        this.cdr.markForCheck();

        return of(null);
      }),
    );
  }

  /** The node's app for an instance, which has the newest status. */
  appFor(inst: CatalogInstance): Application | undefined {
    return this.apps.get(inst.name);
  }

  status(inst: CatalogInstance): number {
    const app = this.appFor(inst);

    return app ? app.status : statusCode(inst.status);
  }

  autostart(inst: CatalogInstance): boolean {
    const app = this.appFor(inst);

    return app ? app.autostart : inst.auto_start;
  }

  detail(inst: CatalogInstance): string {
    const app = this.appFor(inst);

    return (app ? app.detailedStatus : inst.detailed_status) || '';
  }

  /** The detailed status, or nothing when it only repeats the badge. */
  extraDetail(inst: CatalogInstance): string {
    const d = this.detail(inst);

    return detailAddsInfo(d, this.translate.instant(this.statusKey(inst)), inst.status) ? d : '';
  }

  statusKey(inst: CatalogInstance): string {
    return ['apps.status-stopped', 'apps.status-running', 'apps.catalog.failed', 'apps.status-connecting'][this.status(inst)]
      || 'apps.status-stopped';
  }

  statusClass(inst: CatalogInstance): string {
    return 'status-' + appStatusTone(this.status(inst));
  }

  isRunning(inst: CatalogInstance): boolean {
    const s = this.status(inst);

    return s === 1 || s === 3;
  }

  /** Adds the entry's first instance, or another one under a proposed name. */
  add(entry: CatalogEntry, another = false) {
    let name = entry.name;
    if (another) {
      const proposed = nextInstanceName(entry, [...this.apps.keys()]);
      name = (window.prompt(this.translate.instant('apps.catalog.name-prompt'), proposed) || '').trim();
      if (!name) {
        return;
      }
    }
    const key = 'add:' + entry.name;
    if (this.busy.has(key)) {
      return;
    }
    this.busy.add(key);
    this.appsService.addApp(this.nodePK, name, entry.name).subscribe({
      next: () => {
        this.busy.delete(key);
        this.afterAction();
        this.offerStart(name);
      },
      error: err => {
        this.busy.delete(key);
        this.snackbar.showError(processServiceError(err));
        this.cdr.markForCheck();
      },
    });
  }

  private offerStart(name: string) {
    const text = this.translate.instant('apps.catalog.start-prompt', { app: name });
    const dialog = GeneralUtils.createConfirmationDialog(this.dialog, text);
    dialog.componentInstance.operationAccepted.subscribe(() => {
      dialog.close();
      this.run(name, this.appsService.changeAppState(this.nodePK, name, true));
      this.cdr.markForCheck();
    });
  }

  toggleState(inst: CatalogInstance) {
    this.run(inst.name, this.appsService.changeAppState(this.nodePK, inst.name, !this.isRunning(inst)));
  }

  toggleAutostart(inst: CatalogInstance) {
    this.run(inst.name, this.appsService.changeAppAutostart(this.nodePK, inst.name, !this.autostart(inst)));
  }

  private run(name: string, op: Observable<any>) {
    if (this.busy.has(name)) {
      return;
    }
    this.busy.add(name);
    this.cdr.markForCheck();
    op.subscribe({
      next: () => {
        this.busy.delete(name);
        this.snackbar.showDone('apps.operation-completed');
        this.afterAction();
      },
      error: err => {
        this.busy.delete(name);
        this.snackbar.showError(processServiceError(err));
        this.afterAction();
      },
    });
  }

  private afterAction() {
    NodeComponent.refreshCurrentDisplayedData();
    this.refresh();
    this.cdr.markForCheck();
  }

  viewLogs(inst: CatalogInstance) {
    const app = this.appFor(inst);
    if (app && this.isRunning(inst)) {
      LogComponent.openDialog(this.dialog, app);
    } else {
      this.snackbar.showError('apps.apps-list.unavailable-logs-error');
    }
  }

  /** True for a client whose servers are listed in the service discovery. */
  canChooseServer(entry: CatalogEntry): boolean {
    return entry.kind === 'client' && !!serverTypeFor(entry.name);
  }

  /** Opens the instance's settings with the server picker on its server flag. */
  chooseServer(inst: CatalogInstance) {
    this.openSettings.add(inst.name);
    this.pickRequests.set(inst.name, (this.pickRequests.get(inst.name) || 0) + 1);
    this.cdr.markForCheck();
  }

  toggleSettings(name: string) {
    if (this.openSettings.has(name)) {
      this.openSettings.delete(name);
      this.pickRequests.delete(name);
    } else {
      this.openSettings.add(name);
    }
    this.cdr.markForCheck();
  }
}
