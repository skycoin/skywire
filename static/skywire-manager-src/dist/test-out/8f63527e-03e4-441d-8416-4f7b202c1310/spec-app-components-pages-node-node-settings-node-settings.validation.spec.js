import {
  settingError
} from "./chunk-BGNEFYT6.js";
import "./chunk-XSOC4ELV.js";
import "./chunk-CIWHQAPM.js";
import "./chunk-KWVFHEEW.js";
import "./chunk-HLYS7KBE.js";
import "./chunk-B4GMTCLR.js";
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

// src/app/components/pages/node/node-settings/node-settings.validation.spec.ts
describe("settings validation", () => {
  const pk = "02" + "a".repeat(64);
  const row = (format, check, secret = false) => {
    return {
      key: "K",
      flag: "k",
      negate: false,
      format,
      help: "",
      default: "",
      note: "",
      set: false,
      value: "",
      secret,
      check
    };
  };
  it("accepts empty values and the secret placeholder", () => {
    expect(settingError(row("string", "pk"), "")).toBe("");
    expect(settingError(row("string", "pk", true), "(set)")).toBe("");
  });
  it("checks whole numbers", () => {
    expect(settingError(row("int", ""), "-3")).toBe("");
    expect(settingError(row("int", ""), "3.5")).not.toBe("");
  });
  it("checks keys and key lists", () => {
    expect(settingError(row("string", "pk"), pk)).toBe("");
    expect(settingError(row("string", "pk"), "02abc")).not.toBe("");
    expect(settingError(row("bashArray", "pks"), pk + ", " + pk)).toBe("");
    expect(settingError(row("bashArray", "pks"), pk + ",nope")).not.toBe("");
  });
  it("checks host:port, durations and log levels", () => {
    expect(settingError(row("string", "hostport"), ":8000")).toBe("");
    expect(settingError(row("string", "hostport"), "[::1]:80")).toBe("");
    expect(settingError(row("string", "hostport"), "127.0.0.1")).not.toBe("");
    expect(settingError(row("string", "hostport"), "h:70000")).not.toBe("");
    expect(settingError(row("string", "duration"), "1h30m")).toBe("");
    expect(settingError(row("string", "duration"), "10")).not.toBe("");
    expect(settingError(row("string", "loglevel"), "Debug")).toBe("");
    expect(settingError(row("string", "loglevel"), "loud")).not.toBe("");
  });
});
//# debugId=d120a986-06b8-517f-b387-88e96145e827
//# sourceMappingURL=spec-app-components-pages-node-node-settings-node-settings.validation.spec.js.map
