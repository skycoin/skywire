"use strict";

exports.__esModule = true;
exports.VideoOverlay = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const VideoOverlay = (0, _core.createLayerComponent)(function createVideoOverlay({
  bounds,
  url,
  ...options
}, ctx) {
  const instance = new _leaflet.VideoOverlay(url, bounds, options);

  if (options.play === true) {
    var _instance$getElement;

    (_instance$getElement = instance.getElement()) == null ? void 0 : _instance$getElement.play();
  }

  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updateVideoOverlay(overlay, props, prevProps) {
  (0, _core.updateMediaOverlay)(overlay, props, prevProps);

  if (typeof props.url === 'string' && props.url !== prevProps.url) {
    overlay.setUrl(props.url);
  }

  const video = overlay.getElement();

  if (video != null) {
    if (props.play === true && !prevProps.play) {
      video.play();
    } else if (!props.play && prevProps.play === true) {
      video.pause();
    }
  }
});
exports.VideoOverlay = VideoOverlay;