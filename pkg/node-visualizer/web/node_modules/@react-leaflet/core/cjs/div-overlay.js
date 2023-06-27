"use strict";

exports.__esModule = true;
exports.createDivOverlayHook = createDivOverlayHook;

var _attribution = require("./attribution");

var _context = require("./context");

var _events = require("./events");

var _pane = require("./pane");

function createDivOverlayHook(useElement, useLifecycle) {
  return function useDivOverlay(props, setOpen) {
    const context = (0, _context.useLeafletContext)();
    const elementRef = useElement((0, _pane.withPane)(props, context), context);
    (0, _attribution.useAttribution)(context.map, props.attribution);
    (0, _events.useEventHandlers)(elementRef.current, props.eventHandlers);
    useLifecycle(elementRef.current, context, props, setOpen);
    return elementRef;
  };
}