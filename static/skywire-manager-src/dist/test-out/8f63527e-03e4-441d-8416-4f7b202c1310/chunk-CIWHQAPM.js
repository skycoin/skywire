import {
  GeneralUtils,
  homeTabsData,
  ptyUrl
} from "./chunk-KWVFHEEW.js";
import {
  isRemoteVisor,
  nodeTabsData
} from "./chunk-HLYS7KBE.js";
import {
  UntypedFormControl,
  UntypedFormGroup
} from "./chunk-B4GMTCLR.js";
import {
  AppConfig,
  MAT_DIALOG_DATA,
  MatDialog,
  MatDialogConfig,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  TranslateService
} from "./chunk-6WNZTOXO.js";
import {
  LiveService
} from "./chunk-GPHL7TNH.js";
import {
  NodeService,
  bignumber_default
} from "./chunk-XBRWDJ2O.js";
import {
  ActivatedRoute,
  ApiService,
  ComponentPortal,
  DomPortalOutlet,
  DomSanitizer,
  HttpErrorResponse,
  NavigationEnd,
  Router,
  SnackbarService,
  environment,
  processServiceError
} from "./chunk-ZMZW6HXB.js";
import {
  StorageService
} from "./chunk-XOUXCPTJ.js";
import {
  ThemeService
} from "./chunk-VNMLTXOX.js";
import {
  ApplicationRef,
  BehaviorSubject,
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  EMPTY,
  HostListener,
  Inject,
  Injectable,
  Injector,
  Input,
  NgZone,
  ReplaySubject,
  ViewChild,
  catchError,
  concat,
  delay,
  firstValueFrom,
  mergeMap,
  of,
  switchMap,
  tap,
  timer
} from "./chunk-ZNLGPAUZ.js";
import {
  __async
} from "./chunk-ARXMZVRT.js";

// angular:jit:template:src/app/components/pages/node/node.component.html
var node_component_default = `<!-- Use nodeLoaded boolean flag with if/else to avoid Angular @if detection issues -->
@if (nodeLoaded) {
  <div class="row">
    <!-- embed=1 (chrome-less single-tab mode for WinBox/iframe hosts, e.g. the
    wasm desktop's \u2630 Chat window) hides the top bar + tab rows. -->
    @if (!embedMode) {
      <div class="col-12">
        <app-top-bar
          [titleParts]="titleParts"
          [pageHeaderLabel]="headerLabel"
          [pageHeaderIdentifier]="headerIdentifier"
          [tabsData]="tabsData"
          [homeTabsData]="homeNavTabs"
          [switcherTabsData]="switcherTabs"
          [selectedTabIndex]="selectedTabIndex"
          [secondsSinceLastUpdate]="secondsSinceLastUpdate"
          [showLoading]="updating"
          [showAlert]="errorsUpdating"
          [refeshRate]="storageService.getRefreshTime()"
          [optionsData]="nodeActionsHelper ? nodeActionsHelper.options : null"
          [returnText]="nodeActionsHelper ? nodeActionsHelper.returnButtonText : ''"
          (optionSelected)="performAction($event)"
          (refreshRequested)="forceDataRefresh(true)"
        ></app-top-bar>
      </div>
    }
    <div class="col-12">
      <!-- Right-bar / split-view is gone. The active tab content
      is full-width; the visor's identity (label + PK) lives
      in the top bar instead, persistent across tabs. -->
      <div class="full-size-main-area">
        <div class="d-flex flex-column h-100">
          <!-- Router outlet hosts every tab except Terminal. The
          terminal iframe is rendered as a sibling below so its
          DOM element stays mounted across tab switches; pulling
          it through router-outlet would let Angular detach the
          element on navigation, which forces a browser-level
          iframe reload and resets the dmsgpty session. -->
          <div [hidden]="isTerminalTab" class="d-flex flex-column h-100">
            <router-outlet></router-outlet>
          </div>
          @if (terminalIframeUrl) {
            <div [hidden]="!isTerminalTab" class="rounded-elevated-box mt-3 term-box">
              <div class="box-internal-container term-shell">
                <div class="term-controls">
                  <span class="dim small">{{ 'terminal.help' | translate }}</span>
                  <button mat-stroked-button class="term-fullscreen" (click)="openTerminalFullWindow()">
                    <mat-icon [inline]="true">open_in_new</mat-icon>
                    {{ 'terminal.open-fullscreen' | translate }}
                  </button>
                </div>
                <iframe class="term-frame" [src]="terminalIframeUrl" allowfullscreen></iframe>
              </div>
            </div>
          }
          @if (vpnIframeUrl) {
            <div [hidden]="!isVpnTab" class="rounded-elevated-box mt-3 term-box">
              <div class="box-internal-container term-shell">
                <div class="term-controls">
                  <span class="dim small">{{ 'node.tabs.vpn' | translate }}</span>
                  <button mat-stroked-button class="term-fullscreen" (click)="openVpnFullWindow()">
                    <mat-icon [inline]="true">open_in_full</mat-icon>
                    {{ 'common.open-full-page' | translate }}
                  </button>
                </div>
                <iframe class="term-frame" [src]="vpnIframeUrl" allowfullscreen></iframe>
              </div>
            </div>
          }
        </div>
      </div>
    </div>
  </div>
} @else {
  <div class="d-flex flex-column h-100 w-100">
    @if (!embedMode) {
      <div>
        <app-top-bar
          [titleParts]="titleParts"
          [pageHeaderLabel]="headerLabel"
          [pageHeaderIdentifier]="headerIdentifier"
          [tabsData]="tabsData"
          [selectedTabIndex]="selectedTabIndex"
          [showUpdateButton]="false"
          [optionsData]="nodeActionsHelper ? nodeActionsHelper.options : null"
          [returnText]="nodeActionsHelper ? nodeActionsHelper.returnButtonText : ''"
          (optionSelected)="performAction($event)"
        ></app-top-bar>
      </div>
    }
    @if (!notFound && !loadFailed) {
      <app-loading-indicator></app-loading-indicator>
    }
    <!-- Msg shown if the node is not found. -->
    @if (notFound) {
      <div class="w-100 h-100 d-flex not-found-label">
        <div>
          <mat-icon [inline]="true">error</mat-icon>
          {{ 'node.not-found' | translate }}
        </div>
      </div>
    }
    <!-- Msg shown if loading failed after multiple retries. -->
    @if (loadFailed) {
      <div class="w-100 h-100 d-flex not-found-label">
        <div>
          <mat-icon [inline]="true">error</mat-icon>
          {{ 'node.error-load' | translate }}
          <br>
            <button mat-raised-button color="primary" style="margin-top: 16px;" (click)="retryLoading()">
              {{ 'common.refresh' | translate }}
            </button>
          </div>
        </div>
      }
    </div>
  }

`;

// angular:jit:style:src/app/components/pages/node/node.component.scss
var node_component_default2 = "/* src/app/components/pages/node/node.component.scss */\n.not-found-label {\n  align-items: center;\n  justify-content: center;\n  font-size: 1rem;\n  position: relative;\n}\n.not-found-label mat-icon {\n  position: relative;\n  top: 5px;\n  font-size: 22px;\n  opacity: 0.5;\n  margin-right: 3px;\n}\n.full-size-main-area {\n  width: 100%;\n}\n.main-area {\n  width: 100%;\n}\n@media (min-width: 992px) {\n  .main-area {\n    width: 73%;\n    padding-right: 20px;\n    float: left;\n  }\n}\n.right-bar {\n  width: 27%;\n  float: right;\n  display: none;\n}\n@media (min-width: 992px) {\n  .right-bar {\n    display: block;\n    width: 27%;\n    float: right;\n  }\n}\n.term-box {\n  display: flex;\n  flex-direction: column;\n}\n.term-shell {\n  display: flex;\n  flex-direction: column;\n  gap: 8px;\n  padding-bottom: 4px;\n}\n.term-controls {\n  display: flex;\n  align-items: center;\n  gap: 12px;\n  padding: 4px 0;\n}\n.term-controls .dim {\n  color: rgba(var(--sw-fg-rgb), 0.85);\n}\n.term-controls .small {\n  font-size: 0.85em;\n}\n.term-controls .term-fullscreen {\n  margin-left: auto;\n  color: rgba(var(--sw-fg-rgb), 0.85) !important;\n}\n.term-controls .term-fullscreen .mat-icon {\n  color: inherit;\n}\n.term-frame {\n  width: 100%;\n  height: 70vh;\n  min-height: 420px;\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n  border-radius: 6px;\n  background: #000;\n}\n/*# sourceMappingURL=node.component.css.map */\n";

// src/app/utils/visor-switcher.ts
function abbreviatePk(pk) {
  if (!pk) {
    return "?";
  }
  return pk.length <= 12 ? pk : pk.substring(0, 5) + "..." + pk.substring(pk.length - 5);
}
function visorSwitcherChips(nodes) {
  return (Array.isArray(nodes) ? nodes : []).map((n) => ({
    icon: n.isHypervisor ? "router" : n.arch === "wasm" ? "public" : "devices",
    label: n.label || abbreviatePk(n.localPk),
    sublabel: n.localPk || "",
    linkParts: ["/nodes", n.localPk, "info"]
  }));
}

// angular:jit:template:src/app/components/pages/node/actions/proxy-settings/proxy-settings.component.html
var proxy_settings_component_default = `<div class="generic-dialog-container">
  <h2 mat-dialog-title>Skynet Settings</h2>

  <mat-dialog-content>
    @if (loading) {
      <div class="text-center py-3">
        <mat-spinner diameter="30" class="mx-auto"></mat-spinner>
      </div>
    }

    @if (!loading) {
      <div>
        <!-- Resolving Proxy Section -->
        <h3 class="section-title">Resolving Proxy</h3>
        <p class="help-text">
          Browse <strong>.skynet</strong> and <strong>.dmsg</strong> domains.
          Set an upstream SOCKS5 proxy to route all other traffic through skysocks.
        </p>
        @if (proxyStatus) {
          <div class="proxy-status">
            @if (proxyStatus.dmsg_web || proxyStatus.skynet_web) {
              <table class="status-table">
                <tr>
                  <th>Resolver</th>
                  <th>Running</th>
                  <th>SOCKS</th>
                  <th>Upstream</th>
                </tr>
                @if (proxyStatus.dmsg_web) {
                  <tr>
                    <td>.dmsg</td>
                    <td [class]="proxyStatus.dmsg_web.running ? 'status-ok' : 'status-off'">
                      {{ proxyStatus.dmsg_web.running ? 'Yes' : 'No' }}
                    </td>
                    <td>{{ proxyStatus.dmsg_web.socks_addr || '-' }}</td>
                    <td>{{ proxyStatus.dmsg_web.upstream_socks || 'direct' }}</td>
                  </tr>
                }
                @if (proxyStatus.skynet_web) {
                  <tr>
                    <td>.skynet</td>
                    <td [class]="proxyStatus.skynet_web.running ? 'status-ok' : 'status-off'">
                      {{ proxyStatus.skynet_web.running ? 'Yes' : 'No' }}
                    </td>
                    <td>{{ proxyStatus.skynet_web.socks_addr || '-' }}</td>
                    <td>{{ proxyStatus.skynet_web.upstream_socks || 'direct' }}</td>
                  </tr>
                }
              </table>
            }
          </div>
        }
        <div class="proxy-form">
          <div class="form-row toggle-row">
            <mat-checkbox [checked]="form.get('skynetEnabled')!.value"
              (change)="form.get('skynetEnabled')!.setValue($event.checked); toggleProxy()"
              [disabled]="loading">
              Enable resolving proxy (.skynet + .dmsg)
            </mat-checkbox>
          </div>
          <form [formGroup]="form">
            <div class="form-row upstream-row">
              <mat-form-field appearance="outline" class="upstream-field">
                <mat-label>Upstream SOCKS5 (e.g. 127.0.0.1:1080)</mat-label>
                <input matInput formControlName="upstream" placeholder="127.0.0.1:1080">
              </mat-form-field>
              <button mat-raised-button color="primary" (click)="setUpstream()" [disabled]="loading">
                Set
              </button>
            </div>
          </form>
        </div>
      </div>
    }
  </mat-dialog-content>

  <mat-dialog-actions align="end">
    <button mat-button (click)="close()">Close</button>
  </mat-dialog-actions>
</div>
`;

// angular:jit:style:src/app/components/pages/node/actions/proxy-settings/proxy-settings.component.scss
var proxy_settings_component_default2 = "/* src/app/components/pages/node/actions/proxy-settings/proxy-settings.component.scss */\n.section-title {\n  font-size: 15px;\n  font-weight: 500;\n  margin: 16px 0 4px 0;\n  color: rgba(var(--sw-shade-rgb), 0.87);\n}\n.section-title:first-of-type {\n  margin-top: 0;\n}\n.help-text {\n  color: rgba(var(--sw-shade-rgb), 0.6);\n  font-size: 13px;\n  margin-bottom: 12px;\n}\n.help-text code {\n  background: rgba(var(--sw-shade-rgb), 0.06);\n  padding: 1px 4px;\n  border-radius: 3px;\n  font-size: 12px;\n}\n.status-table {\n  width: 100%;\n  margin-bottom: 12px;\n  border-collapse: collapse;\n}\n.status-table th,\n.status-table td {\n  padding: 5px 8px;\n  text-align: left;\n  border-bottom: 1px solid rgba(var(--sw-shade-rgb), 0.08);\n  font-size: 13px;\n}\n.status-table th {\n  color: rgba(var(--sw-shade-rgb), 0.5);\n  font-weight: 500;\n}\n.status-table td {\n  color: rgba(var(--sw-shade-rgb), 0.87);\n}\n.status-table .status-ok {\n  color: rgb(var(--sw-green-rgb));\n  font-weight: 500;\n}\n.status-table .status-off {\n  color: rgba(var(--sw-shade-rgb), 0.38);\n}\n.proxy-form {\n  margin-top: 8px;\n  margin-bottom: 8px;\n}\n.form-row {\n  margin-bottom: 12px;\n}\n.upstream-row {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n}\n.upstream-row .upstream-field {\n  flex: 1;\n}\n.no-ports {\n  color: rgba(var(--sw-shade-rgb), 0.38);\n  font-style: italic;\n  font-size: 13px;\n}\n.port-list {\n  margin-bottom: 8px;\n}\n.port-item {\n  display: flex;\n  align-items: center;\n  justify-content: space-between;\n  padding: 4px 8px;\n  border-bottom: 1px solid rgba(var(--sw-shade-rgb), 0.06);\n}\n.port-item .port-number {\n  font-family: monospace;\n  font-size: 14px;\n  font-weight: 500;\n}\n.add-port-row {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n}\n.add-port-row .port-field {\n  width: 120px;\n}\n/*# sourceMappingURL=proxy-settings.component.css.map */\n";

// src/app/components/pages/node/actions/proxy-settings/proxy-settings.component.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var ProxySettingsComponent = class ProxySettingsComponent2 {
  constructor(dialogRef, data, nodeService, snackbarService, changeDetectorRef) {
    this.dialogRef = dialogRef;
    this.data = data;
    this.nodeService = nodeService;
    this.snackbarService = snackbarService;
    this.changeDetectorRef = changeDetectorRef;
    this.loading = false;
    this.proxyStatus = null;
    this.skynetPorts = [];
    this.newPort = "";
    this.form = new UntypedFormGroup({
      skynetEnabled: new UntypedFormControl(false),
      upstream: new UntypedFormControl("")
    });
    this.loadStatus();
    this.loadPorts();
  }
  loadStatus() {
    this.loading = true;
    this.nodeService.getProxies(this.data.nodeKey).subscribe((status) => {
      this.proxyStatus = status;
      this.loading = false;
      const skynetRunning = status?.skynet_web?.running || false;
      const upstream = status?.skynet_web?.upstream_socks || "";
      this.form.get("skynetEnabled").setValue(skynetRunning);
      this.form.get("upstream").setValue(upstream);
      this.changeDetectorRef.markForCheck();
    }, () => {
      this.loading = false;
      this.changeDetectorRef.markForCheck();
    });
  }
  loadPorts() {
    this.nodeService.getSkynetPorts(this.data.nodeKey).subscribe((ports) => {
      this.skynetPorts = (ports || []).sort((a, b) => a - b);
      this.changeDetectorRef.markForCheck();
    }, () => {
      this.changeDetectorRef.markForCheck();
    });
  }
  toggleProxy() {
    const enable = this.form.get("skynetEnabled").value;
    this.loading = true;
    this.nodeService.setProxyEnabled(this.data.nodeKey, "skynet", enable).subscribe(() => {
      this.nodeService.setProxyEnabled(this.data.nodeKey, "dmsg", enable).subscribe(() => {
        this.loading = false;
        this.snackbarService.showDone(enable ? "Resolving proxy enabled" : "Resolving proxy disabled");
        this.loadStatus();
        this.changeDetectorRef.markForCheck();
      }, () => {
        this.loading = false;
        this.changeDetectorRef.markForCheck();
      });
      this.changeDetectorRef.markForCheck();
    }, () => {
      this.loading = false;
      this.snackbarService.showError("Failed to toggle proxy");
      this.changeDetectorRef.markForCheck();
    });
  }
  setUpstream() {
    const addr = this.form.get("upstream").value.trim();
    this.loading = true;
    this.nodeService.setProxyUpstream(this.data.nodeKey, "skynet", addr).subscribe(() => {
      this.loading = false;
      this.snackbarService.showDone(addr ? `Upstream set to ${addr}` : "Upstream cleared");
      this.loadStatus();
      this.changeDetectorRef.markForCheck();
    }, () => {
      this.loading = false;
      this.snackbarService.showError("Failed to set upstream");
      this.changeDetectorRef.markForCheck();
    });
  }
  addPort() {
    const port = parseInt(this.newPort, 10);
    if (isNaN(port) || port < 1 || port > 65535) {
      this.snackbarService.showError("Invalid port number");
      return;
    }
    this.nodeService.registerSkynetPort(this.data.nodeKey, port).subscribe(() => {
      this.newPort = "";
      this.snackbarService.showDone(`Port ${port} forwarded`);
      this.loadPorts();
      this.changeDetectorRef.markForCheck();
    }, (err) => {
      this.snackbarService.showError(err?.error?.error || "Failed to register port");
      this.changeDetectorRef.markForCheck();
    });
  }
  removePort(port) {
    this.nodeService.deregisterSkynetPort(this.data.nodeKey, port).subscribe(() => {
      this.snackbarService.showDone(`Port ${port} removed`);
      this.loadPorts();
      this.changeDetectorRef.markForCheck();
    }, () => {
      this.snackbarService.showError("Failed to deregister port");
      this.changeDetectorRef.markForCheck();
    });
  }
  close() {
    this.dialogRef.close();
  }
  static {
    this.ctorParameters = () => [
      { type: MatDialogRef },
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: NodeService },
      { type: SnackbarService },
      { type: ChangeDetectorRef }
    ];
  }
};
ProxySettingsComponent = __decorate([
  Component({
    selector: "app-proxy-settings",
    template: proxy_settings_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [proxy_settings_component_default2]
  })
], ProxySettingsComponent);

// angular:jit:template:src/app/components/pages/node/actions/node-logs/node-logs.component.html
var node_logs_component_default = `<app-dialog [headline]="'node.logs.title' | translate" [includeVerticalMargins]="false" [includeScrollableArea]="false" [dialog]="dialogRef">
  <!-- Filter button. -->
  @if (!loading && logEntries && logEntries.length > 0) {
    <div class="top-dialog-button" (click)="showFilters()">
      <div class="top-dialog-button-content">
        <div>
          <span>{{ 'node.logs.selected-filter' | translate }} </span>
          <span class="actual-value">{{ ('node.logs.' + levelDetails.get(currentMinimumLevel)!.levelFilterName) | translate }}</span>
        </div>
        @if (logEntries.length > filteredLogEntries.length) {
          <div class="small">
            {{ 'node.logs.filter-ignored' | translate:{number: logEntries.length - filteredLogEntries.length} }}
          </div>
        }
      </div>
      <div class="top-dialog-button-margin"></div>
    </div>
  }

  @if (totalDropped > 0) {
    <div class="logs-dropped-banner">
      <mat-icon [inline]="true">history</mat-icon>
      {{ 'node.logs.dropped' | translate:{ count: totalDropped } }}
    </div>
  }
  <mat-dialog-content #content>
    <!-- Link for opening all the logs, if there are too much for the modal window. -->
    @if (!loading && hasMoreLogMessages) {
      <div class="log-entry">
        <a [href]="getFullLogsUrl()" target="_blank" class="view-raw-link">
          {{ 'node.logs.view-rest' | translate:{number: totalLogs} }}
        </a>
      </div>
    }

    <!-- All entries. -->
    @if (!loading) {
      @for (entry of filteredLogEntries; track entry) {
        <div class="log-entry">
          <!-- Basic fields. -->
          <span class="transparent">
            {{ entry.time }}
          </span>
          <span [class]="levelDetails.get(entry.level)!.colorClass">
            {{ levelDetails.get(entry.level)!.name }}
          </span>
          [
          <span class="transparent">
            {{ entry.func }}
          </span>
          <span class="module-color">
            {{ entry._module }}
          </span>
          ]@if (entry.msg) {
          :
        }
        @if (entry.msg) {
          <span>
            {{ entry.msg }}
          </span>
        }
        <!-- Extra fields. -->
        @for (extra of entry.extra; track extra) {
          <span class="extra-data-color">
            {{ extra.name }}
          </span>
          <span>
            ="{{ extra.value }}"
          </span>
        }
      </div>
    }
  }

  <!-- Link for opening all the logs. -->
  @if (!loading && logEntries && logEntries.length > 0) {
    <div class="log-entry">
      <a [href]="getFullLogsUrl()" target="_blank" class="view-raw-link">
        @if (!hasMoreLogMessages) {
          {{ 'node.logs.view-all' | translate }}
        }
        @if (hasMoreLogMessages) {
          {{ 'node.logs.view-rest' | translate:{number: totalLogs} }}
        }
      </a>
    </div>
  }

  <!-- Msg if there are no logs. -->
  @if (!loading && (!logEntries || logEntries.length === 0)) {
    <div class="log-empty-msg">
      {{ 'node.logs.no-logs' | translate }}
    </div>
  }
  <!-- Msg if there are no logs with the current filter. -->
  @if (!loading && logEntries && logEntries.length > 0 && filteredLogEntries.length < 1) {
    <div class="log-empty-msg">
      {{ 'node.logs.no-logs-for-filter' | translate }}
    </div>
  }
  <!-- Loading animation. -->
  @if (loading) {
    <app-loading-indicator [showWhite]="false"></app-loading-indicator>
  }

  <!-- Footer: live-tail toggle on the left, manual refresh on
       the right. Live tail polls the visor every livePollMs and
       only auto-scrolls when the viewport is already pinned. -->
  <div class="logs-footer">
    <div class="live-tail-toggle subtle-transparent-button" (click)="toggleLiveTail()">
      <mat-icon class="icon" [inline]="true">{{ liveTail ? 'pause' : 'play_arrow' }}</mat-icon>
      <span>{{ (liveTail ? 'node.logs.live-on' : 'node.logs.live-off') | translate }}</span>
    </div>
    <div class="update-button subtle-transparent-button" (click)="loadData(0)">
      @if (!loading) {
        <div class="update-time">
          <mat-icon class="icon" [inline]="true">refresh</mat-icon>
          <span>{{ ('refresh-button.' + elapsedTime.translationVarName) | translate:{time: elapsedTime.elapsedTime} }}</span>
        </div>
      }
    </div>
  </div>
</mat-dialog-content>
</app-dialog>

`;

