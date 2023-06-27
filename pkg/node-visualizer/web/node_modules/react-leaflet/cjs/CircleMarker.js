"use strict";

exports.__esModule = true;
exports.CircleMarker = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const CircleMarker = (0, _core.createPathComponent)(function createCircleMarker({
  center,
  children: _c,
  ...options
}, ctx) {
  const instance = new _leaflet.CircleMarker(center, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, _core.updateCircle);
exports.CircleMarker = CircleMarker;