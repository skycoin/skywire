import {
  BulkAppActionComponent,
  bulkAppLimit,
  runLimited
} from "./chunk-4PPPV3QE.js";
import {
  TestBed
} from "./chunk-UQCSELO5.js";
import {
  AppsService
} from "./chunk-MX3HPERK.js";
import {
  MAT_DIALOG_DATA,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  TranslatePipe,
  provideTranslateService
} from "./chunk-6WNZTOXO.js";
import {
  ApiService
} from "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import {
  NO_ERRORS_SCHEMA,
  Observable,
  Subject,
  of,
  throwError
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/components/layout/bulk-app-action/bulk-app-action.component.spec.ts
function node(key, apps = [], online = true, viaSubHypervisor = false) {
  return { key, label: "visor-" + key, online, viaSubHypervisor, apps };
}
describe("runLimited", () => {
  it("keeps at most the limit in flight and goes on after a failure", () => {
    const pending = /* @__PURE__ */ new Map();
    let peak = 0;
    const results = [];
    runLimited([1, 2, 3, 4, 5, 6, 7, 8, 9], 4, (i) => new Observable((sub) => {
      const s = new Subject();
      pending.set(i, s);
      peak = Math.max(peak, pending.size);
      const inner = s.subscribe(sub);
      return () => {
        pending.delete(i);
        inner.unsubscribe();
      };
    })).subscribe((r) => results.push(r));
    expect(pending.size).toBe(4);
    while (pending.size > 0) {
      const [i, s] = pending.entries().next().value;
      if (i === 2) {
        s.error({ error: "boom" });
      } else {
        s.next("a");
        s.next("b");
        s.complete();
      }
    }
    expect(peak).toBe(4);
    expect(results.length).toBe(9);
    expect(results.find((r) => r.item === 2).error).toBeTruthy();
    expect(results.filter((r) => r.error === null).length).toBe(8);
  });
});
describe("BulkAppActionComponent", () => {
  let fixture;
  let comp;
  let apps;
  let api;
  const make = (params) => {
    apps = jasmine.createSpyObj("AppsService", ["addApp", "changeAppState"]);
    apps.addApp.and.returnValue(of({}));
    const fail = throwError(() => new Error("refused"));
    apps.changeAppState.and.callFake((key) => key === "bad" ? fail : of({}));
    api = jasmine.createSpyObj("ApiService", ["get"]);
    api.get.and.returnValue(of([
      { name: "skysocks", kind: "server", description: "SOCKS5 server", multi_instance: false, instances: [] },
      { name: "skychat", kind: "service", description: "Chat", multi_instance: false, instances: [] }
    ]));
    TestBed.configureTestingModule({
      declarations: [BulkAppActionComponent],
      imports: [TranslatePipe],
      providers: [
        provideTranslateService(),
        { provide: MatDialogRef, useValue: jasmine.createSpyObj("MatDialogRef", ["close"]) },
        { provide: MAT_DIALOG_DATA, useValue: params },
        { provide: AppsService, useValue: apps },
        { provide: ApiService, useValue: api }
      ],
      schemas: [NO_ERRORS_SCHEMA]
    });
    fixture = TestBed.createComponent(BulkAppActionComponent);
    comp = fixture.componentInstance;
    fixture.detectChanges();
  };
  const state = (key) => comp.rows.find((r) => r.node.key === key);
  it("starts the app on every visor and reports a failure without stopping the rest", () => {
    make({ mode: "start", nodes: [
      node("a", ["skysocks"]),
      node("bad", ["skysocks"]),
      node("c", ["skysocks"], true, true),
      node("off", ["skysocks"], false),
      node("none", ["skychat"])
    ] });
    expect(comp.appOptions).toEqual(["skychat", "skysocks"]);
    comp.app = "skysocks";
    comp.run();
    fixture.detectChanges();
    expect(apps.changeAppState.calls.allArgs().map((a) => a[0])).toEqual(["a", "bad", "c"]);
    expect(apps.changeAppState).toHaveBeenCalledWith("a", "skysocks", true);
    expect(state("a").state).toBe("ok");
    expect(state("bad").state).toBe("failed");
    expect(state("bad").message).toBeTruthy();
    expect(state("c").state).toBe("ok");
    expect(state("off").state).toBe("skipped");
    expect(state("none").state).toBe("skipped");
    expect(comp.finished).toBeTrue();
    expect(comp.counts).toEqual({ ok: 2, failed: 1, skipped: 2 });
    const rows = fixture.nativeElement.querySelectorAll(".result-row");
    expect(rows.length).toBe(5);
    expect(fixture.nativeElement.querySelector('[data-pk="bad"]').getAttribute("data-state")).toBe("failed");
  });
  it("stops the app", () => {
    make({ mode: "stop", nodes: [node("a", ["skysocks"])] });
    comp.app = "skysocks";
    comp.run();
    expect(apps.changeAppState).toHaveBeenCalledWith("a", "skysocks", false);
  });
  it("adds from the catalog of one online visor and starts when asked", () => {
    make({ mode: "add", nodes: [node("off", [], false), node("a"), node("b", ["skysocks"])] });
    expect(api.get).toHaveBeenCalledOnceWith("visors/a/apps/catalog");
    expect(comp.appOptions).toEqual(["skychat", "skysocks"]);
    comp.app = "skysocks";
    comp.startAfterAdd = true;
    comp.run();
    expect(apps.addApp).toHaveBeenCalledOnceWith("a", "skysocks", "skysocks");
    expect(apps.changeAppState.calls.allArgs()).toEqual([["a", "skysocks", true], ["b", "skysocks", true]]);
    expect(state("a").state).toBe("ok");
    expect(state("b").state).toBe("ok");
    expect(state("off").state).toBe("skipped");
  });
  it("offers the configured apps when a visor has no catalog route", () => {
    make({ mode: "add", nodes: [node("a", ["vpn-client"]), node("b", ["skychat", "vpn-client"])] });
    api.get.and.returnValue(throwError(() => new Error("404")));
    comp = TestBed.createComponent(BulkAppActionComponent).componentInstance;
    expect(comp.appOptions).toEqual(["skychat", "vpn-client"]);
    expect(comp.catalogError).toBe("nodes.bulk-apps.catalog-fallback");
  });
  it("skips visors that already have the app when not starting it", () => {
    make({ mode: "add", nodes: [node("a"), node("b", ["skysocks"])] });
    comp.app = "skysocks";
    comp.run();
    expect(apps.addApp).toHaveBeenCalledOnceWith("a", "skysocks", "skysocks");
    expect(apps.changeAppState).not.toHaveBeenCalled();
    expect(state("b").state).toBe("skipped");
  });
  it("asks at most the limit of visors at once", () => {
    const keys = ["1", "2", "3", "4", "5", "6"];
    make({ mode: "stop", nodes: keys.map((k) => node(k, ["skysocks"])) });
    const pending = [];
    apps.changeAppState.and.callFake(() => {
      const s = new Subject();
      pending.push(s);
      return s;
    });
    comp.app = "skysocks";
    comp.run();
    expect(pending.length).toBe(bulkAppLimit);
    expect(comp.rows.filter((r) => r.state === "working").length).toBe(bulkAppLimit);
    pending[0].error({ error: "x" });
    expect(pending.length).toBe(bulkAppLimit + 1);
    pending.slice(1).forEach((s) => {
      s.next({});
      s.complete();
    });
    pending.slice(bulkAppLimit + 1).forEach((s) => {
      s.next({});
      s.complete();
    });
    expect(comp.counts).toEqual({ ok: 5, failed: 1, skipped: 0 });
    expect(comp.finished).toBeTrue();
  });
});
//# debugId=c89f8b75-8f11-53a0-9caf-e523b589fb00
//# sourceMappingURL=spec-app-components-layout-bulk-app-action-bulk-app-action.component.spec.js.map
