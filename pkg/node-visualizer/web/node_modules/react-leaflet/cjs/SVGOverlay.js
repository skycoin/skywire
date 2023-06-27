"use strict";

exports.__esModule = true;
exports.useSVGOverlayElement = exports.useSVGOverlay = exports.SVGOverlay = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

var _react = require("react");

var _reactDom = require("react-dom");

const useSVGOverlayElement = (0, _core.createElementHook)(function createSVGOverlay(props, context) {
  const {
    attributes,
    bounds,
    ...options
  } = props;
  const container = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  container.setAttribute('xmlns', 'http://www.w3.org/2000/svg');

  if (attributes != null) {
    Object.keys(attributes).forEach(name => {
      container.setAttribute(name, attributes[name]);
    });
  }

  return {
    instance: new _leaflet.SVGOverlay(container, bounds, options),
    container,
    context
  };
}, _core.updateMediaOverlay);
exports.useSVGOverlayElement = useSVGOverlayElement;
const useSVGOverlay = (0, _core.createLayerHook)(useSVGOverlayElement);
exports.useSVGOverlay = useSVGOverlay;

function SVGOverlayComponent({
  children,
  ...options
}, ref) {
  const {
    instance,
    container
  } = useSVGOverlay(options).current;
  (0, _react.useImperativeHandle)(ref, () => instance);
  return container == null || children == null ? null : /*#__PURE__*/(0, _reactDom.createPortal)(children, container);
}

const SVGOverlay = /*#__PURE__*/(0, _react.forwardRef)(SVGOverlayComponent);
exports.SVGOverlay = SVGOverlay;