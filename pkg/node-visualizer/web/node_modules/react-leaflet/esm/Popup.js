import { createOverlayComponent } from '@react-leaflet/core';
import { Popup as LeafletPopup } from 'leaflet';
import { useEffect } from 'react';
export const Popup = createOverlayComponent(function createPopup(props, context) {
  return {
    instance: new LeafletPopup(props, context.overlayContainer),
    context
  };
}, function usePopupLifecycle(element, context, props, setOpen) {
  const {
    onClose,
    onOpen,
    position
  } = props;
  useEffect(function addPopup() {
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