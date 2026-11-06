import {
  AppFlagsFormComponent,
  AppSettingsComponent,
  ProxyDiscoveryService,
  SelectProxyServerComponent,
  latencyByPk,
  sortServers
} from "./chunk-LMOBANXL.js";
import {
  serverTypeFor
} from "./chunk-Z3QUGWOD.js";
import "./chunk-BGNEFYT6.js";
import "./chunk-XSOC4ELV.js";
import {
  TestBed
} from "./chunk-UQCSELO5.js";
import "./chunk-ED5WCGEB.js";
import {
  NodeComponent
} from "./chunk-CIWHQAPM.js";
import "./chunk-KWVFHEEW.js";
import "./chunk-HLYS7KBE.js";
import {
  FormsModule,
  ReactiveFormsModule
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
  TranslatePipe,
  provideTranslateService
} from "./chunk-6WNZTOXO.js";
import "./chunk-GPHL7TNH.js";
import "./chunk-H3STL4JE.js";
import "./chunk-XBRWDJ2O.js";
import {
  SnackbarService
} from "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import "./chunk-XOUXCPTJ.js";
import "./chunk-VNMLTXOX.js";
import {
  NO_ERRORS_SCHEMA,
  Subject,
  of
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/components/pages/node/skysocks-tab/select-proxy-server/select-proxy-server.component.spec.ts
var pk = (c) => c.repeat(66);
var NODE = pk("0");
var A = pk("a");
var B = pk("b");
var C = pk("c");
var entry = (key, country, location) => {
  return { pk: key, address: key, port: "", country, location };
};
var servers = () => [entry(A, "DE", "Germany"), entry(B, "US", "United States"), entry(C, "DE", "Germany")];
var transports = () => [
  { id: "1", localPk: NODE, remotePk: C, type: "stcpr", recv: 0, sent: 0, latencyMs: 40 },
  { id: "2", localPk: NODE, remotePk: B, type: "stcpr", recv: 0, sent: 0, latencyMs: 90 },
  { id: "3", localPk: NODE, remotePk: B, type: "sudph", recv: 0, sent: 0, latencyMs: 25 },
  { id: "4", localPk: NODE, remotePk: A, type: "dmsg", recv: 0, sent: 0, latencyMs: 0 }
];
describe("server picker helpers", () => {
  it("maps client apps to their server type", () => {
    expect(serverTypeFor("skysocks-client")).toBe("proxy");
    expect(serverTypeFor("vpn-client-2")).toBe("vpn");
    expect(serverTypeFor("my-exit", "vpn-client")).toBe("vpn");
    expect(serverTypeFor("skynet-client")).toBe("");
    expect(serverTypeFor("skysocks")).toBe("");
  });
  it("sorts measured servers by their fastest transport, then by location", () => {
    const lat = latencyByPk(transports());
    expect(lat.get(B)).toBe(25);
    expect(lat.has(A)).toBeFalse();
    expect(sortServers(servers(), lat).map((s) => s.pk)).toEqual([B, C, A]);
  });
});
describe("ProxyDiscoveryService", () => {
  it("keeps only visors offering the service, with proxies filed as skysocks", () => {
    const body = {
      [A]: { pk: A, services: ["skysocks", "vpn"], country: "DE" },
      [B]: { pk: B, services: ["visor"], country: "US" },
      [C]: { pk: C, services: [], country: "DE" }
    };
    const http = { get: () => of(body) };
    const svc = new ProxyDiscoveryService(http);
    let proxies = [];
    let vpns = [];
    svc.getServices(true).subscribe((l) => proxies = l);
    svc.getServices(false).subscribe((l) => vpns = l);
    expect(proxies.map((s) => s.pk)).toEqual([A]);
    expect(vpns.map((s) => s.pk)).toEqual([A]);
  });
});
describe("SelectProxyServerComponent", () => {
  let fixture;
  let comp;
  let ref;
  let discovery;
  const rows = () => Array.from(fixture.nativeElement.querySelectorAll(".sps-row"));
  beforeEach(() => {
    ref = jasmine.createSpyObj("MatDialogRef", ["close"]);
    discovery = jasmine.createSpyObj("ProxyDiscoveryService", ["getServices"]);
    discovery.getServices.and.returnValue(of(servers()));
    TestBed.configureTestingModule({
      declarations: [SelectProxyServerComponent],
      imports: [TranslatePipe, FormsModule],
      providers: [
        provideTranslateService(),
        { provide: MatDialogRef, useValue: ref },
        { provide: MAT_DIALOG_DATA, useValue: { clientName: "vpn-client", serviceType: "vpn", transports: transports() } },
        { provide: ProxyDiscoveryService, useValue: discovery },
        { provide: SnackbarService, useValue: jasmine.createSpyObj("SnackbarService", ["showError"]) }
      ],
      schemas: [NO_ERRORS_SCHEMA]
    });
    fixture = TestBed.createComponent(SelectProxyServerComponent);
    comp = fixture.componentInstance;
    fixture.detectChanges();
  });
  it("lists the requested type sorted by latency with a country filter", () => {
    expect(discovery.getServices).toHaveBeenCalledWith(false);
    expect(rows().map((r) => r.getAttribute("data-pk"))).toEqual([B, C, A]);
    expect(rows()[0].querySelector(".sps-latency").textContent).toContain("25 ms");
    expect(rows()[2].querySelector(".sps-latency")).toBeNull();
    expect(comp.countries).toEqual([
      { code: "DE", name: "Germany", count: 2 },
      { code: "US", name: "United States", count: 1 }
    ]);
  });
  it("filters by country and by search text", () => {
    const pick = (code) => {
      const sel = fixture.nativeElement.querySelector(".sps-country select");
      sel.value = Array.from(sel.options).find((o) => o.value.endsWith(code)).value;
      sel.dispatchEvent(new Event("change"));
      fixture.detectChanges();
    };
    const search = (text) => {
      const box = fixture.nativeElement.querySelector(".sps-filter input");
      box.value = text;
      box.dispatchEvent(new Event("input"));
      fixture.detectChanges();
    };
    fixture.detectChanges();
    pick("DE");
    expect(comp.country).toBe("DE");
    expect(rows().map((r) => r.getAttribute("data-pk"))).toEqual([C, A]);
    search("aaaa");
    expect(rows().map((r) => r.getAttribute("data-pk"))).toEqual([A]);
    pick("US");
    expect(rows().length).toBe(0);
  });
  it("returns the chosen key", () => {
    rows()[1].click();
    expect(ref.close).toHaveBeenCalledWith(C);
  });
});
describe("choosing a server from the flags form", () => {
  const srv = { name: "srv", type: "string", default: "", usage: "server", check: "pk", set: false };
  let dialog;
  let picked;
  beforeEach(() => {
    picked = new Subject();
    dialog = jasmine.createSpyObj("MatDialog", ["open"]);
    dialog.open.and.returnValue({ afterClosed: () => picked.asObservable() });
    const apps = jasmine.createSpyObj("AppsService", ["getAppFlags", "setAppFullConfig"]);
    apps.getAppFlags.and.returnValue(of({ app: "vpn-client", source: "registry", flags: [srv] }));
    apps.setAppFullConfig.and.returnValue(of({}));
    spyOnProperty(NodeComponent, "currentNode", "get").and.returnValue(of({ localPk: NODE, transports: transports() }));
    TestBed.configureTestingModule({
      declarations: [AppFlagsFormComponent, AppSettingsComponent],
      imports: [TranslatePipe, ReactiveFormsModule],
      providers: [
        provideTranslateService(),
        { provide: AppsService, useValue: apps },
        { provide: MatDialog, useValue: dialog },
        { provide: SnackbarService, useValue: jasmine.createSpyObj("SnackbarService", ["showDone"]) }
      ],
      schemas: [NO_ERRORS_SCHEMA]
    });
  });
  it("writes the key into the server flag and the args", () => {
    const fixture = TestBed.createComponent(AppFlagsFormComponent);
    const comp = fixture.componentInstance;
    comp.appName = "vpn-client";
    comp.args = "--killswitch";
    comp.serverType = "vpn";
    const args = [];
    const chosen = [];
    comp.argsChange.subscribe((a) => args.push(a));
    comp.serverChosen.subscribe((p) => chosen.push(p));
    fixture.detectChanges();
    const button = fixture.nativeElement.querySelector('[data-flag="srv"] .flag-pick');
    button.click();
    expect(dialog.open.calls.mostRecent().args[1].data).toEqual(jasmine.objectContaining({ clientName: "vpn-client", serviceType: "vpn" }));
    picked.next(B);
    fixture.detectChanges();
    expect(args[args.length - 1]).toBe(`--killswitch --srv=${B}`);
    expect(chosen).toEqual([B]);
    expect(fixture.nativeElement.querySelector('[data-flag="srv"] input').value).toBe(B);
  });
  it("shows no picker for an app without a server type", () => {
    const fixture = TestBed.createComponent(AppFlagsFormComponent);
    fixture.componentInstance.appName = "skynet-client";
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector(".flag-pick")).toBeNull();
  });
  const settings = (status) => {
    const fixture = TestBed.createComponent(AppSettingsComponent);
    const comp = fixture.componentInstance;
    comp.app = { name: "vpn-client", args: [], status };
    fixture.detectChanges();
    comp.onFlagArgs(`--srv=${B}`);
    return comp;
  };
  it("offers to save and restart a running client", () => {
    const accepted = new Subject();
    const confirm = { componentInstance: { operationAccepted: accepted }, close: jasmine.createSpy("close") };
    dialog.open.and.returnValue(confirm);
    const apps = TestBed.inject(AppsService);
    const comp = settings(1);
    comp.onServerChosen();
    expect(dialog.open).toHaveBeenCalledTimes(1);
    expect(apps.setAppFullConfig).not.toHaveBeenCalled();
    accepted.next();
    expect(confirm.close).toHaveBeenCalled();
    expect(apps.setAppFullConfig).toHaveBeenCalledWith(NODE, "vpn-client", jasmine.objectContaining({ args: `--srv=${B}` }));
  });
  it("leaves a stopped client for the operator to save", () => {
    const apps = TestBed.inject(AppsService);
    settings(0).onServerChosen();
    expect(dialog.open).not.toHaveBeenCalled();
    expect(apps.setAppFullConfig).not.toHaveBeenCalled();
  });
  it("opens the flags form when the catalog asks for the picker", () => {
    const fixture = TestBed.createComponent(AppSettingsComponent);
    const comp = fixture.componentInstance;
    comp.app = { name: "skysocks-client", args: [], status: 0 };
    comp.pickServer = 1;
    fixture.detectChanges();
    expect(comp.showFlagsForm).toBeTrue();
    expect(comp.serverType).toBe("proxy");
    expect(dialog.open).toHaveBeenCalledTimes(1);
  });
});
//# debugId=a79876f0-045a-5199-8725-2ddc015e32e0
//# sourceMappingURL=spec-app-components-pages-node-skysocks-tab-select-proxy-server-select-proxy-server.component.spec.js.map
