import {
  DOCUMENT,
  Inject,
  Injectable
} from "./chunk-ZNLGPAUZ.js";

// src/app/services/clipboard.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var ClipboardService = class ClipboardService2 {
  constructor(dom) {
    this.dom = dom;
  }
  /**
   * Copies text in the clipboard. Returns false in case of error.
   */
  copy(value) {
    let textarea = null;
    try {
      textarea = this.dom.createElement("textarea");
      textarea.style.height = "0px";
      textarea.style.left = "-100px";
      textarea.style.opacity = "0";
      textarea.style.position = "fixed";
      textarea.style.top = "-100px";
      textarea.style.width = "0px";
      this.dom.body.appendChild(textarea);
      textarea.value = value;
      textarea.select();
      this.dom.execCommand("copy");
      return true;
    } catch (e) {
      return false;
    } finally {
      if (textarea && textarea.parentNode) {
        textarea.parentNode.removeChild(textarea);
      }
    }
  }
  static {
    this.ctorParameters = () => [
      { type: Document, decorators: [{ type: Inject, args: [DOCUMENT] }] }
    ];
  }
};
ClipboardService = __decorate([
  Injectable()
], ClipboardService);

export {
  ClipboardService
};
//# debugId=37da8882-2a8e-5192-a868-fc2ebf9c5e21
//# sourceMappingURL=chunk-XSOC4ELV.js.map
