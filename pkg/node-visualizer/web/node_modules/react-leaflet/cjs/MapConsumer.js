"use strict";

exports.__esModule = true;
exports.MapConsumer = MapConsumer;

var _hooks = require("./hooks");

function MapConsumer({
  children
}) {
  return children((0, _hooks.useMap)());
}