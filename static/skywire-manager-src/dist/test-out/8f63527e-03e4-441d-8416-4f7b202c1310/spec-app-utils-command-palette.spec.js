import {
  BulkRewardAddressChangerComponent,
  MultipleNodeDataService,
  UpdateAllComponent
} from "./chunk-SW25Q345.js";
import {
  ClipboardService
} from "./chunk-XSOC4ELV.js";
import "./chunk-CDB4CLQF.js";
import {
  GeneralUtils,
  homeTabsData,
  ptyUrl
} from "./chunk-KWVFHEEW.js";
import {
  isRemoteVisor,
  nodePaletteTabs,
  nodeTabsData
} from "./chunk-HLYS7KBE.js";
import "./chunk-B4GMTCLR.js";
import {
  MatDialog
} from "./chunk-CAA347WM.js";
import {
  TranslateService
} from "./chunk-6WNZTOXO.js";
import "./chunk-GPHL7TNH.js";
import "./chunk-H3STL4JE.js";
import {
  NodeService
} from "./chunk-XBRWDJ2O.js";
import {
  Router,
  SnackbarService
} from "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import {
  StorageService
} from "./chunk-XOUXCPTJ.js";
import {
  ThemeService,
  themeChoices
} from "./chunk-VNMLTXOX.js";
import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  HostListener,
  Injectable,
  Subject,
  ViewChild,
  take
} from "./chunk-ZNLGPAUZ.js";
import {
  __spreadProps,
  __spreadValues
} from "./chunk-ARXMZVRT.js";

