"use strict";

exports.__esModule = true;
exports.TileLayer = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const TileLayer = (0, _core.createTileLayerComponent)(function createTileLayer({
  url,
  ...options
}, context) {
  return {
    instance: new _leaflet.TileLayer(url, (0, _core.withPane)(options, context)),
    context
  };
}, _core.updateGridLayer);
exports.TileLayer = TileLayer;