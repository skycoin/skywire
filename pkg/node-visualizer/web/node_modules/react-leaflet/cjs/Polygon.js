"use strict";

exports.__esModule = true;
exports.Polygon = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const Polygon = (0, _core.createPathComponent)(function createPolygon({
  positions,
  ...options
}, ctx) {
  const instance = new _leaflet.Polygon(positions, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updatePolygon(layer, props, prevProps) {
  if (props.positions !== prevProps.positions) {
    layer.setLatLngs(props.positions);
  }
});
exports.Polygon = Polygon;