// src/app/utils/command-palette.ts
var actionIcons = {
  "copy-key": "filter_none",
  "terminal": "code",
  "logs": "description",
  "restart": "refresh",
  "update": "get_app",
  "reward": "monetization_on"
};
var onlineOnly = /* @__PURE__ */ new Set(["restart", "update", "reward"]);
function paletteTokens(query) {
  return (query || "").toLowerCase().split(/\s+/).filter((t) => t.length > 0);
}
function matchScore(token, text) {
  if (!token || !text) {
    return 0;
  }
  const t = text.toLowerCase();
  if (t === token) {
    return 100;
  }
  const idx = t.indexOf(token);
  if (idx === 0) {
    return 80 + 10 * token.length / t.length;
  }
  if (idx > 0) {
    const wordStart = !/[a-z0-9]/.test(t[idx - 1]);
    return (wordStart ? 60 : 40) + 10 * token.length / t.length;
  }
  if (token.length < 2 || /^[0-9a-f]{20,}$/.test(t)) {
    return 0;
  }
  let pos = -1;
  let first = -1;
  for (const ch of token) {
    pos = t.indexOf(ch, pos + 1);
    if (pos < 0) {
      return 0;
    }
    if (first < 0) {
      first = pos;
    }
  }
  return 10 + 10 * token.length / (pos - first + 1);
}
function scoreEntry(tokens, entry) {
  const groups = entry.required.concat(entry.optional ? [entry.optional] : []);
  const hit = groups.map(() => false);
  let total = 0;
  for (const token of tokens) {
    let best = 0;
    groups.forEach((texts, gi) => {
      texts.forEach((text) => {
        const s = matchScore(token, text);
        if (s > 0) {
          hit[gi] = true;
        }
        best = Math.max(best, s);
      });
    });
    if (best === 0) {
      return 0;
    }
    total += best;
  }
  for (let i = 0; i < entry.required.length; i++) {
    if (!hit[i]) {
      return 0;
    }
  }
  return total;
}
function visorTexts(v) {
  return [v.label, v.pk, v.ip, v.place, v.hostname].filter((s) => !!s);
}
function visorName(v) {
  return v.label || v.pk;
}
function tabEntries(v, ctx2, combined) {
  return ctx2.tabsFor(v).filter((t) => !t.hidden && (t.linkParts || t.externalUrl)).map((t) => {
    const label = ctx2.translate(t.label);
    return {
      item: {
        group: "pages",
        icon: t.icon,
        title: combined ? visorName(v) + " \u203A " + label : label,
        detail: combined ? v.pk : visorName(v),
        link: t.linkParts || void 0,
        externalUrl: t.externalUrl
      },
      required: combined ? [visorTexts(v), [label]] : [[label]],
      optional: combined ? void 0 : visorTexts(v),
      onEmpty: !combined
    };
  });
}
function actionEntries(v, ctx2, combined) {
  return Object.keys(actionIcons).filter((a) => !onlineOnly.has(a) || v.online !== false).map((a) => {
    const label = ctx2.translate("palette.actions." + a);
    return {
      item: {
        group: "actions",
        icon: actionIcons[a],
        title: label + ": " + visorName(v),
        detail: v.pk,
        action: a,
        visor: v
      },
      required: combined ? [visorTexts(v), [label]] : [[label]],
      optional: combined ? void 0 : visorTexts(v),
      onEmpty: !combined
    };
  });
}
function themeEntries(ctx2) {
  return themeChoices.map((c) => ({
    item: {
      group: "actions",
      icon: c === "light" ? "wb_sunny" : c === "dark" ? "brightness_2" : "brightness_auto",
      title: ctx2.translate("palette.theme." + c),
      detail: c === ctx2.themeChoice ? ctx2.translate("palette.theme.current") : void 0,
      theme: c
    },
    required: [[ctx2.translate("palette.theme." + c), "appearance", "color", "colour"]]
  }));
}
function paletteEntries(ctx2, tokens) {
  const entries = [];
  if (ctx2.current) {
    entries.push(...tabEntries(ctx2.current, ctx2, false));
    entries.push(...actionEntries(ctx2.current, ctx2, false));
  }
  ctx2.visors.forEach((v) => entries.push({
    item: {
      group: "visors",
      icon: v.wasm ? "public" : "devices",
      title: visorName(v),
      detail: v.pk,
      meta: [v.ip, v.place].filter((s) => !!s).join(" \xB7 ") || void 0,
      link: ["/nodes", v.pk, "info"]
    },
    required: [visorTexts(v)],
    onEmpty: true
  }));
  ctx2.homeTabs.filter((t) => !t.hidden).forEach((t) => {
    const label = ctx2.translate(t.label);
    entries.push({
      item: { group: "pages", icon: t.icon, title: label, link: t.linkParts || void 0, externalUrl: t.externalUrl },
      required: [[label]],
      onEmpty: true
    });
  });
  if (ctx2.themeChoice) {
    entries.push(...themeEntries(ctx2));
  }
  if (tokens.length >= 2) {
    ctx2.visors.filter((v) => !ctx2.current || v.pk !== ctx2.current.pk).forEach((v) => {
      entries.push(...tabEntries(v, ctx2, true));
      entries.push(...actionEntries(v, ctx2, true));
    });
  }
  return entries;
}
function rankPalette(entries, query, perGroup = 10) {
  const tokens = paletteTokens(query);
  const byGroup = /* @__PURE__ */ new Map();
  entries.forEach((e, i) => {
    let score;
    if (tokens.length === 0) {
      score = e.onEmpty ? entries.length - i : 0;
    } else {
      score = scoreEntry(tokens, e);
    }
    if (score > 0) {
      const list = byGroup.get(e.item.group) || [];
      list.push(__spreadProps(__spreadValues({}, e.item), { score }));
      byGroup.set(e.item.group, list);
    }
  });
  const order = ["visors", "pages", "actions"];
  const groups = [];
  byGroup.forEach((items, group) => {
    items.sort((a, b) => b.score - a.score);
    groups.push({ group, items: items.slice(0, perGroup) });
  });
  groups.sort((a, b) => b.items[0].score - a.items[0].score || order.indexOf(a.group) - order.indexOf(b.group));
  return groups;
}

