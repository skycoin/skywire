"use strict";

exports.__esModule = true;
exports.Rectangle = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

const Rectangle = (0, _core.createPathComponent)(function createRectangle({
  bounds,
  ...options
}, ctx) {
  const instance = new _leaflet.Rectangle(bounds, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updateRectangle(layer, props, prevProps) {
  if (props.bounds !== prevProps.bounds) {
    layer.setBounds(props.bounds);
  }
});
exports.Rectangle = Rectangle;