// angular:jit:style:src/app/components/pages/node/actions/node-logs/node-logs.component.scss
var node_logs_component_default2 = "/* src/app/components/pages/node/actions/node-logs/node-logs.component.scss */\n.top-dialog-button {\n  color: #777 !important;\n}\n.top-dialog-button .top-dialog-button-content {\n  text-align: center;\n}\n.top-dialog-button .top-dialog-button-content .actual-value {\n  color: #202226;\n}\n.top-dialog-button .top-dialog-button-content .small {\n  font-size: 0.6rem;\n}\n.log-entry {\n  margin-top: 15px;\n  word-break: break-word;\n}\n.log-entry .transparent {\n  color: #999;\n}\n.log-entry:first-of-type {\n  margin-top: 0px;\n}\n.log-entry:last-of-type {\n  margin-bottom: 24px;\n}\n.log-empty-msg {\n  text-align: center;\n}\n.view-raw-link {\n  color: #215f9e;\n}\n.update-button {\n  display: flex;\n  justify-content: center;\n  cursor: pointer;\n}\n.update-button mat-icon {\n  position: relative;\n  top: 5px;\n  margin-right: 5px;\n}\n.update-button .update-time {\n  text-align: center;\n  font-size: 0.7rem;\n  color: #202226;\n  margin: 0 5px;\n}\n.panic-level-color {\n  color: #ff0000;\n}\n.fatal-level-color {\n  color: #ff0000;\n}\n.error-level-color {\n  color: #d10000;\n}\n.warning-level-color {\n  color: #e27505;\n}\n.info-level-color {\n  color: #0d9519;\n}\n.debug-level-color {\n  color: #0065ff;\n}\n.trace-level-color {\n  color: #468cf5;\n}\n.unknown-level-color {\n  color: #e27505;\n}\n.module-color {\n  color: #a119fc;\n}\n.extra-data-color {\n  color: #ad8021;\n}\n.logs-footer {\n  display: flex;\n  justify-content: space-between;\n  align-items: center;\n  padding: 6px 10px;\n  border-top: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n}\n.live-tail-toggle {\n  display: flex;\n  align-items: center;\n  gap: 6px;\n  padding: 4px 10px;\n  border-radius: 4px;\n  cursor: pointer;\n  -webkit-user-select: none;\n  user-select: none;\n  font-size: 13px;\n}\n.live-tail-toggle .icon {\n  font-size: 18px;\n}\n.logs-dropped-banner {\n  display: flex;\n  align-items: center;\n  gap: 6px;\n  padding: 6px 10px;\n  background: rgba(255, 200, 0, 0.08);\n  border-bottom: 1px solid rgba(255, 200, 0, 0.2);\n  font-size: 12px;\n  opacity: 0.9;\n}\n.logs-dropped-banner mat-icon {\n  font-size: 16px;\n}\n/*# sourceMappingURL=node-logs.component.css.map */\n";

// src/app/utils/timeUtils.ts
var TimeRepresentations;
(function(TimeRepresentations2) {
  TimeRepresentations2[TimeRepresentations2["Seconds"] = 0] = "Seconds";
  TimeRepresentations2[TimeRepresentations2["Minutes"] = 1] = "Minutes";
  TimeRepresentations2[TimeRepresentations2["Hours"] = 2] = "Hours";
  TimeRepresentations2[TimeRepresentations2["Days"] = 3] = "Days";
  TimeRepresentations2[TimeRepresentations2["Weeks"] = 4] = "Weeks";
})(TimeRepresentations || (TimeRepresentations = {}));
var ElapsedTime = class {
};
var TimeUtils = class {
  /**
   * Calculates the best way to display in the UI an amount of elapsed time.
   */
  static getElapsedTime(elapsedSeconds) {
    const response = new ElapsedTime();
    response.timeRepresentation = TimeRepresentations.Seconds;
    response.totalMinutes = Math.floor(elapsedSeconds / 60).toString();
    response.translationVarName = "second";
    let divider = 1;
    if (elapsedSeconds >= 60 && elapsedSeconds < 3600) {
      response.timeRepresentation = TimeRepresentations.Minutes;
      divider = 60;
      response.translationVarName = "minute";
    } else if (elapsedSeconds >= 3600 && elapsedSeconds < 86400) {
      response.timeRepresentation = TimeRepresentations.Hours;
      divider = 3600;
      response.translationVarName = "hour";
    } else if (elapsedSeconds >= 86400 && elapsedSeconds < 604800) {
      response.timeRepresentation = TimeRepresentations.Days;
      divider = 86400;
      response.translationVarName = "day";
    } else if (elapsedSeconds >= 604800) {
      response.timeRepresentation = TimeRepresentations.Weeks;
      divider = 604800;
      response.translationVarName = "week";
    }
    const elapsedTime = Math.floor(elapsedSeconds / divider);
    response.elapsedTime = elapsedTime.toString();
    if (response.timeRepresentation === TimeRepresentations.Seconds || elapsedTime > 1) {
      response.translationVarName = response.translationVarName + "s";
    }
    return response;
  }
};

// angular:jit:template:src/app/components/layout/select-option/select-option.component.html
var select_option_component_default = '<app-dialog [headline]="data.title | translate" [dialog]="dialogRef" [includeVerticalMargins]="false">\n  @for (option of data.options; track option; let i = $index) {\n    <div class="options-list-button-container">\n      <button\n        mat-button\n        class="grey-button-background"\n        (click)="closePopup(i + 1)"\n        >\n        <div class="internal-container">\n          @if (option.icon) {\n            <mat-icon class="icon" [inline]="true">{{ option.icon }}</mat-icon>\n          }\n          <span>{{ option.label | translate }}</span>\n        </div>\n      </button>\n    </div>\n  }\n</app-dialog>\n';

// angular:jit:style:src/app/components/layout/select-option/select-option.component.scss
var select_option_component_default2 = "/* src/app/components/layout/select-option/select-option.component.scss */\n.icon {\n  font-size: 14px;\n  width: 14px;\n  line-height: 1;\n}\n.grey-button-background {\n  justify-content: left !important;\n  min-height: 45px;\n}\n/*# sourceMappingURL=select-option.component.css.map */\n";

// src/app/components/layout/select-option/select-option.component.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var SelectOptionComponent_1;
var SelectOptionComponent = class SelectOptionComponent2 {
  static {
    SelectOptionComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  static openDialog(dialog, optionsToShow, title) {
    const config = new MatDialogConfig();
    config.data = {
      options: optionsToShow,
      title
    };
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    return dialog.open(SelectOptionComponent_1, config);
  }
  constructor(data, dialogRef) {
    this.data = data;
    this.dialogRef = dialogRef;
  }
  closePopup(selectedOption) {
    this.dialogRef.close(selectedOption);
  }
  static {
    this.ctorParameters = () => [
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: MatDialogRef }
    ];
  }
};
SelectOptionComponent = SelectOptionComponent_1 = __decorate2([
  Component({
    selector: "app-select-option",
    template: select_option_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [select_option_component_default2]
  })
], SelectOptionComponent);

// src/app/components/pages/node/actions/node-logs/node-logs.component.ts
var __decorate3 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var NodeLogsComponent_1;
var Level;
(function(Level3) {
  Level3[Level3["PanicLevel"] = 0] = "PanicLevel";
  Level3[Level3["FatalLevel"] = 1] = "FatalLevel";
  Level3[Level3["ErrorLevel"] = 2] = "ErrorLevel";
  Level3[Level3["WarnLevel"] = 3] = "WarnLevel";
  Level3[Level3["InfoLevel"] = 4] = "InfoLevel";
  Level3[Level3["DebugLevel"] = 5] = "DebugLevel";
  Level3[Level3["TraceLevel"] = 6] = "TraceLevel";
  Level3[Level3["Unknown"] = 7] = "Unknown";
})(Level || (Level = {}));
var LogEntry = class {
  constructor() {
    this.extra = [];
  }
};
var NodeLogsComponent = class NodeLogsComponent2 {
  static {
    NodeLogsComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  static openDialog(dialog) {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;
    return dialog.open(NodeLogsComponent_1, config);
  }
  constructor(dialogRef, nodeService, snackbarService, ngZone, dialog, changeDetectorRef) {
    this.dialogRef = dialogRef;
    this.nodeService = nodeService;
    this.snackbarService = snackbarService;
    this.ngZone = ngZone;
    this.dialog = dialog;
    this.changeDetectorRef = changeDetectorRef;
    this.levelDetails = /* @__PURE__ */ new Map([
      [
        Level.PanicLevel,
        { name: "PANIC", colorClass: "panic-level-color", levelFilterName: "filter-panic", importance: 8 }
      ],
      [
        Level.FatalLevel,
        { name: "FATAL", colorClass: "fatal-level-color", levelFilterName: "filter-faltal", importance: 7 }
      ],
      [
        Level.ErrorLevel,
        { name: "ERROR", colorClass: "error-level-color", levelFilterName: "filter-error", importance: 6 }
      ],
      [
        Level.WarnLevel,
        { name: "WARNING", colorClass: "warning-level-color", levelFilterName: "filter-warning", importance: 5 }
      ],
      [
        Level.InfoLevel,
        { name: "INFO", colorClass: "info-level-color", levelFilterName: "filter-info", importance: 4 }
      ],
      [
        Level.DebugLevel,
        { name: "DEBUG", colorClass: "debug-level-color", levelFilterName: "filter-debug", importance: 3 }
      ],
      [
        Level.TraceLevel,
        { name: "TRACE", colorClass: "trace-level-color", levelFilterName: "filter-all", importance: 2 }
      ],
      [
        Level.Unknown,
        { name: "UNKNOWN LOG", colorClass: "unknown-level-color", levelFilterName: "filter-all", importance: 1 }
      ]
    ]);
    this.currentMinimumLevel = Level.Unknown;
    this.loading = true;
    this.LoadingMoment = 0;
    this.liveTail = true;
    this.livePollMs = 2e3;
    this.logCursor = 0;
    this.totalDropped = 0;
    this.wasAtBottom = true;
    this.maxElementsPerPage = 1e3;
    this.logEntries = [];
    this.filteredLogEntries = [];
    this.hasMoreLogMessages = false;
    this.totalLogs = 0;
    this.shouldShowError = true;
  }
  ngOnInit() {
    this.loadData(0);
  }
  ngOnDestroy() {
    this.removeSubscription();
    this.removeTimeSubscription();
  }
  // Shows the modal window for selecting the minimum importance level to use as filter.
  showFilters() {
    const options = [
      { icon: "", label: "node.logs.filter-all" },
      { icon: "", label: "node.logs.filter-debug" },
      { icon: "", label: "node.logs.filter-info" },
      { icon: "", label: "node.logs.filter-warning" },
      { icon: "", label: "node.logs.filter-error" },
      { icon: "", label: "node.logs.filter-faltal" },
      { icon: "", label: "node.logs.filter-panic" }
    ];
    const optionTypes = [
      Level.Unknown,
      Level.DebugLevel,
      Level.InfoLevel,
      Level.WarnLevel,
      Level.ErrorLevel,
      Level.FatalLevel,
      Level.PanicLevel
    ];
    for (let i = 0; i <= optionTypes.length; i++) {
      if (this.currentMinimumLevel === optionTypes[i]) {
        options[i].icon = "check";
      }
    }
    SelectOptionComponent.openDialog(this.dialog, options, "node.logs.filter-title").afterClosed().subscribe((selectedOption) => {
      this.currentMinimumLevel = optionTypes[selectedOption - 1];
      this.filter();
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Gets the logs from the back-end. First call (cursor=0) fetches
   * the full buffer via the diff endpoint; subsequent calls receive
   * only the entries that arrived since the previous cursor.
   * @param delayMilliseconds Delay before getting the data; used both
   *   for the initial load and for the live-tail polling cadence.
   */
  loadData(delayMilliseconds) {
    this.removeSubscription();
    this.captureScrollTailState();
    this.loading = this.logEntries.length === 0;
    const cursor = this.logCursor;
    this.subscription = of(1).pipe(delay(delayMilliseconds), mergeMap(() => this.nodeService.getRuntimeLogsSince(NodeComponent.getCurrentNodeKey(), cursor))).subscribe((delta) => {
      this.onLogsDeltaReceived(delta);
      this.changeDetectorRef.markForCheck();
    }, (err) => {
      this.onLogsError(err);
      this.changeDetectorRef.markForCheck();
    });
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
  captureScrollTailState() {
    if (!this.content) {
      this.wasAtBottom = true;
      return;
    }
    const el = this.content.nativeElement;
    this.wasAtBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }
  removeSubscription() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }
  }
  removeTimeSubscription() {
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
  onLogsDeltaReceived(delta) {
    if (!delta) {
      this.loading = false;
      this.scheduleNextPoll();
      return;
    }
    const isInitial = this.logCursor === 0;
    this.logCursor = typeof delta.latest === "number" ? delta.latest : this.logCursor;
    if (typeof delta.dropped === "number" && delta.dropped > 0) {
      this.totalDropped += delta.dropped;
    }
    const entriesRaw = Array.isArray(delta.entries) ? delta.entries : [];
    if (entriesRaw.length === 0) {
      this.loading = false;
      this.LoadingMoment = Date.now();
      this.shouldShowError = true;
      this.startUpdatingTime();
      this.scheduleNextPoll();
      return;
    }
    const parsed = [];
    for (const raw of entriesRaw) {
      try {
        parsed.push(JSON.parse(raw));
      } catch (e) {
      }
    }
    if (isInitial) {
      this.logEntries = [];
    }
    this.appendParsedEntries(parsed);
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
  scheduleNextPoll() {
    if (this.liveTail) {
      this.loadData(this.livePollMs);
    }
  }
  appendParsedEntries(logs) {
    logs.forEach((e) => {
      const entry = new LogEntry();
      entry.time = e.time;
      entry._module = e._module;
      entry.msg = e.msg;
      entry.func = e.func;
      const receivewdLevel = e.level ? e.level.toLowerCase() : "";
      if (receivewdLevel.includes("panic")) {
        entry.level = Level.PanicLevel;
      } else if (receivewdLevel.includes("fatal")) {
        entry.level = Level.FatalLevel;
      } else if (receivewdLevel.includes("error")) {
        entry.level = Level.ErrorLevel;
      } else if (receivewdLevel.includes("warn")) {
        entry.level = Level.WarnLevel;
      } else if (receivewdLevel.includes("info")) {
        entry.level = Level.InfoLevel;
      } else if (receivewdLevel.includes("debug")) {
        entry.level = Level.DebugLevel;
      } else if (receivewdLevel.includes("trace")) {
        entry.level = Level.TraceLevel;
      } else {
        entry.level = Level.Unknown;
      }
      if (e.current_backoff) {
        const seg = Math.floor(e.current_backoff / 1e9);
        const min = Math.floor(seg / 60);
        const segs = Math.floor(seg % 60);
        if (min) {
          entry.extra.push({ name: "current_backoff", value: min + "m" + segs + "s" });
        } else {
          entry.extra.push({ name: "current_backoff", value: segs + "s" });
        }
      }
      if (e.error) {
        entry.extra.push({ name: "error", value: e.error });
      }
      const knownProperties = /* @__PURE__ */ new Set();
      knownProperties.add("time");
      knownProperties.add("_module");
      knownProperties.add("msg");
      knownProperties.add("func");
      knownProperties.add("level");
      knownProperties.add("current_backoff");
      knownProperties.add("error");
      knownProperties.add("log_line");
      for (const key in e) {
        if (!knownProperties.has(key)) {
          entry.extra.push({ name: key, value: e[key] });
        }
      }
      this.logEntries.push(entry);
    });
  }
  // Removes all the entries that do not meet the filter criteria.
  filter() {
    this.filteredLogEntries = [];
    const minimumimportance = this.levelDetails.get(this.currentMinimumLevel).importance;
    this.logEntries.forEach((e) => {
      const importance = this.levelDetails.get(e.level).importance;
      if (minimumimportance <= importance) {
        this.filteredLogEntries.push(e);
      }
    });
    if (this.wasAtBottom) {
      setTimeout(() => {
        if (this.content) {
          this.content.nativeElement.scrollTop = this.content.nativeElement.scrollHeight;
        }
      });
    }
  }
  // Updates the text which says how much time has passed since the data was loaded. It does it
  // periodically.
  startUpdatingTime() {
    this.elapsedTime = TimeUtils.getElapsedTime(Math.floor((Date.now() - this.LoadingMoment) / 1e3));
    this.removeTimeSubscription();
    this.timeUpdateSubscription = timer(5e3, 5e3).subscribe(() => {
      this.ngZone.run(() => {
        this.elapsedTime = TimeUtils.getElapsedTime(Math.floor((Date.now() - this.LoadingMoment) / 1e3));
      });
      this.changeDetectorRef.markForCheck();
    });
  }
  // Returns the URL with the raw log data.
  getFullLogsUrl() {
    const apiPrefix = !environment.production && location.protocol.indexOf("http:") !== -1 ? "http-api" : "api";
    return window.location.origin + "/" + apiPrefix + "/visors/" + NodeComponent.getCurrentNodeKey() + "/runtime-logs";
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
      { type: MatDialogRef },
      { type: NodeService },
      { type: SnackbarService },
      { type: NgZone },
      { type: MatDialog },
      { type: ChangeDetectorRef }
    ];
  }
  static {
    this.propDecorators = {
      content: [{ type: ViewChild, args: ["content"] }]
    };
  }
};
NodeLogsComponent = NodeLogsComponent_1 = __decorate3([
  Component({
    selector: "app-node-logs",
    template: node_logs_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [node_logs_component_default2]
  })
], NodeLogsComponent);

// src/app/components/pages/node/actions/node-actions-helper.ts
var NodeActionsHelper = class {
  constructor(injector, showingFullList) {
    this.canBeUpdated = false;
    this.canOpenTerminal = false;
    this.options = [];
    this.dialog = injector.get(MatDialog);
    this.router = injector.get(Router);
    this.snackbarService = injector.get(SnackbarService);
    this.nodeService = injector.get(NodeService);
    this.storageService = injector.get(StorageService);
    this.showingFullList = showingFullList;
    this.returnButtonText = !showingFullList ? "nodes.title" : "node.title";
    this.updateOptions();
  }
  /**
   * Options for the menu shown in the top bar.
   */
  updateOptions() {
    this.options = [];
    this.options.push({
      name: "actions.menu.turn-off",
      actionName: "shutdown",
      icon: "power_settings_new"
    });
  }
  /**
   * Allows to set the data of the current node.
   */
  setCurrentNode(currentNode) {
    this.currentNode = currentNode;
    if (GeneralUtils.checkIfTagIsUpdatable(currentNode.buildTag)) {
      this.canBeUpdated = true;
    } else {
      this.canBeUpdated = false;
    }
    this.canOpenTerminal = GeneralUtils.checkIfTagCanOpenterminal(currentNode.buildTag);
    this.updateOptions();
  }
  /**
   * Allows to set the key of the current node.
   */
  setCurrentNodeKey(nodeKey) {
    this.currentNodeKey = nodeKey;
  }
  /**
   * Must be called when an option form the top bar is selected.
   * @param actionName Name of the selected option, as defined in the options array.
   */
  performAction(actionName, nodePk) {
    if (actionName === "terminal") {
      this.terminal();
    } else if (actionName === "update") {
      this.update();
    } else if (actionName === "logs") {
      this.runtimeLogs();
    } else if (actionName === "proxy") {
      this.openProxySettings();
    } else if (actionName === "shutdown") {
      this.shutdown();
    } else if (actionName === null) {
      this.back();
    }
  }
  /**
   * Cleans the object. Must be called when the object is no longer needed.
   */
  dispose() {
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }
    if (this.shutdownSubscription) {
      this.shutdownSubscription.unsubscribe();
    }
  }
  shutdown() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, "actions.turn-off.confirmation");
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();
      this.shutdownSubscription = this.nodeService.shutdown(this.currentNodeKey).subscribe(() => {
        this.snackbarService.showDone("actions.turn-off.done");
        confirmationDialog.close();
        this.router.navigate(["nodes"]);
      }, (err) => {
        err = processServiceError(err);
        confirmationDialog.componentInstance.showDone("confirmation.error-header-text", err.translatableErrorMsg);
      });
    });
  }
  update() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, "actions.update.confirmation");
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      window.open(ptyUrl(this.currentNodeKey, "?commands=update"), "_blank", "noopener noreferrer");
      confirmationDialog.close();
    });
  }
  terminal() {
    window.open(ptyUrl(this.currentNodeKey), "_blank", "noopener noreferrer");
  }
  /**
   * Opens the modal window for checking the runtime logs of a node.
   */
  runtimeLogs() {
    NodeLogsComponent.openDialog(this.dialog);
  }
  /**
   * Opens the proxy settings dialog.
   */
  openProxySettings() {
    this.dialog.open(ProxySettingsComponent, {
      width: "550px",
      data: { nodeKey: this.currentNodeKey }
    });
  }
  back() {
    if (!this.showingFullList) {
      this.router.navigate(["nodes"]);
    } else {
      this.router.navigate(["nodes", this.currentNodeKey]);
    }
  }
};

