"use strict";

exports.__esModule = true;
exports.updateGridLayer = updateGridLayer;

function updateGridLayer(layer, props, prevProps) {
  const {
    opacity,
    zIndex
  } = props;

  if (opacity != null && opacity !== prevProps.opacity) {
    layer.setOpacity(opacity);
  }

  if (zIndex != null && zIndex !== prevProps.zIndex) {
    layer.setZIndex(zIndex);
  }
}