"use strict";

exports.__esModule = true;
exports.ImageOverlay = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const ImageOverlay = (0, _core.createLayerComponent)(function createImageOveraly({
  bounds,
  url,
  ...options
}, ctx) {
  const instance = new _leaflet.ImageOverlay(url, bounds, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updateImageOverlay(overlay, props, prevProps) {
  (0, _core.updateMediaOverlay)(overlay, props, prevProps);

  if (props.url !== prevProps.url) {
    overlay.setUrl(props.url);
  }
});
exports.ImageOverlay = ImageOverlay;