// src/app/services/single-node-data.service.ts
var __decorate4 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var SingleNodeBackendData = class {
  constructor() {
    this.trafficData = new TrafficData();
  }
};
var TrafficData = class {
  constructor() {
    this.totalSent = 0;
    this.totalReceived = 0;
    this.sentHistory = [];
    this.receivedHistory = [];
  }
};
var NodeData = class {
  constructor() {
    this.lastEmitedData = new SingleNodeBackendData();
    this.dataSubject = new BehaviorSubject(null);
    this.whenUpdateWasScheduled = 0;
    this.stopRequestedDate = -1;
    this.consecutiveErrors = 0;
  }
};
var SingleNodeDataService = class SingleNodeDataService2 {
  constructor(storageService, nodeService, live) {
    this.storageService = storageService;
    this.nodeService = nodeService;
    this.live = live;
    this.maxTrafficHistorySlots = 10;
    this.expirationTime = 6e4;
    this.nodesMap = /* @__PURE__ */ new Map();
    this.storageService.getRefreshTimeObservable().subscribe((val) => {
      this.dataRefreshDelay = val * 1e3;
      this.nodesMap.forEach((n) => {
        this.forceSpecificNodeRefresh(n.pk);
      });
    });
    this.checkForExpired();
  }
  /**
   * Periodically checks all entries in nodesMap and removes all expired ones.
   * Uses setInterval instead of recursive subscription to avoid subscription leaks.
   */
  checkForExpired() {
    setInterval(() => {
      try {
        this.nodesMap.forEach((n) => {
          this.finishIfExpired(n);
        });
      } catch (e) {
      }
    }, 5e3);
  }
  /**
   * Makes the service start returning data for a specific node. Returns an observable for
   * getting the data.
   * @param publicKey Public key of the specific node to check.
   */
  startRequestingData(publicKey) {
    let nodeData = this.nodesMap.get(publicKey);
    if (!nodeData) {
      nodeData = new NodeData();
      nodeData.pk = publicKey;
      this.nodesMap.set(publicKey, nodeData);
      this.startUpdates(nodeData);
    } else {
      nodeData.stopRequestedDate = -1;
    }
    return nodeData.dataSubject.asObservable();
  }
  /**
   * Makes the service stop updating the data of a specific node. It is just a request, the
   * data will still be refreshed and maintained in cache for some time, to be able to access
   * it quickly if needed shortly after.
   */
  stopRequestingSpecificNode(publicKey) {
    const nodeData = this.nodesMap.get(publicKey);
    if (nodeData) {
      nodeData.stopRequestedDate = Date.now();
    }
  }
  /**
   * Starts periodically getting the data of a specific node.
   * @param delayMs Delay before loading the data.
   * @param nodeData Data about the node.
   */
  startDataSubscription(delayMs, nodeData) {
    if (nodeData.updateSubscription) {
      nodeData.updateSubscription.unsubscribe();
    }
    nodeData.updateSubscription = of(1).pipe(
      // Wait the requested delay.
      delay(delayMs),
      // Additional steps for making sure the UI shows the animation (important in case of quick errors).
      tap(() => {
        nodeData.lastEmitedData.updating = true;
        nodeData.dataSubject.next(nodeData.lastEmitedData);
      }),
      delay(120),
      // Load the data.
      mergeMap(() => this.nodeService.getNode(nodeData.pk))
    ).subscribe((result) => {
      nodeData.consecutiveErrors = 0;
      this.updateTrafficData(result.transports, nodeData.lastEmitedData.trafficData, nodeData.whenUpdateWasScheduled);
      let refreshTime = this.calculateRemainingTime(nodeData.whenUpdateWasScheduled);
      if (refreshTime < 1e3) {
        nodeData.whenUpdateWasScheduled = Date.now();
        refreshTime = this.dataRefreshDelay;
      }
      nodeData.lastEmitedData = {
        data: result,
        error: null,
        momentOfLastCorrectUpdate: Date.now(),
        updating: false,
        trafficData: nodeData.lastEmitedData.trafficData
      };
      nodeData.dataSubject.next(nodeData.lastEmitedData);
      this.startDataSubscription(refreshTime, nodeData);
    }, (err) => {
      err = processServiceError(err);
      nodeData.consecutiveErrors = (nodeData.consecutiveErrors || 0) + 1;
      nodeData.lastEmitedData = {
        data: nodeData.lastEmitedData.data,
        error: err,
        momentOfLastCorrectUpdate: nodeData.lastEmitedData.momentOfLastCorrectUpdate,
        updating: false,
        trafficData: nodeData.lastEmitedData.trafficData
      };
      nodeData.dataSubject.next(nodeData.lastEmitedData);
      const stopUpdating = err.originalError && err.originalError.status === 400;
      if (!stopUpdating) {
        const backoff = Math.min(AppConfig.connectionRetryDelay * Math.pow(2, nodeData.consecutiveErrors - 1), 3e4);
        this.startDataSubscription(backoff, nodeData);
      } else {
        nodeData.dataSubject.complete();
        nodeData.updateSubscription.unsubscribe();
        this.nodesMap.delete(nodeData.pk);
      }
    });
  }
  /**
   * Calculates for how many ms a saved data is still valid before an update should be made.
   * @param momentOfLastCorrectUpdate Moment in which the data was saved.
   */
  calculateRemainingTime(momentOfLastCorrectUpdate) {
    if (momentOfLastCorrectUpdate < 1) {
      return 0;
    }
    let refreshDelay = this.dataRefreshDelay - (Date.now() - momentOfLastCorrectUpdate);
    if (refreshDelay < 0) {
      refreshDelay = 0;
    }
    return refreshDelay;
  }
  /**
   * Updates the traffic data.
   * @param transports Transports of the specific node.
   */
  updateTrafficData(transports, currentData, lastScheduledHistoryUpdateTime) {
    currentData.totalSent = 0;
    currentData.totalReceived = 0;
    if (transports && transports.length > 0) {
      currentData.totalSent = transports.reduce((total, transport) => total + (transport.sent || 0), 0);
      currentData.totalReceived = transports.reduce((total, transport) => total + (transport.recv || 0), 0);
    }
    if (currentData.sentHistory.length === 0) {
      for (let i = 0; i < this.maxTrafficHistorySlots; i++) {
        currentData.sentHistory[i] = currentData.totalSent;
        currentData.receivedHistory[i] = currentData.totalReceived;
      }
    } else {
      const TimeSinceLastHistoryUpdate = Date.now() - lastScheduledHistoryUpdateTime;
      let newSlotsNeeded = new bignumber_default(TimeSinceLastHistoryUpdate).dividedBy(this.dataRefreshDelay).decimalPlaces(0, bignumber_default.ROUND_FLOOR).toNumber();
      if (newSlotsNeeded > this.maxTrafficHistorySlots) {
        newSlotsNeeded = this.maxTrafficHistorySlots;
      }
      if (newSlotsNeeded === 0) {
        currentData.sentHistory[currentData.sentHistory.length - 1] = currentData.totalSent;
        currentData.receivedHistory[currentData.receivedHistory.length - 1] = currentData.totalReceived;
      } else {
        for (let i = 0; i < newSlotsNeeded; i++) {
          currentData.sentHistory.push(currentData.totalSent);
          currentData.receivedHistory.push(currentData.totalReceived);
        }
      }
      if (currentData.sentHistory.length > this.maxTrafficHistorySlots) {
        currentData.sentHistory.splice(0, currentData.sentHistory.length - this.maxTrafficHistorySlots);
        currentData.receivedHistory.splice(0, currentData.receivedHistory.length - this.maxTrafficHistorySlots);
      }
    }
  }
  /**
   * Makes the service immediately refresh the specific node.
   */
  forceSpecificNodeRefresh(publicKey) {
    const nodeData = this.nodesMap.get(publicKey);
    if (nodeData) {
      this.startUpdates(nodeData);
    }
  }
  /** Follows the node over the live socket when possible, else polls. */
  startUpdates(nodeData) {
    if (this.live.usable) {
      this.startLiveSubscription(nodeData);
    } else {
      this.startDataSubscription(0, nodeData);
    }
  }
  /**
   * Gets the node's summary pushed over the live socket, which sends it at
   * once and then whenever it changes. A new subscription also serves as a
   * forced refresh. Falls back to polling if the socket cannot be used.
   */
  startLiveSubscription(nodeData) {
    if (nodeData.updateSubscription) {
      nodeData.updateSubscription.unsubscribe();
    }
    nodeData.updateSubscription = this.live.subscribe(`/api/visors/${nodeData.pk}/summary`, this.dataRefreshDelay).subscribe({
      next: (ev) => {
        if (ev.status === 429) {
          this.startDataSubscription(0, nodeData);
          return;
        }
        if (ev.status >= 200 && ev.status < 300) {
          let node;
          try {
            node = this.nodeService.nodeFromSummary(JSON.parse(ev.body));
          } catch (e) {
            return;
          }
          nodeData.consecutiveErrors = 0;
          const now = Date.now();
          const elapsed = nodeData.whenUpdateWasScheduled > 0 ? now - nodeData.whenUpdateWasScheduled : 0;
          const slots = elapsed >= this.dataRefreshDelay * 0.8 ? Math.max(1, Math.round(elapsed / this.dataRefreshDelay)) : 0;
          this.updateTrafficData(node.transports, nodeData.lastEmitedData.trafficData, now - slots * this.dataRefreshDelay);
          if (slots > 0 || nodeData.whenUpdateWasScheduled === 0) {
            nodeData.whenUpdateWasScheduled = now;
          }
          nodeData.lastEmitedData = {
            data: node,
            error: null,
            momentOfLastCorrectUpdate: now,
            updating: false,
            trafficData: nodeData.lastEmitedData.trafficData
          };
          nodeData.dataSubject.next(nodeData.lastEmitedData);
          return;
        }
        const err = processServiceError(new HttpErrorResponse({ status: ev.status, error: ev.body }));
        nodeData.consecutiveErrors = (nodeData.consecutiveErrors || 0) + 1;
        nodeData.lastEmitedData = {
          data: nodeData.lastEmitedData.data,
          error: err,
          momentOfLastCorrectUpdate: nodeData.lastEmitedData.momentOfLastCorrectUpdate,
          updating: false,
          trafficData: nodeData.lastEmitedData.trafficData
        };
        nodeData.dataSubject.next(nodeData.lastEmitedData);
        if (ev.status === 400) {
          nodeData.dataSubject.complete();
          nodeData.updateSubscription.unsubscribe();
          this.nodesMap.delete(nodeData.pk);
        }
      },
      error: () => this.startDataSubscription(0, nodeData)
    });
  }
  /**
   * Checks a node to see if a call for stopping updates was made and if it is already expired.
   * If so, the updates are stopped and the node is removed form nodesMap.
   */
  finishIfExpired(nodeData) {
    if (nodeData.stopRequestedDate > 0) {
      if (Date.now() - nodeData.stopRequestedDate > this.expirationTime) {
        nodeData.dataSubject.complete();
        nodeData.updateSubscription.unsubscribe();
        this.nodesMap.delete(nodeData.pk);
      }
    }
  }
  static {
    this.ctorParameters = () => [
      { type: StorageService },
      { type: NodeService },
      { type: LiveService }
    ];
  }
};
SingleNodeDataService = __decorate4([
  Injectable({
    providedIn: "root"
  })
], SingleNodeDataService);

// src/app/utils/page-base.ts
var __decorate5 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var PageBaseComponent_1;
var LocalValueData = class {
};
var PageBaseComponent = class PageBaseComponent2 {
  constructor() {
    this.persistentScrollPosKey = "scroll-pos";
  }
  static {
    PageBaseComponent_1 = this;
  }
  static {
    this.mustCallNgOnInitSuper = /* @__PURE__ */ Symbol("You must call super.ngOnInit.");
  }
  ngOnInit() {
    let lastScrollPos = this.getLocalValue(this.persistentScrollPosKey);
    lastScrollPos = lastScrollPos ? lastScrollPos.value : "0";
    window.scrollTo(0, Number(lastScrollPos));
    setTimeout(() => window.scrollTo(0, Number(lastScrollPos)), 1);
    return void 0;
  }
  saveScrollPosition(event) {
    this.saveLocalValue(this.persistentScrollPosKey, window.scrollY + "");
  }
  /**
   * Saves a value on the state inside window.history.
   *
   * @param key Key for identifying the value.
   * @param value Value to save.
   */
  saveLocalValue(key, value) {
    const state = window.history.state || {};
    state[key] = value;
    state[key + "_time"] = (/* @__PURE__ */ new Date()).getTime();
    window.history.replaceState(state, "", window.location.pathname + window.location.hash);
  }
  /**
   * Gets a value from window.history. If the value is not found, it returns null.
   *
   * @param key Key that identifies the value.
   */
  getLocalValue(key) {
    if (!window.history.state || window.history.state[key] === void 0) {
      return null;
    }
    const response = new LocalValueData();
    response.value = window.history.state[key];
    response.date = window.history.state[key + "_time"];
    return response;
  }
  static {
    this.propDecorators = {
      saveScrollPosition: [{ type: HostListener, args: ["window:scroll", ["$event"]] }]
    };
  }
};
PageBaseComponent = PageBaseComponent_1 = __decorate5([
  Component({
    selector: "app-page-base",
    template: "",
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush
  })
], PageBaseComponent);

// angular:jit:template:src/app/app.component.html
var app_component_default = `<!-- Window size alert for the VPN client. -->
@if (inVpnClient) {
  <div class="size-alert d-md-none">
    <div>
      <img src="assets/img/size-alert.png">
      <div>{{ 'common.window-size-error' | translate }}</div>
    </div>
  </div>
}
<div class="flex-1 content container-fluid">
  <div [ngClass]="{'background': inVpnClient}"></div>
  @if (hypervisorPkObtained || inLoginPage) {
    <router-outlet></router-outlet>
  }
  @if (!hypervisorPkObtained && !inLoginPage) {
    <app-loading-indicator class="h-100"></app-loading-indicator>
  }
</div>

@if (showingDataProblemMsg) {
  <div class="connection-problem-container">
    <span class="blinking">
      <mat-icon [inline]="true">error_outline</mat-icon> {{ 'common.data-update-problems' | translate }}
    </span>
  </div>
}

<app-command-palette></app-command-palette>
`;

// angular:jit:style:src/app/app.component.scss
var app_component_default2 = '/* src/app/app.component.scss */\n.size-alert {\n  background-color: rgba(0, 0, 0, 0.85);\n  position: fixed;\n  top: 0;\n  left: 0;\n  width: 100%;\n  height: 100%;\n  z-index: 10000;\n  display: inline-flex;\n  align-items: center;\n  justify-content: center;\n  text-align: center;\n  color: white;\n}\n.size-alert > div {\n  margin: 0px 40px;\n  max-width: 400px;\n}\n:host {\n  display: flex;\n  flex-direction: column;\n  justify-content: space-between;\n  min-height: 100%;\n  height: 100%;\n}\n.content {\n  padding: 20px !important;\n}\n.background {\n  background-image: url("./media/map.png");\n  background-size: cover;\n  background-position: center;\n  opacity: 0.1;\n  width: 100%;\n  height: 100%;\n  top: 0;\n  left: 0;\n  position: fixed;\n  pointer-events: none;\n  z-index: 0;\n}\n.connection-problem-container {\n  position: fixed;\n  bottom: 0;\n  right: 0;\n  background-color: red;\n  padding: 0px 10px 5px;\n  font-size: 10px;\n  opacity: 0.75;\n}\n.connection-problem-container mat-icon {\n  position: relative;\n  top: 4px;\n}\n/*# sourceMappingURL=app.component.css.map */\n';

// src/app/services/language.service.ts
var __decorate6 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var LanguageData = class {
  constructor(langObj) {
    Object.assign(this, langObj);
  }
};
var LanguageService = class LanguageService2 {
  constructor(translate) {
    this.translate = translate;
    this.currentLanguage = new ReplaySubject(1);
    this.languages = new ReplaySubject(1);
    this.storageKey = "lang";
    this.languagesInternal = [];
    this.settingsLoaded = false;
  }
  /**
   * Initializes the language management. Must be called at the start of the app.
   */
  loadLanguageSettings() {
    if (this.settingsLoaded) {
      return;
    }
    this.settingsLoaded = true;
    const langs = [];
    AppConfig.languages.forEach((lang) => {
      const LangObj = new LanguageData(lang);
      this.languagesInternal.push(LangObj);
      langs.push(LangObj.code);
    });
    this.languages.next(this.languagesInternal);
    this.translate.addLangs(langs);
    this.translate.setFallbackLang(AppConfig.defaultLanguage);
    this.translate.onLangChange.subscribe((event) => this.onLanguageChanged(event));
    this.loadCurrentLanguage();
  }
  /**
   * Changes the current language of the UI.
   */
  changeLanguage(langCode) {
    this.translate.use(langCode);
  }
  onLanguageChanged(event) {
    this.currentLanguage.next(this.languagesInternal.find((val) => val.code === event.lang));
    localStorage.setItem(this.storageKey, event.lang);
  }
  /**
   * Makes the UI to use the lastest language selected by the user.
   */
  loadCurrentLanguage() {
    let currentLang = localStorage.getItem(this.storageKey);
    currentLang = currentLang ? currentLang : AppConfig.defaultLanguage;
    setTimeout(() => this.translate.use(currentLang), 16);
  }
  static {
    this.ctorParameters = () => [
      { type: TranslateService }
    ];
  }
};
LanguageService = __decorate6([
  Injectable({
    providedIn: "root"
  })
], LanguageService);

