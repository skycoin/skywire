// src/app/utils/app-status.ts
function appStatusTone(status) {
  switch (status) {
    case 1:
      return "running";
    case 2:
      return "failed";
    case 3:
      return "starting";
    default:
      return "stopped";
  }
}
var dotClasses = {
  running: "dot-green",
  failed: "dot-red",
  starting: "dot-yellow",
  stopped: "dot-grey"
};
function appStatusDot(status) {
  return dotClasses[appStatusTone(status)];
}
function portLabel(port) {
  return port ? String(port) : "\u2014";
}

export {
  appStatusTone,
  appStatusDot,
  portLabel
};
//# debugId=6d11756c-a1a9-533b-9fbb-65888b1d3937
//# sourceMappingURL=chunk-WTGB3LY4.js.map
