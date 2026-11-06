import {
  AppFlagsFormComponent,
  AppSettingsComponent
} from "./chunk-LMOBANXL.js";
import {
  buildFlagArgs,
  parseFlagArgs,
  splitArgs
} from "./chunk-Z3QUGWOD.js";
import "./chunk-BGNEFYT6.js";
import "./chunk-XSOC4ELV.js";
import {
  TestBed,
  fakeAsync,
  flushMicrotasks
} from "./chunk-UQCSELO5.js";
import "./chunk-ED5WCGEB.js";
import {
  NodeComponent
} from "./chunk-CIWHQAPM.js";
import "./chunk-KWVFHEEW.js";
import "./chunk-HLYS7KBE.js";
import {
  ReactiveFormsModule
} from "./chunk-B4GMTCLR.js";
import {
  AppsService
} from "./chunk-MX3HPERK.js";
import "./chunk-CAA347WM.js";
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
  BehaviorSubject,
  NO_ERRORS_SCHEMA,
  of
} from "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/components/pages/node/apps/app-flags-form/app-flags-form.component.spec.ts
var PK = "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c";
var flags = () => [
  { name: "addr", type: "string", default: ":1080", usage: "listen address", check: "hostport", set: false },
  { name: "reconnect", type: "bool", default: "false", usage: "reconnect", set: false },
  { name: "tunnels", type: "int", default: "1", usage: "tunnels", set: false },
  { name: "timeout", type: "duration", default: "10s", usage: "timeout", check: "duration", set: false },
  { name: "peer", type: "stringList", default: "", usage: "peers", set: false },
  { name: "config", shorthand: "c", type: "string", default: "", usage: "config path", set: false }
];
describe("app flag args", () => {
  it("splits quoted args like the visor", () => {
    expect(splitArgs(`app x --a "b c" --d='e f' ""`)).toEqual(["app", "x", "--a", "b c", "--d=e f", ""]);
  });
  it("reads values in every flag form and keeps unknown tokens", () => {
    const p = parseFlagArgs("skycoin daemon --addr :9 --reconnect -c /etc/x --peer=a --peer b --other 1", flags());
    expect(p.positional).toEqual(["skycoin", "daemon"]);
    expect(p.other).toEqual(["--other", "1"]);
    expect(p.state["addr"]).toEqual({ value: ":9", set: true });
    expect(p.state["reconnect"]).toEqual({ value: "true", set: true });
    expect(p.state["config"]).toEqual({ value: "/etc/x", set: true });
    expect(p.state["peer"]).toEqual({ value: "a,b", set: true });
    expect(p.state["tunnels"]).toEqual({ value: "1", set: false });
  });
  it("rebuilds args with only the set flags", () => {
    const fl = flags();
    const state = parseFlagArgs("", fl).state;
    state["reconnect"] = { value: "false", set: true };
    state["peer"] = { value: "a, b", set: true };
    state["config"] = { value: "my file", set: true };
    expect(buildFlagArgs("app sc --other 1 --addr :9", fl, state)).toBe('app sc --other 1 --reconnect=false --peer=a --peer=b "--config=my file"');
  });
});
describe("AppFlagsFormComponent", () => {
  let fixture;
  let comp;
  let el;
  let apps;
  let emitted;
  let valid;
  const row = (name) => el.querySelector(`[data-flag="${name}"]`);
  const input = (name) => row(name).querySelector("input");
  const type = (name, value) => {
    const i = input(name);
    i.value = value;
    i.dispatchEvent(new Event("input"));
    fixture.detectChanges();
  };
  beforeEach(() => {
    apps = jasmine.createSpyObj("AppsService", ["getAppFlags"]);
    apps.getAppFlags.and.returnValue(of({ app: "sc", source: "registry", flags: flags() }));
    spyOnProperty(NodeComponent, "currentNode", "get").and.returnValue(of({ localPk: PK }));
    TestBed.configureTestingModule({
      declarations: [AppFlagsFormComponent],
      imports: [TranslatePipe],
      providers: [provideTranslateService(), { provide: AppsService, useValue: apps }],
      schemas: [NO_ERRORS_SCHEMA]
    });
    fixture = TestBed.createComponent(AppFlagsFormComponent);
    comp = fixture.componentInstance;
    comp.appName = "sc";
    comp.args = "app sc --tunnels=3";
    emitted = [];
    valid = [];
    comp.argsChange.subscribe((a) => emitted.push(a));
    comp.validChange.subscribe((v) => valid.push(v));
    el = fixture.nativeElement;
    fixture.detectChanges();
  });
  it("fetches the schema and renders an input per type", () => {
    expect(apps.getAppFlags).toHaveBeenCalledWith(PK, "sc");
    expect(input("reconnect").type).toBe("checkbox");
    expect(input("tunnels").type).toBe("number");
    expect(input("timeout").type).toBe("text");
    expect(input("peer").type).toBe("text");
    expect(input("addr").type).toBe("text");
    expect(row("config").querySelector(".flag-name").textContent).toContain("-c,");
  });
  it("shows the configured value and the default", () => {
    expect(input("tunnels").value).toBe("3");
    expect(row("tunnels").classList).toContain("flag-set");
    expect(row("timeout").querySelector(".flag-default").textContent).toContain("10s");
    expect(row("timeout").querySelector(".flag-reset")).toBeNull();
  });
  it("writes an edit into the args", () => {
    const box = input("reconnect");
    box.checked = true;
    box.dispatchEvent(new Event("change"));
    expect(emitted[emitted.length - 1]).toBe("app sc --reconnect --tunnels=3");
  });
  it("resets a field to its default", () => {
    row("tunnels").querySelector(".flag-reset").click();
    fixture.detectChanges();
    expect(emitted[emitted.length - 1]).toBe("app sc");
    expect(input("tunnels").value).toBe("1");
    expect(row("tunnels").querySelector(".flag-reset")).toBeNull();
  });
  it("marks a value the app would refuse", () => {
    type("timeout", "soon");
    expect(row("timeout").querySelector(".flag-error").textContent).toContain("not a duration");
    expect(valid[valid.length - 1]).toBeFalse();
    type("timeout", "5m");
    expect(row("timeout").querySelector(".flag-error")).toBeNull();
    expect(valid[valid.length - 1]).toBeTrue();
  });
  it("follows edits made in the raw args field", fakeAsync(() => {
    fixture.componentRef.setInput("args", "app sc --addr=:2000");
    fixture.detectChanges();
    flushMicrotasks();
    fixture.detectChanges();
    expect(input("addr").value).toBe(":2000");
    expect(input("tunnels").value).toBe("1");
  }));
});
describe("AppSettingsComponent with the flags form", () => {
  it("saves the args the flags form wrote through setAppFullConfig", () => {
    const apps = jasmine.createSpyObj("AppsService", ["setAppFullConfig"]);
    apps.setAppFullConfig.and.returnValue(of({}));
    spyOnProperty(NodeComponent, "currentNode", "get").and.returnValue(new BehaviorSubject({ localPk: PK }).asObservable());
    TestBed.configureTestingModule({
      declarations: [AppSettingsComponent],
      imports: [TranslatePipe, ReactiveFormsModule],
      providers: [
        provideTranslateService(),
        { provide: AppsService, useValue: apps },
        { provide: SnackbarService, useValue: jasmine.createSpyObj("SnackbarService", ["showDone"]) }
      ],
      schemas: [NO_ERRORS_SCHEMA]
    });
    const fixture = TestBed.createComponent(AppSettingsComponent);
    const comp = fixture.componentInstance;
    comp.app = { name: "sc", args: ["app", "sc"], status: 0 };
    fixture.detectChanges();
    comp.toggleFlagsForm();
    comp.onFlagArgs("app sc --tunnels=4");
    comp.save();
    expect(apps.setAppFullConfig).toHaveBeenCalledWith(PK, "sc", jasmine.objectContaining({ args: "app sc --tunnels=4" }));
  });
});
//# debugId=a4c9e083-1e0c-594a-b0ff-04dda3ad1213
//# sourceMappingURL=spec-app-components-pages-node-apps-app-flags-form-app-flags-form.component.spec.js.map