// angular:jit:template:src/app/components/pages/node/skychat/skychat.component.html
var skychat_component_default = `@if (node && remoteVisor) {
  <div class="rounded-elevated-box mt-3">
    <div class="box-internal-container">{{ 'skychat.remote-unavailable' | translate }}</div>
  </div>
} @else if (node) {
  <div class="rounded-elevated-box mt-3 skychat-box">
    <div class="box-internal-container sc-shell">

      <!-- Status row spans the full width across the sidebar + chat. -->
      <div class="sc-status">
        <span class="dot" [ngClass]="connected ? 'dot-green' : 'dot-outline-gray'"></span>
        <span class="status-text">{{ 'skychat.title' | translate }}</span>
        <span class="status-sep">\xB7</span>
        <span class="status-conn">{{ connected ? ('skychat.connected' | translate) : ('skychat.disconnected' | translate) }}</span>
        @if (errorText) {
          <span class="error">\xB7 {{ errorText }}</span>
        }
        @if (!historyAvailable) {
          <span class="hint">\xB7 {{ 'skychat.no-history' | translate }}</span>
        }
        <span class="status-sep">\xB7</span>
        <div class="sc-mode-toggle">
          <button mat-button type="button" [ngClass]="{ 'active': chatMode === 'dm' }" (click)="switchMode('dm')">
            <mat-icon [inline]="true">person</mat-icon> {{ 'skychat.mode-direct' | translate }}
          </button>
          <button mat-button type="button" [ngClass]="{ 'active': chatMode === 'groups' }" (click)="switchMode('groups')">
            <mat-icon [inline]="true">groups</mat-icon> {{ 'skychat.mode-groups' | translate }}
          </button>
        </div>
        <span class="sc-status-spacer"></span>
        @if (!embeddedNodeKey) {
          <button mat-button type="button" class="sc-open-full" (click)="openFull()">
            <mat-icon [inline]="true">open_in_full</mat-icon>
            {{ 'common.open-full-page' | translate }}
          </button>
        }
        <button mat-button type="button" class="sc-password-toggle" (click)="togglePassword()">
          <mat-icon class="lock-icon" [inline]="true">{{ pwIsSet ? 'lock' : 'lock_open' }}</mat-icon>
          {{ (pwOpen ? 'skychat.password.hide' : (pwIsSet ? 'skychat.password.change' : 'skychat.password.set')) | translate }}
        </button>
      </div>

      <!-- Password panel (collapsible, full width). -->
      @if (pwOpen) {
        <div class="sc-password">
          <div class="sc-password-help">
            {{ (pwIsSet ? 'skychat.password.help-set' : 'skychat.password.help-unset') | translate }}
          </div>
          @if (pwIsSet) {
            <mat-form-field appearance="outline" class="field-pw">
              <mat-label>{{ 'skychat.password.old' | translate }}</mat-label>
              <input matInput type="password" [(ngModel)]="pwOld" maxlength="64">
            </mat-form-field>
          }
          <mat-form-field appearance="outline" class="field-pw">
            <mat-label>{{ 'skychat.password.new' | translate }}</mat-label>
            <input matInput type="password" [(ngModel)]="pwNew" maxlength="64">
          </mat-form-field>
          <mat-form-field appearance="outline" class="field-pw">
            <mat-label>{{ 'skychat.password.confirm' | translate }}</mat-label>
            <input matInput type="password" [(ngModel)]="pwConfirm" maxlength="64">
          </mat-form-field>
          <div class="sc-password-actions">
            <button mat-raised-button color="primary" (click)="applyPassword()" [disabled]="pwBusy || !pwNew">
              {{ (pwIsSet ? 'skychat.password.change' : 'skychat.password.set') | translate }}
            </button>
            @if (pwIsSet) {
              <button mat-stroked-button color="warn" (click)="clearPassword()" [disabled]="pwBusy || !pwOld">
                {{ 'skychat.password.clear' | translate }}
              </button>
            }
          </div>
        </div>
      }

      <!-- Ringing inbound voice calls (explicit-answer mode). Shown in both
           modes so a call is never missed. -->
      @for (c of voiceIncoming; track c.id) {
        <div class="sc-voice-ring">
          <mat-icon [inline]="true">ring_volume</mat-icon>
          <span class="sc-voice-ring-text">
            {{ 'skychat.voice.incoming' | translate }}
            <span class="mono small">{{ c.from ? shortPk(c.from) : c.id }}</span>
          </span>
          <span class="sc-status-spacer"></span>
          <button mat-raised-button color="primary" (click)="voiceAnswer(c.id)" [disabled]="voiceBusy">
            <mat-icon [inline]="true">call</mat-icon> {{ 'skychat.voice.answer' | translate }}
          </button>
          <button mat-stroked-button color="warn" (click)="voiceDecline(c.id)">
            {{ 'skychat.voice.decline' | translate }}
          </button>
        </div>
      }

      <!-- Two-column layout: peers sidebar + chat panel (Direct mode). -->
      @if (chatMode === 'dm') {
      <div class="sc-body">
        <!-- Peers sidebar (recent senders + the active recipient). -->
        <aside class="sc-sidebar">
          <div class="sc-sidebar-title">{{ 'skychat.peers' | translate }}</div>
          @if (peers.length === 0) {
            <div class="sc-sidebar-empty">{{ 'skychat.peers-empty' | translate }}</div>
          }
          @for (p of peers; track p) {
            <div
              class="sc-peer"
              [ngClass]="{ 'active': toPK === p }"
              (click)="pickRecipient(p)"
              [matTooltip]="p">
              <span class="peer-pk mono small">{{ p }}</span>
            </div>
          }
        </aside>

        <!-- Chat panel: message log (or expanded spectrogram) + composer. -->
        <section class="sc-chat">
          @if (bigViz && inCall) {
            <!-- Expanded spectrograms take over the message area during a call:
                 incoming (peer) on top, outgoing (you) below \u2014 like the CLI. -->
            <div class="sc-viz-big-wrap">
              <div class="sc-viz-big-head">
                <span>{{ 'skychat.voice.spectrogram-title' | translate }}</span>
                <button mat-icon-button class="viz-collapse" (click)="toggleBigViz()"
                        [matTooltip]="'skychat.voice.viz-collapse' | translate">
                  <mat-icon [inline]="true">close_fullscreen</mat-icon>
                </button>
              </div>
              <div class="sc-viz-big-panel">
                <span class="sc-viz-big-label">{{ 'skychat.voice.viz-incoming' | translate }}</span>
                <canvas #vizBig class="sc-viz-big" width="900" height="150"></canvas>
              </div>
              <div class="sc-viz-big-panel">
                <span class="sc-viz-big-label">{{ 'skychat.voice.viz-outgoing' | translate }}</span>
                <canvas #vizBigSent class="sc-viz-big" width="900" height="150"></canvas>
              </div>
            </div>
          } @else {
            <div #logEl class="sc-log">
              @if (messages.length === 0) {
                <div class="sc-empty">{{ 'skychat.empty' | translate }}</div>
              }
              @for (m of messages; track $index) {
                <div class="sc-msg" [ngClass]="m.direction">
                  <div class="sc-msg-meta">
                    <span class="peer mono small" (click)="pickRecipient(m.peer)" [matTooltip]="m.peer">
                      {{ m.direction === 'out' ? '\u2192' : '\u2190' }} {{ m.peer }}
                    </span>
                    <span class="ts">{{ m.ts | date:'mediumTime' }}</span>
                    @if (m.direction === 'out' && !m.deleted) {
                      <span class="sc-tick" [class.read]="m.status === 'read'" [matTooltip]="tickTitle(m.status)">{{ tickGlyph(m.status) }}</span>
                    }
                    @if (m.id && !m.deleted) {
                      <button class="sc-reply-btn" (click)="setReplyTo(m)" matTooltip="Reply">\u21A9</button>
                    }
                    @if (m.id && m.direction === 'out' && !m.deleted) {
                      <button class="sc-del-btn" (click)="deleteMsg(m)" matTooltip="Delete for everyone">\u{1F5D1}</button>
                    }
                  </div>
                  @if (m.replyTo && !m.deleted) {
                    <div class="sc-reply-quote">\u21A9 {{ replyParentText(m.replyTo) }}</div>
                  }
                  @if (m.deleted) {
                    <div class="sc-msg-text sc-deleted">\u{1F6AB} message deleted</div>
                  } @else {
                    <div class="sc-msg-text">{{ m.text }}</div>
                  }
                </div>
              }
            </div>
          }

          <div class="sc-composer">
            <mat-form-field appearance="outline" class="field-pk">
              <mat-label>{{ 'skychat.to' | translate }}</mat-label>
              <input matInput [(ngModel)]="toPK" placeholder="02abc\u2026">
            </mat-form-field>
            <mat-form-field appearance="outline" class="field-sm">
              <mat-label>{{ 'skychat.network' | translate }}</mat-label>
              <mat-select [(ngModel)]="network" panelClass="skynet-select-panel">
                <mat-option value="auto">auto</mat-option>
                <mat-option value="skynet">skynet</mat-option>
                <mat-option value="dmsg">dmsg</mat-option>
              </mat-select>
            </mat-form-field>
          </div>
          @if (replyTarget) {
            <div class="sc-reply-banner">
              <span class="sc-reply-banner-text">\u21A9 {{ replyTarget.text }}</span>
              <button class="sc-reply-cancel" (click)="cancelReply()" matTooltip="Cancel reply">\u2715</button>
            </div>
          }
          <div class="sc-composer">
            <mat-form-field appearance="outline" class="field-msg">
              <mat-label>{{ 'skychat.message' | translate }}</mat-label>
              <textarea matInput rows="2" [(ngModel)]="message"
                        (keydown.enter)="$any($event).ctrlKey && send()"
                        [placeholder]="('skychat.placeholder' | translate)"></textarea>
            </mat-form-field>
            <button mat-raised-button color="primary" class="send-btn" (click)="send()" [disabled]="sending || !message.trim() || !toPK.trim()">
              <mat-icon>send</mat-icon>
              {{ 'skychat.send' | translate }}
            </button>
            @if (voiceAvailable) {
              <button mat-raised-button class="call-btn" [color]="inCall ? 'warn' : 'accent'"
                      (click)="inCall ? voiceHangup() : voiceCall()"
                      [disabled]="voiceBusy || (!inCall && !toPK.trim())"
                      [matTooltip]="(inCall ? 'skychat.voice.hangup-tip' : 'skychat.voice.call-tip') | translate">
                <mat-icon>{{ inCall ? 'call_end' : 'call' }}</mat-icon>
                {{ (inCall ? 'skychat.voice.hangup' : 'skychat.voice.call') | translate }}
              </button>
            }
          </div>

          <!-- In-call control strip: live audio viz (level meter / spectrogram),
               mute mic / mute caller, duration. -->
          @if (voiceAvailable && inCall) {
            <div class="sc-incall-bar">
              <mat-icon class="sc-incall-dot" [inline]="true">graphic_eq</mat-icon>
              <span class="sc-incall-timer mono">{{ callDuration }}</span>
              <canvas #vizCanvas class="sc-viz" width="360" height="44"
                      [matTooltip]="(showSpectrogram ? 'skychat.voice.viz-spectrogram' : 'skychat.voice.viz-levels') | translate"></canvas>
              <button mat-icon-button class="viz-toggle" [ngClass]="{ 'active': showSpectrogram }"
                      (click)="toggleSpectrogram()"
                      [matTooltip]="'skychat.voice.viz-toggle' | translate">
                <mat-icon [inline]="true">{{ showSpectrogram ? 'graphic_eq' : 'gradient' }}</mat-icon>
              </button>
              <button mat-icon-button class="viz-toggle" [ngClass]="{ 'active': bigViz }"
                      (click)="toggleBigViz()"
                      [matTooltip]="'skychat.voice.viz-expand' | translate">
                <mat-icon [inline]="true">{{ bigViz ? 'close_fullscreen' : 'open_in_full' }}</mat-icon>
              </button>
              <button mat-stroked-button class="mute-btn" [ngClass]="{ 'muted': micMuted }"
                      (click)="toggleMicMute()"
                      [matTooltip]="(micMuted ? 'skychat.voice.unmute-mic-tip' : 'skychat.voice.mute-mic-tip') | translate">
                <mat-icon [inline]="true">{{ micMuted ? 'mic_off' : 'mic' }}</mat-icon>
                {{ (micMuted ? 'skychat.voice.mic-muted' : 'skychat.voice.mute-mic') | translate }}
              </button>
              <button mat-stroked-button class="mute-btn" [ngClass]="{ 'muted': spkMuted }"
                      (click)="toggleSpeakerMute()"
                      [matTooltip]="(spkMuted ? 'skychat.voice.unmute-caller-tip' : 'skychat.voice.mute-caller-tip') | translate">
                <mat-icon [inline]="true">{{ spkMuted ? 'volume_off' : 'volume_up' }}</mat-icon>
                {{ (spkMuted ? 'skychat.voice.caller-muted' : 'skychat.voice.mute-caller') | translate }}
              </button>
            </div>
          }
        </section>
      </div>
      }

      <!-- Groups mode: rooms sidebar + room log/composer. -->
      @if (chatMode === 'groups') {
      <div class="sc-body">
        <aside class="sc-sidebar">
          <div class="sc-sidebar-title sc-rooms-head">
            <span>{{ 'skychat.rooms' | translate }}</span>
            <span class="sc-rooms-actions">
              <button mat-icon-button matTooltip="{{ 'skychat.create-room' | translate }}"
                      (click)="showCreate = !showCreate; showJoin = false">
                <mat-icon [inline]="true">add</mat-icon>
              </button>
              <button mat-icon-button matTooltip="{{ 'skychat.join-room' | translate }}"
                      (click)="showJoin = !showJoin; showCreate = false">
                <mat-icon [inline]="true">login</mat-icon>
              </button>
            </span>
          </div>

          @if (showCreate) {
            <div class="sc-room-form">
              <input class="sc-inline-input" [(ngModel)]="newGroupName" placeholder="{{ 'skychat.room-name' | translate }}">
              <select class="sc-inline-input" [(ngModel)]="newGroupMode">
                <option value="public">public</option>
                <option value="private">private</option>
              </select>
              <button mat-raised-button color="primary" (click)="createGroup()" [disabled]="groupSending || !newGroupName.trim()">
                {{ 'skychat.create' | translate }}
              </button>
            </div>
          }
          @if (showJoin) {
            <div class="sc-room-form">
              <input class="sc-inline-input" [(ngModel)]="joinInvite" placeholder="{{ 'skychat.invite-link' | translate }}">
              <button mat-raised-button color="primary" (click)="joinGroup()" [disabled]="groupSending || !joinInvite.trim()">
                {{ 'skychat.join' | translate }}
              </button>
            </div>
          }

          @if (groupError) { <div class="sc-sidebar-empty error">{{ groupError }}</div> }
          @if (groups.length === 0 && !groupError) {
            <div class="sc-sidebar-empty">{{ 'skychat.rooms-empty' | translate }}</div>
          }
          @for (g of groups; track g.id) {
            <div class="sc-peer sc-room" [ngClass]="{ 'active': selectedGroup?.id === g.id }" (click)="selectGroup(g)">
              <span class="sc-room-name">{{ g.name }}</span>
              <span class="sc-room-meta mono small">
                <mat-icon [inline]="true">{{ g.mode === 'private' ? 'lock' : 'public' }}</mat-icon>
                @if (g.status === 'awaiting_approval') {
                  <span class="sc-room-badge pending" title="waiting for an admin to approve your request">pending</span>
                } @else if (g.status === 'denied') {
                  <span class="sc-room-badge denied" title="an admin declined your request">declined</span>
                  <button mat-button class="sc-ask-again" type="button" (click)="askAgain(g, $event)"
                          [disabled]="groupSending" title="ask again \u2014 re-queues your request after the same checks as a first ask">
                    {{ 'skychat.ask-again' | translate }}
                  </button>
                } @else {
                  <ng-container>{{ g.members }} \xB7 {{ g.role }}</ng-container>
                }
              </span>
            </div>
          }
        </aside>

        <section class="sc-chat">
          @if (!selectedGroup) {
            <div class="sc-empty">{{ 'skychat.rooms-pick' | translate }}</div>
          }
          @if (selectedGroup) {
            <div class="sc-room-header">
              <span class="sc-room-title">{{ selectedGroup.name }}</span>
              <span class="mono small sc-room-sub">
                {{ selectedGroup.mode }} \xB7 {{ selectedGroup.members }} {{ 'skychat.members' | translate }} \xB7 {{ selectedGroup.role }}
              </span>
              <span class="sc-status-spacer"></span>
              <button mat-button type="button" (click)="showInvite()">
                <mat-icon [inline]="true">share</mat-icon> {{ 'skychat.invite' | translate }}
              </button>
            </div>

            @if (inviteLink) {
              <div class="sc-invite-row">
                <input class="sc-inline-input mono small" [value]="inviteLink" readonly (click)="$any($event.target).select()">
                <button mat-icon-button matTooltip="{{ 'skychat.copy' | translate }}" (click)="copyInvite()">
                  <mat-icon [inline]="true">content_copy</mat-icon>
                </button>
              </div>
            }
            @if (selectedGroup.role === 'owner') {
              <div class="sc-invite-row">
                <input class="sc-inline-input mono small" [(ngModel)]="addMemberPk" placeholder="{{ 'skychat.add-member-pk' | translate }}">
                <button mat-stroked-button (click)="addMember()" [disabled]="!addMemberPk.trim()">
                  <mat-icon [inline]="true">person_add</mat-icon> {{ 'skychat.add' | translate }}
                </button>
              </div>
            }

            <div #logEl class="sc-log">
              @if (groupMessages.length === 0) {
                <div class="sc-empty">{{ 'skychat.empty' | translate }}</div>
              }
              @for (m of groupMessages; track $index) {
                <div class="sc-msg" [ngClass]="m.out ? 'out' : 'in'">
                  <div class="sc-msg-meta">
                    <span class="peer mono small" [matTooltip]="m.from">
                      {{ m.out ? '\u2192 ' + ('skychat.you' | translate) : '\u2190 ' + shortPk(m.from) }}
                    </span>
                    <span class="ts">{{ m.ts | date:'mediumTime' }}</span>
                  </div>
                  <div class="sc-msg-text">{{ m.text }}</div>
                </div>
              }
            </div>

            <div class="sc-composer">
              <mat-form-field appearance="outline" class="field-msg">
                <mat-label>{{ 'skychat.message' | translate }}</mat-label>
                <textarea matInput rows="2" [(ngModel)]="groupMessage"
                          (keydown.enter)="$any($event).ctrlKey && sendGroup()"
                          [placeholder]="('skychat.group-placeholder' | translate)"></textarea>
              </mat-form-field>
              <button mat-raised-button color="primary" class="send-btn" (click)="sendGroup()" [disabled]="groupSending || !groupMessage.trim()">
                <mat-icon>send</mat-icon>
                {{ 'skychat.send' | translate }}
              </button>
            </div>
          }
        </section>
      </div>
      }
    </div>
  </div>
}
`;

// angular:jit:style:src/app/components/pages/node/skychat/skychat.component.scss
var skychat_component_default2 = "/* src/app/components/pages/node/skychat/skychat.component.scss */\n.skychat-box {\n  display: flex;\n  flex-direction: column;\n}\n.sc-shell {\n  display: flex;\n  flex-direction: column;\n  gap: 4px;\n}\n.sc-status {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n  font-size: 13px;\n  padding: 6px 4px;\n}\n.sc-status .status-text {\n  font-weight: 600;\n  font-size: 14px;\n}\n.sc-status .status-sep {\n  opacity: 0.5;\n}\n.sc-status .status-conn {\n  font-weight: 500;\n}\n.sc-status .error {\n  opacity: 0.85;\n  color: rgba(var(--sw-red-rgb), 0.9);\n}\n.sc-status .hint {\n  opacity: 0.6;\n}\n.sc-status .dot {\n  display: inline-block;\n  width: 8px;\n  height: 8px;\n  border-radius: 50%;\n}\n.sc-status .dot-green {\n  background: rgb(var(--sw-green-rgb));\n}\n.sc-status .dot-outline-gray {\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.4);\n}\n.sc-status .sc-status-spacer {\n  flex: 1 1 auto;\n}\n.sc-status .sc-password-toggle {\n  font-size: 12px;\n  line-height: 1.2;\n  color: rgba(var(--sw-fg-rgb), 0.8);\n}\n.sc-status .sc-password-toggle .lock-icon {\n  font-size: 16px;\n  vertical-align: middle;\n  margin-right: 4px;\n  color: inherit;\n}\n.sc-status .sc-open-full {\n  color: rgba(var(--sw-fg-rgb), 0.8);\n}\n.sc-status .sc-open-full mat-icon {\n  color: inherit;\n}\n:host ::ng-deep .sc-shell .mdc-floating-label,\n:host ::ng-deep .sc-shell mat-label,\n:host ::ng-deep .sc-shell .mat-mdc-floating-label,\n:host ::ng-deep .sc-shell .mat-mdc-select-value-text,\n:host ::ng-deep .sc-shell .mat-mdc-select-placeholder,\n:host ::ng-deep .sc-shell .mat-mdc-select-arrow {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60)) !important;\n}\n:host ::ng-deep .sc-shell .mdc-text-field--focused .mdc-floating-label {\n  color: rgb(var(--sw-violet-rgb)) !important;\n}\n.sc-password {\n  margin: 8px 0 4px;\n  padding: 12px;\n  background: rgba(var(--sw-fg-rgb), 0.03);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.06);\n  border-radius: 6px;\n  display: flex;\n  flex-wrap: wrap;\n  gap: 12px 16px;\n  align-items: flex-end;\n}\n.sc-password .sc-password-help {\n  flex: 1 1 100%;\n  font-size: 12px;\n  opacity: 0.75;\n  margin-bottom: 4px;\n}\n.sc-password .field-pw {\n  flex: 1 1 200px;\n  min-width: 180px;\n}\n.sc-password .sc-password-actions {\n  flex: 1 1 100%;\n  display: flex;\n  gap: 8px;\n  justify-content: flex-end;\n}\n.sc-body {\n  display: flex;\n  gap: 12px;\n  align-items: stretch;\n}\n.sc-sidebar {\n  flex: 0 0 220px;\n  max-width: 240px;\n  display: flex;\n  flex-direction: column;\n  gap: 4px;\n  padding: 8px;\n  background: rgba(var(--sw-shade-rgb), 0.18);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.06);\n  border-radius: 6px;\n  overflow-y: auto;\n  max-height: 60vh;\n}\n.sc-sidebar .sc-sidebar-title {\n  font-size: 11px;\n  text-transform: uppercase;\n  letter-spacing: 0.5px;\n  opacity: max(0.6, var(--sw-dim));\n  padding: 2px 4px 6px;\n}\n.sc-sidebar .sc-sidebar-empty {\n  padding: 8px 4px;\n  font-size: 12px;\n  opacity: 0.5;\n}\n.sc-sidebar .sc-peer {\n  cursor: pointer;\n  padding: 6px 8px;\n  border-radius: 4px;\n  border: 1px solid transparent;\n  background: rgba(var(--sw-fg-rgb), 0.03);\n}\n.sc-sidebar .sc-peer:hover {\n  background: rgba(var(--sw-fg-rgb), 0.06);\n}\n.sc-sidebar .sc-peer.active {\n  background: rgba(var(--sw-blue-rgb), 0.18);\n  border-color: rgba(var(--sw-blue-rgb), 0.45);\n}\n.sc-sidebar .sc-peer .peer-pk {\n  display: block;\n  word-break: break-all;\n  line-height: 1.25;\n  font-size: 10px;\n}\n@media (max-width: 720px) {\n  .sc-body {\n    flex-direction: column;\n  }\n  .sc-sidebar {\n    flex: 0 0 auto;\n    max-width: none;\n    max-height: 140px;\n  }\n}\n.sc-chat {\n  flex: 1 1 auto;\n  display: flex;\n  flex-direction: column;\n  min-width: 0;\n}\n.sc-log {\n  background: rgba(var(--sw-shade-rgb), 0.18);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.06);\n  border-radius: 6px;\n  padding: 8px;\n  height: 50vh;\n  min-height: 320px;\n  overflow-y: auto;\n  display: flex;\n  flex-direction: column;\n  gap: 6px;\n}\n.sc-empty {\n  margin: auto;\n  opacity: 0.5;\n  font-size: 13px;\n}\n.sc-msg {\n  display: flex;\n  flex-direction: column;\n  gap: 2px;\n  padding: 6px 10px;\n  border-radius: 6px;\n  background: rgba(var(--sw-fg-rgb), 0.03);\n  max-width: 80%;\n}\n.sc-msg.in {\n  align-self: flex-start;\n}\n.sc-msg.out {\n  align-self: flex-end;\n  background: rgba(var(--sw-blue-rgb), 0.18);\n}\n.sc-msg-meta {\n  display: flex;\n  gap: 8px;\n  align-items: center;\n  font-size: 11px;\n  opacity: 0.7;\n  flex-wrap: wrap;\n}\n.sc-msg-meta .peer {\n  cursor: pointer;\n  text-decoration: underline dotted transparent;\n  word-break: break-all;\n}\n.sc-msg-meta .peer:hover {\n  text-decoration-color: currentColor;\n}\n.sc-msg-text {\n  white-space: pre-wrap;\n  word-break: break-word;\n  font-size: 13px;\n}\n.sc-reply-btn {\n  background: none;\n  border: none;\n  cursor: pointer;\n  color: inherit;\n  opacity: 0;\n  font-size: 12px;\n  padding: 0 2px;\n  transition: opacity 0.15s;\n}\n.sc-msg:hover .sc-reply-btn {\n  opacity: 0.55;\n}\n.sc-reply-btn:hover {\n  opacity: 1;\n}\n.sc-del-btn {\n  background: none;\n  border: none;\n  cursor: pointer;\n  color: inherit;\n  opacity: 0;\n  font-size: 12px;\n  padding: 0 2px;\n  transition: opacity 0.15s;\n}\n.sc-msg:hover .sc-del-btn {\n  opacity: 0.55;\n}\n.sc-del-btn:hover {\n  opacity: 1;\n}\n.sc-deleted {\n  opacity: 0.5;\n  font-style: italic;\n}\n.sc-tick {\n  font-size: 11px;\n  opacity: 0.55;\n  margin-left: 2px;\n  letter-spacing: -1px;\n}\n.sc-tick.read {\n  color: #4aa3ff;\n  opacity: 1;\n}\n.sc-reply-quote {\n  font-size: 11px;\n  opacity: 0.7;\n  border-left: 2px solid rgba(var(--sw-violet-rgb), 0.7);\n  padding: 1px 6px;\n  border-radius: 3px;\n  background: rgba(var(--sw-violet-rgb), 0.1);\n  white-space: nowrap;\n  overflow: hidden;\n  text-overflow: ellipsis;\n  max-width: 100%;\n}\n.sc-reply-banner {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n  padding: 4px 10px;\n  margin: 0 0 6px 0;\n  font-size: 12px;\n  border-left: 3px solid rgba(var(--sw-violet-rgb), 0.8);\n  background: rgba(var(--sw-violet-rgb), 0.1);\n  border-radius: 4px;\n}\n.sc-reply-banner .sc-reply-banner-text {\n  flex: 1;\n  white-space: nowrap;\n  overflow: hidden;\n  text-overflow: ellipsis;\n  opacity: 0.85;\n}\n.sc-reply-banner .sc-reply-cancel {\n  background: none;\n  border: none;\n  cursor: pointer;\n  color: inherit;\n  opacity: 0.7;\n  font-size: 13px;\n}\n.sc-reply-banner .sc-reply-cancel:hover {\n  opacity: 1;\n}\n.sc-composer {\n  display: flex;\n  gap: 12px;\n  align-items: flex-start;\n  margin-top: 8px;\n}\n.sc-composer .field-pk {\n  flex: 1 1 360px;\n}\n.sc-composer .field-sm {\n  flex: 0 1 130px;\n}\n.sc-composer .field-msg {\n  flex: 1 1 auto;\n  min-width: 0;\n}\n.sc-composer .send-btn {\n  flex: 0 0 auto;\n  align-self: center;\n  height: 56px;\n  white-space: nowrap;\n}\n.sc-mode-toggle {\n  display: inline-flex;\n  gap: 2px;\n}\n.sc-mode-toggle button {\n  font-size: 12px;\n  line-height: 1.2;\n  min-width: 0;\n  padding: 0 10px;\n  opacity: max(0.6, var(--sw-dim));\n  color: rgba(var(--sw-fg-rgb), 0.8);\n}\n.sc-mode-toggle button mat-icon {\n  font-size: 15px;\n  vertical-align: middle;\n  margin-right: 3px;\n}\n.sc-mode-toggle button.active {\n  opacity: 1;\n  font-weight: 600;\n  background: rgba(var(--sw-blue-rgb), 0.18);\n  border-radius: 4px;\n}\n.sc-rooms-head {\n  display: flex;\n  align-items: center;\n  justify-content: space-between;\n}\n.sc-rooms-head .sc-rooms-actions {\n  display: inline-flex;\n}\n.sc-rooms-head .sc-rooms-actions button {\n  width: 28px;\n  height: 28px;\n  line-height: 28px;\n}\n.sc-rooms-head .sc-rooms-actions mat-icon {\n  font-size: 18px;\n}\n.sc-room-form {\n  display: flex;\n  flex-direction: column;\n  gap: 6px;\n  padding: 6px 4px 8px;\n  margin-bottom: 4px;\n  border-bottom: 1px solid rgba(var(--sw-fg-rgb), 0.06);\n}\n.sc-inline-input {\n  width: 100%;\n  box-sizing: border-box;\n  padding: 6px 8px;\n  font-size: 12px;\n  color: inherit;\n  background: rgba(var(--sw-shade-rgb), 0.25);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.14);\n  border-radius: 4px;\n}\n.sc-inline-input:focus {\n  outline: none;\n  border-color: rgba(var(--sw-blue-rgb), 0.6);\n}\n.sc-room {\n  display: flex;\n  flex-direction: column;\n  gap: 2px;\n}\n.sc-room .sc-room-name {\n  font-size: 13px;\n  font-weight: 500;\n  word-break: break-word;\n}\n.sc-room .sc-room-meta {\n  display: inline-flex;\n  align-items: center;\n  gap: 4px;\n  font-size: 10px;\n  opacity: 0.65;\n}\n.sc-room .sc-room-meta mat-icon {\n  font-size: 12px;\n  width: 12px;\n  height: 12px;\n}\n.sc-room-header {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n  padding: 4px 2px 8px;\n  flex-wrap: wrap;\n}\n.sc-room-header .sc-room-title {\n  font-weight: 600;\n  font-size: 14px;\n}\n.sc-room-header .sc-room-sub {\n  opacity: 0.6;\n}\n.sc-room-header button {\n  font-size: 12px;\n}\n.sc-room-header button mat-icon {\n  font-size: 15px;\n  vertical-align: middle;\n  margin-right: 3px;\n}\n.sc-invite-row {\n  display: flex;\n  align-items: center;\n  gap: 6px;\n  margin-bottom: 6px;\n}\n.sc-invite-row .sc-inline-input {\n  flex: 1 1 auto;\n}\n.sc-voice-ring {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n  padding: 8px 10px;\n  margin: 4px 0 8px;\n  border-radius: 6px;\n  background: rgba(158, 206, 106, 0.12);\n  border: 1px solid rgba(158, 206, 106, 0.45);\n  font-size: 13px;\n}\n.sc-voice-ring .sc-voice-ring-text {\n  display: flex;\n  align-items: center;\n  gap: 6px;\n}\n.sc-voice-ring .sc-status-spacer {\n  flex: 1 1 auto;\n}\n.call-btn {\n  margin-left: 8px;\n}\n.sc-viz-big-wrap {\n  flex: 1 1 auto;\n  display: flex;\n  flex-direction: column;\n  min-height: 0;\n  border: 1px solid #2a2342;\n  border-radius: 6px;\n  overflow: hidden;\n  background: #0e0c14;\n}\n.sc-viz-big-wrap .sc-viz-big-head {\n  display: flex;\n  align-items: center;\n  justify-content: space-between;\n  padding: 4px 8px;\n  font-size: 12px;\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n  background: #15131c;\n  border-bottom: 1px solid #2a2342;\n}\n.sc-viz-big-wrap .sc-viz-big-head .viz-collapse {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n}\n.sc-viz-big-wrap .sc-viz-big-panel {\n  flex: 1 1 150px;\n  min-height: 150px;\n  display: flex;\n  flex-direction: column;\n  position: relative;\n}\n.sc-viz-big-wrap .sc-viz-big-panel:first-of-type {\n  border-bottom: 1px solid #2a2342;\n}\n.sc-viz-big-wrap .sc-viz-big-panel .sc-viz-big-label {\n  position: absolute;\n  top: 3px;\n  left: 6px;\n  font-size: 10px;\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n  background: rgba(14, 12, 20, 0.6);\n  padding: 0 4px;\n  border-radius: 3px;\n  pointer-events: none;\n  z-index: 1;\n}\n.sc-viz-big-wrap .sc-viz-big {\n  flex: 1 1 auto;\n  width: 100%;\n  height: 100%;\n  min-height: 148px;\n  display: block;\n}\n.sc-incall-bar {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n  padding: 6px 8px;\n  margin-top: 6px;\n  border-radius: 6px;\n  background: rgba(122, 162, 247, 0.1);\n  border: 1px solid rgba(122, 162, 247, 0.35);\n}\n.sc-incall-bar .sc-incall-dot {\n  color: #9ece6a;\n}\n.sc-incall-bar .sc-incall-timer {\n  font-size: 13px;\n  color: rgba(var(--sw-fg-rgb), 0.8);\n}\n.sc-incall-bar .sc-viz {\n  flex: 1 1 auto;\n  min-width: 80px;\n  height: 44px;\n  background: #0e0c14;\n  border: 1px solid #2a2342;\n  border-radius: 5px;\n}\n.sc-incall-bar .viz-toggle {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n}\n.sc-incall-bar .viz-toggle.active {\n  color: rgb(var(--sw-violet-rgb));\n}\n.sc-incall-bar .mute-btn {\n  color: rgba(var(--sw-fg-rgb), 0.8);\n  border-color: #2a2342;\n}\n.sc-incall-bar .mute-btn ::ng-deep .mat-icon {\n  color: inherit;\n}\n.sc-incall-bar .mute-btn.muted {\n  color: #f7768e;\n  border-color: rgba(247, 118, 142, 0.6);\n}\n.sc-room-badge {\n  padding: 0 0.4em;\n  border-radius: 3px;\n  font-size: 10px;\n  line-height: 1.5;\n}\n.sc-room-badge.pending {\n  background: rgba(224, 175, 104, 0.18);\n  color: #e0af68;\n}\n.sc-room-badge.denied {\n  background: rgba(247, 118, 142, 0.18);\n  color: #f7768e;\n}\n.sc-ask-again {\n  min-width: 0;\n  padding: 0 0.5em;\n  margin-left: 0.35em;\n  font-size: 11px;\n  line-height: 1.6;\n}\n/*# sourceMappingURL=skychat.component.css.map */\n";

