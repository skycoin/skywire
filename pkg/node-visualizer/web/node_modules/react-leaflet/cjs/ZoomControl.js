"use strict";

exports.__esModule = true;
exports.ZoomControl = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const ZoomControl = (0, _core.createControlComponent)(function createZoomControl(props) {
  return new _leaflet.Control.Zoom(props);
});
exports.ZoomControl = ZoomControl;