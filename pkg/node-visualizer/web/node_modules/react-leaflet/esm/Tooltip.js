import { createOverlayComponent } from '@react-leaflet/core';
import { Tooltip as LeafletTooltip } from 'leaflet';
import { useEffect } from 'react';
export const Tooltip = createOverlayComponent(function createTooltip(props, context) {
  return {
    instance: new LeafletTooltip(props, context.overlayContainer),
    context
  };
}, function useTooltipLifecycle(element, context, props, setOpen) {
  const {
    onClose,
    onOpen
  } = props;
  useEffect(function addTooltip() {
    const container = context.overlayContainer;

    if (container == null) {
      return;
    }

    const {
      instance
    } = element;

    const onTooltipOpen = event => {
      if (event.tooltip === instance) {
        instance.update();
        setOpen(true);
        onOpen == null ? void 0 : onOpen();
      }
    };

    const onTooltipClose = event => {
      if (event.tooltip === instance) {
        setOpen(false);
        onClose == null ? void 0 : onClose();
      }
    };

    container.on({
      tooltipopen: onTooltipOpen,
      tooltipclose: onTooltipClose
    });
    container.bindTooltip(instance);
    return function removeTooltip() {
      container.off({
        tooltipopen: onTooltipOpen,
        tooltipclose: onTooltipClose
      }); // @ts-ignore protected property

      if (container._map != null) {
        container.unbindTooltip();
      }
    };
  }, [element, context, setOpen, onClose, onOpen]);
});