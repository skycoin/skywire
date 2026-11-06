import {
  ClipboardService
} from "./chunk-XSOC4ELV.js";
import {
  NodeComponent,
  PageBaseComponent
} from "./chunk-CIWHQAPM.js";
import {
  ApiService,
  SnackbarService,
  processServiceError
} from "./chunk-ZMZW6HXB.js";
import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  Injectable
} from "./chunk-ZNLGPAUZ.js";

// angular:jit:template:src/app/components/pages/node/node-settings/node-settings.component.html
var node_settings_component_default = `<div class="container-elevated-translucid mt-4.5 settings-container">
  @if (loading) {
    <div class="text-center py-2">
      <mat-spinner diameter="24" class="mx-auto"></mat-spinner>
    </div>
  }

  @if (!loading && loadError) {
    <p class="status error">{{ loadError }}</p>
    <button mat-stroked-button class="secondary" (click)="load()">Retry</button>
  }

  @if (!loading && state && !loadError) {
    <p class="status">
      <span class="mono">{{ state.path }}</span>
      @if (!state.exists) {
        \u2014 does not exist yet; saving creates it.
      }
      @if (!state.writable) {
        \u2014 this visor cannot write it. Review a change to get the
        <code>sudo skywire autoconfig</code> command that makes it.
      }
      <br>
      The visor reads skywire.json, which <code>skywire autoconfig</code> regenerates from this file.
      A change applies once autoconfig runs again and the visor restarts.
    </p>

    @if (!reviewing) {
      <div class="bar">
        <input class="filter" type="search" placeholder="Filter" [(ngModel)]="filter">
        <mat-checkbox [(ngModel)]="setOnly" color="primary">Set in the file only</mat-checkbox>
        <span class="count">{{ visibleCount }} of {{ rows.length }}</span>
        <span class="spacer"></span>
        @if (invalidCount > 0) {
          <span class="invalid-count">{{ invalidCount }} to fix</span>
        }
        <button mat-raised-button color="primary" [disabled]="invalidCount > 0" (click)="review()">Review changes</button>
      </div>

      @for (r of rows; track r.key) {
        @if (visible(r)) {
          <div class="row" [class.is-set]="r.set">
            <div class="about">
              <div class="name"><span class="mono">{{ r.key }}</span> <span class="flag">--{{ r.flag }}</span></div>
              <div class="help">{{ r.help }}</div>
              @if (r.note) {
                <div class="note">{{ r.note }}</div>
              }
            </div>
            <div class="field">
              @if (r.format === 'bool') {
                <select [(ngModel)]="form[r.key]">
                  <option value="">default{{ r.default ? ' (' + (r.default === 'true' ? 'on' : 'off') + ')' : '' }}</option>
                  <option value="true">on</option>
                  <option value="false">off</option>
                </select>
              } @else {
                <input [type]="r.secret ? 'password' : 'text'" [(ngModel)]="form[r.key]"
                  [class.invalid]="errorFor(r)" [placeholder]="placeholder(r)" spellcheck="false" autocomplete="off">
                @if (errorFor(r); as err) {
                  <div class="field-error">{{ err }}</div>
                }
              }
            </div>
          </div>
        }
      }
    }

    @if (reviewing) {
      <div class="review">
        @if (saved) {
          <p class="status ok">Saved to {{ state.path }}.</p>
          <p class="status">
            The visor runs with its old settings until skywire.json is regenerated and it restarts:
          </p>
          <div class="cmd">
            <code class="mono">sudo skywire autoconfig</code>
            <button mat-icon-button (click)="copyCommand('sudo skywire autoconfig')"><mat-icon>content_copy</mat-icon></button>
          </div>
        } @else {
          <table class="data-table">
            <tr><th>Variable</th><th>Now</th><th>After</th></tr>
            @for (c of changes; track c.key) {
              <tr><td class="mono">{{ c.key }}</td><td class="mono">{{ c.from }}</td><td class="mono">{{ c.to }}</td></tr>
            }
          </table>
          @if (command) {
            <p class="status">Run this on the visor's host; it edits the file, regenerates skywire.json and restarts the visor:</p>
            <div class="cmd">
              <code class="mono">{{ command }}</code>
              <button mat-icon-button (click)="copyCommand(command)"><mat-icon>content_copy</mat-icon></button>
            </div>
          }
        }
        <div class="bar">
          @if (!saved) {
            <button mat-stroked-button class="secondary" (click)="back()">Back</button>
            @if (state.writable) {
              <button mat-raised-button color="primary" [disabled]="saving" (click)="save()">Save</button>
            }
          } @else {
            <button mat-stroked-button class="secondary" (click)="done()">Back to settings</button>
          }
        </div>
      </div>
    }
  }
</div>
`;

