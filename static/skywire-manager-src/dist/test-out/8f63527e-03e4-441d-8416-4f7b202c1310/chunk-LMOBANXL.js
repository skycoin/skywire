import {
  buildFlagArgs,
  flagError,
  isServerFlag,
  parseFlagArgs,
  serverTypeFor
} from "./chunk-Z3QUGWOD.js";
import {
  countriesList
} from "./chunk-ED5WCGEB.js";
import {
  NodeComponent
} from "./chunk-CIWHQAPM.js";
import {
  GeneralUtils
} from "./chunk-KWVFHEEW.js";
import {
  UntypedFormBuilder,
  UntypedFormControl
} from "./chunk-B4GMTCLR.js";
import {
  AppsService
} from "./chunk-MX3HPERK.js";
import {
  MAT_DIALOG_DATA,
  MatDialog,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  TranslateService
} from "./chunk-6WNZTOXO.js";
import {
  ProxyDiscoveryEntry
} from "./chunk-XBRWDJ2O.js";
import {
  HttpClient,
  SnackbarService
} from "./chunk-ZMZW6HXB.js";
import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  EventEmitter,
  Inject,
  Injectable,
  Input,
  Output,
  delay,
  map,
  retryWhen,
  take
} from "./chunk-ZNLGPAUZ.js";

// angular:jit:template:src/app/components/pages/node/apps/app-settings/app-settings.component.html
var app_settings_component_default = `<div class="inline-settings">
  <div class="inline-title">
    <span class="dim small">{{ 'app-settings.title' | translate }}:</span>
    <span class="mono name">{{ app?.name }}</span>
  </div>

  @if (isErrored) {
    <div class="error-box">
      <mat-icon [inline]="true">error_outline</mat-icon>
      <span>{{ 'app-settings.last-error' | translate }}: {{ lastError }}</span>
    </div>
  }

  <form [formGroup]="form" (ngSubmit)="save()" class="inline-form">

    <div class="field">
      <label for="args-{{ app?.name }}">{{ 'app-settings.args' | translate }}</label>
      <input id="args-{{ app?.name }}" type="text" formControlName="args"
             placeholder="--addr :1080 --srv 0123abc...">
      <button type="button" class="show-flags-toggle" (click)="toggleHelp()">
        <mat-icon [inline]="true">{{ showHelp ? 'expand_less' : 'expand_more' }}</mat-icon>
        {{ 'app-settings.show-help' | translate }}
      </button>
      @if (showHelp) {
        @if (helpLoading) {
          <div class="help-status dim small">{{ 'app-settings.help-loading' | translate }}</div>
        }
        @if (helpError) {
          <div class="help-status help-error">{{ helpError }}</div>
        }
        @if (helpText) {
          <pre class="help-text">{{ helpText }}</pre>
        }
      }
      <button type="button" class="show-flags-toggle flags-form-toggle" (click)="toggleFlagsForm()">
        <mat-icon [inline]="true">{{ showFlagsForm ? 'expand_less' : 'expand_more' }}</mat-icon>
        {{ 'app-settings.show-flags-form' | translate }}
      </button>
      @if (showFlagsForm) {
        <app-app-flags-form [appName]="app.name" [args]="form.get('args')!.value"
                            [serverType]="serverType" [pickServer]="pickServer"
                            (argsChange)="onFlagArgs($event)" (validChange)="flagsValid = $event"
                            (serverChosen)="onServerChosen()">
        </app-app-flags-form>
      }
    </div>

    <div class="field">
      <label>{{ 'app-settings.env' | translate }}</label>
      <div class="env-list" formArrayName="env">
        @for (ctrl of envArray.controls; track $index) {
          <div class="env-row">
            <input type="text" [formControlName]="$index" placeholder="KEY=value">
            <button type="button" class="env-remove" (click)="removeEnvRow($index)" title="{{ 'common.remove' | translate }}">
              <mat-icon [inline]="true">close</mat-icon>
            </button>
          </div>
        }
        <button type="button" class="env-add" (click)="addEnvRow()">
          <mat-icon [inline]="true">add</mat-icon>
          {{ 'app-settings.env-add' | translate }}
        </button>
      </div>
    </div>

    <div class="field-row mode-row">
      <div class="field">
        <label>{{ 'app-settings.launcher-mode' | translate }}</label>
        <div class="mode-radios">
          <label class="radio-opt">
            <input type="radio" formControlName="launcher_mode" value=""> {{ 'app-settings.mode-default' | translate }}
          </label>
          @if (internalModeAvailable) {
            <label class="radio-opt">
              <input type="radio" formControlName="launcher_mode" value="internal"> {{ 'app-settings.mode-internal' | translate }}
            </label>
          }
          <label class="radio-opt">
            <input type="radio" formControlName="launcher_mode" value="external"> {{ 'app-settings.mode-external' | translate }}
          </label>
        </div>
        @if (!internalModeAvailable) {
          <div class="hint dim small">{{ 'app-settings.internal-unavailable' | translate }}</div>
        }
      </div>
      <div class="field auto-field">
        <label class="checkbox-opt">
          <input type="checkbox" formControlName="autostart">
          <span>{{ 'app-settings.autostart' | translate }}</span>
        </label>
      </div>
    </div>

    @if (!flagsValid) {
      <div class="error-box">
        <mat-icon [inline]="true">error_outline</mat-icon>
        <span>{{ 'app-settings.flags-invalid' | translate }}</span>
      </div>
    }

    @if (saveError) {
      <div class="error-box">
        <mat-icon [inline]="true">error_outline</mat-icon>
        <span>{{ saveError }}</span>
      </div>
    }

    <div class="inline-actions">
      <button type="button" mat-button (click)="cancel()" [disabled]="saving">
        {{ 'common.cancel' | translate }}
      </button>
      <button type="submit" mat-raised-button color="primary" [disabled]="saving || !flagsValid">
        <mat-icon [inline]="true">save</mat-icon>
        {{ saving ? ('common.loading' | translate) : ('common.save' | translate) }}
      </button>
    </div>

  </form>
</div>
`;

