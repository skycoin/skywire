import {
  Pipe
} from "./chunk-ZNLGPAUZ.js";

// src/app/pipes/version-label.pipe.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
function versionLabel(version) {
  const v = version || "";
  const m = /^(.*-g([0-9a-f]{4,}))-([0-9a-f]{4,})$/.exec(v);
  if (m && (m[3].startsWith(m[2]) || m[2].startsWith(m[3]))) {
    return m[1];
  }
  return v;
}
var VersionLabelPipe = class VersionLabelPipe2 {
  transform(version) {
    return versionLabel(version);
  }
};
VersionLabelPipe = __decorate([
  Pipe({
    name: "versionLabel",
    standalone: false
  })
], VersionLabelPipe);

export {
  versionLabel
};
//# debugId=fb930302-2777-5f90-bfad-380eb001ac4e
//# sourceMappingURL=chunk-H3STL4JE.js.map
