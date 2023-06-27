"use strict";

exports.__esModule = true;
exports.GeoJSON = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const GeoJSON = (0, _core.createPathComponent)(function createGeoJSON({
  data,
  ...options
}, ctx) {
  const instance = new _leaflet.GeoJSON(data, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updateGeoJSON(layer, props, prevProps) {
  if (props.style !== prevProps.style) {
    if (props.style == null) {
      layer.resetStyle();
    } else {
      layer.setStyle(props.style);
    }
  }
});
exports.GeoJSON = GeoJSON;