// angular:jit:style:src/app/components/pages/node/node-settings/node-settings.component.scss
var node_settings_component_default2 = "/* src/app/components/pages/node/node-settings/node-settings.component.scss */\n.settings-container {\n  padding: 20px;\n  max-width: 960px;\n}\n.mono {\n  font-family: monospace;\n}\ncode {\n  background: rgba(var(--sw-fg-rgb), 0.1);\n  padding: 1px 4px;\n  border-radius: 3px;\n  font-size: 12px;\n}\n.status {\n  color: rgba(var(--sw-fg-rgb), 0.7);\n  font-size: 13px;\n  margin-bottom: 12px;\n}\n.status.error {\n  color: rgb(var(--sw-coral-rgb));\n}\n.status.ok {\n  color: rgb(var(--sw-green-rgb));\n}\n.bar {\n  display: flex;\n  align-items: center;\n  flex-wrap: wrap;\n  gap: 8px 16px;\n  margin: 8px 0 12px;\n}\n.spacer {\n  flex: 1;\n}\n.count {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n  font-size: 12px;\n}\ninput,\nselect {\n  background: rgba(var(--sw-shade-rgb), 0.25);\n  color: var(--sw-input-text);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.2);\n  border-radius: 4px;\n  padding: 6px 8px;\n  font-size: 13px;\n  box-sizing: border-box;\n}\ninput::placeholder,\nselect::placeholder {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a40));\n}\nselect option {\n  background: var(--sw-menu-bg);\n}\n.filter {\n  width: 220px;\n}\n.row {\n  display: flex;\n  flex-wrap: wrap;\n  align-items: center;\n  gap: 6px 16px;\n  padding: 8px 4px;\n  border-bottom: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n}\n.row.is-set .name .mono {\n  color: rgb(var(--sw-green-rgb));\n}\n.about {\n  flex: 1 1 260px;\n  min-width: 0;\n}\n.name {\n  font-size: 13px;\n}\n.flag {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a50));\n  font-size: 12px;\n  margin-left: 6px;\n}\n.help {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a65));\n  font-size: 12px;\n}\n.note {\n  color: rgb(var(--sw-amber-rgb));\n  font-size: 12px;\n}\n.field {\n  flex: none;\n  width: 300px;\n  max-width: 100%;\n}\n.field input,\n.field select {\n  width: 100%;\n}\n.data-table {\n  width: 100%;\n  border-collapse: collapse;\n  margin-bottom: 16px;\n}\n.data-table th,\n.data-table td {\n  padding: 6px 10px;\n  text-align: left;\n  border-bottom: 1px solid rgba(var(--sw-fg-rgb), 0.08);\n  overflow-wrap: anywhere;\n}\n.data-table th {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n  font-weight: 500;\n  font-size: 12px;\n}\n.cmd {\n  display: flex;\n  align-items: center;\n  gap: 8px;\n  margin-bottom: 12px;\n}\n.cmd code {\n  flex: 1;\n  padding: 8px;\n  word-break: break-all;\n}\n.secondary {\n  color: var(--sw-text) !important;\n  border-color: rgba(var(--sw-fg-rgb), 0.35) !important;\n}\n.field input.invalid {\n  border-color: rgb(var(--sw-rose-rgb));\n}\n.field-error {\n  color: rgb(var(--sw-rose-rgb));\n  font-size: 0.85em;\n  margin-top: 2px;\n}\n.invalid-count {\n  color: rgb(var(--sw-rose-rgb));\n  font-size: 0.9em;\n}\n/*# sourceMappingURL=node-settings.component.css.map */\n";

// src/app/services/skyenv.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var skyenvRedacted = "(set)";
var SkyenvService = class SkyenvService2 {
  constructor(apiService) {
    this.apiService = apiService;
  }
  get(pk) {
    return this.apiService.get(`visors/${pk}/skyenv`);
  }
  set(pk, edits) {
    return this.apiService.put(`visors/${pk}/skyenv`, edits);
  }
  static {
    this.ctorParameters = () => [
      { type: ApiService }
    ];
  }
};
SkyenvService = __decorate([
  Injectable({
    providedIn: "root"
  })
], SkyenvService);