// src/app/components/pages/node/skychat/skychat.component.ts
var __decorate7 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var SkychatComponent = class SkychatComponent2 extends PageBaseComponent {
  // Distinct peers seen so far, in last-touched order. Drives the
  // sidebar list. Recomputed lazily when messages change.
  get peers() {
    const seen = [];
    const have = /* @__PURE__ */ new Set();
    for (let i = this.messages.length - 1; i >= 0; i--) {
      const pk = this.messages[i].peer;
      if (!pk || have.has(pk)) {
        continue;
      }
      have.add(pk);
      seen.push(pk);
    }
    return seen;
  }
  constructor(api, live, snackbar, cdr, router, storage) {
    super();
    this.api = api;
    this.live = live;
    this.snackbar = snackbar;
    this.cdr = cdr;
    this.router = router;
    this.storage = storage;
    this.remoteVisor = false;
    this.toPK = "";
    this.message = "";
    this.network = "auto";
    this.sending = false;
    this.messages = [];
    this.replyTarget = null;
    this.msgById = /* @__PURE__ */ new Map();
    this.orphanReceipts = /* @__PURE__ */ new Map();
    this.connected = false;
    this.errorText = "";
    this.historyAvailable = true;
    this.wasAtBottom = true;
    this.es = null;
    this.wasmChat = false;
    this.pollTimer = null;
    this.lastPollLen = -1;
    this.wasmReadSent = /* @__PURE__ */ new Set();
    this.pwOpen = false;
    this.pwIsSet = false;
    this.pwOld = "";
    this.pwNew = "";
    this.pwConfirm = "";
    this.pwBusy = false;
    this.chatMode = "dm";
    this.groups = [];
    this.selectedGroup = null;
    this.groupMessages = [];
    this.groupMessage = "";
    this.groupSending = false;
    this.groupError = "";
    this.showCreate = false;
    this.showJoin = false;
    this.newGroupName = "";
    this.newGroupMode = "public";
    this.joinInvite = "";
    this.openGroupsOnLoad = false;
    this.addMemberPk = "";
    this.inviteLink = "";
    this.groupTimer = null;
    this.groupSub = null;
    this.lastGroupMsgLen = -1;
    this.sentByGroup = {};
    this.voiceActive = [];
    this.voiceIncoming = [];
    this.voiceBusy = false;
    this.voiceAvailable = true;
    this.micMuted = false;
    this.spkMuted = false;
    this.callStartMs = 0;
    this.voiceTimer = null;
    this.voiceSub = null;
    this.voiceRingSeen = /* @__PURE__ */ new Set();
    this.showSpectrogram = false;
    this.bigViz = false;
    this.vizTimer = null;
    this.lvlSent = [];
    this.lvlRecv = [];
    this.lvlMax = 96;
  }
  /** Open this visor's chat as a full-page view (unified in-SPA app host). Only
   *  offered from the routed node tab (not when already embedded in the host). */
  openFull() {
    if (this.node && this.node.localPk) {
      this.router.navigate(["/app", "skychat", this.node.localPk]);
    }
  }
  ngOnInit() {
    const hashQuery = window.location.hash.split("?")[1] || "";
    const peerParam = this.embeddedPeer || new URLSearchParams(hashQuery).get("peer") || "";
    if (/^[0-9a-fA-F]{66}$/.test(peerParam)) {
      this.toPK = peerParam;
    }
    const joinParam = new URLSearchParams(hashQuery).get("join") || "";
    if (joinParam) {
      this.openGroupsOnLoad = true;
      this.joinInvite = joinParam;
      this.showJoin = true;
    }
    if (this.embeddedNodeKey) {
      this.node = { localPk: this.embeddedNodeKey };
      this.remoteVisor = isRemoteVisor(this.embeddedNodeKey, this.storage.getLocalHypervisorPk());
      if (this.remoteVisor) {
        return super.ngOnInit();
      }
      const bridge = window.skywireVisor;
      if (bridge && typeof bridge.skychatMessages === "function") {
        this.node.arch = "wasm";
      }
      this.connectSSE();
      this.tryLoadPeers();
      this.refreshPasswordState();
      this.startVoicePoll();
      this.openGroupsIfLinked();
    } else {
      this.nodeSub = NodeComponent.currentNode.subscribe((node) => {
        const wasUnset = !this.node;
        this.node = node;
        if (wasUnset) {
          this.remoteVisor = isRemoteVisor(node.localPk, this.storage.getLocalHypervisorPk());
        }
        if (wasUnset && !this.remoteVisor) {
          this.connectSSE();
          this.tryLoadPeers();
          this.refreshPasswordState();
          this.startVoicePoll();
          this.openGroupsIfLinked();
        }
        this.cdr.markForCheck();
      });
    }
    return super.ngOnInit();
  }
  ngOnDestroy() {
    if (this.nodeSub) {
      this.nodeSub.unsubscribe();
    }
    this.disconnectSSE();
    this.stopGroupPoll();
    this.stopVoicePoll();
    this.stopViz();
  }
  ngAfterViewChecked() {
    if (this.wasAtBottom && this.logEl) {
      const el = this.logEl.nativeElement;
      el.scrollTop = el.scrollHeight;
    }
  }
  /** Build the proxy URL for a skychat path. */
  proxyUrl(path) {
    const apiPrefix = !environment.production && location.protocol.indexOf("http:") !== -1 ? "http-api" : "api";
    return new URL(`${apiPrefix}/visors/${this.node.localPk}/skychat/proxy/${path.replace(/^\/+/, "")}`, document.baseURI).href;
  }
  connectSSE() {
    if (!this.node || this.es || this.pollTimer) {
      return;
    }
    const sv = window.skywireVisor;
    this.wasmChat = this.node.arch === "wasm" && !!sv && typeof sv.skychatMessages === "function";
    if (this.wasmChat) {
      this.connectWasm(sv);
      return;
    }
    try {
      this.es = new EventSource(this.proxyUrl("sse"));
      this.es.onopen = () => {
        this.connected = true;
        this.errorText = "";
        this.cdr.markForCheck();
      };
      this.es.onerror = () => {
        this.connected = false;
        this.errorText = "Disconnected \u2014 retrying\u2026";
        this.cdr.markForCheck();
      };
      this.es.onmessage = (ev) => this.handleSSE(ev.data);
    } catch (e) {
      this.errorText = `SSE setup failed: ${e?.message || e}`;
    }
  }
  disconnectSSE() {
    if (this.es) {
      this.es.close();
      this.es = null;
    }
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }
  /** In-browser wasm skychat: poll skychatMessages() (JSON [{from,text,ts,out}],
   *  newest last) and mirror it into the message list. Sends honor the UI's
   *  network selector (skychatSend's 3rd arg), whose default is skynet — a routed
   *  transport survives dmsg session churn and measured faster than direct dmsg
   *  when a route is up — so DON'T clobber this.network here (was forced to dmsg,
   *  a stale wasm/native divergence). */
  connectWasm(sv) {
    const handle = (raw) => {
      let arr;
      try {
        arr = JSON.parse(raw || "[]") || [];
      } catch (e) {
        this.connected = false;
        this.errorText = "chat hook error";
        this.cdr.markForCheck();
        return;
      }
      if (!Array.isArray(arr)) {
        return;
      }
      this.connected = true;
      this.errorText = "";
      const delCount = arr.reduce((n, m) => n + (m.deleted ? 1 : 0), 0);
      const statWeight = arr.reduce((n, m) => n + (m.status === "read" ? 2 : m.status === "received" ? 1 : 0), 0);
      const sig = arr.length * 1e6 + delCount * 1e3 + statWeight;
      if (sig !== this.lastPollLen) {
        this.captureScroll();
        this.messages = arr.slice(-500).map((m) => {
          return {
            peer: m.from || "",
            direction: m.out ? "out" : "in",
            text: m.text || "",
            ts: m.ts || Date.now(),
            id: m.id || "",
            replyTo: m.reply_to || "",
            deleted: !!m.deleted,
            status: m.status || ""
          };
        });
        this.rebuildMsgIndex();
        this.lastPollLen = sig;
        this.cdr.markForCheck();
        for (const m of this.messages) {
          if (m.direction === "in" && m.id && !m.deleted && !this.wasmReadSent.has(m.id)) {
            this.wasmReadSent.add(m.id);
            Promise.resolve(sv.skychatReadReceipt(m.peer, m.id)).catch(() => {
            });
          }
        }
      }
    };
    const poll = () => {
      Promise.resolve(sv.skychatMessages()).then(handle).catch(() => {
        this.connected = false;
        this.errorText = "chat hook error";
        this.cdr.markForCheck();
      });
    };
    poll();
    this.pollTimer = setInterval(poll, 1500);
  }
  /** Skychat /sse emits a stringified JSON {sender, message} payload
   *  per data: line. Capture arrival as 'in' regardless of who sent
   *  — sender is the peer's PK which is what we want to display. */
  handleSSE(raw) {
    let data = null;
    try {
      data = JSON.parse(raw);
    } catch (e) {
    }
    if (!data || typeof data !== "object") {
      return;
    }
    if (data.channel === "dm-status") {
      if (data.status === "deleted" && data.id) {
        const dm = this.messages.find((m) => m.id && m.id === data.id);
        if (dm) {
          dm.deleted = true;
          dm.text = "";
          this.cdr.markForCheck();
        }
      } else if ((data.status === "received" || data.status === "read") && data.id) {
        this.applyReceipt(data.id, data.status);
      }
      return;
    }
    const out = data.dir === "out" || data.sender === "self";
    const sentAt = typeof data.ts === "number" ? data.ts : Date.parse(data.ts || "");
    const msg = {
      peer: out ? data.to || data.peer || data.from || "" : data.sender || data.from || "",
      direction: out ? "out" : "in",
      text: typeof data.message === "string" ? data.message : data.text || "",
      ts: sentAt > 0 ? sentAt : Date.now(),
      id: data.id || "",
      replyTo: data.reply_to_id || ""
    };
    if (!msg.peer || !msg.text) {
      return;
    }
    if (out && msg.id && this.msgById.has(msg.id)) {
      return;
    }
    this.captureScroll();
    this.indexMsg(msg);
    this.messages.push(msg);
    if (this.messages.length > 500) {
      this.messages.shift();
    }
    this.cdr.markForCheck();
    if (!out && msg.id && msg.peer && !this.wasmChat) {
      this.sendReadReceipt(msg.peer, msg.id);
    }
  }
  /** POST /read-receipt so the peer's bubble advances to 'read'. Best-effort. */
  sendReadReceipt(peer, id) {
    fetch(this.proxyUrl("read-receipt"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pk: peer, ids: [id] })
    }).catch(() => {
    });
  }
  /** Apply a delivery receipt to our own bubble by wire id. Never downgrades a
   *  bubble that already reached 'read' (a late 'received' is ignored). If the
   *  bubble isn't known yet (receipt raced the /message response), stash it. */
  applyReceipt(id, status) {
    const m = this.messages.find((x) => x.id === id && x.direction === "out");
    if (!m) {
      this.orphanReceipts.set(id, status);
      if (this.orphanReceipts.size > 64) {
        this.orphanReceipts.delete(this.orphanReceipts.keys().next().value);
      }
      return;
    }
    if (m.status === "read") {
      return;
    }
    m.status = status;
    this.cdr.markForCheck();
  }
  /** Claim a receipt that arrived before this outgoing bubble had its id. */
  claimOrphanReceipt(m) {
    if (!m.id) {
      return;
    }
    const status = this.orphanReceipts.get(m.id);
    if (!status) {
      return;
    }
    this.orphanReceipts.delete(m.id);
    m.status = status;
    this.cdr.markForCheck();
  }
  /** Tick glyph for an outgoing bubble's delivery status (WhatsApp-style). */
  tickGlyph(status) {
    return status === "received" || status === "read" ? "\u2713\u2713" : "\u2713";
  }
  /** Human-readable tooltip for the delivery tick. */
  tickTitle(status) {
    if (status === "read") {
      return "Read";
    }
    if (status === "received") {
      return "Received";
    }
    return "Sent";
  }
  /** Send the composed message. */
  send() {
    if (this.sending) {
      return;
    }
    const recipient = this.toPK.trim();
    const text = this.message.trim();
    if (!recipient || !text) {
      return;
    }
    if (recipient.length !== 66 || !/^[0-9a-fA-F]+$/.test(recipient)) {
      this.snackbar.showError("Recipient must be a 66-char hex public key");
      return;
    }
    this.sending = true;
    const replyToId = this.replyTarget ? this.replyTarget.id || "" : "";
    if (this.wasmChat) {
      const sv = window.skywireVisor;
      Promise.resolve(sv.skychatSend(recipient, text, this.network, replyToId)).then(() => {
        this.message = "";
        this.cancelReply();
        this.cdr.markForCheck();
      }).catch((e) => this.snackbar.showError(this.groupErrMsg(e))).finally(() => {
        this.sending = false;
        this.cdr.markForCheck();
      });
      return;
    }
    const payload = { recipient, message: text, network: this.network, receipts: true };
    if (replyToId) {
      payload.reply_to = replyToId;
    }
    fetch(this.proxyUrl("message"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    }).then((resp) => __async(this, null, function* () {
      if (!resp.ok) {
        const body = yield resp.text();
        throw new Error(body || `HTTP ${resp.status}`);
      }
      const res = yield resp.json().catch(() => ({}));
      const wireId = res && res.id || "";
      this.captureScroll();
      const out = { peer: recipient, direction: "out", text, ts: Date.now(), id: wireId, replyTo: replyToId, status: "sent" };
      this.messages.push(out);
      this.indexMsg(out);
      this.claimOrphanReceipt(out);
      if (this.messages.length > 500) {
        this.messages.shift();
      }
      this.message = "";
      this.cancelReply();
      this.cdr.markForCheck();
    })).catch((err) => {
      this.snackbar.showError(err?.message || String(err));
    }).finally(() => {
      this.sending = false;
      this.cdr.markForCheck();
    });
  }
  /** Try to seed the message list from skychat's history. Silently
   *  skips when persistence isn't enabled (skychat returns 503). */
  tryLoadPeers() {
    if (!this.node || !this.historyAvailable || this.wasmChat) {
      return;
    }
    fetch(this.proxyUrl("history?limit=100")).then((resp) => __async(this, null, function* () {
      if (resp.status === 503) {
        this.historyAvailable = false;
        return null;
      }
      if (!resp.ok) {
        throw new Error(`HTTP ${resp.status}`);
      }
      return resp.json();
      this.cdr.markForCheck();
    })).then((rows) => {
      if (!Array.isArray(rows)) {
        return;
      }
      const seeded = rows.map((m) => {
        return {
          peer: m.peer || m.sender || "",
          direction: m.outgoing || m.direction === "out" ? "out" : "in",
          text: m.message || m.text || "",
          ts: m.timestamp || m.ts || Date.now(),
          id: m.id || "",
          replyTo: m.reply_to || ""
        };
      }).filter((m) => m.peer && m.text);
      this.messages = seeded.concat(this.messages);
      this.rebuildMsgIndex();
      this.cdr.markForCheck();
    }).catch(() => {
    });
  }
  pickRecipient(pk) {
    this.toPK = pk;
  }
  indexMsg(m) {
    if (m.id) {
      this.msgById.set(m.id, m.text);
    }
  }
  rebuildMsgIndex() {
    this.msgById.clear();
    for (const m of this.messages) {
      if (m.id) {
        this.msgById.set(m.id, m.text);
      }
    }
  }
  /** Quoted-parent snippet for a reply, resolved by id (O(1) from the index). */
  replyParentText(id) {
    const t = this.msgById.get(id);
    if (!t) {
      return "\u2026";
    }
    return t.length > 80 ? t.slice(0, 80) + "\u2026" : t;
  }
  /** Arm the next send as a quoted reply to m (id-bearing messages only). */
  setReplyTo(m) {
    if (!m || !m.id) {
      return;
    }
    this.replyTarget = m;
    this.cdr.markForCheck();
  }
  /** Delete one of our own messages for everyone. Sends a chat-delete (wasm hook
   *  or the native /delete proxy) and tombstones the bubble optimistically; the
   *  peer drops it on their side via the controller's OnDelete. */
  deleteMsg(m) {
    if (!m || !m.id) {
      return;
    }
    const pk = m.peer;
    if (this.wasmChat) {
      const sv = window.skywireVisor;
      Promise.resolve(sv.skychatDelete(pk, m.id)).catch(() => {
      });
    } else {
      fetch(this.proxyUrl("delete"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pk, id: m.id })
      }).catch(() => {
      });
    }
    m.deleted = true;
    m.text = "";
    this.cdr.markForCheck();
  }
  /** Disarm the reply compose state. */
  cancelReply() {
    this.replyTarget = null;
    this.cdr.markForCheck();
  }
  captureScroll() {
    if (!this.logEl) {
      this.wasAtBottom = true;
      return;
    }
    const el = this.logEl.nativeElement;
    this.wasAtBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }
  // --- Group chat -------------------------------------------------
  // Transport split mirrors the DM path: an in-browser wasm visor drives
  // the in-process skywireVisor.skychatGroup* JS hooks; a native visor
  // goes over the hypervisor group HTTP bridge (/skychat/groups*). Both
  // return the same shapes so the UI below is transport-agnostic.
  /** window.skywireVisor when this node is an in-browser wasm visor with the
   *  group hooks present, else null. */
  get groupSv() {
    const sv = window.skywireVisor;
    if (this.node && this.node.arch === "wasm" && sv && typeof sv.skychatGroupList === "function") {
      return sv;
    }
    return null;
  }
  /** Native group-bridge call over ApiService so CSRF/auth are handled
   *  (the hypervisor group POST routes enforce the CSRF token, unlike the
   *  DM /skychat/proxy passthrough). Returns a Promise to unify with the
   *  wasm hooks. */
  groupApi(path, method, body) {
    const url = `visors/${this.node.localPk}/skychat/groups${path}`;
    const obs = method === "POST" ? this.api.post(url, body || {}) : this.api.get(url);
    return firstValueFrom(obs);
  }
  /** Extract a human message from an ApiService error. */
  groupErrMsg(e) {
    return e?.originalError?.error?.error || e?.error?.error || e?.message || String(e);
  }
  openGroupsIfLinked() {
    if (this.openGroupsOnLoad) {
      this.openGroupsOnLoad = false;
      this.switchMode("groups");
    }
  }
  switchMode(mode) {
    if (this.chatMode === mode) {
      return;
    }
    this.chatMode = mode;
    if (mode === "groups") {
      this.refreshGroups();
      this.startGroupPoll();
    } else {
      this.stopGroupPoll();
    }
  }
  startGroupPoll() {
    if (!this.groupSv) {
      this.followGroups();
      return;
    }
    if (this.groupTimer) {
      return;
    }
    this.groupTimer = setInterval(() => {
      this.refreshGroups();
      if (this.selectedGroup) {
        this.pollGroupMessages();
      }
      this.cdr.markForCheck();
    }, 2e3);
  }
  stopGroupPoll() {
    if (this.groupTimer) {
      clearInterval(this.groupTimer);
      this.groupTimer = null;
    }
    this.groupSub?.unsubscribe();
    this.groupSub = null;
  }
  /**
   * Native: follows the room list and the selected room over the live socket
   * (polling without it). Called again when the selection changes.
   */
  followGroups() {
    this.groupSub?.unsubscribe();
    if (!this.node) {
      this.groupSub = null;
      return;
    }
    const base = `visors/${this.node.localPk}/skychat/groups`;
    this.groupSub = this.live.watch(base, 2e3, () => this.api.get(base)).pipe(switchMap((res) => res.pipe(catchError((e) => {
      this.groupError = this.groupErrMsg(e);
      this.cdr.markForCheck();
      return EMPTY;
    })))).subscribe((arr) => {
      this.applyGroups(arr);
      this.cdr.markForCheck();
    });
    if (this.selectedGroup) {
      const id = this.selectedGroup.id;
      const url = `${base}/messages?group_id=${encodeURIComponent(id)}`;
      this.groupSub.add(this.live.watch(url, 2e3, () => this.api.get(url)).pipe(switchMap((res) => res.pipe(catchError(() => EMPTY)))).subscribe((arr) => {
        this.applyGroupMessages(id, this.nativeGroupMessages(arr));
        this.cdr.markForCheck();
      }));
    }
  }
  applyGroups(arr) {
    if (!Array.isArray(arr)) {
      return;
    }
    this.groups = arr.map((g) => {
      return {
        id: g.id,
        name: g.name,
        mode: g.mode,
        role: g.role,
        members: typeof g.members === "number" ? g.members : Array.isArray(g.members) ? g.members.length : 0,
        status: g.status
      };
    });
    if (this.selectedGroup) {
      const upd = this.groups.find((g) => g.id === this.selectedGroup.id);
      if (upd) {
        this.selectedGroup = upd;
      }
    }
    this.groupError = "";
    this.cdr.markForCheck();
  }
  /** Refresh the room list from whichever transport this node uses. */
  refreshGroups() {
    if (!this.node) {
      return;
    }
    const sv = this.groupSv;
    const done = (arr) => this.applyGroups(arr);
    if (sv) {
      Promise.resolve(sv.skychatGroupList()).then((raw) => {
        const result = done(JSON.parse(raw || "[]") || []);
        this.cdr.markForCheck();
        return result;
      }).catch(() => {
      });
      return;
    }
    this.groupApi("", "GET").then(done).catch((e) => {
      this.groupError = this.groupErrMsg(e);
      this.cdr.markForCheck();
    });
  }
  // Re-request to join a group an admin declined (the "ask again" button on a
  // room whose status is 'denied'). Derives everything from the stored record
  // server-side and pays the same PoW + rate-limit gates as a first ask, so a
  // declined key cannot spam the queue faster than a new one. $event.stop keeps
  // the row's (click)=selectGroup from also firing.
  askAgain(g, ev) {
    if (ev) {
      ev.stopPropagation();
    }
    if (this.groupSending) {
      return;
    }
    this.groupSending = true;
    const sv = this.groupSv;
    const after = () => {
      this.refreshGroups();
      this.snackbar.showDone("Re-requested to join \u2014 waiting for an admin");
    };
    const p = sv ? Promise.resolve(sv.skychatGroupAskAgain(g.id)).then(after) : this.groupApi(`/${encodeURIComponent(g.id)}/ask-again`, "POST").then(after);
    p.catch((e) => this.snackbar.showError(this.groupErrMsg(e))).finally(() => {
      this.groupSending = false;
      this.cdr.markForCheck();
    });
  }
  selectGroup(g) {
    this.selectedGroup = g;
    this.groupMessages = [];
    this.lastGroupMsgLen = -1;
    this.inviteLink = "";
    this.addMemberPk = "";
    this.pollGroupMessages();
    this.startGroupPoll();
  }
  /** Poll the selected room's message ring (full snapshot each tick — the
   *  ring is bounded, so rebuild-on-change is simplest + robust). */
  pollGroupMessages() {
    if (!this.node || !this.selectedGroup) {
      return;
    }
    const id = this.selectedGroup.id;
    const done = (arr) => this.applyGroupMessages(id, arr);
    const sv = this.groupSv;
    if (sv) {
      Promise.resolve(sv.skychatGroupMessages(id)).then((raw) => {
        const result = done(JSON.parse(raw || "[]") || []);
        this.cdr.markForCheck();
        return result;
      }).catch(() => {
      });
      return;
    }
    this.groupApi(`/messages?group_id=${encodeURIComponent(id)}`, "GET").then((arr) => {
      const result = done(this.nativeGroupMessages(arr));
      this.cdr.markForCheck();
      return result;
    }).catch(() => {
    });
  }
  /** The native bridge names the sender sender_pk and may send ts as a string. */
  nativeGroupMessages(arr) {
    return (arr || []).map((m) => {
      return {
        group_id: m.group_id,
        from: m.sender_pk || m.from || "",
        text: m.text || "",
        ts: m.ts ? typeof m.ts === "number" ? m.ts : Date.parse(m.ts) : Date.now()
      };
    });
  }
  applyGroupMessages(id, arr) {
    if (!Array.isArray(arr) || !this.selectedGroup || this.selectedGroup.id !== id) {
      return;
    }
    const me = this.node.localPk;
    const sent = this.sentByGroup[id] || [];
    if (arr.length === this.lastGroupMsgLen && sent.length === 0) {
      return;
    }
    this.captureScroll();
    const incoming = arr.map((m) => {
      return {
        group_id: m.group_id,
        from: m.from || m.sender_pk || "",
        text: m.text || "",
        ts: m.ts || Date.now(),
        out: (m.from || m.sender_pk || "") === me
      };
    });
    const inKeys = new Set(incoming.map((m) => m.from + "|" + m.text));
    const sentUnique = sent.filter((m) => !inKeys.has(m.from + "|" + m.text));
    this.groupMessages = incoming.concat(sentUnique).sort((a, b) => a.ts - b.ts).slice(-500);
    this.lastGroupMsgLen = arr.length;
    this.cdr.markForCheck();
  }
  createGroup() {
    if (!this.node || this.groupSending) {
      return;
    }
    const name = this.newGroupName.trim();
    if (!name) {
      this.snackbar.showError("Room name required");
      return;
    }
    this.groupSending = true;
    const sv = this.groupSv;
    const after = (id, invite) => {
      this.newGroupName = "";
      this.showCreate = false;
      this.inviteLink = invite || "";
      this.refreshGroups();
      this.snackbar.showDone("Room created");
    };
    const p = sv ? Promise.resolve(sv.skychatGroupCreate(name, this.newGroupMode)).then((r) => {
      const result = after(r.id, r.invite);
      this.cdr.markForCheck();
      return result;
    }) : this.groupApi("", "POST", { name, mode: this.newGroupMode }).then((r) => {
      const result = after(r.info?.id, r.invite);
      this.cdr.markForCheck();
      return result;
    });
    p.catch((e) => this.snackbar.showError(this.groupErrMsg(e))).finally(() => {
      this.groupSending = false;
      this.cdr.markForCheck();
    });
  }
  joinGroup() {
    if (!this.node || this.groupSending) {
      return;
    }
    const invite = this.joinInvite.trim();
    if (!invite) {
      this.snackbar.showError("Invite link required");
      return;
    }
    this.groupSending = true;
    const sv = this.groupSv;
    const after = () => {
      this.joinInvite = "";
      this.showJoin = false;
      this.refreshGroups();
      this.snackbar.showDone("Joined room");
    };
    const p = sv ? Promise.resolve(sv.skychatGroupJoin(invite)).then(after) : this.groupApi("/join", "POST", { invite }).then(after);
    p.catch((e) => this.snackbar.showError(this.groupErrMsg(e))).finally(() => {
      this.groupSending = false;
      this.cdr.markForCheck();
    });
  }
  sendGroup() {
    if (!this.node || this.groupSending || !this.selectedGroup) {
      return;
    }
    const text = this.groupMessage.trim();
    if (!text) {
      return;
    }
    const id = this.selectedGroup.id;
    this.groupSending = true;
    const sv = this.groupSv;
    const p = sv ? Promise.resolve(sv.skychatGroupSend(id, text)) : this.groupApi("/send", "POST", { id, text });
    p.then(() => {
      this.groupMessage = "";
      (this.sentByGroup[id] = this.sentByGroup[id] || []).push({
        group_id: id,
        from: this.node.localPk,
        text,
        ts: Date.now(),
        out: true
      });
      this.lastGroupMsgLen = -1;
      this.pollGroupMessages();
      this.cdr.markForCheck();
    }).catch((e) => this.snackbar.showError(this.groupErrMsg(e))).finally(() => {
      this.groupSending = false;
      this.cdr.markForCheck();
    });
  }
  addMember() {
    if (!this.node || !this.selectedGroup) {
      return;
    }
    const pk = this.addMemberPk.trim();
    if (pk.length !== 66 || !/^[0-9a-fA-F]+$/.test(pk)) {
      this.snackbar.showError("Member must be a 66-char hex public key");
      return;
    }
    const id = this.selectedGroup.id;
    const sv = this.groupSv;
    const p = sv ? Promise.resolve(sv.skychatGroupAddMember(id, pk)) : this.groupApi("/add-member", "POST", { id, pk });
    p.then(() => {
      this.addMemberPk = "";
      this.refreshGroups();
      this.snackbar.showDone("Member added");
      this.cdr.markForCheck();
    }).catch((e) => this.snackbar.showError(this.groupErrMsg(e))).finally(() => this.cdr.markForCheck());
  }
  /** Mint a fresh invite link for the selected room and reveal it. */
  showInvite() {
    if (!this.node || !this.selectedGroup) {
      return;
    }
    const id = this.selectedGroup.id;
    const sv = this.groupSv;
    if (sv) {
      this.snackbar.showError("Invite is shown when the room is created (wasm visor)");
      return;
    }
    this.groupApi(`/invite?group_id=${encodeURIComponent(id)}`, "GET").then((r) => {
      this.inviteLink = r.invite || "";
      this.cdr.markForCheck();
    }).catch((e) => this.snackbar.showError(this.groupErrMsg(e)));
  }
  copyInvite() {
    if (!this.inviteLink) {
      return;
    }
    try {
      navigator.clipboard.writeText(this.inviteLink);
      this.snackbar.showDone("Invite copied");
    } catch (e) {
    }
  }
  shortPk(pk) {
    return pk && pk.length > 12 ? pk.slice(0, 6) + "\u2026" + pk.slice(-4) : pk;
  }
  // --- Voice calls -----------------------------------------------
  // Opus voice over the encrypted mesh (dmsg/skynet), same media plane as
  // the wasm visor. Native audio uses the visor's audio device; on a host
  // without one the call still connects (silent), so the control plane is
  // always demonstrable from here.
  /** window.skywireVisor when this node is an in-browser wasm visor with the
   *  voice hooks present, else null (native → HTTP bridge). */
  get voiceSv() {
    const sv = window.skywireVisor;
    if (this.node && this.node.arch === "wasm" && sv && typeof sv.skychatVoiceCall === "function") {
      return sv;
    }
    return null;
  }
  /** Native voice-bridge call over ApiService (CSRF/auth handled). */
  voiceApi(path, method, body) {
    const url = `visors/${this.node.localPk}/skychat/voice${path}`;
    const obs = method === "POST" ? this.api.post(url, body || {}) : this.api.get(url);
    return firstValueFrom(obs);
  }
  /** True while at least one call is up (drives the Call/Hang-up toggle). */
  get inCall() {
    return this.voiceActive.length > 0;
  }
  startVoicePoll() {
    if (this.voiceTimer || this.voiceSub || !this.node) {
      return;
    }
    if (!this.voiceSv) {
      this.followVoice();
      return;
    }
    this.pollVoice();
    this.voiceTimer = setInterval(() => {
      this.pollVoice();
      this.cdr.markForCheck();
    }, 1800);
  }
  stopVoicePoll() {
    this.voiceSub?.unsubscribe();
    this.voiceSub = null;
    if (this.voiceTimer) {
      clearInterval(this.voiceTimer);
      this.voiceTimer = null;
    }
  }
  /** Native: follows the active and incoming calls over the live socket (polling without it). */
  followVoice() {
    const base = `visors/${this.node.localPk}/skychat/voice`;
    const active = `${base}/active`;
    const incoming = `${base}/incoming`;
    this.voiceSub = this.live.watch(active, 1800, () => this.api.get(active)).pipe(switchMap((res) => res.pipe(catchError((e) => {
      if (e?.originalError?.status === 503) {
        this.voiceAvailable = false;
        this.cdr.markForCheck();
      }
      return EMPTY;
    })))).subscribe((r) => {
      this.voiceActive = this.parseIds(r);
      this.voiceAvailable = true;
      this.syncCallState();
      this.cdr.markForCheck();
    });
    this.voiceSub.add(this.live.watch(incoming, 1800, () => this.api.get(incoming)).pipe(switchMap((res) => res.pipe(catchError(() => EMPTY)))).subscribe((r) => {
      this.voiceIncoming = this.parseIncoming(r);
      this.cdr.markForCheck();
    }));
  }
  /** Normalize active-call ids from either transport (wasm returns a JSON
   *  string, native an array). */
  parseIds(raw) {
    let arr = raw;
    if (typeof raw === "string") {
      try {
        arr = JSON.parse(raw || "[]");
      } catch (e) {
        arr = [];
      }
    }
    return Array.isArray(arr) ? arr.map((x) => String(x)) : [];
  }
  /** Normalize ringing calls: wasm gives [{id,from}] (as a JSON string),
   *  native gives ["<id> from <pk>"]. */
  parseIncoming(raw) {
    let arr = raw;
    if (typeof raw === "string") {
      try {
        arr = JSON.parse(raw || "[]");
      } catch (e) {
        arr = [];
      }
    }
    if (!Array.isArray(arr)) {
      return [];
    }
    return arr.map((x) => {
      if (x && typeof x === "object") {
        return { id: String(x.id || ""), from: String(x.from || "") };
      }
      const s = String(x);
      const m = s.match(/^(\S+)\s+from\s+(\S+)/);
      return m ? { id: m[1], from: m[2] } : { id: s, from: "" };
    }).filter((c) => c.id);
  }
  /** Track call start/end so the duration timer + mute state reset cleanly. */
  syncCallState() {
    if (this.voiceActive.length && !this.callStartMs) {
      this.callStartMs = Date.now();
      this.micMuted = false;
      this.spkMuted = false;
      setTimeout(() => {
        this.startViz();
        this.cdr.markForCheck();
      }, 300);
    } else if (!this.voiceActive.length && this.callStartMs) {
      this.callStartMs = 0;
      this.micMuted = false;
      this.spkMuted = false;
      this.stopViz();
    }
  }
  pollVoice() {
    if (!this.node) {
      return;
    }
    const sv = this.voiceSv;
    if (sv) {
      Promise.resolve(sv.skychatVoiceActive()).then((r) => {
        this.voiceActive = this.parseIds(r);
        this.syncCallState();
        this.cdr.markForCheck();
      }).catch(() => {
      });
      Promise.resolve(sv.skychatVoiceIncoming()).then((r) => {
        this.voiceIncoming = this.parseIncoming(r);
        this.cdr.markForCheck();
      }).catch(() => {
      });
      return;
    }
    this.voiceApi("/active", "GET").then((r) => {
      this.voiceActive = this.parseIds(r);
      this.voiceAvailable = true;
      this.syncCallState();
      this.cdr.markForCheck();
    }).catch((e) => {
      if (e?.originalError?.status === 503) {
        this.voiceAvailable = false;
        this.cdr.markForCheck();
      }
    });
    this.voiceApi("/incoming", "GET").then((r) => {
      this.voiceIncoming = this.parseIncoming(r);
      this.cdr.markForCheck();
    }).catch(() => {
    });
  }
  /** mm:ss since the current call went active ('' when not in a call). */
  get callDuration() {
    if (!this.callStartMs) {
      return "";
    }
    const s = Math.max(0, Math.floor((Date.now() - this.callStartMs) / 1e3));
    const mm = Math.floor(s / 60), ss = s % 60;
    return `${mm}:${ss < 10 ? "0" : ""}${ss}`;
  }
  /** Push the current mute state to the active call (both directions). */
  voiceMute() {
    const id = this.voiceActive[0];
    if (!id) {
      return;
    }
    const sv = this.voiceSv;
    const p = sv ? Promise.resolve(sv.skychatVoiceMute ? sv.skychatVoiceMute(id, this.micMuted, this.spkMuted) : null) : this.voiceApi("/mute", "POST", { call_id: id, mic: this.micMuted, speaker: this.spkMuted });
    p.catch((e) => this.snackbar.showError(this.groupErrMsg(e)));
  }
  /** Toggle our mic (the peer hears silence while muted). */
  toggleMicMute() {
    this.micMuted = !this.micMuted;
    this.voiceMute();
    this.cdr.markForCheck();
  }
  /** Toggle the caller's audio (we hear silence while muted). */
  toggleSpeakerMute() {
    this.spkMuted = !this.spkMuted;
    this.voiceMute();
    this.cdr.markForCheck();
  }
  voiceLevels(id) {
    const sv = this.voiceSv;
    if (sv && typeof sv.skychatVoiceLevels === "function") {
      return Promise.resolve(sv.skychatVoiceLevels(id)).then((r) => {
        const result = JSON.parse(r || "{}");
        this.cdr.markForCheck();
        return result;
      });
    }
    return this.voiceApi(`/levels?call=${encodeURIComponent(id)}`, "GET");
  }
  voiceAudioData(id) {
    const sv = this.voiceSv;
    if (sv && typeof sv.skychatVoiceAudio === "function") {
      return Promise.resolve(sv.skychatVoiceAudio(id)).then((r) => {
        const result = JSON.parse(r || "{}");
        this.cdr.markForCheck();
        return result;
      });
    }
    return this.voiceApi(`/audio?call=${encodeURIComponent(id)}`, "GET");
  }
  toggleSpectrogram() {
    this.showSpectrogram = !this.showSpectrogram;
    this.clearCanvas(this.vizCanvas?.nativeElement);
    this.cdr.markForCheck();
  }
  /** Expand a large spectrogram into the message-log area (toggle off = messages). */
  toggleBigViz() {
    this.bigViz = !this.bigViz;
    this.cdr.markForCheck();
  }
  clearCanvas(c) {
    if (c) {
      c.getContext("2d")?.clearRect(0, 0, c.width, c.height);
    }
  }
  startViz() {
    if (this.vizTimer) {
      return;
    }
    this.lvlSent = [];
    this.lvlRecv = [];
    this.vizTimer = setInterval(() => {
      this.tickViz();
      this.cdr.markForCheck();
    }, 100);
  }
  stopViz() {
    if (this.vizTimer) {
      clearInterval(this.vizTimer);
      this.vizTimer = null;
    }
    this.lvlSent = [];
    this.lvlRecv = [];
  }
  tickViz() {
    const id = this.voiceActive[0];
    if (!id) {
      return;
    }
    const needAudio = this.showSpectrogram || this.bigViz;
    const needLevels = !this.showSpectrogram;
    if (needLevels) {
      this.voiceLevels(id).then((l) => {
        this.lvlRecv.push(Math.min(1, l && l.recv || 0));
        this.lvlSent.push(Math.min(1, l && l.sent || 0));
        if (this.lvlRecv.length > this.lvlMax) {
          this.lvlRecv.shift();
        }
        if (this.lvlSent.length > this.lvlMax) {
          this.lvlSent.shift();
        }
        this.drawLevels(this.vizCanvas?.nativeElement);
        this.cdr.markForCheck();
      }).catch(() => {
      });
    }
    if (needAudio) {
      this.voiceAudioData(id).then((d) => {
        const recv = d && d.recv || [], sent = d && d.sent || [];
        if (this.showSpectrogram) {
          this.drawSpectrogram(this.vizCanvas?.nativeElement, recv);
        }
        if (this.bigViz) {
          this.drawSpectrogram(this.vizBig?.nativeElement, recv);
          this.drawSpectrogram(this.vizBigSent?.nativeElement, sent);
        }
        this.cdr.markForCheck();
      }).catch(() => {
      });
    }
  }
  /** Telegram-style dual level meter: peer (green) foreground, you (violet) behind. */
  drawLevels(c) {
    if (!c) {
      return;
    }
    const ctx = c.getContext("2d");
    if (!ctx) {
      return;
    }
    const W = c.width, H = c.height, n = this.lvlMax, bw = W / n;
    ctx.clearRect(0, 0, W, H);
    const bar = (hist, color, wf) => {
      ctx.fillStyle = color;
      for (let i = 0; i < hist.length; i++) {
        const v = hist[i];
        const h = Math.max(1, Math.pow(v, 0.6) * H);
        const x = i * bw;
        ctx.fillRect(x + bw * (1 - wf) / 2, (H - h) / 2, Math.max(1, bw * wf - 1), h);
      }
    };
    bar(this.lvlSent, "rgba(157,124,255,0.55)", 1);
    bar(this.lvlRecv, "rgba(158,206,106,0.95)", 0.6);
  }
  /** Scrolling FFT heatmap (newest column on the right), matched to the
   *  audioprism-go spectrogram: Hann window, magnitude = |FFT| (no N scaling),
   *  ScaleLog 20·log10, normalized over 0..45 dB, heat colormap. 0..12 kHz with
   *  low frequencies at the bottom. Silence → 0 dB → black. */
  drawSpectrogram(c, pcm) {
    const N = 2048;
    if (!c || pcm.length < N) {
      return;
    }
    const ctx = c.getContext("2d");
    if (!ctx) {
      return;
    }
    const W = c.width, H = c.height, col = 2;
    const start = pcm.length - N;
    const re = new Float64Array(N), im = new Float64Array(N);
    for (let i = 0; i < N; i++) {
      re[i] = pcm[start + i] / 32768 * (0.5 - 0.5 * Math.cos(2 * Math.PI * i / (N - 1)));
    }
    fft(re, im);
    ctx.drawImage(c, -col, 0);
    const maxBin = N / 4;
    for (let y = 0; y < H; y++) {
      const bin = Math.floor((1 - y / H) * (maxBin - 1));
      const mag = Math.hypot(re[bin], im[bin]);
      const dB = 20 * Math.log10(mag + 1e-10);
      const t = Math.max(0, Math.min(1, dB / 45));
      ctx.fillStyle = heat(t);
      ctx.fillRect(W - col, y, col, 1);
    }
  }
  /** Place a call to the composed recipient PK. */
  voiceCall() {
    if (this.voiceBusy || this.inCall) {
      return;
    }
    const peer = this.toPK.trim();
    if (peer.length !== 66 || !/^[0-9a-fA-F]+$/.test(peer)) {
      this.snackbar.showError("Recipient must be a 66-char hex public key");
      return;
    }
    this.voiceBusy = true;
    const sv = this.voiceSv;
    const done = () => {
      this.voiceBusy = false;
      this.pollVoice();
      this.cdr.markForCheck();
    };
    const p = sv ? Promise.resolve(sv.skychatVoiceCall(peer)) : this.voiceApi("/call", "POST", { peer });
    p.then(() => {
      this.snackbar.showDone("Call connected");
      done();
      this.cdr.markForCheck();
    }).catch((e) => {
      this.snackbar.showError(this.groupErrMsg(e));
      done();
    });
  }
  /** Hang up an active call (defaults to the first). */
  voiceHangup(id) {
    const callID = id || this.voiceActive[0];
    if (!callID) {
      return;
    }
    const sv = this.voiceSv;
    const done = () => {
      this.pollVoice();
      this.cdr.markForCheck();
    };
    const p = sv ? Promise.resolve(sv.skychatVoiceHangup(callID)) : this.voiceApi("/hangup", "POST", { call_id: callID });
    p.then(done).catch((e) => {
      this.snackbar.showError(this.groupErrMsg(e));
      done();
    });
  }
  /** Answer a ringing inbound call. */
  voiceAnswer(id) {
    if (this.voiceBusy) {
      return;
    }
    this.voiceBusy = true;
    this.voiceRingSeen.add(id);
    const sv = this.voiceSv;
    const done = () => {
      this.voiceBusy = false;
      this.pollVoice();
      this.cdr.markForCheck();
    };
    const p = sv ? Promise.resolve(sv.skychatVoiceAnswer(id)) : this.voiceApi("/answer", "POST", { call_id: id });
    p.then(() => {
      this.snackbar.showDone("Call answered");
      done();
      this.cdr.markForCheck();
    }).catch((e) => {
      this.snackbar.showError(this.groupErrMsg(e));
      done();
    });
  }
  /** Decline a ringing inbound call. */
  voiceDecline(id) {
    this.voiceRingSeen.add(id);
    const sv = this.voiceSv;
    const done = () => {
      this.pollVoice();
      this.cdr.markForCheck();
    };
    const p = sv ? Promise.resolve(sv.skychatVoiceDecline ? sv.skychatVoiceDecline(id) : null) : this.voiceApi("/decline", "POST", { call_id: id });
    p.then(done).catch(() => done());
  }
  // --- Password gate management ----------------------------------
  togglePassword() {
    this.pwOpen = !this.pwOpen;
    if (this.pwOpen) {
      this.refreshPasswordState();
    } else {
      this.resetPasswordForm();
    }
  }
  refreshPasswordState() {
    if (!this.node) {
      return;
    }
    this.api.get(`visors/${this.node.localPk}/skychat/password`).subscribe((resp) => {
      this.pwIsSet = !!(resp && resp.set);
      this.cdr.markForCheck();
    }, () => {
      this.cdr.markForCheck();
    });
  }
  resetPasswordForm() {
    this.pwOld = "";
    this.pwNew = "";
    this.pwConfirm = "";
  }
  validateNewPassword() {
    if (this.pwNew.length < 6 || this.pwNew.length > 64) {
      return "skychat.password.errors.length";
    }
    if (this.pwNew !== this.pwConfirm) {
      return "skychat.password.errors.mismatch";
    }
    return null;
  }
  applyPassword() {
    if (!this.node || this.pwBusy) {
      return;
    }
    const err = this.validateNewPassword();
    if (err) {
      this.snackbar.showError(err);
      return;
    }
    this.pwBusy = true;
    this.api.put(`visors/${this.node.localPk}/skychat/password`, {
      old_password: this.pwIsSet ? this.pwOld : "",
      new_password: this.pwNew
    }).subscribe(() => {
      this.pwBusy = false;
      this.pwIsSet = true;
      this.resetPasswordForm();
      this.snackbar.showDone("skychat.password.saved");
      this.cdr.markForCheck();
    }, (e) => {
      this.pwBusy = false;
      this.snackbar.showError(e?.originalError?.error?.error || e?.message || String(e));
      this.cdr.markForCheck();
    });
  }
  clearPassword() {
    if (!this.node || this.pwBusy || !this.pwIsSet) {
      return;
    }
    if (!this.pwOld) {
      this.snackbar.showError("skychat.password.errors.old-required");
      return;
    }
    this.pwBusy = true;
    this.api.delete(`visors/${this.node.localPk}/skychat/password?old_password=${encodeURIComponent(this.pwOld)}`).subscribe(() => {
      this.pwBusy = false;
      this.pwIsSet = false;
      this.resetPasswordForm();
      this.snackbar.showDone("skychat.password.cleared");
      this.cdr.markForCheck();
    }, (e) => {
      this.pwBusy = false;
      this.snackbar.showError(e?.originalError?.error?.error || e?.message || String(e));
      this.cdr.markForCheck();
    });
  }
  static {
    this.ctorParameters = () => [
      { type: ApiService },
      { type: LiveService },
      { type: SnackbarService },
      { type: ChangeDetectorRef },
      { type: Router },
      { type: StorageService }
    ];
  }
  static {
    this.propDecorators = {
      logEl: [{ type: ViewChild, args: ["logEl"] }],
      embeddedNodeKey: [{ type: Input }],
      embeddedPeer: [{ type: Input }],
      vizCanvas: [{ type: ViewChild, args: ["vizCanvas"] }],
      vizBig: [{ type: ViewChild, args: ["vizBig"] }],
      vizBigSent: [{ type: ViewChild, args: ["vizBigSent"] }]
    };
  }
};
SkychatComponent = __decorate7([
  Component({
    selector: "app-skychat",
    template: skychat_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [skychat_component_default2]
  })
], SkychatComponent);
function fft(re, im) {
  const n = re.length;
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1;
    for (; j & bit; bit >>= 1) {
      j ^= bit;
    }
    j ^= bit;
    if (i < j) {
      [re[i], re[j]] = [re[j], re[i]];
      [im[i], im[j]] = [im[j], im[i]];
    }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = -2 * Math.PI / len;
    const wRe = Math.cos(ang), wIm = Math.sin(ang);
    for (let i = 0; i < n; i += len) {
      let curRe = 1, curIm = 0;
      for (let k = 0; k < len / 2; k++) {
        const a = i + k, b = i + k + len / 2;
        const tRe = re[b] * curRe - im[b] * curIm;
        const tIm = re[b] * curIm + im[b] * curRe;
        re[b] = re[a] - tRe;
        im[b] = im[a] - tIm;
        re[a] += tRe;
        im[a] += tIm;
        const nRe = curRe * wRe - curIm * wIm;
        curIm = curRe * wIm + curIm * wRe;
        curRe = nRe;
      }
    }
  }
}
function heat(v) {
  v = Math.max(0, Math.min(1, v));
  const n = (x, a, c) => {
    const t = (x - a) / (c - a);
    return t < 0 ? 0 : t > 1 ? 1 : t;
  };
  let r = 0, g = 0, b;
  if (v < 1 / 5) {
    b = Math.round(255 * n(v, 0, 1 / 5));
  } else if (v < 2 / 5) {
    const c = Math.round(255 * n(v, 1 / 5, 2 / 5));
    r = 0;
    g = c;
    b = 255 - c;
  } else if (v < 3 / 5) {
    r = Math.round(255 * n(v, 2 / 5, 3 / 5));
    g = 255;
    b = 0;
  } else if (v < 4 / 5) {
    r = 255;
    g = Math.round(255 - 255 * n(v, 3 / 5, 4 / 5));
    b = 0;
  } else {
    const c = Math.round(255 * n(v, 4 / 5, 1));
    r = 255;
    g = c;
    b = c;
  }
  return `rgb(${r},${g},${b})`;
}

