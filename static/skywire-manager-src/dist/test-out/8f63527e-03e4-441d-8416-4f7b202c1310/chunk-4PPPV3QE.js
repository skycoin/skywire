import {
  AppsService
} from "./chunk-MX3HPERK.js";
import {
  AppConfig,
  MAT_DIALOG_DATA,
  MatDialogConfig,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  ApiService,
  processServiceError
} from "./chunk-ZMZW6HXB.js";
import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  Inject,
  catchError,
  concat,
  defer,
  from,
  map,
  mergeMap,
  of,
  toArray
} from "./chunk-ZNLGPAUZ.js";

// angular:jit:template:src/app/components/layout/bulk-app-action/bulk-app-action.component.html
var bulk_app_action_component_default = `<app-dialog [headline]="headline | translate" [dialog]="dialogRef" [disableDismiss]="started && !finished">
  @if (loadingCatalog) {
    <div class="note blinking">{{ 'nodes.bulk-apps.loading-catalog' | translate }}</div>
  }
  @if (catalogError) {
    <div class="note">{{ catalogError | translate }}</div>
  }
  @if (appOptions.length > 0) {
    <mat-form-field>
      <div class="field-container">
        <label class="field-label">{{ 'nodes.bulk-apps.app' | translate }}</label>
        <mat-select class="app-select" [value]="app" (selectionChange)="app = $event.value" [disabled]="started">
          @for (name of appOptions; track name) {
            <mat-option [value]="name">{{ name }}@if (descriptions.get(name)) { <span class="desc"> &middot; {{ descriptions.get(name) }}</span> }</mat-option>
          }
        </mat-select>
      </div>
    </mat-form-field>
  }
  @if (data.mode === 'add') {
    <mat-checkbox class="start-after" color="primary" [checked]="startAfterAdd" [disabled]="started" (change)="startAfterAdd = $event.checked">
      {{ 'nodes.bulk-apps.start-after-add' | translate }}
    </mat-checkbox>
  }

  <div class="rows">
    @for (row of rows; track row.node.key) {
      <div class="result-row" [attr.data-pk]="row.node.key" [attr.data-state]="row.state">
        <span class="label">
          {{ row.node.label }}
          @if (row.node.viaSubHypervisor) {
            <span class="via">({{ 'nodes.bulk-apps.via-sub' | translate }})</span>
          }
        </span>
        <span class="result" [class.green-text]="row.state === 'ok'" [class.red-text]="row.state === 'failed'">
          @if (row.state === 'ok') {
            {{ 'nodes.bulk-apps.ok' | translate }}
          } @else if (row.state === 'failed' || row.state === 'skipped') {
            {{ row.message | translate }}
          } @else if (started) {
            <span [class.blinking]="row.state === 'working'">{{ 'nodes.bulk-apps.' + row.state | translate }}</span>
          } @else if (app && skipReason(row.node)) {
            <span class="muted">{{ skipReason(row.node) | translate }}</span>
          }
        </span>
      </div>
    }
  </div>

  @if (finished) {
    <div class="note summary">{{ 'nodes.bulk-apps.summary' | translate: counts }}</div>
  }

  <div class="buttons">
    @if (!started) {
      <button mat-raised-button color="primary" class="run-button" [disabled]="!app" (click)="run()">
        {{ 'nodes.bulk-apps.run' | translate: { n: rows.length } }}
      </button>
    } @else {
      <button mat-raised-button color="primary" class="close-button" [disabled]="!finished" (click)="close()">
        {{ 'common.close' | translate }}
      </button>
    }
  </div>
</app-dialog>
`;

// angular:jit:style:src/app/components/layout/bulk-app-action/bulk-app-action.component.scss
var bulk_app_action_component_default2 = "/* src/app/components/layout/bulk-app-action/bulk-app-action.component.scss */\n.note {\n  font-size: 14px;\n  margin: 6px 0;\n  color: #215f9e;\n}\n.desc {\n  opacity: 0.7;\n  font-size: 0.9em;\n}\n.start-after {\n  display: block;\n  margin: 4px 0 8px;\n}\n.rows {\n  font-size: 14px;\n  color: #215f9e;\n  max-height: 45vh;\n  overflow-y: auto;\n  margin: 8px 0;\n}\n.rows .result-row {\n  display: flex;\n  justify-content: space-between;\n  gap: 12px;\n  padding: 5px 0;\n  border-bottom: 1px solid rgba(var(--sw-shade-rgb), 0.08);\n}\n.rows .label {\n  word-break: break-word;\n}\n.rows .via,\n.rows .muted {\n  color: #4a5563;\n  font-size: 0.9em;\n}\n.rows .result {\n  text-align: right;\n  flex-shrink: 0;\n  max-width: 55%;\n  word-break: break-word;\n}\n.buttons {\n  margin-top: 12px;\n  text-align: right;\n}\n/*# sourceMappingURL=bulk-app-action.component.css.map */\n";