// src/app/components/pages/node/node-settings/node-settings.component.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var logLevels = /* @__PURE__ */ new Set(["trace", "debug", "info", "warn", "warning", "error", "fatal", "panic"]);
function checkOne(rule, v) {
  switch (rule) {
    case "pk":
      return /^[0-9a-fA-F]{66}$/.test(v) ? "" : `"${v}" is not a public key`;
    case "hostport": {
      const m = /^(\[[^\]]*\]|[^:\s[\]]*):(\d{1,5})$/.exec(v);
      return m && Number(m[2]) <= 65535 ? "" : `"${v}" is not host:port`;
    }
    case "duration":
      return /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/.test(v) ? "" : `"${v}" is not a duration like 10s or 5m`;
    case "loglevel":
      return logLevels.has(v.toLowerCase()) ? "" : `"${v}" is not a log level`;
  }
  return "";
}
function settingError(r, value) {
  const v = (value || "").trim();
  if (v === "" || r.secret && v === skyenvRedacted) {
    return "";
  }
  if (r.format === "int" && !/^-?\d+$/.test(v)) {
    return `"${v}" is not a whole number`;
  }
  if (r.check === "pks") {
    for (const item of v.split(",").map((s) => s.trim()).filter((s) => s)) {
      const err = checkOne("pk", item);
      if (err) {
        return err;
      }
    }
    return "";
  }
  return checkOne(r.check, v);
}
var secretKeys = /* @__PURE__ */ new Set(["SK", "DMSGWEBSK", "VPNROUTERPASSPHRASE"]);
function settingRows(st) {
  const rows = [];
  const at = /* @__PURE__ */ new Map();
  for (const f of st.flags || []) {
    if (!f.env_key || f.env_key === "SK") {
      continue;
    }
    const i = at.get(f.env_key);
    if (i === void 0) {
      at.set(f.env_key, rows.length);
      rows.push(rowFor(f, st.values || {}));
    } else if (rows[i].negate && !f.env_negate) {
      rows[i] = rowFor(f, st.values || {});
    }
  }
  return rows;
}
function rowFor(f, vals) {
  const key = f.env_key;
  const r = {
    key,
    flag: f.name,
    negate: !!f.env_negate,
    format: f.env_format || "string",
    help: flagHelp(f.description),
    default: f.env_default || "",
    note: f.env_note || "",
    set: false,
    value: "",
    secret: false,
    check: f.check || ""
  };
  if (key in vals) {
    const v = vals[key];
    r.set = true;
    r.secret = v === skyenvRedacted;
    r.value = r.format === "bashArray" ? v.split(/\s+/).filter((s) => s).join(",") : v;
  }
  return r;
}
function flagHelp(desc) {
  const i = desc.indexOf(" \u2014 writes ");
  return i >= 0 ? desc.substring(0, i).trim() : desc;
}
function defaultShown(r) {
  return r.default === "" ? "(default)" : `(default ${r.default})`;
}
function shown(r) {
  return r.set ? r.value : defaultShown(r);
}
function normalizeList(v) {
  return v.split(/[\s,]+/).filter((s) => s).join(",");
}
function parseBool(v) {
  switch (v.toLowerCase()) {
    case "true":
    case "on":
    case "yes":
    case "1":
      return true;
    case "false":
    case "off":
    case "no":
    case "0":
      return false;
  }
  return null;
}
function settingsEdits(rows, form) {
  const edits = { set: {}, unset: [] };
  const changes = [];
  for (const r of rows) {
    if (!(r.key in form)) {
      continue;
    }
    let v = (form[r.key] || "").trim();
    if (v === "") {
      if (r.set && r.value !== "") {
        edits.unset.push(r.flag);
        changes.push({ key: r.key, from: shown(r), to: defaultShown(r) });
      }
      continue;
    }
    if (r.format === "bashArray") {
      v = normalizeList(v);
    }
    if (r.set && v === r.value || r.secret && v === skyenvRedacted) {
      continue;
    }
    let send = v;
    if (r.format === "bool") {
      const b = parseBool(v);
      if (b === null) {
        throw new Error(`${r.key}: "${v}" is not on or off`);
      }
      if (r.set && b === (r.value === "true")) {
        continue;
      }
      v = String(b);
      send = String(b !== r.negate);
    }
    edits.set[r.flag] = send;
    const to = r.secret || secretKeys.has(r.key) ? "(changed)" : v;
    changes.push({ key: r.key, from: shown(r), to });
  }
  return { edits, changes };
}
function shellWord(s) {
  return /^[A-Za-z0-9_.,:/@=+-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}
function autoconfigCommand(rows, edits) {
  const byFlag = new Map(rows.map((r) => [r.flag, r]));
  const args = [];
  for (const [flag, v] of Object.entries(edits.set || {})) {
    args.push(shellWord(`--${flag}=${v}`));
  }
  for (const flag of edits.unset || []) {
    const r = byFlag.get(flag);
    let v = r ? r.default : "";
    if (r && r.format === "bool" && r.negate) {
      v = String(v !== "true");
    }
    args.push(shellWord(`--${flag}=${v}`));
  }
  return "sudo skywire autoconfig " + args.join(" ");
}
var NodeSettingsComponent = class NodeSettingsComponent2 extends PageBaseComponent {
  constructor(skyenvService, snackbarService, clipboardService, changeDetectorRef) {
    super();
    this.skyenvService = skyenvService;
    this.snackbarService = snackbarService;
    this.clipboardService = clipboardService;
    this.changeDetectorRef = changeDetectorRef;
    this.nodeKey = "";
    this.loading = true;
    this.loadError = "";
    this.state = null;
    this.rows = [];
    this.form = {};
    this.filter = "";
    this.setOnly = false;
    this.reviewing = false;
    this.changes = [];
    this.edits = {};
    this.command = "";
    this.saving = false;
    this.saved = false;
    this.sub = null;
  }
  ngOnInit() {
    this.nodeKey = NodeComponent.getCurrentNodeKey();
    this.load();
    return super.ngOnInit();
  }
  ngOnDestroy() {
    if (this.sub) {
      this.sub.unsubscribe();
    }
  }
  load() {
    this.loading = true;
    if (this.sub) {
      this.sub.unsubscribe();
    }
    this.sub = this.skyenvService.get(this.nodeKey).subscribe({
      next: (st) => this.show(st),
      error: (err) => {
        this.loading = false;
        this.loadError = processServiceError(err).originalServerErrorMsg || "Could not read the settings.";
        this.changeDetectorRef.markForCheck();
      }
    });
  }
  show(st) {
    this.state = st;
    this.loadError = "";
    const rows = settingRows(st);
    this.rows = rows.filter((r) => r.set).concat(rows.filter((r) => !r.set));
    this.form = {};
    for (const r of this.rows) {
      this.form[r.key] = r.set ? r.value : "";
    }
    this.loading = false;
    this.reviewing = false;
    this.changeDetectorRef.markForCheck();
  }
  visible(r) {
    if (this.setOnly && !r.set) {
      return false;
    }
    const q = this.filter.trim().toLowerCase();
    return !q || (r.key + " " + r.flag + " " + r.help).toLowerCase().includes(q);
  }
  get visibleCount() {
    return this.rows.filter((r) => this.visible(r)).length;
  }
  /** The field's problem, shown under it; '' when the value is acceptable. */
  errorFor(r) {
    return settingError(r, this.form[r.key]);
  }
  /** How many fields hold a value the visor would refuse. */
  get invalidCount() {
    return this.rows.filter((r) => this.errorFor(r) !== "").length;
  }
  placeholder(r) {
    if (r.secret) {
      return "set \u2014 type to replace";
    }
    return r.default !== "" ? r.default : "";
  }
  review() {
    try {
      const { edits, changes } = settingsEdits(this.rows, this.form);
      this.edits = edits;
      this.changes = changes;
      this.command = this.state && !this.state.writable ? autoconfigCommand(this.rows, edits) : "";
    } catch (e) {
      this.snackbarService.showError(e.message);
      return;
    }
    if (this.changes.length === 0) {
      this.snackbarService.showWarning("Nothing has changed.");
      return;
    }
    this.saved = false;
    this.reviewing = true;
    this.changeDetectorRef.markForCheck();
  }
  back() {
    this.reviewing = false;
    this.changeDetectorRef.markForCheck();
  }
  save() {
    this.saving = true;
    this.skyenvService.set(this.nodeKey, this.edits).subscribe({
      next: (st) => {
        this.saving = false;
        this.show(st);
        this.saved = true;
        this.reviewing = true;
        this.changeDetectorRef.markForCheck();
      },
      error: (err) => {
        this.saving = false;
        this.snackbarService.showError(err);
        this.changeDetectorRef.markForCheck();
      }
    });
  }
  done() {
    this.saved = false;
    this.reviewing = false;
    this.changeDetectorRef.markForCheck();
  }
  copyCommand(cmd) {
    if (this.clipboardService.copy(cmd)) {
      this.snackbarService.showDone("copy.copied");
    }
  }
  static {
    this.ctorParameters = () => [
      { type: SkyenvService },
      { type: SnackbarService },
      { type: ClipboardService },
      { type: ChangeDetectorRef }
    ];
  }
};
NodeSettingsComponent = __decorate2([
  Component({
    selector: "app-node-settings",
    template: node_settings_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [node_settings_component_default2]
  })
], NodeSettingsComponent);

export {
  settingError
};
//# debugId=0884daf4-8943-5ab7-82e6-8dcbd336736c
//# sourceMappingURL=chunk-BGNEFYT6.js.map
