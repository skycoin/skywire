import {
  BehaviorSubject,
  Injectable
} from "./chunk-ZNLGPAUZ.js";

// src/app/services/theme.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var ThemeService_1;
var themeChoices = ["system", "light", "dark"];
var ThemeService = class ThemeService2 {
  static {
    ThemeService_1 = this;
  }
  static {
    this.storageKey = "hvui-theme";
  }
  constructor() {
    this.onMediaChange = () => this.apply();
    this.media = typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: light)") : null;
    this.choiceSubject = new BehaviorSubject(this.readChoice());
    this.media?.addEventListener?.("change", this.onMediaChange);
    this.apply();
  }
  ngOnDestroy() {
    this.media?.removeEventListener?.("change", this.onMediaChange);
  }
  get choice() {
    return this.choiceSubject.value;
  }
  get choice$() {
    return this.choiceSubject.asObservable();
  }
  /** The theme in effect, with 'system' resolved. */
  get theme() {
    if (this.choice !== "system") {
      return this.choice;
    }
    return this.media?.matches ? "light" : "dark";
  }
  setChoice(choice) {
    if (!themeChoices.includes(choice)) {
      return;
    }
    try {
      if (choice === "system") {
        window.localStorage.removeItem(ThemeService_1.storageKey);
      } else {
        window.localStorage.setItem(ThemeService_1.storageKey, choice);
      }
    } catch (e) {
    }
    this.choiceSubject.next(choice);
    this.apply();
  }
  readChoice() {
    try {
      const saved = window.localStorage.getItem(ThemeService_1.storageKey);
      return themeChoices.includes(saved) ? saved : "system";
    } catch (e) {
      return "system";
    }
  }
  apply() {
    document.documentElement.setAttribute("data-theme", this.theme);
  }
  static {
    this.ctorParameters = () => [];
  }
};
ThemeService = ThemeService_1 = __decorate([
  Injectable({ providedIn: "root" })
], ThemeService);

export {
  themeChoices,
  ThemeService
};
//# debugId=b5bac97a-b5f8-5e10-bfc3-2acfa41c373f
//# sourceMappingURL=chunk-VNMLTXOX.js.map