// angular:jit:template:src/app/components/pages/node/logs/logs.component.html
var logs_component_default = `@if (node) {
  <div class="rounded-elevated-box mt-3 logs-box">
    <div class="box-internal-container logs-shell">
      <div class="logs-controls">
        <div class="logs-filter-row">
          <span class="control-label dim">{{ 'logs.filter' | translate }}:</span>
          @for (l of levels; track l.level) {
            <button mat-button [class.active]="minLevel === l.level" (click)="setLevel(l.level)">
              {{ l.label }}
            </button>
          }
        </div>
        <div class="logs-filter-row">
          <span class="control-label dim">{{ 'logs.module' | translate }}:</span>
          <input matInput class="logs-text-input"
                 [value]="moduleFilter"
                 (input)="setModuleFilter($any($event.target).value)"
                 [placeholder]="'logs.module-placeholder' | translate"
                 spellcheck="false" autocomplete="off" />
          @if (moduleFilterError) {
            <span class="logs-filter-error">{{ moduleFilterError }}</span>
          }
          <span class="control-label dim ml-3">{{ 'logs.search' | translate }}:</span>
          <input matInput class="logs-text-input"
                 [value]="textFilter"
                 (input)="setTextFilter($any($event.target).value)"
                 [placeholder]="'logs.search-placeholder' | translate"
                 spellcheck="false" autocomplete="off" />
        </div>
        <div class="logs-action-row">
          <button mat-button class="logs-tail" [class.active]="liveTail" (click)="toggleLiveTail()">
            <mat-icon [inline]="true">{{ liveTail ? 'pause' : 'play_arrow' }}</mat-icon>
            {{ liveTail ? ('logs.pause' | translate) : ('logs.resume' | translate) }}
          </button>
          <a [href]="fullLogsUrl()" target="_blank" rel="noreferrer" class="logs-raw-link">
            <mat-icon [inline]="true">open_in_new</mat-icon>
            {{ 'logs.open-raw' | translate }}
          </a>
        </div>
      </div>

      @if (totalDropped > 0) {
        <div class="logs-dropped-banner">
          <mat-icon [inline]="true">history</mat-icon>
          {{ 'logs.dropped' | translate:{ count: totalDropped } }}
        </div>
      }

      @if (loading && logEntries.length === 0) {
        <div class="loading-row">
          <mat-spinner [diameter]="16"></mat-spinner>
          <span class="ml-2">{{ 'logs.loading' | translate }}</span>
        </div>
      }

      @if (filteredLogEntries.length === 0 && !loading) {
        <div class="logs-empty">{{ 'logs.empty' | translate }}</div>
      }

      <div #content class="logs-list">
        @if (hasMoreLogMessages) {
          <div class="log-entry">
            <a [href]="fullLogsUrl()" target="_blank" class="logs-raw-link">
              {{ 'logs.view-rest' | translate:{number: totalLogs} }}
            </a>
          </div>
        }
        @for (entry of filteredLogEntries; track trackEntry($index, entry)) {
          <div class="log-entry">
            <span class="le-time dim">{{ entry.time }}</span>
            <span [class]="levelClass(entry.level)" class="le-level">{{ levelName(entry.level) }}</span>
            @if (entry.func) { <span class="le-func dim">[{{ entry.func }}]</span> }
            <span class="le-msg">{{ entry.msg }}</span>
            @for (kv of entry.extra; track kv.name) {
              <span class="le-extra dim"> {{ kv.name }}={{ kv.value }}</span>
            }
          </div>
        }
      </div>
    </div>
  </div>
}
`;

