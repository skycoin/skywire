"use strict";

exports.__esModule = true;
exports.Polyline = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const Polyline = (0, _core.createPathComponent)(function createPolyline({
  positions,
  ...options
}, ctx) {
  const instance = new _leaflet.Polyline(positions, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updatePolyline(layer, props, prevProps) {
  if (props.positions !== prevProps.positions) {
    layer.setLatLngs(props.positions);
  }
});
exports.Polyline = Polyline;