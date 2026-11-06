import {
  appStatusDot,
  appStatusTone,
  portLabel
} from "./chunk-WTGB3LY4.js";
import "./chunk-ARXMZVRT.js";

// src/app/utils/app-status.spec.ts
describe("app status colours", () => {
  it("uses grey for stopped and red only for failed", () => {
    expect(appStatusDot(0)).toBe("dot-grey");
    expect(appStatusDot(1)).toBe("dot-green");
    expect(appStatusDot(2)).toBe("dot-red");
    expect(appStatusDot(3)).toBe("dot-yellow");
  });
  it("treats an unknown status as stopped", () => {
    expect(appStatusTone(7)).toBe("stopped");
  });
});
describe("portLabel", () => {
  it("shows a dash for a service with no port", () => {
    expect(portLabel(0)).toBe("\u2014");
    expect(portLabel(void 0)).toBe("\u2014");
    expect(portLabel(44)).toBe("44");
  });
});
//# debugId=8a0e7778-2d5d-5876-8329-deb4e3f273a4
//# sourceMappingURL=spec-app-utils-app-status.spec.js.map
