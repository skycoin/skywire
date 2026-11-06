import {
  isRemoteVisor,
  nodePaletteTabs,
  nodeTabsData
} from "./chunk-HLYS7KBE.js";
import "./chunk-ARXMZVRT.js";

// src/app/utils/node-tabs.spec.ts
var local = "02" + "a".repeat(64);
var remote = "03" + "b".repeat(64);
var hasChat = (tabs) => tabs.some((t) => t.label === "node.tabs.chat");
describe("node tabs for a remote visor", () => {
  it("knows a remote visor from the local one", () => {
    expect(isRemoteVisor(remote, local)).toBeTrue();
    expect(isRemoteVisor(local, local)).toBeFalse();
    expect(isRemoteVisor(remote, void 0)).toBeFalse();
    expect(isRemoteVisor(void 0, local)).toBeFalse();
  });
  it("drops the Chat tab only for a remote visor", () => {
    expect(hasChat(nodeTabsData(local, false))).toBeTrue();
    expect(hasChat(nodeTabsData(remote, false, true))).toBeFalse();
    expect(hasChat(nodePaletteTabs(remote, false, true))).toBeFalse();
    expect(nodeTabsData(remote, false, true).length).toBe(nodeTabsData(remote, false).length - 1);
  });
});
//# debugId=1c04b9b3-c2f4-5b52-b2aa-62b56696e743
//# sourceMappingURL=spec-app-utils-node-tabs.spec.js.map