// angular:jit:style:src/app/components/pages/node/logs/logs.component.scss
var logs_component_default2 = '/* src/app/components/pages/node/logs/logs.component.scss */\n.dim {\n  color: rgba(var(--sw-fg-rgb), 0.85);\n}\n.logs-box {\n  display: flex;\n  flex-direction: column;\n}\n.logs-shell {\n  display: flex;\n  flex-direction: column;\n  gap: 8px;\n}\n.logs-controls {\n  display: flex;\n  align-items: center;\n  gap: 12px;\n  flex-wrap: wrap;\n  padding-bottom: 4px;\n  border-bottom: 1px solid rgba(var(--sw-fg-rgb), 0.06);\n}\n.logs-filter-row,\n.logs-action-row {\n  display: flex;\n  align-items: center;\n  gap: 4px;\n  flex-wrap: wrap;\n}\n.logs-action-row {\n  margin-left: auto;\n  gap: 8px;\n}\n.logs-controls button {\n  min-width: 0;\n  padding: 0 10px !important;\n  opacity: max(0.65, var(--sw-dim));\n  color: rgba(var(--sw-fg-rgb), 0.85) !important;\n}\n.logs-controls button.active {\n  opacity: 1;\n  background: rgba(var(--sw-blue-rgb), 0.18) !important;\n  color: var(--sw-text) !important;\n}\n.control-label {\n  margin-right: 4px;\n  font-size: 0.85em;\n}\n.logs-raw-link {\n  color: rgba(var(--sw-fg-rgb), 0.85);\n  text-decoration: none;\n  display: inline-flex;\n  align-items: center;\n  gap: 4px;\n  font-size: 0.9em;\n}\n.logs-raw-link:hover {\n  text-decoration: underline;\n}\n.logs-raw-link .mat-icon {\n  font-size: 16px;\n}\n.logs-dropped-banner {\n  background: rgba(var(--sw-red-rgb), 0.12);\n  border: 1px solid rgba(var(--sw-red-rgb), 0.35);\n  color: rgba(var(--sw-blush-rgb), 0.95);\n  padding: 6px 10px;\n  border-radius: 4px;\n  font-size: 0.85em;\n  display: flex;\n  align-items: center;\n  gap: 6px;\n}\n.loading-row {\n  display: flex;\n  align-items: center;\n  padding: 16px 0;\n}\n.logs-empty {\n  padding: 24px 0;\n  text-align: center;\n  color: rgba(var(--sw-fg-rgb), var(--sw-a50));\n}\n.logs-list {\n  background: rgba(var(--sw-shade-rgb), 0.25);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.06);\n  border-radius: 6px;\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n  font-size: 0.78em;\n  height: 65vh;\n  min-height: 400px;\n  overflow-y: auto;\n  padding: 8px 12px;\n}\n.log-entry {\n  white-space: pre-wrap;\n  word-break: break-word;\n  padding: 1px 0;\n  line-height: 1.4;\n}\n.log-entry .le-time {\n  margin-right: 6px;\n}\n.log-entry .le-level {\n  display: inline-block;\n  min-width: 50px;\n  margin-right: 6px;\n  font-weight: 600;\n}\n.log-entry .le-func {\n  margin-right: 6px;\n}\n.log-entry .le-msg {\n  color: rgba(var(--sw-fg-rgb), 0.92);\n}\n.log-entry .le-extra {\n  font-size: 0.92em;\n}\n.lvl-fatal {\n  color: rgb(var(--sw-salmon-rgb));\n}\n.lvl-error {\n  color: rgb(var(--sw-red-alt-rgb));\n}\n.lvl-warn {\n  color: rgb(var(--sw-orange-rgb));\n}\n.lvl-info {\n  color: rgb(var(--sw-green-rgb));\n}\n.lvl-debug {\n  color: #03a9f4;\n}\n.lvl-trace {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a55));\n}\n.lvl-unknown {\n  color: rgba(var(--sw-fg-rgb), 0.7);\n}\n.logs-text-input {\n  display: inline-block;\n  min-width: 220px;\n  max-width: 320px;\n  background-color: rgba(var(--sw-fg-rgb), 0.08);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.18);\n  border-radius: 4px;\n  padding: 4px 8px;\n  margin-left: 6px;\n  color: rgba(var(--sw-fg-rgb), 0.92);\n  font-family: inherit;\n  font-size: 0.92em;\n}\n.logs-text-input:focus {\n  outline: none;\n  border-color: rgba(var(--sw-fg-rgb), 0.4);\n  background-color: rgba(var(--sw-fg-rgb), 0.12);\n}\n.logs-filter-error {\n  display: inline-block;\n  margin-left: 8px;\n  color: rgb(var(--sw-salmon-rgb));\n  font-size: 0.85em;\n}\n/*# sourceMappingURL=logs.component.css.map */\n';

