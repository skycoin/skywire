import {
  NodeListComponent,
  appChips,
  fleetAppNames,
  runsApp
} from "./chunk-T47OQHHJ.js";
import "./chunk-SW25Q345.js";
import "./chunk-XSOC4ELV.js";
import "./chunk-CDB4CLQF.js";
import "./chunk-4PPPV3QE.js";
import "./chunk-4XQ3VDCY.js";
import "./chunk-CIWHQAPM.js";
import "./chunk-KWVFHEEW.js";
import "./chunk-HLYS7KBE.js";
import "./chunk-B4GMTCLR.js";
import "./chunk-MX3HPERK.js";
import "./chunk-CAA347WM.js";
import "./chunk-6WNZTOXO.js";
import "./chunk-GPHL7TNH.js";
import "./chunk-H3STL4JE.js";
import "./chunk-XBRWDJ2O.js";
import "./chunk-ZMZW6HXB.js";
import "./chunk-CJYNOHF2.js";
import "./chunk-VGD7AO6Y.js";
import "./chunk-XOUXCPTJ.js";
import "./chunk-VNMLTXOX.js";
import "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/components/pages/node-list/node-list.apps.spec.ts
function visor(pk, apps) {
  return { localPk: pk, label: pk, apps: apps.map(([name, status]) => {
    return { name, status };
  }) };
}
describe("node list apps", () => {
  const a = visor("a", [["skysocks", 1], ["vpn-client", 0]]);
  const b = visor("b", [["vpn-server", 3], ["skysocks", 2]]);
  const c = visor("c", []);
  it("counts running and starting apps as running", () => {
    expect(runsApp(a, "skysocks")).toBeTrue();
    expect(runsApp(a, "vpn-client")).toBeFalse();
    expect(runsApp(b, "vpn-server")).toBeTrue();
    expect(runsApp(b, "skysocks")).toBeFalse();
    expect(runsApp(c, "skysocks")).toBeFalse();
  });
  it("offers every configured app name once, sorted", () => {
    expect(fleetAppNames([a, b, c])).toEqual(["skysocks", "vpn-client", "vpn-server"]);
  });
  it("lists running chips before stopped ones", () => {
    expect(appChips(b)).toEqual([{ name: "vpn-server", running: true }, { name: "skysocks", running: false }]);
    expect(appChips(c)).toEqual([]);
  });
  it("filters the list to visors running the chosen app", () => {
    const matches = NodeListComponent.prototype.matchesQuickFilter;
    const list = { quickChip: "all", quickSearch: "", appFilter: "skysocks" };
    expect([a, b, c].filter((n) => matches.call(list, n)).map((n) => n.localPk)).toEqual(["a"]);
    list.appFilter = "";
    expect([a, b, c].filter((n) => matches.call(list, n)).length).toBe(3);
  });
});
//# debugId=45a04c40-935e-51b1-8464-786493adcc02
//# sourceMappingURL=spec-app-components-pages-node-list-node-list.apps.spec.js.map