// src/app/components/layout/bulk-app-action/bulk-app-action.component.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var BulkAppActionComponent_1;
var bulkAppLimit = 4;
function runLimited(items, limit, op) {
  return from(items).pipe(mergeMap((item) => op(item).pipe(toArray(), map(() => {
    return { item, error: null };
  }), catchError((err) => {
    const e = processServiceError(err);
    return of({ item, error: e.translatableErrorMsg || e.originalServerErrorMsg || String(err) });
  })), limit));
}
var BulkAppActionComponent = class BulkAppActionComponent2 {
  static {
    BulkAppActionComponent_1 = this;
  }
  static openDialog(dialog, params) {
    const config = new MatDialogConfig();
    config.data = params;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;
    return dialog.open(BulkAppActionComponent_1, config);
  }
  constructor(dialogRef, data, api, appsService, cdr) {
    this.dialogRef = dialogRef;
    this.data = data;
    this.api = api;
    this.appsService = appsService;
    this.cdr = cdr;
    this.appOptions = [];
    this.descriptions = /* @__PURE__ */ new Map();
    this.app = "";
    this.startAfterAdd = false;
    this.loadingCatalog = false;
    this.catalogError = "";
    this.started = false;
    this.finished = false;
    this.changed = false;
    this.rows = data.nodes.map((n) => {
      return { node: n, state: "waiting", message: "" };
    });
    if (data.mode === "add") {
      this.loadCatalog();
    } else {
      this.appOptions = this.configuredApps();
    }
  }
  configuredApps() {
    return [...new Set(this.data.nodes.flatMap((n) => n.apps))].sort();
  }
  get headline() {
    return "nodes.bulk-apps.title-" + this.data.mode;
  }
  // The catalog is the same on every visor of a version, so one online visor's is enough.
  loadCatalog() {
    const source = this.data.nodes.find((n) => n.online && !n.viaSubHypervisor) || this.data.nodes.find((n) => n.online);
    if (!source) {
      this.catalogError = "nodes.bulk.none-online";
      return;
    }
    this.loadingCatalog = true;
    this.sub = this.api.get(`visors/${source.key}/apps/catalog`).subscribe({
      next: (entries) => {
        (entries || []).forEach((e) => this.descriptions.set(e.name, e.description));
        this.appOptions = (entries || []).map((e) => e.name).sort();
        this.loadingCatalog = false;
        this.cdr.markForCheck();
      },
      // Visors older than the catalog route still offer their configured apps.
      error: () => {
        this.catalogError = "nodes.bulk-apps.catalog-fallback";
        this.appOptions = this.configuredApps();
        this.loadingCatalog = false;
        this.cdr.markForCheck();
      }
    });
  }
  /** Why a visor is left out before anything is sent, or '' when it is not. */
  skipReason(n) {
    if (!n.online) {
      return "nodes.bulk-apps.offline";
    }
    const has = n.apps.includes(this.app);
    if (this.data.mode === "add") {
      return has && !this.startAfterAdd ? "nodes.bulk-apps.already-added" : "";
    }
    return has ? "" : "nodes.bulk-apps.not-configured";
  }
  operation(n) {
    if (this.data.mode !== "add") {
      return this.appsService.changeAppState(n.key, this.app, this.data.mode === "start");
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
    const todo = [];
    this.rows.forEach((r) => {
      const reason = this.skipReason(r.node);
      r.state = reason ? "skipped" : "waiting";
      r.message = reason;
      if (!reason) {
        todo.push(r);
      }
    });
    this.sub?.unsubscribe();
    this.sub = runLimited(todo, bulkAppLimit, (r) => {
      r.state = "working";
      this.cdr.markForCheck();
      return this.operation(r.node);
    }).subscribe({
      next: (res) => {
        res.item.state = res.error ? "failed" : "ok";
        res.item.message = res.error || "";
        this.changed = this.changed || !res.error;
        this.cdr.markForCheck();
      },
      complete: () => {
        this.finished = true;
        this.cdr.markForCheck();
      }
    });
    this.cdr.markForCheck();
  }
  get counts() {
    return {
      ok: this.rows.filter((r) => r.state === "ok").length,
      failed: this.rows.filter((r) => r.state === "failed").length,
      skipped: this.rows.filter((r) => r.state === "skipped").length
    };
  }
  close() {
    this.dialogRef.close(this.changed);
  }
  ngOnDestroy() {
    this.sub?.unsubscribe();
  }
  static {
    this.ctorParameters = () => [
      { type: MatDialogRef },
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: ApiService },
      { type: AppsService },
      { type: ChangeDetectorRef }
    ];
  }
};
BulkAppActionComponent = BulkAppActionComponent_1 = __decorate([
  Component({
    selector: "app-bulk-app-action",
    template: bulk_app_action_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [bulk_app_action_component_default2]
  })
], BulkAppActionComponent);

export {
  bulkAppLimit,
  runLimited,
  BulkAppActionComponent
};
//# debugId=6721335c-1b18-5de1-8b6d-ce52a4a58e91
//# sourceMappingURL=chunk-4PPPV3QE.js.map