// angular:jit:style:src/app/components/pages/node/apps/app-settings/app-settings.component.scss
var app_settings_component_default2 = '/* src/app/components/pages/node/apps/app-settings/app-settings.component.scss */\n.inline-settings {\n  margin-top: 8px;\n  padding: 12px 14px;\n  background: rgba(var(--sw-shade-rgb), 0.18);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n  border-left: 2px solid rgba(var(--sw-cyan-rgb), 0.4);\n  border-radius: 4px;\n}\n.inline-title {\n  display: flex;\n  align-items: baseline;\n  gap: 6px;\n  margin-bottom: 10px;\n}\n.inline-title .name {\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n  color: rgb(var(--sw-cyan-rgb));\n  font-weight: 600;\n}\n.inline-form {\n  display: flex;\n  flex-direction: column;\n  gap: 12px;\n}\n.field {\n  display: flex;\n  flex-direction: column;\n  gap: 4px;\n}\n.field > label {\n  font-size: 0.85em;\n  color: rgba(var(--sw-fg-rgb), 0.78);\n}\n.field input[type=text] {\n  background: rgba(var(--sw-shade-rgb), 0.3);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.15);\n  border-radius: 4px;\n  padding: 7px 9px;\n  color: rgba(var(--sw-fg-rgb), 0.95);\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n  font-size: 0.88em;\n  outline: none;\n}\n.field input[type=text]:focus {\n  border-color: rgba(var(--sw-cyan-rgb), 0.6);\n}\n.field-row {\n  display: flex;\n  gap: 16px;\n  flex-wrap: wrap;\n  align-items: flex-start;\n}\n.field-row .field {\n  flex: 1;\n  min-width: 180px;\n}\n.mode-row .auto-field {\n  flex: 0 0 auto;\n  align-self: center;\n  padding-top: 18px;\n}\n.mode-radios {\n  display: flex;\n  gap: 14px;\n  flex-wrap: wrap;\n}\n.radio-opt {\n  display: inline-flex;\n  align-items: center;\n  gap: 5px;\n  font-size: 0.88em;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n  cursor: pointer;\n}\n.radio-opt input[type=radio] {\n  accent-color: rgb(var(--sw-cyan-rgb));\n}\n.checkbox-opt {\n  display: inline-flex;\n  align-items: center;\n  gap: 6px;\n  font-size: 0.88em;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n  cursor: pointer;\n}\n.checkbox-opt input[type=checkbox] {\n  accent-color: rgb(var(--sw-cyan-rgb));\n}\n.env-list {\n  display: flex;\n  flex-direction: column;\n  gap: 4px;\n}\n.env-row {\n  display: flex;\n  align-items: stretch;\n  gap: 4px;\n}\n.env-row input {\n  flex: 1;\n}\n.env-remove,\n.env-add,\n.show-flags-toggle {\n  background: rgba(var(--sw-fg-rgb), 0.05);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.15);\n  border-radius: 4px;\n  color: rgba(var(--sw-fg-rgb), 0.75);\n  font-size: 0.85em;\n  padding: 4px 9px;\n  cursor: pointer;\n  display: inline-flex;\n  align-items: center;\n  gap: 4px;\n}\n.env-remove:hover,\n.env-add:hover,\n.show-flags-toggle:hover {\n  color: rgba(var(--sw-fg-rgb), 0.95);\n  border-color: rgba(var(--sw-fg-rgb), 0.3);\n}\n.env-remove mat-icon,\n.env-add mat-icon,\n.show-flags-toggle mat-icon {\n  font-size: 16px;\n  height: 16px;\n  width: 16px;\n}\n.env-remove {\n  padding: 4px 6px;\n}\n.env-add {\n  align-self: flex-start;\n  margin-top: 2px;\n}\n.show-flags-toggle {\n  align-self: flex-start;\n  margin-top: 2px;\n}\n.help-text {\n  margin-top: 6px;\n  padding: 9px 11px;\n  background: rgba(var(--sw-shade-rgb), 0.4);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n  border-radius: 4px;\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n  font-size: 0.82em;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n  white-space: pre-wrap;\n  max-height: 240px;\n  overflow-y: auto;\n}\n.help-status {\n  margin-top: 4px;\n}\n.help-error {\n  color: rgba(var(--sw-err-rgb), 0.95);\n}\n.error-box {\n  padding: 7px 9px;\n  background: rgba(var(--sw-red-rgb), 0.12);\n  border: 1px solid rgba(var(--sw-red-rgb), 0.35);\n  border-radius: 4px;\n  color: rgba(var(--sw-blush-rgb), 0.95);\n  font-size: 0.85em;\n  display: flex;\n  align-items: center;\n  gap: 6px;\n}\n.inline-actions {\n  display: flex;\n  justify-content: flex-end;\n  gap: 8px;\n  margin-top: 4px;\n}\n.inline-actions button.mat-mdc-button-base:not(:disabled):not(.mat-primary) {\n  color: rgba(var(--sw-fg-rgb), 0.92) !important;\n}\n.dim {\n  color: rgba(var(--sw-fg-rgb), 0.85);\n}\n.small {\n  font-size: 0.85em;\n}\n.mono {\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n}\n/*# sourceMappingURL=app-settings.component.css.map */\n';

