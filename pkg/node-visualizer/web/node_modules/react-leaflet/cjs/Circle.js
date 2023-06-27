"use strict";

exports.__esModule = true;
exports.Circle = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const Circle = (0, _core.createPathComponent)(function createCircle({
  center,
  children: _c,
  ...options
}, ctx) {
  const instance = new _leaflet.Circle(center, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, _core.updateCircle);
exports.Circle = Circle;