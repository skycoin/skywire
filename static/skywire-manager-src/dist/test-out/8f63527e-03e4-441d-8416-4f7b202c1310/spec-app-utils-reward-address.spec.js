import {
  needsAcceptRisk,
  rewardAddressValidator
} from "./chunk-CDB4CLQF.js";
import {
  UntypedFormControl
} from "./chunk-B4GMTCLR.js";
import "./chunk-VGD7AO6Y.js";
import "./chunk-ZNLGPAUZ.js";
import "./chunk-ARXMZVRT.js";

// src/app/utils/reward-address.spec.ts
describe("rewardAddressValidator", () => {
  const xpub = "xpub6CcSesQqGHf226EZjBZjRTkTrTPmQ8oCAjAHtDBNpYTj1Yu1v46V5TaYEZeSE8iyHEj3eXs7ozZok4ux9b5uLHZRtHacskodD7B53xRG6sE";
  const addr = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6";
  it("needs the opt-in for a single address only", () => {
    let risk = false;
    const c = new UntypedFormControl("", rewardAddressValidator(() => risk));
    expect(c.valid).toBeTrue();
    c.setValue(xpub);
    expect(c.valid).toBeTrue();
    c.setValue(addr);
    expect(c.errors).toEqual({ xpubRequired: true });
    risk = true;
    c.updateValueAndValidity();
    expect(c.valid).toBeTrue();
  });
  it("sends the opt-in only with a single address", () => {
    expect(needsAcceptRisk(addr, true)).toBeTrue();
    expect(needsAcceptRisk(addr, false)).toBeFalse();
    expect(needsAcceptRisk(" " + xpub, true)).toBeFalse();
    expect(needsAcceptRisk("", true)).toBeFalse();
  });
});
//# debugId=d1ae1c35-41ed-526c-9ccb-3b7269efd506
//# sourceMappingURL=spec-app-utils-reward-address.spec.js.map
