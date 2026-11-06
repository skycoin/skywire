import {
  serverTypeFor
} from "./chunk-Z3QUGWOD.js";
import "./chunk-BGNEFYT6.js";
import {
  appStatusTone
} from "./chunk-WTGB3LY4.js";
import "./chunk-XSOC4ELV.js";
import {
  TestBed
} from "./chunk-UQCSELO5.js";
import {
  NodeComponent
} from "./chunk-CIWHQAPM.js";
import {
  GeneralUtils
} from "./chunk-KWVFHEEW.js";
import "./chunk-HLYS7KBE.js";
import {
  UntypedFormBuilder
} from "./chunk-B4GMTCLR.js";
import {
  AppsService
} from "./chunk-MX3HPERK.js";
import {
  AppConfig,
  MAT_DIALOG_DATA,
  MatDialog,
  MatDialogConfig,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  TranslatePipe,
  TranslateService,
  provideTranslateService
} from "./chunk-6WNZTOXO.js";
import {
  LiveService
} from "./chunk-GPHL7TNH.js";
import "./chunk-H3STL4JE.js";
import "./chunk-XBRWDJ2O.js";
import {
  ApiService,
  SnackbarService,
  processServiceError
} from "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import "./chunk-XOUXCPTJ.js";
import "./chunk-VNMLTXOX.js";
import {
  BehaviorSubject,
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  Inject,
  NO_ERRORS_SCHEMA,
  ViewChild,
  catchError,
  delay,
  inject,
  mergeMap,
  of,
  switchMap
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// angular:jit:template:src/app/components/pages/node/apps/app-catalog/app-catalog.component.html
var app_catalog_component_default = `@if (loading && groups.length === 0) {
  <div class="cat-note">{{ 'apps.catalog.loading' | translate }}</div>
}
@if (error) {
  <div class="cat-note cat-error">{{ error | translate }}</div>
}

@for (group of groups; track group.kind) {
  <section class="cat-group">
    <h3 class="cat-group-title">
      {{ 'apps.catalog.kinds.' + group.kind | translate }}
      <span class="cat-count">{{ group.entries.length }}</span>
    </h3>
    <div class="cat-grid">
      @for (entry of group.entries; track entry.name) {
        <div class="cat-card" [attr.data-app]="entry.name">
          <div class="cat-card-header">
            <span class="cat-name">{{ entry.name }}</span>
            <span class="cat-kind">{{ 'apps.catalog.kind.' + entry.kind | translate }}</span>
          </div>
          @if (entry.description) {
            <div class="cat-desc">{{ entry.description }}</div>
          }

          @if (entry.instances.length === 0) {
            <div class="cat-empty">
              <span class="cat-not-added">{{ 'apps.catalog.not-added' | translate }}</span>
              <button mat-button class="cat-btn cat-add" [disabled]="busy.has('add:' + entry.name)" (click)="add(entry)">
                <mat-icon [inline]="true">add</mat-icon>
                {{ 'apps.catalog.add' | translate }}
              </button>
            </div>
          }

          @for (inst of entry.instances; track inst.name) {
            <div class="cat-instance" [attr.data-instance]="inst.name">
              <div class="cat-instance-row">
                <span class="cat-instance-name">{{ inst.name }}</span>
                <span class="cat-status" [ngClass]="statusClass(inst)" [title]="detail(inst)">
                  {{ statusKey(inst) | translate }}
                </span>
              </div>
              <div class="cat-instance-meta">
                @if (inst.port) {
                  <span>{{ 'apps.catalog.port' | translate }} {{ inst.port }}</span>
                }
                <span>{{ 'apps.catalog.autostart' | translate }}:
                  {{ (autostart(inst) ? 'apps.catalog.on' : 'apps.catalog.off') | translate }}</span>
              </div>
              @if (extraDetail(inst)) {
                <div class="cat-detail">{{ extraDetail(inst) }}</div>
              }
              <div class="cat-actions">
                <button mat-button class="cat-btn cat-state" [disabled]="busy.has(inst.name)" (click)="toggleState(inst)">
                  <mat-icon [inline]="true">{{ isRunning(inst) ? 'stop' : 'play_arrow' }}</mat-icon>
                  {{ (isRunning(inst) ? 'apps.stop-app' : 'apps.start-app') | translate }}
                </button>
                @let autostartLabel = (autostart(inst) ? 'apps.apps-list.disable-autostart' : 'apps.apps-list.enable-autostart') | translate;
                <button mat-icon-button class="cat-icon-btn cat-autostart" [class.on]="autostart(inst)" [disabled]="busy.has(inst.name)"
                  (click)="toggleAutostart(inst)" [matTooltip]="autostartLabel" [attr.aria-label]="autostartLabel">
                  <mat-icon>power_settings_new</mat-icon>
                </button>
                <button mat-icon-button class="cat-icon-btn cat-logs" (click)="viewLogs(inst)"
                  [matTooltip]="'apps.view-logs' | translate" [attr.aria-label]="'apps.view-logs' | translate">
                  <mat-icon>list</mat-icon>
                </button>
                @if (appFor(inst) && canChooseServer(entry)) {
                  <button mat-icon-button class="cat-icon-btn cat-choose-server" (click)="chooseServer(inst)"
                    [matTooltip]="'apps.catalog.choose-server' | translate" [attr.aria-label]="'apps.catalog.choose-server' | translate">
                    <mat-icon>dns</mat-icon>
                  </button>
                }
                @if (appFor(inst)) {
                  <button mat-icon-button class="cat-icon-btn cat-settings" [class.on]="openSettings.has(inst.name)" (click)="toggleSettings(inst.name)"
                    [matTooltip]="'apps.settings' | translate" [attr.aria-label]="'apps.settings' | translate"
                    [attr.aria-expanded]="openSettings.has(inst.name)">
                    <mat-icon>{{ openSettings.has(inst.name) ? 'expand_less' : 'settings' }}</mat-icon>
                  </button>
                }
              </div>
              @if (openSettings.has(inst.name) && appFor(inst)) {
                <app-app-settings
                  [app]="appFor(inst)!"
                  [pickServer]="pickRequests.get(inst.name) || 0"
                  (saved)="toggleSettings(inst.name); refresh()"
                  (cancelled)="toggleSettings(inst.name)">
                </app-app-settings>
              }
            </div>
          }

          @if (entry.multi_instance && entry.instances.length > 0) {
            <button mat-button class="cat-btn cat-add-another" [disabled]="busy.has('add:' + entry.name)" (click)="add(entry, true)">
              <mat-icon [inline]="true">add</mat-icon>
              {{ 'apps.catalog.add-another' | translate }}
            </button>
          }
        </div>
      }
    </div>
  </section>
}
`;

// angular:jit:style:src/app/components/pages/node/apps/app-catalog/app-catalog.component.scss
var app_catalog_component_default2 = "/* src/app/components/pages/node/apps/app-catalog/app-catalog.component.scss */\n.cat-note {\n  margin-top: 14px;\n  color: rgba(var(--sw-fg-rgb), 0.75);\n}\n.cat-note.cat-error {\n  color: rgb(var(--sw-err-rgb));\n}\n.cat-group {\n  margin-top: 18px;\n}\n.cat-group-title {\n  margin: 0 0 10px 0;\n  font-size: 1em;\n  font-weight: 600;\n  letter-spacing: 0.5px;\n  text-transform: uppercase;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n}\n.cat-count {\n  margin-left: 6px;\n  font-weight: 400;\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n}\n.cat-grid {\n  display: grid;\n  grid-template-columns: repeat(auto-fit, minmax(min(280px, 100%), 1fr));\n  gap: 12px;\n}\n.cat-card {\n  padding: 14px 16px;\n  background: rgba(var(--sw-shade-rgb), 0.18);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n  border-radius: 6px;\n  min-width: 0;\n}\n.cat-card-header {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n}\n.cat-name {\n  font-size: 1.05em;\n  font-weight: 600;\n  color: rgba(var(--sw-fg-rgb), 0.92);\n  overflow-wrap: anywhere;\n}\n.cat-kind {\n  margin-left: auto;\n  font-size: 0.8em;\n  padding: 1px 8px;\n  border-radius: 999px;\n  border: 1px solid rgba(var(--sw-cyan-rgb), 0.35);\n  color: rgb(var(--sw-cyan-rgb));\n  white-space: nowrap;\n}\n.cat-desc {\n  margin: 6px 0 4px 0;\n  line-height: 1.4;\n  color: rgba(var(--sw-fg-rgb), 0.72);\n}\n.cat-empty {\n  display: flex;\n  align-items: center;\n  gap: 10px;\n  margin-top: 10px;\n}\n.cat-not-added {\n  font-style: italic;\n  color: rgba(var(--sw-fg-rgb), var(--sw-a65));\n}\n.cat-instance {\n  margin-top: 10px;\n  padding: 10px 12px;\n  background: rgba(var(--sw-cyan-rgb), 0.05);\n  border-left: 2px solid rgba(var(--sw-cyan-rgb), 0.3);\n  border-radius: 4px;\n}\n.cat-instance-row {\n  display: flex;\n  align-items: center;\n  gap: 10px;\n}\n.cat-instance-name {\n  color: rgba(var(--sw-fg-rgb), 0.92);\n  overflow-wrap: anywhere;\n}\n.cat-status {\n  margin-left: auto;\n  font-size: 0.85em;\n  padding: 2px 10px;\n  border-radius: 999px;\n  white-space: nowrap;\n  background: rgba(var(--sw-fg-rgb), 0.08);\n  color: rgba(var(--sw-fg-rgb), 0.75);\n}\n.cat-status.status-running {\n  background: rgba(var(--sw-ok-rgb), 0.18);\n  color: rgb(var(--sw-ok-rgb));\n}\n.cat-status.status-stopped {\n  background: rgba(var(--sw-fg-rgb), 0.08);\n  color: var(--sw-status-stopped);\n}\n.cat-status.status-failed {\n  background: rgba(var(--sw-err-rgb), 0.25);\n  color: #fca5a5;\n}\n.cat-status.status-starting {\n  background: rgba(var(--sw-warn-rgb), 0.15);\n  color: rgb(var(--sw-warn-rgb));\n}\n.cat-instance-meta {\n  display: flex;\n  flex-wrap: wrap;\n  gap: 14px;\n  margin-top: 4px;\n  font-size: 0.88em;\n  color: rgba(var(--sw-fg-rgb), 0.7);\n}\n.cat-detail {\n  margin-top: 4px;\n  font-size: 0.85em;\n  color: rgb(var(--sw-warn-rgb));\n  overflow-wrap: anywhere;\n}\n.cat-actions {\n  display: flex;\n  flex-wrap: wrap;\n  align-items: center;\n  gap: 2px;\n  margin-top: 8px;\n}\n.cat-actions .cat-state {\n  margin-right: auto;\n  border: 1px solid rgba(var(--sw-cyan-rgb), 0.5);\n  color: rgb(var(--sw-cyan-rgb)) !important;\n}\n.cat-icon-btn {\n  width: 34px !important;\n  height: 34px !important;\n  padding: 0 !important;\n  display: inline-flex !important;\n  align-items: center;\n  justify-content: center;\n  color: rgba(var(--sw-fg-rgb), 0.75) !important;\n}\n.cat-icon-btn.on {\n  color: rgb(var(--sw-cyan-rgb)) !important;\n}\n.cat-icon-btn:hover {\n  color: rgb(var(--sw-cyan-rgb)) !important;\n}\n.cat-icon-btn[disabled] {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a40)) !important;\n}\n.cat-icon-btn .mat-icon {\n  font-size: 20px;\n  height: 20px;\n  width: 20px;\n}\n.cat-btn {\n  color: rgba(var(--sw-fg-rgb), 0.8) !important;\n  display: inline-flex;\n  align-items: center;\n  gap: 4px;\n  font-size: 0.88em;\n}\n.cat-btn:hover {\n  color: rgb(var(--sw-cyan-rgb)) !important;\n}\n.cat-btn[disabled] {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a40)) !important;\n}\n.cat-btn .mat-icon {\n  font-size: 16px;\n  height: 16px;\n  width: 16px;\n}\n.cat-add,\n.cat-add-another {\n  color: rgb(var(--sw-cyan-rgb)) !important;\n}\n.cat-add-another {\n  margin-top: 8px;\n}\n/*# sourceMappingURL=app-catalog.component.css.map */\n";

// angular:jit:template:src/app/components/pages/node/apps/node-apps-list/log/log.component.html
var log_component_default = `<app-dialog [headline]="'apps.log.title' | translate" [includeVerticalMargins]="false" [includeScrollableArea]="false" [dialog]="dialogRef">
  <!-- Filter button. -->
  <div class="top-dialog-button" (click)="filter()">
    <div class="top-dialog-button-content">
      <div>
        <span>{{ 'apps.log.filter-button' | translate }} </span>
        <span class="actual-value">{{ currentFilter.text | translate }}</span>
      </div>
    </div>
    <div class="top-dialog-button-margin"></div>
  </div>

  <mat-dialog-content #content>
    <!-- Button for opening all the logs. -->
    <a [href]="getLogsUrl()" target="_blank">
      @if (hasMoreLogMessages) {
        <app-button class="full-logs-button" color="primary">
          <div class="text-container">
            {{ 'apps.log.view-all' | translate:{ totalLogs: totalLogs } }}
          </div>
        </app-button>
      }
    </a>

    <!-- All entries. -->
    @for (message of logMessages; track message) {
      <div class="app-log-message">
        <span class="transparent">
          {{ message.time }}
        </span>
        {{ message.msg }}
      </div>
    }

    <!-- Msg if empty. -->
    @if (!loading && (!logMessages || logMessages.length === 0)) {
      <div class="app-log-empty mt-3">
        {{ 'apps.log.empty' | translate }}
      </div>
    }
    @if (loading) {
      <app-loading-indicator [showWhite]="false"></app-loading-indicator>
    }
  </mat-dialog-content>
</app-dialog>
`;

// angular:jit:style:src/app/components/pages/node/apps/node-apps-list/log/log.component.scss
var log_component_default2 = "/* src/app/components/pages/node/apps/node-apps-list/log/log.component.scss */\n.mat-mdc-dialog-content {\n  font-size: 0.875rem;\n}\n.app-log-message {\n  margin-top: 15px;\n  word-break: break-word;\n}\n.app-log-message .transparent {\n  color: #999;\n}\n.app-log-message:first-of-type {\n  margin-top: 0px;\n}\n.app-log-message:last-of-type {\n  margin-bottom: 24px;\n}\n.top-dialog-button {\n  color: #777 !important;\n}\n.top-dialog-button .top-dialog-button-content {\n  text-align: center;\n}\n.top-dialog-button .top-dialog-button-content .actual-value {\n  color: #202226;\n}\n.full-logs-button ::ng-deep button {\n  width: 100% !important;\n  display: block;\n  text-align: center;\n  margin-bottom: 15px;\n}\n.full-logs-button .text-container {\n  width: 100%;\n}\n/*# sourceMappingURL=log.component.css.map */\n";

// angular:jit:template:src/app/components/pages/node/apps/node-apps-list/log/log-filter/log-filter.component.html
var log_filter_component_default = `<app-dialog [headline]="'apps.log.filter.title' | translate" [dialog]="dialogRef">
  <form [formGroup]="form">
    <mat-form-field>
      <div class="field-container">
        <label class="field-label" for="remoteKey">{{ 'apps.log.filter.filter' | translate }}</label>
        <mat-select formControlName="filter">
          @for (filter of filters; track filter) {
            <mat-option [value]="filter.days">{{ filter.text | translate }}</mat-option>
          }
        </mat-select>
      </div>
    </mat-form-field>
  </form>
</app-dialog>
`;

// angular:jit:style:src/app/components/pages/node/apps/node-apps-list/log/log-filter/log-filter.component.scss
var log_filter_component_default2 = "/* src/app/components/pages/node/apps/node-apps-list/log/log-filter/log-filter.component.scss */\nmat-form-field {\n  margin-bottom: -24px;\n}\n/*# sourceMappingURL=log-filter.component.css.map */\n";

// src/app/components/pages/node/apps/node-apps-list/log/log-filter/log-filter.component.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var LogFilterComponent_1;
var LogFilterComponent = class LogFilterComponent2 {
  static {
    LogFilterComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  static openDialog(dialog, currentFilter) {
    const config = new MatDialogConfig();
    config.data = currentFilter;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    return dialog.open(LogFilterComponent_1, config);
  }
  constructor(data, dialogRef, formBuilder, changeDetectorRef) {
    this.data = data;
    this.dialogRef = dialogRef;
    this.formBuilder = formBuilder;
    this.changeDetectorRef = changeDetectorRef;
  }
  ngOnInit() {
    this.filters = [
      {
        text: "apps.log.filter.7-days",
        days: 7
      },
      {
        text: "apps.log.filter.1-month",
        days: 30
      },
      {
        text: "apps.log.filter.3-months",
        days: 90
      },
      {
        text: "apps.log.filter.6-months",
        days: 180
      },
      {
        text: "apps.log.filter.1-year",
        days: 365
      },
      {
        text: "apps.log.filter.all",
        days: -1
      }
    ];
    this.form = this.formBuilder.group({
      filter: [this.data.days]
    });
    this.formSubscription = this.form.get("filter").valueChanges.subscribe((days) => {
      this.dialogRef.close(this.filters.find((filter) => filter.days === days));
      this.changeDetectorRef.markForCheck();
    });
  }
  ngOnDestroy() {
    this.formSubscription.unsubscribe();
  }
  static {
    this.ctorParameters = () => [
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: MatDialogRef },
      { type: UntypedFormBuilder },
      { type: ChangeDetectorRef }
    ];
  }
};
LogFilterComponent = LogFilterComponent_1 = __decorate([
  Component({
    selector: "app-log-filter",
    template: log_filter_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [log_filter_component_default2]
  })
], LogFilterComponent);

// src/app/components/pages/node/apps/node-apps-list/log/log.component.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var LogComponent_1;
var LogComponent = class LogComponent2 {
  static {
    LogComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  static openDialog(dialog, app) {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;
    return dialog.open(LogComponent_1, config);
  }
  constructor(data, dialogRef, appsService, dialog, snackbarService, apiService, changeDetectorRef) {
    this.data = data;
    this.dialogRef = dialogRef;
    this.appsService = appsService;
    this.dialog = dialog;
    this.snackbarService = snackbarService;
    this.apiService = apiService;
    this.changeDetectorRef = changeDetectorRef;
    this.logMessages = [];
    this.hasMoreLogMessages = false;
    this.totalLogs = 0;
    this.loading = false;
    this.currentFilter = {
      text: "apps.log.filter.7-days",
      days: 7
    };
    this.shouldShowError = true;
  }
  ngOnInit() {
    this.loadData(0);
  }
  ngOnDestroy() {
    this.removeSubscription();
  }
  filter() {
    LogFilterComponent.openDialog(this.dialog, this.currentFilter).afterClosed().subscribe((result) => {
      if (result) {
        this.currentFilter = result;
        this.logMessages = [];
        this.loadData(0);
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  // Returns the URL to get the full logs from the backend.
  getLogsUrl() {
    return "/" + this.apiService.apiPrefix + this.appsService.getLogMessagesUrl(NodeComponent.getCurrentNodeKey(), this.data.name);
  }
  loadData(delayMilliseconds) {
    this.removeSubscription();
    this.loading = true;
    this.subscription = of(1).pipe(
      // Wait the delay.
      delay(delayMilliseconds),
      // Load the data. The node pk is obtained from the currently openned node page.
      mergeMap(() => this.appsService.getLogMessages(NodeComponent.getCurrentNodeKey(), this.data.name, this.currentFilter.days))
    ).subscribe((log) => {
      this.onLogsReceived(log);
      this.changeDetectorRef.markForCheck();
    }, (err) => {
      this.onLogsError(err);
      this.changeDetectorRef.markForCheck();
    });
  }
  removeSubscription() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }
  }
  onLogsReceived(logs = []) {
    this.loading = false;
    this.shouldShowError = true;
    this.snackbarService.closeCurrentIfTemporaryError();
    let amount = 0;
    this.hasMoreLogMessages = false;
    this.totalLogs = logs.length;
    logs.forEach((log) => {
      if (amount < 5e3) {
        const dateStart = log.startsWith("[") ? 0 : -1;
        const dateEnd = dateStart !== -1 ? log.indexOf("]") : -1;
        if (dateStart !== -1 && dateEnd !== -1) {
          this.logMessages.push({
            time: log.substr(dateStart, dateEnd + 1),
            msg: log.substr(dateEnd + 1)
          });
        } else {
          this.logMessages.push({
            time: "",
            msg: log
          });
        }
      } else {
        this.hasMoreLogMessages = true;
      }
      amount += 1;
    });
    setTimeout(() => {
      this.content.nativeElement.scrollTop = this.content.nativeElement.scrollHeight;
    });
  }
  onLogsError(err) {
    err = processServiceError(err);
    if (this.shouldShowError) {
      this.snackbarService.showError("common.loading-error", null, true, err);
      this.shouldShowError = false;
    }
    this.loadData(AppConfig.connectionRetryDelay);
  }
  static {
    this.ctorParameters = () => [
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: MatDialogRef },
      { type: AppsService },
      { type: MatDialog },
      { type: SnackbarService },
      { type: ApiService },
      { type: ChangeDetectorRef }
    ];
  }
  static {
    this.propDecorators = {
      content: [{ type: ViewChild, args: ["content"] }]
    };
  }
};
LogComponent = LogComponent_1 = __decorate2([
  Component({
    selector: "app-log",
    template: log_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [log_component_default2]
  })
], LogComponent);

// src/app/components/pages/node/apps/app-catalog/app-catalog.component.ts
var __decorate3 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var catalogKinds = ["server", "client", "service", "external"];
function groupCatalog(entries) {
  const kindOf = (e) => catalogKinds.includes(e.kind) ? e.kind : "external";
  return catalogKinds.map((kind) => {
    return { kind, entries: entries.filter((e) => kindOf(e) === kind) };
  }).filter((g) => g.entries.length > 0);
}
function nextInstanceName(entry, taken = []) {
  const used = new Set(entry.instances.map((i) => i.name).concat(taken));
  let n = 2;
  while (used.has(`${entry.name}-${n}`)) {
    n++;
  }
  return `${entry.name}-${n}`;
}
function statusCode(status) {
  switch (status) {
    case "running":
      return 1;
    case "errored":
      return 2;
    case "starting":
      return 3;
    default:
      return 0;
  }
}
function detailAddsInfo(detail, ...shown) {
  const norm = (s) => (s || "").trim().toLowerCase();
  return norm(detail) !== "" && !shown.some((s) => norm(s) === norm(detail));
}
var AppCatalogComponent = class AppCatalogComponent2 {
  constructor() {
    this.cdr = inject(ChangeDetectorRef);
    this.api = inject(ApiService);
    this.appsService = inject(AppsService);
    this.live = inject(LiveService);
    this.snackbar = inject(SnackbarService);
    this.dialog = inject(MatDialog);
    this.translate = inject(TranslateService);
    this.groups = [];
    this.loading = true;
    this.error = "";
    this.nodePK = "";
    this.busy = /* @__PURE__ */ new Set();
    this.openSettings = /* @__PURE__ */ new Set();
    this.pickRequests = /* @__PURE__ */ new Map();
    this.apps = /* @__PURE__ */ new Map();
  }
  ngOnInit() {
    this.nodeSub = NodeComponent.currentNode.subscribe((node) => {
      const known = new Set(this.apps.keys());
      this.apps = new Map((node.apps || []).map((a) => [a.name, a]));
      const changed = known.size !== this.apps.size || [...this.apps.keys()].some((n) => !known.has(n));
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
  get url() {
    return `visors/${this.nodePK}/apps/catalog`;
  }
  follow() {
    this.liveSub?.unsubscribe();
    this.loading = true;
    this.liveSub = this.live.watch(this.url, 5e3, () => this.api.get(this.url)).pipe(switchMap((res) => this.load(res))).subscribe();
  }
  /** Fetches the catalog once, after an action or an apps change. */
  refresh() {
    if (!this.nodePK) {
      return;
    }
    this.refreshSub?.unsubscribe();
    this.refreshSub = this.load(this.api.get(this.url)).subscribe();
  }
  load(source) {
    return source.pipe(switchMap((entries) => {
      this.groups = groupCatalog(entries || []);
      this.error = "";
      this.loading = false;
      this.cdr.markForCheck();
      return of(null);
    }), catchError((err) => {
      this.error = processServiceError(err).translatableErrorMsg;
      this.loading = false;
      this.cdr.markForCheck();
      return of(null);
    }));
  }
  /** The node's app for an instance, which has the newest status. */
  appFor(inst) {
    return this.apps.get(inst.name);
  }
  status(inst) {
    const app = this.appFor(inst);
    return app ? app.status : statusCode(inst.status);
  }
  autostart(inst) {
    const app = this.appFor(inst);
    return app ? app.autostart : inst.auto_start;
  }
  detail(inst) {
    const app = this.appFor(inst);
    return (app ? app.detailedStatus : inst.detailed_status) || "";
  }
  /** The detailed status, or nothing when it only repeats the badge. */
  extraDetail(inst) {
    const d = this.detail(inst);
    return detailAddsInfo(d, this.translate.instant(this.statusKey(inst)), inst.status) ? d : "";
  }
  statusKey(inst) {
    return ["apps.status-stopped", "apps.status-running", "apps.catalog.failed", "apps.status-connecting"][this.status(inst)] || "apps.status-stopped";
  }
  statusClass(inst) {
    return "status-" + appStatusTone(this.status(inst));
  }
  isRunning(inst) {
    const s = this.status(inst);
    return s === 1 || s === 3;
  }
  /** Adds the entry's first instance, or another one under a proposed name. */
  add(entry, another = false) {
    let name = entry.name;
    if (another) {
      const proposed = nextInstanceName(entry, [...this.apps.keys()]);
      name = (window.prompt(this.translate.instant("apps.catalog.name-prompt"), proposed) || "").trim();
      if (!name) {
        return;
      }
    }
    const key = "add:" + entry.name;
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
      error: (err) => {
        this.busy.delete(key);
        this.snackbar.showError(processServiceError(err));
        this.cdr.markForCheck();
      }
    });
  }
  offerStart(name) {
    const text = this.translate.instant("apps.catalog.start-prompt", { app: name });
    const dialog = GeneralUtils.createConfirmationDialog(this.dialog, text);
    dialog.componentInstance.operationAccepted.subscribe(() => {
      dialog.close();
      this.run(name, this.appsService.changeAppState(this.nodePK, name, true));
      this.cdr.markForCheck();
    });
  }
  toggleState(inst) {
    this.run(inst.name, this.appsService.changeAppState(this.nodePK, inst.name, !this.isRunning(inst)));
  }
  toggleAutostart(inst) {
    this.run(inst.name, this.appsService.changeAppAutostart(this.nodePK, inst.name, !this.autostart(inst)));
  }
  run(name, op) {
    if (this.busy.has(name)) {
      return;
    }
    this.busy.add(name);
    this.cdr.markForCheck();
    op.subscribe({
      next: () => {
        this.busy.delete(name);
        this.snackbar.showDone("apps.operation-completed");
        this.afterAction();
      },
      error: (err) => {
        this.busy.delete(name);
        this.snackbar.showError(processServiceError(err));
        this.afterAction();
      }
    });
  }
  afterAction() {
    NodeComponent.refreshCurrentDisplayedData();
    this.refresh();
    this.cdr.markForCheck();
  }
  viewLogs(inst) {
    const app = this.appFor(inst);
    if (app && this.isRunning(inst)) {
      LogComponent.openDialog(this.dialog, app);
    } else {
      this.snackbar.showError("apps.apps-list.unavailable-logs-error");
    }
  }
  /** True for a client whose servers are listed in the service discovery. */
  canChooseServer(entry) {
    return entry.kind === "client" && !!serverTypeFor(entry.name);
  }
  /** Opens the instance's settings with the server picker on its server flag. */
  chooseServer(inst) {
    this.openSettings.add(inst.name);
    this.pickRequests.set(inst.name, (this.pickRequests.get(inst.name) || 0) + 1);
    this.cdr.markForCheck();
  }
  toggleSettings(name) {
    if (this.openSettings.has(name)) {
      this.openSettings.delete(name);
      this.pickRequests.delete(name);
    } else {
      this.openSettings.add(name);
    }
    this.cdr.markForCheck();
  }
};
AppCatalogComponent = __decorate3([
  Component({
    selector: "app-app-catalog",
    template: app_catalog_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [app_catalog_component_default2]
  })
], AppCatalogComponent);

// src/app/components/pages/node/apps/app-catalog/app-catalog.component.spec.ts
var PK = "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c";
var catalog = () => [
  {
    name: "custom-thing",
    kind: "external",
    description: "",
    multi_instance: false,
    instances: [{ name: "custom-thing", port: 0, auto_start: false, status: "stopped" }]
  },
  { name: "skychat", kind: "service", description: "Chat", multi_instance: false, instances: [] },
  {
    name: "skysocks",
    kind: "server",
    description: "SOCKS5 server",
    multi_instance: false,
    instances: [{ name: "skysocks", port: 3, auto_start: true, status: "running" }]
  },
  {
    name: "skysocks-client",
    kind: "client",
    description: "SOCKS5 client",
    multi_instance: true,
    instances: [{ name: "skysocks-client", port: 13, auto_start: false, status: "stopped" }]
  }
];
describe("app catalog grouping", () => {
  it("groups by kind in server, client, service, external order", () => {
    const groups = groupCatalog(catalog());
    expect(groups.map((g) => g.kind)).toEqual(["server", "client", "service", "external"]);
    expect(groups[0].entries.map((e) => e.name)).toEqual(["skysocks"]);
  });
  it("drops empty groups and files unknown kinds as external", () => {
    const groups = groupCatalog([{ name: "x", kind: "odd", description: "", multi_instance: false, instances: [] }]);
    expect(groups.map((g) => g.kind)).toEqual(["external"]);
  });
  it("proposes the first free instance name", () => {
    const e = catalog()[3];
    expect(nextInstanceName(e)).toBe("skysocks-client-2");
    expect(nextInstanceName(e, ["skysocks-client-2"])).toBe("skysocks-client-3");
  });
  it("shows a detailed status only when it differs from the badge", () => {
    expect(detailAddsInfo("Running", "Running", "running")).toBe(false);
    expect(detailAddsInfo(" running ", "L\xE4uft", "running")).toBe(false);
    expect(detailAddsInfo("", "Stopped")).toBe(false);
    expect(detailAddsInfo("permission denied", "Failed", "errored")).toBe(true);
  });
});
describe("AppCatalogComponent", () => {
  let fixture;
  let el;
  let apps;
  let api;
  let dialog;
  let node$;
  const card = (name) => el.querySelector(`[data-app="${name}"]`);
  const click = (root, sel) => {
    root.querySelector(sel).click();
    fixture.detectChanges();
  };
  beforeEach(() => {
    apps = jasmine.createSpyObj("AppsService", ["addApp", "changeAppState", "changeAppAutostart"]);
    apps.addApp.and.returnValue(of({}));
    apps.changeAppState.and.returnValue(of({}));
    apps.changeAppAutostart.and.returnValue(of({}));
    api = jasmine.createSpyObj("ApiService", ["get"]);
    api.get.and.callFake(() => of(catalog()));
    const live = jasmine.createSpyObj("LiveService", ["watch"]);
    live.watch.and.callFake((_u, _ms, req) => of(req()));
    dialog = jasmine.createSpyObj("MatDialog", ["open"]);
    node$ = new BehaviorSubject({ localPk: PK, apps: [
      { name: "skysocks", status: 1, autostart: true },
      { name: "skysocks-client", status: 0, autostart: false },
      { name: "custom-thing", status: 0, autostart: false }
    ] });
    spyOnProperty(NodeComponent, "currentNode", "get").and.returnValue(node$.asObservable());
    spyOn(NodeComponent, "refreshCurrentDisplayedData");
    TestBed.configureTestingModule({
      declarations: [AppCatalogComponent],
      imports: [TranslatePipe],
      providers: [
        provideTranslateService(),
        { provide: AppsService, useValue: apps },
        { provide: ApiService, useValue: api },
        { provide: LiveService, useValue: live },
        { provide: MatDialog, useValue: dialog },
        { provide: SnackbarService, useValue: jasmine.createSpyObj("SnackbarService", ["showDone", "showError"]) }
      ],
      schemas: [NO_ERRORS_SCHEMA]
    });
    fixture = TestBed.createComponent(AppCatalogComponent);
    el = fixture.nativeElement;
    fixture.detectChanges();
  });
  it("renders one card per entry under its kind heading", () => {
    expect(el.querySelectorAll(".cat-group").length).toBe(4);
    expect(el.querySelectorAll(".cat-card").length).toBe(4);
    expect(api.get).toHaveBeenCalledWith(`visors/${PK}/apps/catalog`);
  });
  it("shows not added with an Add button for an entry without instances", () => {
    const c = card("skychat");
    expect(c.querySelector(".cat-not-added")).toBeTruthy();
    expect(c.querySelector(".cat-instance")).toBeNull();
  });
  it("adds an app under its own name, refreshes and offers to start it", () => {
    const accepted = { subscribe: (fn) => fn() };
    dialog.open.and.returnValue({ componentInstance: { operationAccepted: accepted }, close: () => void 0 });
    api.get.calls.reset();
    click(card("skychat"), ".cat-add");
    expect(apps.addApp).toHaveBeenCalledWith(PK, "skychat", "skychat");
    expect(api.get).toHaveBeenCalled();
    expect(dialog.open).toHaveBeenCalled();
    expect(apps.changeAppState).toHaveBeenCalledWith(PK, "skychat", true);
  });
  it("adds another instance of a multi-instance app under the proposed name", () => {
    spyOn(window, "prompt").and.callFake((_m, d) => d || "");
    dialog.open.and.returnValue({ componentInstance: { operationAccepted: { subscribe: () => void 0 } } });
    expect(card("skysocks").querySelector(".cat-add-another")).toBeNull();
    click(card("skysocks-client"), ".cat-add-another");
    expect(apps.addApp).toHaveBeenCalledWith(PK, "skysocks-client-2", "skysocks-client");
  });
  it("stops a running instance and starts a stopped one", () => {
    click(card("skysocks"), ".cat-state");
    expect(apps.changeAppState).toHaveBeenCalledWith(PK, "skysocks", false);
    click(card("skysocks-client"), ".cat-state");
    expect(apps.changeAppState).toHaveBeenCalledWith(PK, "skysocks-client", true);
    expect(NodeComponent.refreshCurrentDisplayedData).toHaveBeenCalled();
  });
  it("offers the server picker on proxy and vpn client cards only", () => {
    expect(card("skysocks").querySelector(".cat-choose-server")).toBeNull();
    const comp = fixture.componentInstance;
    click(card("skysocks-client"), ".cat-choose-server");
    expect(comp.openSettings.has("skysocks-client")).toBeTrue();
    expect(comp.pickRequests.get("skysocks-client")).toBe(1);
    comp.chooseServer({ name: "skysocks-client", port: 13, auto_start: false, status: "stopped" });
    expect(comp.pickRequests.get("skysocks-client")).toBe(2);
    comp.toggleSettings("skysocks-client");
    expect(comp.pickRequests.has("skysocks-client")).toBeFalse();
  });
  it("flips autostart", () => {
    click(card("skysocks"), ".cat-autostart");
    expect(apps.changeAppAutostart).toHaveBeenCalledWith(PK, "skysocks", false);
  });
  it("does not repeat the badge as a detail line", () => {
    node$.next({ localPk: PK, apps: [
      { name: "skysocks", status: 1, autostart: true, detailedStatus: "Running" },
      { name: "skysocks-client", status: 2, autostart: false, detailedStatus: "dial failed" },
      { name: "custom-thing", status: 0, autostart: false }
    ] });
    fixture.detectChanges();
    expect(card("skysocks").querySelector(".cat-detail")).toBeNull();
    expect(card("skysocks-client").querySelector(".cat-detail")?.textContent).toContain("dial failed");
  });
  it("takes the status from the node apps and refetches when the app set changes", () => {
    const badge = () => card("skysocks-client").querySelector(".cat-status");
    expect(badge().classList).toContain("status-stopped");
    api.get.calls.reset();
    node$.next({ localPk: PK, apps: [
      { name: "skysocks", status: 1, autostart: true },
      { name: "skysocks-client", status: 1, autostart: false },
      { name: "custom-thing", status: 0, autostart: false }
    ] });
    fixture.detectChanges();
    expect(badge().classList).toContain("status-running");
    expect(api.get).not.toHaveBeenCalled();
    node$.next({ localPk: PK, apps: [{ name: "skychat", status: 0, autostart: false }] });
    expect(api.get).toHaveBeenCalled();
  });
});
//# debugId=b5d282c8-8f0e-5250-bfa3-b6e01afc183f
//# sourceMappingURL=spec-app-components-pages-node-apps-app-catalog-app-catalog.component.spec.js.map
