"use strict";

exports.__esModule = true;
exports.Marker = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const Marker = (0, _core.createLayerComponent)(function createMarker({
  position,
  ...options
}, ctx) {
  const instance = new _leaflet.Marker(position, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updateMarker(marker, props, prevProps) {
  if (props.position !== prevProps.position) {
    marker.setLatLng(props.position);
  }

  if (props.icon != null && props.icon !== prevProps.icon) {
    marker.setIcon(props.icon);
  }

  if (props.zIndexOffset != null && props.zIndexOffset !== prevProps.zIndexOffset) {
    marker.setZIndexOffset(props.zIndexOffset);
  }

  if (props.opacity != null && props.opacity !== prevProps.opacity) {
    marker.setOpacity(props.opacity);
  }

  if (marker.dragging != null && props.draggable !== prevProps.draggable) {
    if (props.draggable === true) {
      marker.dragging.enable();
    } else {
      marker.dragging.disable();
    }
  }
});
exports.Marker = Marker;