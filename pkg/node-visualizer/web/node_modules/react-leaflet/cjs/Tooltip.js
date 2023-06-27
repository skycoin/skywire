"use strict";

exports.__esModule = true;
exports.Tooltip = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

var _react = require("react");

const Tooltip = (0, _core.createOverlayComponent)(function createTooltip(props, context) {
  return {
    instance: new _leaflet.Tooltip(props, context.overlayContainer),
    context
  };
}, function useTooltipLifecycle(element, context, props, setOpen) {
  const {
    onClose,
    onOpen
  } = props;
  (0, _react.useEffect)(function addTooltip() {
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
exports.Tooltip = Tooltip;