// src/app/components/pages/node/logs/logs.component.ts
var __decorate8 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var Level2;
(function(Level3) {
  Level3[Level3["Panic"] = 8] = "Panic";
  Level3[Level3["Fatal"] = 7] = "Fatal";
  Level3[Level3["Error"] = 6] = "Error";
  Level3[Level3["Warn"] = 5] = "Warn";
  Level3[Level3["Info"] = 4] = "Info";
  Level3[Level3["Debug"] = 3] = "Debug";
  Level3[Level3["Trace"] = 2] = "Trace";
  Level3[Level3["Unknown"] = 1] = "Unknown";
})(Level2 || (Level2 = {}));
var LogsComponent = class LogsComponent2 extends PageBaseComponent {
  constructor(nodeService, snackbar, ngZone, cdr) {
    super();
    this.nodeService = nodeService;
    this.snackbar = snackbar;
    this.ngZone = ngZone;
    this.cdr = cdr;
    this.loading = true;
    this.liveTail = true;
    this.livePollMs = 2e3;
    this.totalDropped = 0;
    this.totalLogs = 0;
    this.hasMoreLogMessages = false;
    this.minLevel = Level2.Unknown;
    this.levels = [
      { level: Level2.Unknown, label: "All", cls: "" },
      { level: Level2.Debug, label: "Debug+", cls: "" },
      { level: Level2.Info, label: "Info+", cls: "" },
      { level: Level2.Warn, label: "Warn+", cls: "" },
      { level: Level2.Error, label: "Error+", cls: "" },
      { level: Level2.Fatal, label: "Fatal+", cls: "" },
      { level: Level2.Panic, label: "Panic", cls: "" }
    ];
    this.logEntries = [];
    this.filteredLogEntries = [];
    this.maxBufferEntries = 1e3;
    this.moduleFilter = "";
    this.moduleFilterError = null;
    this.moduleRe = null;
    this.textFilter = "";
    this.logCursor = 0;
    this.wasAtBottom = true;
    this.shouldShowError = true;
  }
  ngOnInit() {
    if (this.embeddedNodeKey) {
      this.node = { localPk: this.embeddedNodeKey };
      this.loadData(0);
    } else {
      this.nodeSub = NodeComponent.currentNode.subscribe((node) => {
        const wasUnset = !this.node;
        this.node = node;
        if (wasUnset && node) {
          this.loadData(0);
        }
        this.cdr.markForCheck();
      });
    }
    return super.ngOnInit();
  }
  ngOnDestroy() {
    this.nodeSub?.unsubscribe();
    this.subscription?.unsubscribe();
  }
  setLevel(l) {
    this.minLevel = l;
    this.applyFilter();
  }
  setModuleFilter(value) {
    this.moduleFilter = value || "";
    if (!this.moduleFilter) {
      this.moduleRe = null;
      this.moduleFilterError = null;
    } else {
      try {
        this.moduleRe = new RegExp(this.moduleFilter, "i");
        this.moduleFilterError = null;
      } catch (e) {
        this.moduleFilterError = e?.message || "invalid regex";
        return;
      }
    }
    this.applyFilter();
  }
  setTextFilter(value) {
    this.textFilter = value || "";
    this.applyFilter();
  }
  toggleLiveTail() {
    this.liveTail = !this.liveTail;
    if (this.liveTail) {
      this.loadData(0);
    } else {
      this.subscription?.unsubscribe();
    }
  }
  fullLogsUrl() {
    if (!this.node) {
      return "";
    }
    const apiPrefix = !environment.production && location.protocol.indexOf("http:") !== -1 ? "http-api" : "api";
    return window.location.origin + "/" + apiPrefix + "/visors/" + this.node.localPk + "/runtime-logs";
  }
  loadData(delayMs) {
    if (!this.node) {
      return;
    }
    this.subscription?.unsubscribe();
    this.captureScrollTailState();
    this.loading = this.logEntries.length === 0;
    const cursor = this.logCursor;
    this.subscription = of(1).pipe(delay(delayMs), mergeMap(() => this.nodeService.getRuntimeLogsSince(this.node.localPk, cursor))).subscribe((delta) => {
      this.onDelta(delta);
      this.cdr.markForCheck();
    }, (err) => {
      this.onError(err);
      this.cdr.markForCheck();
    });
  }
  onDelta(delta) {
    if (!delta) {
      this.loading = false;
      this.scheduleNext();
      return;
    }
    const isInitial = this.logCursor === 0;
    this.logCursor = typeof delta.latest === "number" ? delta.latest : this.logCursor;
    if (typeof delta.dropped === "number" && delta.dropped > 0) {
      this.totalDropped += delta.dropped;
    }
    const raws = Array.isArray(delta.entries) ? delta.entries : [];
    const parsed = [];
    for (const r of raws) {
      try {
        parsed.push(JSON.parse(r));
      } catch (e) {
      }
    }
    if (isInitial) {
      this.logEntries = [];
    }
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
  // Some visor log lines arrive with the message field already carrying a
  // fully-rendered logrus prefix ("[<RFC3339>] LEVEL [module]: <msg>"). Since
  // the UI renders its own time + level chips, that prefix would double up the
  // timestamp and level on every such line. Strip only the leading
  // "[<timestamp>] LEVEL " so the module (kept as "[module]: ...") and message
  // survive; lines without the prefix are left untouched.
  stripDupPrefix(msg) {
    if (!msg) {
      return msg;
    }
    return msg.replace(/^\[\d{4}-\d{2}-\d{2}T[0-9:.,+\-Z]*\]\s+(PANIC|FATAL|ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)\s+/i, "");
  }
  appendParsed(rows) {
    for (const e of rows) {
      const entry = {
        time: e.time,
        level: this.parseLevel(e.level),
        msg: this.stripDupPrefix(e.msg),
        func: e.func,
        module: e._module,
        extra: []
      };
      if (e.error) {
        entry.extra.push({ name: "error", value: e.error });
      }
      const known = /* @__PURE__ */ new Set(["time", "_module", "msg", "func", "level", "log_line", "error"]);
      for (const k in e) {
        if (!known.has(k)) {
          entry.extra.push({ name: k, value: e[k] });
        }
      }
      this.logEntries.push(entry);
    }
  }
  parseLevel(s) {
    const lc = (s || "").toLowerCase();
    if (lc.includes("panic")) {
      return Level2.Panic;
    }
    if (lc.includes("fatal")) {
      return Level2.Fatal;
    }
    if (lc.includes("error")) {
      return Level2.Error;
    }
    if (lc.includes("warn")) {
      return Level2.Warn;
    }
    if (lc.includes("info")) {
      return Level2.Info;
    }
    if (lc.includes("debug")) {
      return Level2.Debug;
    }
    if (lc.includes("trace")) {
      return Level2.Trace;
    }
    return Level2.Unknown;
  }
  applyFilter() {
    const moduleRe = this.moduleRe;
    const textLC = this.textFilter.toLowerCase();
    this.filteredLogEntries = this.logEntries.filter((e) => {
      if (e.level < this.minLevel) {
        return false;
      }
      if (moduleRe && !moduleRe.test(e.module || "")) {
        return false;
      }
      if (textLC) {
        if (!(e.msg || "").toLowerCase().includes(textLC) && !(e.module || "").toLowerCase().includes(textLC) && !e.extra.some((kv) => (kv.value || "").toLowerCase().includes(textLC) || (kv.name || "").toLowerCase().includes(textLC))) {
          return false;
        }
      }
      return true;
    });
    if (this.wasAtBottom) {
      setTimeout(() => {
        if (this.content) {
          const el = this.content.nativeElement;
          el.scrollTop = el.scrollHeight;
        }
      });
    }
  }
  captureScrollTailState() {
    if (!this.content) {
      this.wasAtBottom = true;
      return;
    }
    const el = this.content.nativeElement;
    this.wasAtBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }
  scheduleNext() {
    if (this.liveTail) {
      this.ngZone.run(() => this.loadData(this.livePollMs));
    }
  }
  onError(err) {
    err = processServiceError(err);
    if (this.shouldShowError) {
      this.snackbar.showError("common.loading-error", null, true, err);
      this.shouldShowError = false;
    }
    this.loadData(AppConfig.connectionRetryDelay);
  }
  levelClass(l) {
    switch (l) {
      case Level2.Panic:
      case Level2.Fatal:
        return "lvl-fatal";
      case Level2.Error:
        return "lvl-error";
      case Level2.Warn:
        return "lvl-warn";
      case Level2.Info:
        return "lvl-info";
      case Level2.Debug:
        return "lvl-debug";
      case Level2.Trace:
        return "lvl-trace";
      default:
        return "lvl-unknown";
    }
  }
  levelName(l) {
    switch (l) {
      case Level2.Panic:
        return "PANIC";
      case Level2.Fatal:
        return "FATAL";
      case Level2.Error:
        return "ERROR";
      case Level2.Warn:
        return "WARN";
      case Level2.Info:
        return "INFO";
      case Level2.Debug:
        return "DEBUG";
      case Level2.Trace:
        return "TRACE";
      default:
        return "LOG";
    }
  }
  trackEntry(_, e) {
    return e;
  }
  static {
    this.ctorParameters = () => [
      { type: NodeService },
      { type: SnackbarService },
      { type: NgZone },
      { type: ChangeDetectorRef }
    ];
  }
  static {
    this.propDecorators = {
      content: [{ type: ViewChild, args: ["content"] }],
      embeddedNodeKey: [{ type: Input }]
    };
  }
};
LogsComponent = __decorate8([
  Component({
    selector: "app-logs",
    template: logs_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [logs_component_default2]
  })
], LogsComponent);

// src/app/shared/ng-bridge.service.ts
var __decorate9 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var NgBridgeService = class NgBridgeService2 {
  constructor(appRef, injector) {
    this.appRef = appRef;
    this.injector = injector;
    this.registry = {
      skychat: SkychatComponent,
      logs: LogsComponent
    };
  }
  /** Publish the bridge on `window.SkywireNg` (called once at app startup). */
  install() {
    window.SkywireNg = {
      mountComponent: (el, name, opts = {}) => this.mount(el, name, opts)
    };
  }
  mount(el, name, opts) {
    const component = this.registry[name];
    if (!component) {
      console.error("SkywireNg.mountComponent: unknown component", name);
      return null;
    }
    const outlet = new DomPortalOutlet(el, this.appRef, this.injector);
    const ref = outlet.attach(new ComponentPortal(component));
    if (opts.nodeKey) {
      ref.setInput("embeddedNodeKey", opts.nodeKey);
    }
    if (opts.peer) {
      ref.setInput("embeddedPeer", opts.peer);
    }
    ref.changeDetectorRef.detectChanges();
    return {
      dispose: () => {
        try {
          outlet.dispose();
        } catch (e) {
        }
      }
    };
  }
  static {
    this.ctorParameters = () => [
      { type: ApplicationRef },
      { type: Injector }
    ];
  }
};
NgBridgeService = __decorate9([
  Injectable({ providedIn: "root" })
], NgBridgeService);

// src/app/app.component.ts
var __decorate10 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AppComponent_1;
var AppComponent = class AppComponent2 {
  static {
    AppComponent_1 = this;
  }
  constructor(storage, router, dialog, snackbarService, languageService, apiService, ngBridge, changeDetectorRef, _themeService) {
    this.storage = storage;
    this.snackbarService = snackbarService;
    this.languageService = languageService;
    this.apiService = apiService;
    this.changeDetectorRef = changeDetectorRef;
    this.inVpnClient = false;
    this.inLoginPage = false;
    this.hypervisorPkObtained = false;
    this.pkErrorShown = false;
    this.pkErrorsFound = 0;
    this.showingDataProblemMsg = false;
    AppComponent_1.currentInstance = this;
    ngBridge.install();
    dialog.afterOpened.subscribe(() => {
      snackbarService.closeCurrent();
      this.changeDetectorRef.markForCheck();
    });
    if (history.scrollRestoration) {
      history.scrollRestoration = "manual";
    }
    router.events.subscribe((e) => {
      if (e instanceof NavigationEnd) {
        snackbarService.closeCurrent();
        dialog.closeAll();
      }
      this.changeDetectorRef.markForCheck();
    });
    dialog.afterAllClosed.subscribe(() => {
      snackbarService.closeCurrentIfTemporaryError();
      this.changeDetectorRef.markForCheck();
    });
    router.events.subscribe((e) => {
      this.inVpnClient = router.url.includes("/vpn/") || router.url.includes("vpnlogin");
      if (e.url) {
        const previousInLoginPageValue = this.inLoginPage;
        this.inLoginPage = e.url.includes("login");
        if (previousInLoginPageValue && !this.inLoginPage && !this.hypervisorPkObtained) {
          this.checkHypervisorPk(0);
        }
      }
      if (router.url.length > 2) {
        if (this.inVpnClient) {
          document.title = "Skywire VPN";
        } else {
          document.title = "Skywire Hypervisor";
        }
      }
      this.changeDetectorRef.markForCheck();
    });
    this.languageService.loadLanguageSettings();
    this.checkHypervisorPk(0);
  }
  /**
   * This should be called a frame before leaving the login page, to avoid race conditions in which the
   * automatic event code in the constructor changes the value of inLoginPage to false but Angular still
   * loads the content of the new page just before taking that value into account.
   */
  processLoginDone() {
    this.inLoginPage = false;
    if (!this.hypervisorPkObtained) {
      this.checkHypervisorPk(0);
    }
  }
  /**
   * Shows a box at the bottom-right corner indicating that there is a problem getting the data.
   */
  showDataProblemMsg() {
    this.showingDataProblemMsg = true;
  }
  /**
   * Hides the box at the bottom-right corner indicating that there is a problem getting the data.
   */
  hideDataProblemMsg() {
    this.showingDataProblemMsg = false;
  }
  /**
   * Gets the pk of the hypervisor. After that, it initializes services and allows the app to start working.
   */
  checkHypervisorPk(delayMs) {
    if (this.obtainPkSubscription) {
      this.obtainPkSubscription.unsubscribe();
    }
    this.obtainPkSubscription = of(1).pipe(delay(delayMs), mergeMap(() => this.apiService.get("about"))).subscribe((result) => {
      if (result.public_key) {
        this.finishStartup(result.public_key);
        this.hypervisorPkObtained = true;
      } else {
        if (!this.pkErrorShown) {
          this.snackbarService.showError("start.loading-error", null, true);
          this.pkErrorShown = true;
        }
        this.checkHypervisorPk(1e3);
      }
      this.changeDetectorRef.markForCheck();
    }, (err) => {
      this.pkErrorsFound += 1;
      if (this.pkErrorsFound > 4 && !this.pkErrorShown) {
        const e = processServiceError(err);
        this.snackbarService.showError("start.loading-error", null, true, e);
        this.pkErrorShown = true;
      }
      if (!this.inLoginPage && this.pkErrorsFound < 30) {
        this.checkHypervisorPk(Math.min(this.pkErrorsFound * 1e3, 1e4));
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  finishStartup(hypervisorPk) {
    this.storage.initialize(hypervisorPk);
  }
  static {
    this.ctorParameters = () => [
      { type: StorageService },
      { type: Router },
      { type: MatDialog },
      { type: SnackbarService },
      { type: LanguageService },
      { type: ApiService },
      { type: NgBridgeService },
      { type: ChangeDetectorRef },
      { type: ThemeService }
    ];
  }
};
AppComponent = AppComponent_1 = __decorate10([
  Component({
    selector: "app-root",
    template: app_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [app_component_default2]
  })
], AppComponent);

// src/app/components/pages/node/node.component.ts
var __decorate11 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var NodeComponent_1;
var NodeComponent = class NodeComponent2 extends PageBaseComponent {
  static {
    NodeComponent_1 = this;
  }
  // Throttle timestamp for the visor-switcher summaries call (see refreshNavRows).
  /**
   * Ask the currently displayed instance of this page to reload the node data.
   */
  static refreshCurrentDisplayedData() {
    if (NodeComponent_1.currentInstanceInternal) {
      NodeComponent_1.currentInstanceInternal.forceDataRefresh(false);
    }
  }
  /**
   * Gets the publick key of the node of the currently displayed instance of this page.
   */
  static getCurrentNodeKey() {
    return NodeComponent_1.currentNodeKey;
  }
  /**
   * Gets the lastest node data downloaded by the currently active instance of this page.
   */
  static get currentNode() {
    return NodeComponent_1.nodeSubject.asObservable();
  }
  /**
   * Gets the lastest node traffic data downloaded by the currently active instance of this page.
   */
  static get currentTrafficData() {
    return NodeComponent_1.trafficDataSubject.asObservable();
  }
  constructor(storageService, singleNodeDataService, nodeService, route, ngZone, snackbarService, injector, cdr, sanitizer, router, live) {
    super();
    this.storageService = storageService;
    this.singleNodeDataService = singleNodeDataService;
    this.nodeService = nodeService;
    this.route = route;
    this.ngZone = ngZone;
    this.snackbarService = snackbarService;
    this.injector = injector;
    this.cdr = cdr;
    this.sanitizer = sanitizer;
    this.router = router;
    this.live = live;
    this.persistentDataResponseKey = "serv-dat-response";
    this.nodeLoaded = false;
    this.notFound = false;
    this.titleParts = [];
    this.tabsData = [];
    this.homeNavTabs = [];
    this.switcherTabs = [];
    this.selectedTabIndex = -1;
    this.headerLabel = "";
    this.headerIdentifier = "";
    this.embedMode = false;
    this.showingFullList = false;
    this.initialRouteEventFired = false;
    this.requestedNodeKey = "";
    this.secondsSinceLastUpdate = 0;
    this.lastUpdate = Date.now();
    this.updating = false;
    this.errorsUpdating = false;
    this.lastUpdateRequestedManually = false;
    this.loadFailed = false;
    this.consecutiveLoadErrors = 0;
    this.instanceId = "";
    this.terminalIframeUrl = null;
    this.terminalFullWindowUrl = "";
    this.selectedSeg = "";
    this.vpnIframeUrl = null;
    this.vpnFullWindowUrl = "";
    this.boundVpnPk = "";
    this.boundTerminalPk = "";
    NodeComponent_1.nodeSubject = new ReplaySubject(1);
    NodeComponent_1.trafficDataSubject = new ReplaySubject(1);
    NodeComponent_1.currentInstanceInternal = this;
    this.instanceId = Math.random().toString(36).substring(7);
    this.navigationsSubscription = router.events.subscribe((event) => {
      if (event["urlAfterRedirects"]) {
        this.lastUrl = event["urlAfterRedirects"];
        this.processRouteUpdate();
        this.initialRouteEventFired = true;
      }
      this.cdr.markForCheck();
    });
  }
  ngOnInit() {
    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription = timer(5e3, 5e3).subscribe(() => {
        this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1e3);
        });
        this.cdr.markForCheck();
      });
    });
    this.initSubscription = of(0).pipe(delay(500)).subscribe(() => {
      if (!this.initialRouteEventFired) {
        this.lastUrl = window.location.href;
        this.processRouteUpdate();
      }
      this.cdr.markForCheck();
    });
    return super.ngOnInit();
  }
  processRouteUpdate() {
    this.embedMode = (this.lastUrl || "").includes("embed=1") && window.self !== window.top;
    NodeComponent_1.currentNodeKey = this.route.snapshot.params["key"];
    if (this.nodeActionsHelper) {
      this.nodeActionsHelper.setCurrentNodeKey(NodeComponent_1.currentNodeKey);
    }
    this.updateTabBar();
    this.maybeBuildTerminalUrl();
    this.startGettingData(true);
  }
  /**
   * Populate the header label + identifier from the loaded node so
   * the top bar always shows the visor's identity (replaces the
   * old translated "Visor details" string). Falls back to a short
   * PK if the user hasn't set a label.
   */
  refreshHeader() {
    if (!this.node) {
      this.headerLabel = "";
      this.headerIdentifier = "";
      return;
    }
    const labelInfo = this.storageService.getLabelInfo(this.node.localPk);
    this.headerLabel = labelInfo && labelInfo.label ? labelInfo.label : this.node.label || (this.node.localPk ? this.node.localPk.slice(0, 8) + "\u2026" : "");
    this.headerIdentifier = this.node.localPk || "";
  }
  updateTabBar() {
    if (this.lastUrl && (this.lastUrl.includes("/info") || this.lastUrl.includes("/routing") || this.lastUrl.includes("/transports") || this.lastUrl.includes("/bandwidth") || this.lastUrl.includes("/dmsg") || this.lastUrl.includes("/reachability") || this.lastUrl.includes("/uptime") || this.lastUrl.includes("/rewards") || this.lastUrl.includes("/skynet") || this.lastUrl.includes("/chat") || this.lastUrl.includes("/mail") || this.lastUrl.includes("/contacts") || this.lastUrl.includes("/web-proxy") || this.lastUrl.includes("/vpn") || this.lastUrl.includes("/resources") || this.lastUrl.includes("/terminal") || this.lastUrl.includes("/wallet") || this.lastUrl.includes("/logs") || this.lastUrl.includes("/settings") || this.lastUrl.includes("/apps") && !this.lastUrl.includes("/apps-list"))) {
      this.titleParts = ["nodes.title", "node.title"];
      this.refreshNavRows();
      this.tabsData = nodeTabsData(NodeComponent_1.currentNodeKey, !!this.node && this.node.arch === "wasm", isRemoteVisor(NodeComponent_1.currentNodeKey, this.storageService.getLocalHypervisorPk()));
      let matchedSeg = "info";
      const routeOrder = [
        "routing",
        "transports",
        "bandwidth",
        "uptime",
        "apps",
        "chat",
        "mail",
        "contacts",
        "rewards",
        "skynet",
        "web-proxy",
        "vpn",
        "resources",
        "terminal",
        "wallet",
        "logs"
      ];
      for (const seg of routeOrder) {
        if (seg === "apps") {
          if (this.lastUrl.includes("/apps") && !this.lastUrl.includes("/apps-list")) {
            matchedSeg = "apps";
          }
        } else if (this.lastUrl.includes("/" + seg)) {
          matchedSeg = seg;
        }
      }
      const selIdx = this.tabsData.findIndex((t) => t.linkParts && t.linkParts[t.linkParts.length - 1] === matchedSeg);
      this.selectedTabIndex = selIdx >= 0 ? selIdx : 0;
      this.selectedSeg = matchedSeg;
      this.maybeBuildTerminalUrl();
      this.maybeBuildVpnUrl();
      this.showingFullList = false;
      this.nodeActionsHelper = new NodeActionsHelper(this.injector, this.showingFullList);
      this.nodeActionsHelper.setCurrentNodeKey(NodeComponent_1.currentNodeKey);
      if (this.node) {
        this.nodeActionsHelper.setCurrentNode(this.node);
      }
    } else if (this.lastUrl && this.lastUrl.includes("/apps-list")) {
      this.showingFullList = true;
      this.nodeActionsHelper = new NodeActionsHelper(this.injector, this.showingFullList);
      this.nodeActionsHelper.setCurrentNodeKey(NodeComponent_1.currentNodeKey);
      if (this.node) {
        this.nodeActionsHelper.setCurrentNode(this.node);
      }
      const prefix = "apps.apps-list";
      this.titleParts = ["nodes.title", "node.title", prefix + ".title"];
      this.tabsData = [
        {
          icon: "view_headline",
          label: prefix + ".list-title",
          linkParts: []
        }
      ];
      this.selectedTabIndex = 0;
    } else {
      this.titleParts = [];
      this.tabsData = [];
    }
  }
  // refreshNavRows builds the two persistent navigation rows shown above the
  // per-node tabs: the home nav (top-level hypervisor tabs, incl. the "Local
  // Visor" tab, kept alongside "Visor list") and the visor switcher (one chip
  // per visor in the list, so the user can jump between visors without backing
  // out). The switcher list follows the summaries over the live socket, every
  // 10 s, started once per page.
  refreshNavRows() {
    this.homeNavTabs = homeTabsData();
    if (this.switcherSubscription) {
      return;
    }
    this.switcherSubscription = this.live.watch("visors-summary", 1e4, () => this.nodeService.getNodes(), (b) => this.nodeService.nodesFromSummaries(b)).pipe(switchMap((res) => res.pipe(catchError(() => EMPTY)))).subscribe((nodes) => {
      this.ngZone.run(() => {
        this.switcherTabs = visorSwitcherChips(nodes);
        this.cdr.markForCheck();
      });
    }, () => {
      this.cdr.markForCheck();
    });
  }
  get isTerminalTab() {
    return this.selectedSeg === "terminal";
  }
  get isVpnTab() {
    return this.selectedSeg === "vpn";
  }
  /**
   * Build the VPN iframe URL the first time the user lands on the VPN tab for
   * this visor. Kept mounted (hidden off-tab) like the terminal iframe so the
   * VPN UI's session survives tab switches. Points at the full VPN UI route.
   */
  maybeBuildVpnUrl() {
    if (!this.isVpnTab) {
      return;
    }
    if (!this.node || !this.node.localPk) {
      return;
    }
    if (this.boundVpnPk === this.node.localPk) {
      return;
    }
    const url = "/#/vpn/" + this.node.localPk + "/status";
    this.vpnIframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(url);
    this.vpnFullWindowUrl = window.location.origin + url;
    this.boundVpnPk = this.node.localPk;
  }
  openVpnFullWindow() {
    if (this.node && this.node.localPk) {
      this.router.navigate(["/app", "vpn", this.node.localPk]);
    }
  }
  /**
   * Build the iframe URL the first time the user lands on the
   * Terminal tab for this visor. Once built, the iframe element is
   * kept mounted (just hidden when off-tab) so the websocket and
   * shell session live for as long as NodeComponent does.
   */
  maybeBuildTerminalUrl() {
    if (!this.isTerminalTab) {
      return;
    }
    if (!this.node || !this.node.localPk) {
      return;
    }
    if (this.boundTerminalPk === this.node.localPk) {
      return;
    }
    const url = ptyUrl(this.node.localPk);
    this.terminalIframeUrl = this.sanitizer.bypassSecurityTrustResourceUrl(url);
    this.terminalFullWindowUrl = url;
    this.boundTerminalPk = this.node.localPk;
  }
  openTerminalFullWindow() {
    if (!this.terminalFullWindowUrl) {
      return;
    }
    window.open(this.terminalFullWindowUrl, "_blank", "noopener noreferrer");
  }
  /**
   * Called when an option form the top bar is selected.
   * @param actionName Name of the selected option.
   */
  performAction(actionName) {
    this.nodeActionsHelper.performAction(actionName, NodeComponent_1.currentNodeKey);
  }
  /**
   * Makes the node info to be immediately refreshed.
   * @param requestedManually True if the data is going to be loaded because of a direct request
   * from the user.
   */
  forceDataRefresh(requestedManually = false) {
    if (requestedManually) {
      this.lastUpdateRequestedManually = true;
    }
    this.singleNodeDataService.forceSpecificNodeRefresh(NodeComponent_1.currentNodeKey);
  }
  /**
   * Retries loading after a load failure.
   */
  retryLoading() {
    this.loadFailed = false;
    this.consecutiveLoadErrors = 0;
    this.startGettingData(false);
  }
  /**
   * Starts getting the data from the backend.
   */
  startGettingData(checkSavedData) {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }
    const key = NodeComponent_1.currentNodeKey;
    if (this.requestedNodeKey && this.requestedNodeKey !== key) {
      this.singleNodeDataService.stopRequestingSpecificNode(this.requestedNodeKey);
    }
    this.requestedNodeKey = key;
    const savedValue = checkSavedData ? this.getLocalValue(this.persistentDataResponseKey) : null;
    const savedData = savedValue ? JSON.parse(savedValue.value) : null;
    let nextOperation = this.singleNodeDataService.startRequestingData(key);
    if (savedData && savedData.data && savedData.data.localPk === key) {
      nextOperation = concat(of(savedData), nextOperation);
    }
    this.dataSubscription = nextOperation.subscribe((result) => {
      if (result !== savedData) {
        this.saveLocalValue(this.persistentDataResponseKey, JSON.stringify(result));
      }
      this.updating = result ? result.updating : true;
      if (result && !result.updating) {
        if (result.data && !result.error) {
          this.ngZone.run(() => {
            this.node = result.data;
            this.trafficData = result.trafficData;
            this.nodeLoaded = true;
            this.refreshHeader();
            this.maybeBuildTerminalUrl();
            this.updateTabBar();
          });
          if (NodeComponent_1.nodeSubject) {
            NodeComponent_1.nodeSubject.next(this.node);
          }
          if (NodeComponent_1.trafficDataSubject) {
            NodeComponent_1.trafficDataSubject.next(this.trafficData);
          }
          if (this.nodeActionsHelper) {
            this.nodeActionsHelper.setCurrentNode(this.node);
          }
          this.snackbarService.closeCurrentIfTemporaryError();
          this.lastUpdate = result.momentOfLastCorrectUpdate;
          this.secondsSinceLastUpdate = Math.floor((Date.now() - result.momentOfLastCorrectUpdate) / 1e3);
          this.errorsUpdating = false;
          this.consecutiveLoadErrors = 0;
          this.loadFailed = false;
          AppComponent.currentInstance.hideDataProblemMsg();
          this.cdr.detectChanges();
          if (this.lastUpdateRequestedManually) {
            this.snackbarService.showDone("common.refreshed", null);
            this.lastUpdateRequestedManually = false;
          }
        } else if (result.error) {
          if (result.error.originalError && result.error.originalError.status === 400) {
            this.notFound = true;
            return;
          }
          this.consecutiveLoadErrors++;
          if (!this.node && this.consecutiveLoadErrors >= 3) {
            this.loadFailed = true;
            this.singleNodeDataService.stopRequestingSpecificNode(NodeComponent_1.currentNodeKey);
            this.snackbarService.showError("common.loading-error", null, true, result.error);
            AppComponent.currentInstance.showDataProblemMsg();
            return;
          }
          if (!this.errorsUpdating) {
            if (!this.node) {
              this.snackbarService.showError("common.loading-error", null, true, result.error);
            } else {
              this.snackbarService.showError("node.error-load", null, true, result.error);
            }
          }
          this.errorsUpdating = true;
          AppComponent.currentInstance.showDataProblemMsg();
        }
      }
    });
  }
  ngOnDestroy() {
    this.singleNodeDataService.stopRequestingSpecificNode(NodeComponent_1.currentNodeKey);
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.initSubscription.unsubscribe();
    if (this.switcherSubscription) {
      this.switcherSubscription.unsubscribe();
    }
    NodeComponent_1.currentInstanceInternal = void 0;
    NodeComponent_1.currentNodeKey = void 0;
    NodeComponent_1.nodeSubject.complete();
    NodeComponent_1.nodeSubject = void 0;
    NodeComponent_1.trafficDataSubject.complete();
    NodeComponent_1.trafficDataSubject = void 0;
    this.nodeActionsHelper.dispose();
  }
  static {
    this.ctorParameters = () => [
      { type: StorageService },
      { type: SingleNodeDataService },
      { type: NodeService },
      { type: ActivatedRoute },
      { type: NgZone },
      { type: SnackbarService },
      { type: Injector },
      { type: ChangeDetectorRef },
      { type: DomSanitizer },
      { type: Router },
      { type: LiveService }
    ];
  }
};
NodeComponent = NodeComponent_1 = __decorate11([
  Component({
    selector: "app-node",
    template: node_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [node_component_default2]
  })
], NodeComponent);

export {
  visorSwitcherChips,
  SelectOptionComponent,
  PageBaseComponent,
  NodeComponent,
  AppComponent
};
//# debugId=813dacde-6b3e-5da4-8551-1554997a2cc5
//# sourceMappingURL=chunk-CIWHQAPM.js.map
