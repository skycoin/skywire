"use strict";

exports.__esModule = true;
exports.Popup = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

var _react = require("react");

const Popup = (0, _core.createOverlayComponent)(function createPopup(props, context) {
  return {
    instance: new _leaflet.Popup(props, context.overlayContainer),
    context
  };
}, function usePopupLifecycle(element, context, props, setOpen) {
  const {
    onClose,
    onOpen,
    position
  } = props;
  (0, _react.useEffect)(function addPopup() {
    const {
      instance
    } = element;

    function onPopupOpen(event) {
      if (event.popup === instance) {
        instance.update();
        setOpen(true);
        onOpen == null ? void 0 : onOpen();
      }
    }

    function onPopupClose(event) {
      if (event.popup === instance) {
        setOpen(false);
        onClose == null ? void 0 : onClose();
      }
    }

    context.map.on({
      popupopen: onPopupOpen,
      popupclose: onPopupClose
    });

    if (context.overlayContainer == null) {
      // Attach to a Map
      if (position != null) {
        instance.setLatLng(position);
      }

      instance.openOn(context.map);
    } else {
      // Attach to container component
      context.overlayContainer.bindPopup(instance);
    }

    return function removePopup() {
      var _context$overlayConta;

      context.map.off({
        popupopen: onPopupOpen,
        popupclose: onPopupClose
      });
      (_context$overlayConta = context.overlayContainer) == null ? void 0 : _context$overlayConta.unbindPopup();
      context.map.removeLayer(instance);
    };
  }, [element, context, setOpen, onClose, onOpen, position]);
});
exports.Popup = Popup;