// angular:jit:template:src/app/components/layout/command-palette/command-palette.component.html
var command_palette_component_default = `@if (open) {
  <div class="palette-backdrop" (click)="close()">
    <div class="palette" role="dialog" [attr.aria-label]="'palette.placeholder' | translate" (click)="$event.stopPropagation()">
      <div class="palette-search">
        <mat-icon [inline]="true">search</mat-icon>
        <input
          #searchInput
          type="text"
          autocomplete="off"
          spellcheck="false"
          [value]="query"
          [placeholder]="'palette.placeholder' | translate"
          (input)="onInput($event)"
          (keydown)="onInputKey($event)"
          >
        <span class="palette-key">Esc</span>
      </div>
      <div class="palette-results">
        @for (g of groups; track g.group) {
          <div class="palette-group">{{ ('palette.groups.' + g.group) | translate }}</div>
          @for (row of g.items; track row.index) {
            <div
              class="palette-item"
              [id]="'palette-item-' + row.index"
              [class.selected]="row.index === selected"
              (mousemove)="hover(row.index, $event)"
              (click)="run(row.item)"
              >
              <mat-icon [inline]="true">{{ row.item.icon }}</mat-icon>
              <div class="palette-text">
                <div class="palette-title">{{ row.item.title }}</div>
                @if (row.item.detail) {
                  <div class="palette-detail">{{ row.item.detail }}</div>
                }
                @if (row.item.meta) {
                  <div class="palette-meta">{{ row.item.meta }}</div>
                }
              </div>
            </div>
          }
        } @empty {
          <div class="palette-empty">{{ 'palette.no-results' | translate }}</div>
        }
      </div>
      <div class="palette-footer">{{ 'palette.footer' | translate }}</div>
    </div>
  </div>
}
`;

// angular:jit:style:src/app/components/layout/command-palette/command-palette.component.scss
var command_palette_component_default2 = "/* src/app/components/layout/command-palette/command-palette.component.scss */\n.palette-backdrop {\n  position: fixed;\n  inset: 0;\n  z-index: 999;\n  background: rgba(var(--sw-shade-rgb), 0.55);\n  display: flex;\n  justify-content: center;\n  align-items: flex-start;\n  padding: 12vh 16px 16px;\n}\n.palette {\n  width: 640px;\n  max-width: 100%;\n  max-height: 72vh;\n  display: flex;\n  flex-direction: column;\n  background: var(--sw-page-bg-2);\n  color: var(--sw-text);\n  border: 1px solid rgba(var(--sw-cyan-rgb), 0.35);\n  border-radius: 10px;\n  box-shadow: 0 12px 40px rgba(var(--sw-shade-rgb), 0.6);\n  overflow: hidden;\n}\n.palette-search {\n  display: flex;\n  align-items: center;\n  gap: 10px;\n  padding: 12px 16px;\n  border-bottom: 1px solid rgba(var(--sw-fg-rgb), 0.15);\n  font-size: 1.2rem;\n}\n.palette-search input {\n  flex: 1;\n  min-width: 0;\n  background: transparent;\n  border: none;\n  outline: none;\n  color: var(--sw-text);\n  font-size: 1rem;\n}\n.palette-search input::placeholder {\n  color: rgba(var(--sw-fg-rgb), var(--sw-a60));\n}\n.palette-key {\n  font-size: 0.7rem;\n  padding: 2px 6px;\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.45);\n  border-radius: 4px;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n}\n.palette-results {\n  overflow-y: auto;\n  padding: 4px 0 8px;\n}\n.palette-group {\n  padding: 10px 16px 4px;\n  font-size: 0.75rem;\n  font-weight: bold;\n  text-transform: uppercase;\n  letter-spacing: 1px;\n  color: rgb(var(--sw-cyan-rgb));\n}\n.palette-item {\n  display: flex;\n  align-items: flex-start;\n  gap: 10px;\n  padding: 7px 16px;\n  cursor: pointer;\n  border-left: 3px solid transparent;\n}\n.palette-item mat-icon {\n  font-size: 1.1rem;\n  margin-top: 2px;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n}\n.palette-item.selected {\n  background: rgba(var(--sw-cyan-rgb), 0.14);\n  border-left-color: rgb(var(--sw-cyan-rgb));\n}\n.palette-text {\n  min-width: 0;\n  flex: 1;\n}\n.palette-title {\n  font-size: 0.95rem;\n  overflow-wrap: anywhere;\n}\n.palette-detail {\n  font-family: monospace;\n  font-size: 0.75rem;\n  color: rgba(var(--sw-fg-rgb), 0.78);\n  word-break: break-all;\n}\n.palette-empty {\n  padding: 16px;\n  color: rgba(var(--sw-fg-rgb), 0.78);\n}\n.palette-footer {\n  padding: 6px 16px;\n  font-size: 0.75rem;\n  color: rgba(var(--sw-fg-rgb), 0.72);\n  border-top: 1px solid rgba(var(--sw-fg-rgb), 0.15);\n}\n.palette-meta {\n  font-size: 0.75rem;\n  color: rgba(var(--sw-fg-rgb), 0.78);\n}\n/*# sourceMappingURL=command-palette.component.css.map */\n";

