import {
  versionLabel
} from "./chunk-H3STL4JE.js";
import "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/pipes/version-label.pipe.spec.ts
describe("versionLabel", () => {
  it("drops the commit repeated after git describe", () => {
    expect(versionLabel("v1.3.103-175-gf0faaa145-f0faaa1453c5")).toBe("v1.3.103-175-gf0faaa145");
  });
  it("keeps versions that do not repeat the commit", () => {
    expect(versionLabel("v1.3.103")).toBe("v1.3.103");
    expect(versionLabel("v1.3.104-0-dc3827b05ab7")).toBe("v1.3.104-0-dc3827b05ab7");
    expect(versionLabel("v1.3.103-175-gf0faaa145-0123abcd9999")).toBe("v1.3.103-175-gf0faaa145-0123abcd9999");
    expect(versionLabel("83261cd6-tinygo-cores-83261cd66208")).toBe("83261cd6-tinygo-cores-83261cd66208");
  });
  it("handles a missing version", () => {
    expect(versionLabel("")).toBe("");
    expect(versionLabel(void 0)).toBe("");
  });
});
//# debugId=bc4ccd6b-3265-5442-a7df-b5157711c295
//# sourceMappingURL=spec-app-pipes-version-label.pipe.spec.js.map
