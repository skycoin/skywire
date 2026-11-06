import {
  ChangeDetectionStrategy,
  Component,
  Input
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// angular:jit:template:src/app/components/pages/node/node-info/visor-overview/visor-overview.component.html
var visor_overview_component_default = `@if (node) {
  <div class="overview">
    <div class="ov-card">
      <span class="ov-title">{{ 'node.overview.health' | translate }}</span>
      <span class="ov-big" [class]="healthClass(node.health?.servicesHealth)">
        {{ 'node.overview.health-' + healthClass(node.health?.servicesHealth) | translate }}
      </span>
      <span class="ov-row"><span class="dot" [class]="healthClass(node.health?.uptimeTrackerHealth)"></span>{{ 'node.overview.uptime-tracker' | translate }}</span>
      <span class="ov-row"><span class="dot" [class]="healthClass(node.health?.autoconnectHealth)"></span>{{ 'node.overview.autoconnect' | translate }}</span>
      <span class="ov-row"><span class="dot" [class]="healthClass(node.health?.transportabilityHealth)"></span>{{ 'node.overview.transportability' | translate }}</span>
    </div>

    <a class="ov-card link" [routerLink]="['/nodes', node.localPk, 'transports']">
      <span class="ov-title">{{ 'node.overview.transports' | translate }}</span>
      <span class="ov-big">{{ node.transports?.length || 0 }}
        @if (knowsDirection) {
          <span class="ov-small"><span class="in">{{ incoming }}&#8595;</span> <span class="out">{{ outgoing }}&#8593;</span></span>
        }
      </span>
      @for (t of transportTypes.slice(0, 4); track t.type) {
        <span class="ov-row"><span class="ov-key">{{ t.type }}</span>{{ t.count }}</span>
      }
      @if (transportTypes.length > 4) {
        <span class="ov-row dim">{{ 'node.overview.more-types' | translate: { n: transportTypes.length - 4 } }}</span>
      }
    </a>

    <a class="ov-card link" [routerLink]="['/nodes', node.localPk, 'routing']">
      <span class="ov-title">{{ 'node.overview.network' | translate }}</span>
      <span class="ov-row"><span class="ov-key">{{ 'node.overview.dmsg-servers' | translate }}</span>{{ node.dmsgServers?.length || 0 }}</span>
      <span class="ov-row"><span class="ov-key">{{ 'node.overview.routes' | translate }}</span>{{ node.routesCount || 0 }}</span>
      <span class="ov-row"><span class="ov-key">{{ 'node.overview.min-hops' | translate }}</span>{{ node.minHops || 0 }}</span>
      <span class="ov-row"><span class="ov-key">{{ 'node.overview.public' | translate }}</span>{{ (node.isPublic ? 'common.yes' : 'common.no') | translate }}</span>
    </a>

    <a class="ov-card link" [routerLink]="['/nodes', node.localPk, 'apps']">
      <span class="ov-title">{{ 'node.overview.apps' | translate }}</span>
      <span class="ov-big">{{ appsRunning }}<span class="ov-small"> / {{ node.apps?.length || 0 }} {{ 'node.overview.running' | translate }}</span></span>
      <span class="ov-row"><span class="ov-key">{{ 'node.overview.autostart' | translate }}</span>{{ appsAutostart }}</span>
      <span class="ov-row" [class.bad]="appsFailed > 0"><span class="ov-key">{{ 'node.overview.failed' | translate }}</span>{{ appsFailed }}</span>
    </a>

    <div class="ov-card">
      <a class="ov-title link" [routerLink]="['/nodes', node.localPk, 'rewards']">{{ 'node.overview.rewards' | translate }} &rsaquo;</a>
      @if (!node.rewardsAddress) {
        <span class="ov-big wait">{{ 'node.overview.reward-not-set' | translate }}</span>
      } @else if (node.rewardEligible === false) {
        <span class="ov-big bad">{{ 'node.overview.reward-ineligible' | translate }}</span>
      } @else {
        <span class="ov-big ok">{{ 'node.overview.reward-set' | translate }}</span>
      }
      <a class="ov-row sub" [routerLink]="['/nodes', node.localPk, 'bandwidth']">{{ 'node.overview.bandwidth' | translate }} &rsaquo;</a>
      <a class="ov-row sub" [routerLink]="['/nodes', node.localPk, 'uptime']">{{ 'node.overview.uptime' | translate }} &rsaquo;</a>
    </div>
  </div>

  <div class="alerts">
    @if (alerts.length === 0) {
      <span class="alert ok">{{ 'node.overview.no-alerts' | translate }}</span>
    }
    @for (a of alerts; track a.text + ($index)) {
      <span class="alert" [class.warn]="a.level === 'warn'">{{ a.text | translate: a.params }}</span>
    }
  </div>
}
`;

// angular:jit:style:src/app/components/pages/node/node-info/visor-overview/visor-overview.component.scss
var visor_overview_component_default2 = "/* src/app/components/pages/node/node-info/visor-overview/visor-overview.component.scss */\n.overview {\n  display: grid;\n  grid-template-columns: repeat(auto-fit, minmax(min(190px, 100%), 1fr));\n  gap: 12px;\n  margin-top: 16px;\n}\n.ov-card {\n  background: rgba(var(--sw-cyan-rgb), 0.03);\n  border: 1px solid rgba(var(--sw-cyan-rgb), 0.12);\n  border-radius: 8px;\n  padding: 12px 16px;\n  display: flex;\n  flex-direction: column;\n  gap: 4px;\n  min-width: 0;\n  color: inherit;\n  text-decoration: none;\n}\n.ov-card.link:hover {\n  border-color: rgba(var(--sw-cyan-rgb), 0.4);\n}\n.ov-title {\n  font-weight: bold;\n  text-transform: uppercase;\n  letter-spacing: 1px;\n  font-size: 0.8em;\n  color: rgb(var(--sw-cyan-rgb));\n  margin-bottom: 4px;\n  text-decoration: none;\n}\n.ov-big {\n  font-size: 1.4em;\n  font-weight: 600;\n}\n.ov-small {\n  font-size: 0.6em;\n  font-weight: normal;\n  opacity: 0.85;\n}\n.ov-row {\n  font-size: 0.85em;\n  display: flex;\n  align-items: center;\n  gap: 6px;\n  color: inherit;\n  text-decoration: none;\n}\n.ov-row.sub:hover {\n  text-decoration: underline;\n}\n.ov-key {\n  opacity: 0.7;\n  min-width: 70px;\n}\n.dim {\n  opacity: 0.7;\n}\n.in {\n  color: rgb(var(--sw-green-rgb));\n}\n.out {\n  color: rgb(var(--sw-sky-rgb));\n}\n.ok {\n  color: rgb(var(--sw-green-rgb));\n}\n.bad {\n  color: rgb(var(--sw-red-alt-rgb));\n}\n.wait {\n  color: rgb(var(--sw-amber-rgb));\n}\n.dot {\n  width: 8px;\n  height: 8px;\n  border-radius: 50%;\n  flex: none;\n  background: currentColor;\n}\n.alerts {\n  display: flex;\n  flex-direction: column;\n  gap: 4px;\n  margin-top: 12px;\n}\n.alerts .alert {\n  font-size: 0.85em;\n  padding: 6px 10px;\n  border-radius: 4px;\n  border-left: 3px solid rgba(var(--sw-fg-rgb), 0.35);\n  background: rgba(var(--sw-fg-rgb), 0.04);\n}\n.alerts .alert.warn {\n  border-left-color: rgb(var(--sw-amber-rgb));\n  background: rgba(255, 183, 77, 0.1);\n}\n.alerts .alert.ok {\n  border-left-color: rgb(var(--sw-green-rgb));\n  color: inherit;\n}\n@media (max-width: 767.98px) {\n  a.ov-title,\n  a.ov-row {\n    display: flex;\n    align-items: center;\n    min-height: 40px;\n  }\n}\n/*# sourceMappingURL=visor-overview.component.css.map */\n";

// src/app/components/pages/node/node-info/visor-overview/visor-overview.component.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var appRunning = 1;
var appConnecting = 3;
var appFailed = 2;
var healthGraceSeconds = 600;
function sameRelease(version, configVersion) {
  const release = (v) => /^v?(\d+\.\d+\.\d+)/.exec(v)?.[1];
  const a = release(version);
  const b = release(configVersion);
  return a && b ? a === b : version.startsWith(configVersion);
}
function overviewAlerts(node) {
  const alerts = [];
  if (node.connectivity === "offline") {
    alerts.push({ level: "warn", text: "node.overview.alert-offline" });
  } else if (node.connectivity === "dmsg-only") {
    alerts.push({ level: "info", text: "node.overview.alert-dmsg-only" });
  }
  const health = node.health || {};
  const settled = (node.secondsOnline || 0) >= healthGraceSeconds;
  const checks = [
    [health.uptimeTrackerHealth, "node.overview.alert-uptime-tracker"],
    [health.autoconnectHealth, "node.overview.alert-autoconnect"],
    [health.transportabilityHealth, "node.overview.alert-transportability"]
  ];
  checks.forEach(([state, text]) => {
    if (state && state !== "healthy") {
      alerts.push({ level: settled ? "warn" : "info", text });
    }
  });
  (node.apps || []).forEach((app) => {
    if (app.status === appFailed) {
      alerts.push({ level: "warn", text: "node.overview.alert-app-failed", params: { app: app.name } });
    } else if (app.autostart && app.status !== appRunning && app.status !== appConnecting) {
      const why = (app.detailedStatus || "").trim();
      alerts.push(why ? { level: "warn", text: "node.overview.alert-app-stopped-why", params: { app: app.name, why } } : { level: "warn", text: "node.overview.alert-app-stopped", params: { app: app.name } });
    }
  });
  if (node.transports && node.transports.length === 0) {
    alerts.push({ level: "warn", text: "node.overview.alert-no-transports" });
  }
  if (node.dmsgServers && node.dmsgServers.length === 0) {
    alerts.push({ level: "warn", text: "node.overview.alert-no-dmsg" });
  }
  if (!node.rewardsAddress) {
    alerts.push({ level: "info", text: "node.overview.alert-no-reward" });
  } else if (node.rewardEligible === false) {
    alerts.push({
      level: "warn",
      text: "node.overview.alert-reward-ineligible",
      params: { reason: node.rewardIneligibleReason || "-" }
    });
  }
  if (node.version && node.configVersion && !sameRelease(node.version, node.configVersion)) {
    alerts.push({ level: "info", text: "node.overview.alert-config-version", params: { config: node.configVersion } });
  }
  if (node.isSymmeticNat) {
    alerts.push({ level: "info", text: "node.overview.alert-symmetric-nat" });
  }
  return alerts;
}
var VisorOverviewComponent = class VisorOverviewComponent2 {
  constructor() {
    this.alerts = [];
    this.transportTypes = [];
    this.incoming = 0;
    this.outgoing = 0;
    this.knowsDirection = false;
    this.appsRunning = 0;
    this.appsAutostart = 0;
    this.appsFailed = 0;
  }
  ngOnChanges() {
    if (!this.node) {
      return;
    }
    this.alerts = overviewAlerts(this.node);
    const byType = {};
    this.incoming = 0;
    this.outgoing = 0;
    (this.node.transports || []).forEach((t) => {
      const type = (t.type || "?").toUpperCase();
      const entry = byType[type] || (byType[type] = { type, count: 0, incoming: 0, outgoing: 0 });
      entry.count++;
      if (t.initiator) {
        entry.outgoing++;
        this.outgoing++;
      } else {
        entry.incoming++;
        this.incoming++;
      }
    });
    this.transportTypes = Object.values(byType).sort((a, b) => b.count - a.count);
    this.knowsDirection = (this.node.transports || []).some((t) => t.initiator !== void 0);
    const apps = this.node.apps || [];
    this.appsRunning = apps.filter((a) => a.status === appRunning).length;
    this.appsAutostart = apps.filter((a) => a.autostart).length;
    this.appsFailed = apps.filter((a) => a.status === appFailed).length;
  }
  /** The css class for a health value: healthy, unhealthy or still connecting. */
  healthClass(state) {
    return state === "healthy" ? "ok" : state === "unhealthy" ? "bad" : "wait";
  }
  static {
    this.propDecorators = {
      node: [{ type: Input }]
    };
  }
};
VisorOverviewComponent = __decorate([
  Component({
    selector: "app-visor-overview",
    template: visor_overview_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [visor_overview_component_default2]
  })
], VisorOverviewComponent);

// src/app/components/pages/node/node-info/visor-overview/visor-overview.component.spec.ts
describe("visor overview alerts", () => {
  const base = () => ({
    localPk: "02aa",
    label: "x",
    version: "v1.3.103",
    configVersion: "v1.3.103",
    rewardsAddress: "xpub",
    secondsOnline: 3600,
    health: { servicesHealth: "healthy", uptimeTrackerHealth: "healthy", autoconnectHealth: "healthy", transportabilityHealth: "healthy" },
    apps: [],
    transports: [{ type: "stcpr", initiator: true }],
    dmsgServers: [{}]
  });
  const texts = (n) => overviewAlerts(n).map((a) => a.text);
  it("reports nothing for a healthy visor", () => {
    expect(overviewAlerts(base())).toEqual([]);
  });
  it("warns about a check still connecting only after the grace time", () => {
    const n = base();
    n.health.uptimeTrackerHealth = "connecting";
    expect(overviewAlerts(n)[0].level).toBe("warn");
    n.secondsOnline = 60;
    expect(overviewAlerts(n)[0].level).toBe("info");
  });
  it("names failed apps and autostart apps that are not running", () => {
    const n = base();
    n.apps = [
      { name: "vpn-client", status: 2, autostart: false },
      { name: "skychat", status: 0, autostart: true },
      { name: "skysocks", status: 1, autostart: true }
    ];
    const alerts = overviewAlerts(n);
    expect(alerts.map((a) => a.params?.["app"])).toEqual(["vpn-client", "skychat"]);
    expect(alerts[1].text).toBe("node.overview.alert-app-stopped");
  });
  it("says why a stopped autostart app is not running when it reports a reason", () => {
    const n = base();
    const why = "port 25 is served by the host's mail server (`serve add 25`)";
    n.apps = [{ name: "skymail", status: 0, autostart: true, detailedStatus: why }];
    const alerts = overviewAlerts(n);
    expect(alerts.length).toBe(1);
    expect(alerts[0].text).toBe("node.overview.alert-app-stopped-why");
    expect(alerts[0].params).toEqual({ app: "skymail", why });
  });
  it("flags a missing reward address, no transports and no dmsg", () => {
    const n = base();
    n.rewardsAddress = "";
    n.transports = [];
    n.dmsgServers = [];
    expect(texts(n)).toEqual(jasmine.arrayContaining([
      "node.overview.alert-no-reward",
      "node.overview.alert-no-transports",
      "node.overview.alert-no-dmsg"
    ]));
  });
  it("says the visor is offline when it reports so", () => {
    const n = base();
    n.connectivity = "offline";
    expect(texts(n)[0]).toBe("node.overview.alert-offline");
    n.connectivity = "dmsg-only";
    expect(texts(n)[0]).toBe("node.overview.alert-dmsg-only");
    n.connectivity = "online";
    expect(texts(n)).not.toContain("node.overview.alert-offline");
  });
});
describe("visor overview config version", () => {
  it("treats a commit suffix as the same release", () => {
    expect(sameRelease("v1.3.103-122-g7845bbc81-7845bbc81358", "v1.3.103-122-g7845bbc81")).toBeTrue();
    expect(sameRelease("develop-latest-tinygo-cores-7845bbc81358", "develop-latest-tinygo-cores")).toBeTrue();
  });
  it("flags a config from another release", () => {
    expect(sameRelease("v1.3.104", "v1.3.99")).toBeFalse();
    expect(sameRelease("develop-a", "develop-b")).toBeFalse();
  });
});
//# debugId=50ea7d62-a9e0-54fe-a75e-8ddf55ad07ad
//# sourceMappingURL=spec-app-components-pages-node-node-info-visor-overview-visor-overview.component.spec.js.map