// src/app/services/command-palette.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var CommandPaletteService = class CommandPaletteService2 {
  constructor() {
    this.openRequests = new Subject();
  }
  open() {
    this.openRequests.next();
  }
};
CommandPaletteService = __decorate([
  Injectable({
    providedIn: "root"
  })
], CommandPaletteService);
function paletteIsMac() {
  return typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || "");
}

// src/app/components/layout/command-palette/command-palette.component.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
function isPaletteShortcut(e, mac, inTextField) {
  if ((e.key || "").toLowerCase() !== "k" || e.altKey || e.shiftKey) {
    return false;
  }
  if (mac && e.metaKey && !e.ctrlKey) {
    return true;
  }
  return e.ctrlKey && !e.metaKey && !(mac && inTextField);
}
function paletteVisor(n) {
  return {
    pk: n.localPk,
    label: n.label,
    ip: n.publicIp || n.ip || void 0,
    place: [n.cityName, n.regionName, n.countryCode].filter((p) => !!p).join(", ") || void 0,
    hostname: n.hostname,
    online: n.online,
    wasm: n.arch === "wasm",
    version: n.version,
    buildTag: n.buildTag
  };
}
function visorPkFromUrl(url) {
  const m = /^\/nodes\/([0-9a-fA-F]{66})(?:[/?#]|$)/.exec(url || "");
  return m ? m[1] : void 0;
}
var CommandPaletteComponent = class CommandPaletteComponent2 {
  constructor(router, dialog, translate, paletteService, multipleNodeDataService, nodeService, clipboardService, snackbarService, cdr, themeService, storageService) {
    this.router = router;
    this.dialog = dialog;
    this.translate = translate;
    this.paletteService = paletteService;
    this.multipleNodeDataService = multipleNodeDataService;
    this.nodeService = nodeService;
    this.clipboardService = clipboardService;
    this.snackbarService = snackbarService;
    this.cdr = cdr;
    this.themeService = themeService;
    this.storageService = storageService;
    this.open = false;
    this.query = "";
    this.groups = [];
    this.selected = 0;
    this.mac = paletteIsMac();
    this.flat = [];
    this.visors = [];
    this.previousFocus = null;
  }
  ngOnInit() {
    this.openSubscription = this.paletteService.openRequests.subscribe(() => {
      this.show();
      this.cdr.markForCheck();
    });
  }
  ngOnDestroy() {
    this.openSubscription.unsubscribe();
    this.fetchSubscription?.unsubscribe();
    this.restartSubscription?.unsubscribe();
  }
  onDocumentKey(e) {
    const target = e.target;
    const inTextField = !!target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName));
    if (isPaletteShortcut(e, this.mac, inTextField)) {
      if (this.open) {
        e.preventDefault();
        this.close();
      } else if (this.enabled()) {
        e.preventDefault();
        this.show();
      }
    } else if (this.open && e.key === "Escape") {
      e.preventDefault();
      this.close();
    }
  }
  onInput(e) {
    this.query = e.target.value;
    this.refresh();
  }
  onInputKey(e) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (this.flat.length) {
        const step = e.key === "ArrowDown" ? 1 : -1;
        this.selected = (this.selected + step + this.flat.length) % this.flat.length;
        this.cdr.detectChanges();
        document.getElementById("palette-item-" + this.selected)?.scrollIntoView({ block: "nearest" });
      }
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (this.flat[this.selected]) {
        this.run(this.flat[this.selected]);
      }
    }
  }
  // A list opening under a still pointer fires mousemove too, so only a real move selects.
  hover(index, e) {
    const pos = e.screenX + "," + e.screenY;
    if (this.lastPointer !== void 0 && this.lastPointer !== pos) {
      this.selected = index;
    }
    this.lastPointer = pos;
  }
  close() {
    this.open = false;
    this.cdr.markForCheck();
    if (this.previousFocus && document.contains(this.previousFocus)) {
      this.previousFocus.focus();
    }
    this.previousFocus = null;
  }
  run(item) {
    this.previousFocus = null;
    this.close();
    if (item.theme) {
      this.themeService.setChoice(item.theme);
    } else if (item.action && item.visor) {
      this.runAction(item);
    } else if (item.externalUrl) {
      window.open(item.externalUrl, "_blank", "noopener noreferrer");
    } else if (item.link) {
      this.router.navigate(item.link);
    }
  }
  // The embedded single-tab views, the VPN client and the login page have no use for it.
  enabled() {
    const url = this.router.url || "";
    return !window.location.href.includes("embed=1") && !url.includes("/vpn/") && !url.includes("vpnlogin") && !url.includes("login");
  }
  show() {
    if (!this.enabled()) {
      return;
    }
    this.previousFocus = document.activeElement;
    this.open = true;
    this.query = "";
    this.lastPointer = void 0;
    this.currentPk = visorPkFromUrl(this.router.url);
    this.loadVisors();
    this.refresh();
    setTimeout(() => this.searchInput?.nativeElement.focus());
  }
  loadVisors() {
    const data = this.multipleNodeDataService.lastEmitedData;
    let nodes = [];
    if (data && data.sections && data.sections.length) {
      data.sections.forEach((s) => nodes = nodes.concat(s.nodes || []));
    } else if (data && Array.isArray(data.data)) {
      nodes = data.data;
    }
    if (nodes.length) {
      this.setVisors(nodes);
    }
    if (this.visors.length === 0 && !this.fetchSubscription) {
      this.fetchSubscription = this.nodeService.getNodes().pipe(take(1)).subscribe({
        next: (list) => {
          this.setVisors(list);
          this.refresh();
          this.cdr.markForCheck();
        },
        error: () => {
          this.fetchSubscription = void 0;
          this.cdr.markForCheck();
        },
        complete: () => {
          this.fetchSubscription = void 0;
          this.cdr.markForCheck();
        }
      });
    }
  }
  setVisors(nodes) {
    const seen = /* @__PURE__ */ new Set();
    this.visors = (Array.isArray(nodes) ? nodes : []).filter((n) => n && n.localPk && !seen.has(n.localPk) && !!seen.add(n.localPk)).map(paletteVisor);
  }
  refresh() {
    let current = this.visors.find((v) => v.pk === this.currentPk);
    if (!current && this.currentPk) {
      current = { pk: this.currentPk, label: "" };
    }
    const entries = paletteEntries({
      visors: this.visors,
      current,
      homeTabs: homeTabsData(),
      tabsFor: (v) => nodePaletteTabs(v.pk, !!v.wasm, isRemoteVisor(v.pk, this.storageService.getLocalHypervisorPk())),
      translate: (k) => this.translate.instant(k),
      themeChoice: this.themeService.choice
    }, paletteTokens(this.query));
    const ranked = rankPalette(entries, this.query);
    this.flat = [];
    this.groups = ranked.map((g) => {
      const items = g.items.map((item) => {
        this.flat.push(item);
        return { item, index: this.flat.length - 1 };
      });
      return { group: g.group, items };
    });
    this.selected = 0;
  }
  runAction(item) {
    const v = item.visor;
    const name = v.label || v.pk;
    if (item.action === "copy-key") {
      if (this.clipboardService.copy(v.pk)) {
        this.snackbarService.showDone("copy.copied");
      }
    } else if (item.action === "terminal") {
      window.open(ptyUrl(v.pk), "_blank", "noopener noreferrer");
    } else if (item.action === "logs") {
      this.router.navigate(["/nodes", v.pk, "logs"]);
    } else if (item.action === "restart") {
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, this.translate.instant("palette.restart-confirmation", { name }));
      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.close();
        this.restartSubscription = this.nodeService.restart(v.pk).subscribe({
          next: () => {
            this.snackbarService.showDone(this.translate.instant("nodes.bulk.restarting", { name }));
            this.cdr.markForCheck();
          },
          error: () => {
            this.snackbarService.showError(this.translate.instant("nodes.bulk.restart-error", { name }));
            this.cdr.markForCheck();
          }
        });
        this.cdr.markForCheck();
      });
    } else if (item.action === "update") {
      const data = { key: v.pk, label: name, version: v.version || "", tag: v.buildTag || "" };
      const updatable = GeneralUtils.checkIfTagIsUpdatable(data.tag);
      UpdateAllComponent.openDialog(this.dialog, updatable ? [data] : [], updatable ? [] : [data]);
    } else if (item.action === "reward") {
      BulkRewardAddressChangerComponent.openDialog(this.dialog, { nodes: [{ key: v.pk, label: name }] });
    }
  }
  static {
    this.ctorParameters = () => [
      { type: Router },
      { type: MatDialog },
      { type: TranslateService },
      { type: CommandPaletteService },
      { type: MultipleNodeDataService },
      { type: NodeService },
      { type: ClipboardService },
      { type: SnackbarService },
      { type: ChangeDetectorRef },
      { type: ThemeService },
      { type: StorageService }
    ];
  }
  static {
    this.propDecorators = {
      searchInput: [{ type: ViewChild, args: ["searchInput"] }],
      onDocumentKey: [{ type: HostListener, args: ["document:keydown", ["$event"]] }]
    };
  }
};
CommandPaletteComponent = __decorate2([
  Component({
    selector: "app-command-palette",
    template: command_palette_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [command_palette_component_default2]
  })
], CommandPaletteComponent);

