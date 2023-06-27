"use strict";

exports.__esModule = true;
exports.ScaleControl = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const ScaleControl = (0, _core.createControlComponent)(function createScaleControl(props) {
  return new _leaflet.Control.Scale(props);
});
exports.ScaleControl = ScaleControl;