// src/app/components/pages/node/apps/app-settings/app-settings.component.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AppSettingsComponent_1;
var AppSettingsComponent = class AppSettingsComponent2 {
  static {
    AppSettingsComponent_1 = this;
  }
  static {
    this.internalCapable = /* @__PURE__ */ new Set([
      "skysocks",
      "skysocks-client",
      "vpn-client",
      "vpn-server",
      "skychat",
      "skynet",
      "skynet-client"
    ]);
  }
  constructor(appsService, formBuilder, snackbarService, changeDetectorRef, dialog, translate) {
    this.appsService = appsService;
    this.formBuilder = formBuilder;
    this.snackbarService = snackbarService;
    this.changeDetectorRef = changeDetectorRef;
    this.dialog = dialog;
    this.translate = translate;
    this.saved = new EventEmitter();
    this.cancelled = new EventEmitter();
    this.pickServer = 0;
    this.saving = false;
    this.saveError = "";
    this.showHelp = false;
    this.helpLoading = false;
    this.helpError = "";
    this.helpText = "";
    this.showFlagsForm = false;
    this.flagsValid = true;
    this.subs = [];
    this.boundAppName = "";
  }
  /** True iff the launcher's "internal" mode is meaningful for this
   *  app — i.e. an in-process RunFunc is registered for it. */
  get internalModeAvailable() {
    if (!this.app) {
      return false;
    }
    const candidates = [this.app.name];
    const bin = this.app.binary;
    if (bin) {
      candidates.push(bin);
    }
    for (const c of candidates) {
      if (AppSettingsComponent_1.internalCapable.has(c)) {
        return true;
      }
      for (const base of AppSettingsComponent_1.internalCapable) {
        if (c.startsWith(base + "-")) {
          return true;
        }
      }
    }
    return false;
  }
  /** The servers this app connects to, for the picker on its server flag. */
  get serverType() {
    return this.app ? serverTypeFor(this.app.name, this.app.binary) : "";
  }
  ngOnInit() {
    this.buildForm();
    this.boundAppName = this.app?.name || "";
    if (this.pickServer > 0) {
      this.showFlagsForm = true;
    }
  }
  ngOnChanges(changes) {
    if (changes["pickServer"] && this.pickServer > 0) {
      this.showFlagsForm = true;
    }
    if (!changes["app"]) {
      return;
    }
    const next = this.app && this.app.name || "";
    if (next === this.boundAppName) {
      return;
    }
    this.boundAppName = next;
    this.buildForm();
    this.showHelp = false;
    this.showFlagsForm = false;
    this.flagsValid = true;
    this.helpText = "";
    this.helpError = "";
  }
  ngOnDestroy() {
    this.subs.forEach((s) => s.unsubscribe());
  }
  /**
   * Joins string args back into a shell-like string for the text
   * field. We don't quote here — the operator can edit raw and the
   * server-side parser handles re-tokenization. Tokens with
   * whitespace get wrapped in quotes for round-trip.
   */
  argsToString(args) {
    if (!args || args.length === 0) {
      return "";
    }
    return args.map((a) => /[\s"']/.test(a) ? `"${a.replace(/"/g, '\\"')}"` : a).join(" ");
  }
  buildForm() {
    const env = this.app && this.app.env || [];
    const envControls = env.map((e) => new UntypedFormControl(e));
    const mode = this.app && this.app.launcher_mode;
    this.form = this.formBuilder.group({
      args: [this.argsToString(this.app?.args)],
      env: this.formBuilder.array(envControls),
      launcher_mode: [mode || ""],
      autostart: [!!(this.app && this.app.autostart)]
    });
  }
  get envArray() {
    return this.form.get("env");
  }
  addEnvRow() {
    this.envArray.push(new UntypedFormControl(""));
  }
  removeEnvRow(i) {
    this.envArray.removeAt(i);
  }
  toggleHelp() {
    this.showHelp = !this.showHelp;
    if (this.showHelp && !this.helpText && !this.helpLoading) {
      this.fetchHelp();
    }
  }
  fetchHelp() {
    this.helpLoading = true;
    this.helpError = "";
    NodeComponent.currentNode.pipe(take(1)).subscribe((node) => {
      if (!node) {
        this.helpLoading = false;
        return;
      }
      const sub = this.appsService.getAppHelp(node.localPk, this.app.name).subscribe({
        next: (resp) => {
          this.helpLoading = false;
          this.helpText = resp && resp.help || "";
        },
        error: (e) => {
          this.helpLoading = false;
          this.helpError = e && e.message ? e.message : "Failed to fetch help";
        }
      });
      this.subs.push(sub);
      this.changeDetectorRef.markForCheck();
    });
  }
  toggleFlagsForm() {
    this.showFlagsForm = !this.showFlagsForm;
    if (!this.showFlagsForm) {
      this.flagsValid = true;
      this.pickServer = 0;
    }
  }
  onFlagArgs(args) {
    this.form.get("args").setValue(args);
  }
  /** Offers to save at once when the app runs, as saving args restarts it. */
  onServerChosen() {
    const status = this.app ? this.app.status : 0;
    if (status !== 1 && status !== 3) {
      return;
    }
    const text = this.translate.instant("app-settings.restart-prompt", { app: this.app.name });
    const confirm = GeneralUtils.createConfirmationDialog(this.dialog, text);
    confirm.componentInstance.operationAccepted.subscribe(() => {
      confirm.close();
      if (this.flagsValid) {
        this.save();
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  cancel() {
    this.cancelled.emit();
  }
  save() {
    if (this.saving) {
      return;
    }
    this.saving = true;
    this.saveError = "";
    NodeComponent.currentNode.pipe(take(1)).subscribe((node) => {
      this.doSave(node);
      this.changeDetectorRef.markForCheck();
    });
  }
  doSave(node) {
    if (!node) {
      this.saveError = "No active node";
      this.saving = false;
      return;
    }
    const env = (this.envArray.value || []).map((s) => (s || "").trim()).filter((s) => s.length > 0);
    const body = {
      args: (this.form.get("args").value || "").trim(),
      env_full: env,
      launcher_mode: this.form.get("launcher_mode").value || "",
      autostart: !!this.form.get("autostart").value
    };
    const sub = this.appsService.setAppFullConfig(node.localPk, this.app.name, body).subscribe({
      next: () => {
        this.saving = false;
        this.snackbarService.showDone("app-settings.saved");
        this.saved.emit();
      },
      error: (e) => {
        this.saving = false;
        this.saveError = e && e.message ? e.message : typeof e === "string" ? e : "Failed to save";
      }
    });
    this.subs.push(sub);
  }
  // Surface the visor's last-known DetailedStatus when status is Errored.
  // The AppStatus enum: 0=stopped, 1=running, 2=errored, 3=starting.
  get isErrored() {
    return this.app && this.app.status === 2;
  }
  get lastError() {
    if (!this.isErrored) {
      return "";
    }
    return this.app.detailed_status || this.app.detailedStatus || "errored";
  }
  static {
    this.ctorParameters = () => [
      { type: AppsService },
      { type: UntypedFormBuilder },
      { type: SnackbarService },
      { type: ChangeDetectorRef },
      { type: MatDialog },
      { type: TranslateService }
    ];
  }
  static {
    this.propDecorators = {
      app: [{ type: Input }],
      saved: [{ type: Output }],
      cancelled: [{ type: Output }],
      pickServer: [{ type: Input }]
    };
  }
};
AppSettingsComponent = AppSettingsComponent_1 = __decorate([
  Component({
    selector: "app-app-settings",
    template: app_settings_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [app_settings_component_default2]
  })
], AppSettingsComponent);

// src/app/services/proxy-discovery.service.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var ProxyDiscoveryService = class ProxyDiscoveryService2 {
  constructor(http) {
    this.http = http;
    this.discoveryServiceUrl = "api/services?type=";
  }
  /**
   * Get the proxies or vpn servers registered in the discovery service.
   * @param getProxies If true, get proxies (skysocks); if false, get vpn servers.
   */
  getServices(getProxies) {
    const wantType = getProxies ? "proxy" : "vpn";
    const accepted = getProxies ? ["proxy", "skysocks"] : ["vpn"];
    return this.http.get(this.discoveryServiceUrl + wantType).pipe(
      // In case of error, retry.
      retryWhen((errors) => errors.pipe(delay(4e3))),
      map((result) => {
        const response = [];
        if (!result) {
          return response;
        }
        const entries = Array.isArray(result) ? result : Object.keys(result).map((k) => result[k]);
        entries.forEach((raw) => {
          const currentEntry = new ProxyDiscoveryEntry();
          let pk = "";
          if (raw.address) {
            const parts = raw.address.split(":");
            pk = parts[0];
            currentEntry.address = raw.address;
            currentEntry.port = parts.length === 2 ? parts[1] : "";
          } else if (raw.pk) {
            pk = raw.pk;
            currentEntry.address = raw.pk;
          }
          if (!/^[0-9a-fA-F]{66}$/.test(pk)) {
            return;
          }
          currentEntry.pk = pk;
          if (Array.isArray(raw.services) && !raw.services.some((t) => accepted.includes(t))) {
            return;
          }
          currentEntry.location = "";
          const country = raw.country || raw.geo && raw.geo.country;
          const region = raw.geo && raw.geo.region;
          if (country) {
            currentEntry.country = country;
            currentEntry.location += countriesList[String(country).toUpperCase()] || country;
          }
          if (region) {
            currentEntry.region = region;
            currentEntry.location += (country ? ", " : "") + region;
          }
          response.push(currentEntry);
        });
        return response;
      })
    );
  }
  static {
    this.ctorParameters = () => [
      { type: HttpClient }
    ];
  }
};
ProxyDiscoveryService = __decorate2([
  Injectable({
    providedIn: "root"
  })
], ProxyDiscoveryService);

// angular:jit:template:src/app/components/pages/node/skysocks-tab/select-proxy-server/select-proxy-server.component.html
var select_proxy_server_component_default = `<h1 mat-dialog-title>{{ (isVpn ? 'skysocks-tab.select-server.title-vpn' : 'skysocks-tab.select-server.title') | translate }}</h1>

<div mat-dialog-content class="sps-content">
  <p class="sps-sub">{{ (isVpn ? 'skysocks-tab.select-server.help-vpn' : 'skysocks-tab.select-server.help') | translate:{ client: data.clientName } }}</p>

  <!-- Manual PK entry -->
  <div class="sps-manual">
    <mat-form-field class="sps-manual-field">
      <mat-label>{{ 'skysocks-tab.select-server.manual-pk' | translate }}</mat-label>
      <input matInput [(ngModel)]="manualPk" spellcheck="false" autocomplete="off" placeholder="0000...">
    </mat-form-field>
    <button mat-raised-button color="primary" (click)="useManual()">
      {{ 'skysocks-tab.select-server.use' | translate }}
    </button>
  </div>

  <!-- Discovery list -->
  <div class="sps-filters">
    <mat-form-field class="sps-filter">
      <mat-label>{{ 'skysocks-tab.select-server.filter' | translate }}</mat-label>
      <input matInput [(ngModel)]="filterText" (ngModelChange)="applyFilter()" spellcheck="false" autocomplete="off">
    </mat-form-field>
    <label class="sps-country">
      <span class="sps-country-label">{{ 'skysocks-tab.select-server.country' | translate }}</span>
      <select [(ngModel)]="country" (ngModelChange)="applyFilter()">
        <option value="">{{ 'skysocks-tab.select-server.all-countries' | translate }}</option>
        @for (c of countries; track c.code) {
          <option [value]="c.code">{{ c.name }} ({{ c.count }})</option>
        }
      </select>
    </label>
  </div>

  @if (loading) {
    <div class="sps-msg">{{ 'common.loading' | translate }}</div>
  } @else if (loadError) {
    <div class="sps-msg sps-error">{{ 'skysocks-tab.select-server.load-error' | translate }}</div>
  } @else if (!filtered.length) {
    <div class="sps-msg">{{ (isVpn ? 'skysocks-tab.select-server.empty-vpn' : 'skysocks-tab.select-server.empty') | translate }}</div>
  } @else {
    <div class="sps-count small">{{ 'skysocks-tab.select-server.count' | translate:{ shown: filtered.length, total: servers.length } }}</div>
    <div class="sps-list">
      @for (s of filtered; track s.pk) {
        <button class="sps-row" [attr.data-pk]="s.pk" (click)="select(s)">
          @if (s.country) {
            <span class="sps-flag" [style.background-image]="'url(' + countryFlag(s.country) + ')'"></span>
          } @else {
            <mat-icon class="sps-flag-icon">public</mat-icon>
          }
          <span class="sps-row-text">
            <span class="sps-loc">{{ s.location || ('skysocks-tab.select-server.unknown-location' | translate) }}</span>
            <span class="sps-pk mono">{{ s.pk }}</span>
          </span>
          @if (latency.has(s.pk)) {
            <span class="sps-latency" [title]="'skysocks-tab.select-server.latency-hint' | translate">{{ latency.get(s.pk) | number:'1.0-0' }} ms</span>
          }
          <mat-icon class="sps-go">chevron_right</mat-icon>
        </button>
      }
    </div>
  }
</div>

<div mat-dialog-actions align="end">
  <button mat-button (click)="close()">{{ 'common.cancel' | translate }}</button>
</div>
`;

// angular:jit:style:src/app/components/pages/node/skysocks-tab/select-proxy-server/select-proxy-server.component.scss
var select_proxy_server_component_default2 = "/* src/app/components/pages/node/skysocks-tab/select-proxy-server/select-proxy-server.component.scss */\n.sps-content {\n  min-width: 340px;\n  max-width: 560px;\n}\n.sps-sub {\n  opacity: 0.75;\n  margin-bottom: 12px;\n}\n.sps-manual {\n  display: flex;\n  align-items: baseline;\n  gap: 10px;\n}\n.sps-manual .sps-manual-field {\n  flex: 1 1 auto;\n}\n.sps-filters {\n  display: flex;\n  align-items: baseline;\n  gap: 10px;\n  flex-wrap: wrap;\n}\n.sps-filter {\n  flex: 1 1 220px;\n}\n.sps-country {\n  display: flex;\n  flex-direction: column;\n  gap: 2px;\n  flex: 0 1 200px;\n  font-size: 12px;\n}\n.sps-country .sps-country-label {\n  color: rgba(32, 34, 38, 0.8);\n}\n.sps-country select {\n  background: #F8F9F9;\n  color: #202226;\n  border: 1px solid rgba(0, 0, 0, 0.12);\n  border-radius: 4px;\n  padding: 6px;\n  font-size: 13px;\n}\n.sps-count {\n  color: rgba(32, 34, 38, 0.8);\n  margin-bottom: 4px;\n}\n.sps-latency {\n  flex: 0 0 auto;\n  font-size: 12px;\n  color: #1b6e30;\n  font-variant-numeric: tabular-nums;\n}\n.sps-msg {\n  padding: 1rem 0;\n  opacity: 0.7;\n  text-align: center;\n}\n.sps-msg.sps-error {\n  color: #DA3439;\n}\n.sps-list {\n  max-height: 320px;\n  overflow-y: auto;\n  display: flex;\n  flex-direction: column;\n}\n.sps-row {\n  display: flex;\n  align-items: center;\n  gap: 10px;\n  width: 100%;\n  padding: 8px 6px;\n  background: transparent;\n  border: 0;\n  border-bottom: 1px solid rgba(0, 0, 0, 0.12);\n  cursor: pointer;\n  text-align: left;\n  color: inherit;\n}\n.sps-row:hover {\n  background: rgba(var(--sw-shade-rgb), 0.05);\n}\n.sps-flag {\n  width: 24px;\n  height: 18px;\n  flex: 0 0 auto;\n  background-size: cover;\n  background-position: center;\n  border-radius: 2px;\n}\n.sps-flag-icon {\n  opacity: 0.6;\n}\n.sps-row-text {\n  display: flex;\n  flex-direction: column;\n  flex: 1 1 auto;\n  min-width: 0;\n}\n.sps-row-text .sps-loc {\n  font-weight: 500;\n}\n.sps-row-text .sps-pk {\n  font-size: 11px;\n  opacity: 0.75;\n  overflow: hidden;\n  text-overflow: ellipsis;\n  white-space: nowrap;\n}\n.sps-go {\n  opacity: 0.5;\n  flex: 0 0 auto;\n}\n/*# sourceMappingURL=select-proxy-server.component.css.map */\n";

// src/app/components/pages/node/skysocks-tab/select-proxy-server/select-proxy-server.component.ts
var __decorate3 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var SelectProxyServerComponent_1;
function latencyByPk(transports = []) {
  const out = /* @__PURE__ */ new Map();
  transports.forEach((t) => {
    const ms = t.latencyMs || 0;
    if (ms > 0 && (!out.has(t.remotePk) || ms < out.get(t.remotePk))) {
      out.set(t.remotePk, ms);
    }
  });
  return out;
}
function sortServers(servers, latency) {
  return [...servers].sort((a, b) => {
    const la = latency.get(a.pk);
    const lb = latency.get(b.pk);
    if (la !== void 0 || lb !== void 0) {
      return la === void 0 ? 1 : lb === void 0 ? -1 : la - lb;
    }
    return (a.location || "~").localeCompare(b.location || "~") || a.pk.localeCompare(b.pk);
  });
}
var SelectProxyServerComponent = class SelectProxyServerComponent2 {
  static {
    SelectProxyServerComponent_1 = this;
  }
  /** Opens the picker and emits the chosen key, or nothing when cancelled. */
  static openDialog(dialog, data) {
    return dialog.open(SelectProxyServerComponent_1, { width: "600px", data }).afterClosed();
  }
  constructor(dialogRef, data, discovery, snackbar, cdr) {
    this.dialogRef = dialogRef;
    this.data = data;
    this.discovery = discovery;
    this.snackbar = snackbar;
    this.cdr = cdr;
    this.servers = [];
    this.filtered = [];
    this.countries = [];
    this.latency = /* @__PURE__ */ new Map();
    this.loading = true;
    this.loadError = false;
    this.filterText = "";
    this.country = "";
    this.manualPk = "";
  }
  get isVpn() {
    return this.data.serviceType === "vpn";
  }
  ngOnInit() {
    this.latency = latencyByPk(this.data.transports);
    this.discovery.getServices(!this.isVpn).subscribe({
      next: (list) => {
        this.servers = sortServers(list || [], this.latency);
        this.countries = this.countriesOf(this.servers);
        this.applyFilter();
        this.loading = false;
        this.cdr.markForCheck();
      },
      error: () => {
        this.loading = false;
        this.loadError = true;
        this.cdr.markForCheck();
      }
    });
  }
  countriesOf(servers) {
    const counts = /* @__PURE__ */ new Map();
    servers.forEach((s) => {
      const code = (s.country || "").toUpperCase();
      if (code) {
        counts.set(code, (counts.get(code) || 0) + 1);
      }
    });
    return [...counts.entries()].map(([code, count]) => {
      return { code, name: countriesList[code] || code, count };
    }).sort((a, b) => a.name.localeCompare(b.name));
  }
  applyFilter() {
    const t = this.filterText.trim().toLowerCase();
    this.filtered = this.servers.filter((s) => (!this.country || (s.country || "").toUpperCase() === this.country) && (!t || (s.pk || "").toLowerCase().includes(t) || (s.location || "").toLowerCase().includes(t) || (s.country || "").toLowerCase().includes(t)));
  }
  countryFlag(code) {
    return code ? "assets/img/big-flags/" + code.toLowerCase() + ".png" : "";
  }
  select(server) {
    this.dialogRef.close(server.pk);
  }
  useManual() {
    const pk = this.manualPk.trim();
    if (!/^[0-9a-fA-F]{66}$/.test(pk)) {
      this.snackbar.showError("skysocks-tab.select-server.invalid-pk");
      return;
    }
    this.dialogRef.close(pk);
  }
  close() {
    this.dialogRef.close();
  }
  static {
    this.ctorParameters = () => [
      { type: MatDialogRef },
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: ProxyDiscoveryService },
      { type: SnackbarService },
      { type: ChangeDetectorRef }
    ];
  }
};
SelectProxyServerComponent = SelectProxyServerComponent_1 = __decorate3([
  Component({
    selector: "app-select-proxy-server",
    template: select_proxy_server_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [select_proxy_server_component_default2]
  })
], SelectProxyServerComponent);

// angular:jit:template:src/app/components/pages/node/apps/app-flags-form/app-flags-form.component.html
var app_flags_form_component_default = `<div class="flags-form">
  @if (loading) {
    <div class="flags-status small">{{ 'app-flags.loading' | translate }}</div>
  }
  @if (error) {
    <div class="flags-status flags-error-text">{{ error }}</div>
  }
  @if (!loading && !error && flags.length === 0) {
    <div class="flags-status small">{{ 'app-flags.none' | translate }}</div>
  }
  @for (f of flags; track f.name) {
    <div class="flag-row" [attr.data-flag]="f.name" [class.flag-set]="state[f.name]?.set">
      <div class="flag-head">
        <label class="flag-name" for="flag-{{ appName }}-{{ f.name }}">
          @if (f.shorthand) {
            <span>-{{ f.shorthand }}, </span>
          }--{{ f.name }}
        </label>
        <span class="flag-type">{{ 'app-flags.type-' + f.type | translate }}</span>
        @if (state[f.name]?.set) {
          <button type="button" class="flag-reset" (click)="reset(f)" title="{{ 'app-flags.reset' | translate }}">
            <mat-icon [inline]="true">undo</mat-icon>
            {{ 'app-flags.reset' | translate }}
          </button>
        }
      </div>
      <div class="flag-input">
        @switch (f.type) {
          @case ('bool') {
            <label class="flag-check">
              <input id="flag-{{ appName }}-{{ f.name }}" type="checkbox"
                     [checked]="state[f.name]?.value === 'true'"
                     (change)="setValue(f, $any($event.target).checked)">
              <span>{{ (state[f.name]?.value === 'true' ? 'app-flags.on' : 'app-flags.off') | translate }}</span>
            </label>
          }
          @case ('int') {
            <input id="flag-{{ appName }}-{{ f.name }}" type="number" step="1"
                   [value]="state[f.name]?.value" [placeholder]="f.default"
                   (input)="setValue(f, $any($event.target).value)">
          }
          @default {
            <input id="flag-{{ appName }}-{{ f.name }}" type="text"
                   [value]="state[f.name]?.value" [placeholder]="f.type === 'stringList' ? ('app-flags.list-placeholder' | translate) : f.default"
                   (input)="setValue(f, $any($event.target).value)">
            @if (canPick(f)) {
              <button type="button" class="flag-pick" (click)="chooseServer(f)">
                <mat-icon [inline]="true">dns</mat-icon>
                {{ 'app-flags.choose-server' | translate }}
              </button>
            }
          }
        }
      </div>
      @if (errorFor(f)) {
        <div class="flag-error">{{ errorFor(f) }}</div>
      }
      <div class="flag-usage small">
        {{ f.usage }}
        <span class="flag-default">{{ 'app-flags.default' | translate }}: <span class="mono">{{ f.default || ('app-flags.empty' | translate) }}</span></span>
      </div>
    </div>
  }
</div>
`;

// angular:jit:style:src/app/components/pages/node/apps/app-flags-form/app-flags-form.component.scss
var app_flags_form_component_default2 = '/* src/app/components/pages/node/apps/app-flags-form/app-flags-form.component.scss */\n.flags-form {\n  display: flex;\n  flex-direction: column;\n  gap: 6px;\n  margin-top: 6px;\n  max-height: 420px;\n  overflow-y: auto;\n  padding-right: 4px;\n  scrollbar-color: rgba(var(--sw-fg-rgb), 0.25) transparent;\n}\n.flag-row {\n  padding: 7px 9px;\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n  border-radius: 4px;\n  background: rgba(var(--sw-shade-rgb), 0.25);\n}\n.flag-row.flag-set {\n  border-left: 2px solid rgba(var(--sw-cyan-rgb), 0.6);\n}\n.flag-head {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n  margin-bottom: 4px;\n}\n.flag-name {\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n  font-size: 0.88em;\n  color: rgba(var(--sw-fg-rgb), 0.95);\n}\n.flag-type {\n  font-size: 0.75em;\n  color: rgba(var(--sw-fg-rgb), 0.7);\n  text-transform: lowercase;\n}\n.flag-reset {\n  margin-left: auto;\n  background: rgba(var(--sw-fg-rgb), 0.05);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.15);\n  border-radius: 4px;\n  color: rgba(var(--sw-fg-rgb), 0.8);\n  font-size: 0.78em;\n  padding: 2px 7px;\n  cursor: pointer;\n  display: inline-flex;\n  align-items: center;\n  gap: 3px;\n}\n.flag-reset:hover {\n  color: rgba(var(--sw-fg-rgb), 0.95);\n  border-color: rgba(var(--sw-fg-rgb), 0.3);\n}\n.flag-input input[type=text],\n.flag-input input[type=number] {\n  width: 100%;\n  box-sizing: border-box;\n  background: rgba(var(--sw-shade-rgb), 0.3);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.15);\n  border-radius: 4px;\n  padding: 6px 8px;\n  color: rgba(var(--sw-fg-rgb), 0.95);\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n  font-size: 0.86em;\n  outline: none;\n}\n.flag-input input[type=text]:focus,\n.flag-input input[type=number]:focus {\n  border-color: rgba(var(--sw-cyan-rgb), 0.6);\n}\n.flag-pick {\n  margin-top: 5px;\n  background: rgba(var(--sw-cyan-rgb), 0.12);\n  border: 1px solid rgba(var(--sw-cyan-rgb), 0.45);\n  border-radius: 4px;\n  color: rgba(var(--sw-fg-rgb), 0.95);\n  font-size: 0.8em;\n  padding: 3px 9px;\n  cursor: pointer;\n  display: inline-flex;\n  align-items: center;\n  gap: 4px;\n}\n.flag-pick:hover {\n  background: rgba(var(--sw-cyan-rgb), 0.22);\n}\n.flag-check {\n  display: inline-flex;\n  align-items: center;\n  gap: 6px;\n  font-size: 0.86em;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n  cursor: pointer;\n}\n.flag-check input[type=checkbox] {\n  accent-color: rgb(var(--sw-cyan-rgb));\n}\n.flag-error {\n  margin-top: 3px;\n  font-size: 0.8em;\n  color: rgba(var(--sw-err-rgb), 0.95);\n}\n.flag-usage {\n  margin-top: 4px;\n  color: rgba(var(--sw-fg-rgb), 0.78);\n}\n.flag-default {\n  margin-left: 6px;\n  color: rgba(var(--sw-fg-rgb), 0.7);\n}\n.flags-status {\n  color: rgba(var(--sw-fg-rgb), 0.85);\n}\n.flags-error-text {\n  color: rgba(var(--sw-err-rgb), 0.95);\n}\n.small {\n  font-size: 0.82em;\n}\n.mono {\n  font-family:\n    "Consolas",\n    "Monaco",\n    "Courier New",\n    monospace;\n}\n/*# sourceMappingURL=app-flags-form.component.css.map */\n';

// src/app/components/pages/node/apps/app-flags-form/app-flags-form.component.ts
var __decorate4 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var AppFlagsFormComponent = class AppFlagsFormComponent2 {
  constructor(appsService, changeDetectorRef, dialog) {
    this.appsService = appsService;
    this.changeDetectorRef = changeDetectorRef;
    this.dialog = dialog;
    this.appName = "";
    this.args = "";
    this.argsChange = new EventEmitter();
    this.validChange = new EventEmitter();
    this.serverType = "";
    this.pickServer = 0;
    this.serverChosen = new EventEmitter();
    this.flags = [];
    this.state = {};
    this.loading = false;
    this.error = "";
    this.lastArgs = "";
    this.transports = [];
  }
  ngOnInit() {
    this.loading = true;
    NodeComponent.currentNode.pipe(take(1)).subscribe((node) => {
      if (!node) {
        this.loading = false;
        return;
      }
      this.transports = node.transports || [];
      this.sub = this.appsService.getAppFlags(node.localPk, this.appName).subscribe({
        next: (schema) => {
          this.loading = false;
          this.flags = schema && schema.flags || [];
          this.readArgs();
          this.changeDetectorRef.markForCheck();
          if (this.pickServer > 0) {
            this.openPicker();
          }
        },
        error: (e) => {
          this.loading = false;
          this.error = e && e.message ? e.message : "Failed to fetch flags";
          this.changeDetectorRef.markForCheck();
        }
      });
    });
  }
  ngOnChanges(changes) {
    if (changes["args"] && this.args !== this.lastArgs && this.flags.length) {
      this.readArgs();
    }
    if (changes["pickServer"] && !changes["pickServer"].firstChange && this.pickServer > 0 && this.flags.length) {
      this.openPicker();
    }
  }
  canPick(f) {
    return !!this.serverType && isServerFlag(f);
  }
  openPicker() {
    const f = this.flags.find((fl) => this.canPick(fl));
    if (f) {
      this.chooseServer(f);
    }
  }
  /** Opens the server picker and writes the chosen key into the flag. */
  chooseServer(f) {
    SelectProxyServerComponent.openDialog(this.dialog, {
      clientName: this.appName,
      serviceType: this.serverType || "proxy",
      transports: this.transports
    }).subscribe((pk) => {
      if (!pk) {
        return;
      }
      this.setValue(f, pk);
      this.changeDetectorRef.markForCheck();
      this.serverChosen.emit(pk);
    });
  }
  ngOnDestroy() {
    if (this.sub) {
      this.sub.unsubscribe();
    }
  }
  readArgs() {
    this.lastArgs = this.args;
    this.state = parseFlagArgs(this.args, this.flags).state;
    Promise.resolve().then(() => {
      this.validChange.emit(this.valid);
      this.changeDetectorRef.markForCheck();
    });
  }
  get valid() {
    return this.flags.every((f) => !this.errorFor(f));
  }
  errorFor(f) {
    const st = this.state[f.name];
    return st && st.set ? flagError(f, st.value) : "";
  }
  setValue(f, value) {
    this.state[f.name] = { value: String(value), set: true };
    this.emit();
  }
  reset(f) {
    this.state[f.name] = { value: f.default, set: false };
    this.emit();
  }
  emit() {
    this.lastArgs = buildFlagArgs(this.args, this.flags, this.state);
    this.args = this.lastArgs;
    this.argsChange.emit(this.lastArgs);
    this.validChange.emit(this.valid);
  }
  static {
    this.ctorParameters = () => [
      { type: AppsService },
      { type: ChangeDetectorRef },
      { type: MatDialog }
    ];
  }
  static {
    this.propDecorators = {
      appName: [{ type: Input }],
      args: [{ type: Input }],
      argsChange: [{ type: Output }],
      validChange: [{ type: Output }],
      serverType: [{ type: Input }],
      pickServer: [{ type: Input }],
      serverChosen: [{ type: Output }]
    };
  }
};
AppFlagsFormComponent = __decorate4([
  Component({
    selector: "app-app-flags-form",
    template: app_flags_form_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [app_flags_form_component_default2]
  })
], AppFlagsFormComponent);

export {
  AppSettingsComponent,
  ProxyDiscoveryService,
  latencyByPk,
  sortServers,
  SelectProxyServerComponent,
  AppFlagsFormComponent
};
//# debugId=6a033838-9c5f-52c1-9044-09d2d043f838
//# sourceMappingURL=chunk-LMOBANXL.js.map