// src/app/utils/command-palette.spec.ts
var pkA = "02" + "a".repeat(64);
var pkB = "03" + "b".repeat(64);
var pkC = "03" + "c".repeat(64);
var labels = {
  "node.tabs.routing": "Routing",
  "node.tabs.info": "Info",
  "node.tabs.logs": "Logs",
  "node.tabs.transports": "Transports",
  "nodes.transports-title": "Transports",
  "palette.actions.restart": "Restart",
  "palette.actions.copy-key": "Copy public key"
};
function ctx(current) {
  return {
    visors: [
      { pk: pkA, label: "magnetosphere", ip: "203.0.113.7", place: "Dallas, TX, US", online: true },
      { pk: pkB, label: "mainframe", online: true },
      { pk: pkC, label: "pi-relay", online: false }
    ],
    current,
    homeTabs: homeTabsData(),
    tabsFor: (v) => nodeTabsData(v.pk, !!v.wasm),
    translate: (k) => labels[k] || k
  };
}
function rank(query, current) {
  return rankPalette(paletteEntries(ctx(current), paletteTokens(query)), query);
}
describe("command palette matcher", () => {
  it("matches case-insensitively and ranks exact, prefix, word, inner, then in-order letters", () => {
    const exact = matchScore("routing", "Routing");
    const prefix = matchScore("rout", "Routing");
    const word = matchScore("rout", "Mesh Routing");
    const inner = matchScore("out", "Routing");
    const letters = matchScore("rtng", "Routing");
    expect(exact).toBeGreaterThan(prefix);
    expect(prefix).toBeGreaterThan(word);
    expect(word).toBeGreaterThan(inner);
    expect(inner).toBeGreaterThan(letters);
    expect(letters).toBeGreaterThan(0);
    expect(matchScore("xyz", "Routing")).toBe(0);
  });
  it("matches a public key only by substring", () => {
    expect(matchScore("aaaa", pkA)).toBeGreaterThan(0);
    expect(matchScore("a2a", pkA)).toBe(0);
  });
});
describe("command palette ranking", () => {
  it("finds a visor by label, full key, ip and place, and shows the full key", () => {
    for (const q of ["magneto", pkA, "203.0.113", "dallas"]) {
      const top = rank(q)[0];
      expect(top.group).toBe("visors");
      expect(top.items[0].title).toBe("magnetosphere");
      expect(top.items[0].detail).toContain(pkA);
      expect(top.items[0].link).toEqual(["/nodes", pkA, "info"]);
    }
  });
  it("puts the closest match first", () => {
    expect(rank("ma")[0].items.map((i) => i.title)).toEqual(["mainframe", "magnetosphere"]);
    expect(rank("frame")[0].items.map((i) => i.title)).toEqual(["mainframe"]);
  });
  it("offers a visor tab for a visor name plus a tab", () => {
    const top = rank("magnetosphere routing")[0];
    expect(top.group).toBe("pages");
    expect(top.items[0].title).toBe("magnetosphere \u203A Routing");
    expect(top.items[0].link).toEqual(["/nodes", pkA, "routing"]);
    expect(top.items[0].detail).toBe(pkA);
  });
  it("offers the open visor tabs and actions without naming the visor", () => {
    const current = ctx().visors[1];
    const tab = rank("routing", current)[0];
    expect(tab.items[0].link).toEqual(["/nodes", pkB, "routing"]);
    const action = rank("restart", current)[0];
    expect(action.group).toBe("actions");
    expect(action.items[0].action).toBe("restart");
    expect(action.items[0].visor.pk).toBe(pkB);
  });
  it("offers actions on a named visor but not restart on an offline one", () => {
    const top = rank("copy magnetosphere")[0];
    expect(top.items[0].action).toBe("copy-key");
    expect(top.items[0].visor.pk).toBe(pkA);
    const offline = rank("restart pi-relay");
    expect(offline.some((g) => g.items.some((i) => i.action === "restart"))).toBeFalse();
  });
  it("leads with the open visor tabs and actions for an empty query", () => {
    expect(rank("").map((g) => g.group)).toEqual(["visors", "pages"]);
    const groups = rank("", ctx().visors[0]);
    expect(groups.map((g) => g.group)).toEqual(["pages", "actions", "visors"]);
    expect(groups[0].items[0].link).toEqual(["/nodes", pkA, "info"]);
  });
});
describe("command palette shortcut", () => {
  const key = (init) => new KeyboardEvent("keydown", init);
  it("opens on Ctrl-K, and on Cmd-K on a Mac", () => {
    expect(isPaletteShortcut(key({ key: "k", ctrlKey: true }), false, false)).toBeTrue();
    expect(isPaletteShortcut(key({ key: "k", ctrlKey: true }), false, true)).toBeTrue();
    expect(isPaletteShortcut(key({ key: "K", metaKey: true }), true, true)).toBeTrue();
  });
  it("leaves ordinary typing and the Mac text field Ctrl-K alone", () => {
    expect(isPaletteShortcut(key({ key: "k" }), false, true)).toBeFalse();
    expect(isPaletteShortcut(key({ key: "k", ctrlKey: true, shiftKey: true }), false, false)).toBeFalse();
    expect(isPaletteShortcut(key({ key: "k", ctrlKey: true }), true, true)).toBeFalse();
    expect(isPaletteShortcut(key({ key: "j", ctrlKey: true }), false, false)).toBeFalse();
  });
  it("reads the open visor from the url", () => {
    expect(visorPkFromUrl("/nodes/" + pkA + "/routing")).toBe(pkA);
    expect(visorPkFromUrl("/nodes/" + pkA)).toBe(pkA);
    expect(visorPkFromUrl("/nodes/list/1")).toBeUndefined();
  });
});
describe("command palette node pages", () => {
  it("reaches the app catalog without adding it to the tab row", () => {
    expect(nodeTabsData(pkA, false).some((t) => t.label === "node.tabs.app-catalog")).toBeFalse();
    const c = ctx({ pk: pkA, label: "magnetosphere" });
    c.tabsFor = (v) => nodePaletteTabs(v.pk, !!v.wasm);
    c.translate = (k) => k === "node.tabs.app-catalog" ? "App catalog" : labels[k] || k;
    const top = rankPalette(paletteEntries(c, paletteTokens("catalog")), "catalog")[0];
    expect(top.items[0].title).toBe("App catalog");
    expect(top.items[0].link).toEqual(["/nodes", pkA, "apps", "catalog"]);
  });
});
describe("command palette theme", () => {
  const themed = (query) => rankPalette(paletteEntries(__spreadProps(__spreadValues({}, ctx()), { themeChoice: "system" }), paletteTokens(query)), query);
  it("offers the three theme choices and marks the current one", () => {
    const actions = themed("theme").find((g) => g.group === "actions");
    expect(actions.items.map((i) => i.theme).sort()).toEqual(["dark", "light", "system"]);
    expect(actions.items.find((i) => i.theme === "system").detail).toBe("palette.theme.current");
    expect(themed("dark")[0].items[0].theme).toBe("dark");
  });
  it("keeps the theme out of the empty query", () => {
    expect(themed("").some((g) => g.items.some((i) => !!i.theme))).toBeFalse();
  });
});
//# debugId=ad1929f1-2e6c-5336-a7bc-0ffdcda13c75
//# sourceMappingURL=spec-app-utils-command-palette.spec.js.map
