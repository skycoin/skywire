import {
  compareVersions,
  versionParts
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

// src/app/components/pages/node-list/node-list.versions.spec.ts
describe("node list version order", () => {
  it("parses tagged and post-tag versions", () => {
    expect(versionParts("v1.3.103")).toEqual([1, 3, 103, 0]);
    expect(versionParts("v1.3.103-122-g7845bbc81-7845bbc81358")).toEqual([1, 3, 103, 122]);
    expect(versionParts("1.3.104")).toEqual([1, 3, 104, 0]);
  });
  it("gives null for a build name that is not a version", () => {
    expect(versionParts("develop-latest-tinygo-cores-7845bbc81358")).toBeNull();
    expect(versionParts(void 0)).toBeNull();
  });
  it("orders by release, then by commits after the tag", () => {
    const order = ["v1.3.99", "v1.3.103", "v1.3.103-5-gabc", "v1.3.103-122-gdef", "v1.3.104", "v1.4.0"].map((v) => versionParts(v));
    for (let i = 1; i < order.length; i++) {
      expect(compareVersions(order[i - 1], order[i])).toBeLessThan(0);
    }
    expect(compareVersions(order[2], order[2])).toBe(0);
  });
});
//# debugId=a2a56b60-8754-5e66-b7a1-3352766bc5bf
//# sourceMappingURL=spec-app-components-pages-node-list-node-list.versions.spec.js.map
