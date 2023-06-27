"use strict";

exports.__esModule = true;
exports.useEventHandlers = useEventHandlers;

var _react = require("react");

function useEventHandlers(element, eventHandlers) {
  const eventHandlersRef = (0, _react.useRef)();
  (0, _react.useEffect)(function addEventHandlers() {
    if (eventHandlers != null) {
      element.instance.on(eventHandlers);
    }

    eventHandlersRef.current = eventHandlers;
    return function removeEventHandlers() {
      if (eventHandlersRef.current != null) {
        element.instance.off(eventHandlersRef.current);
      }

      eventHandlersRef.current = null;
    };
  }, [element, eventHandlers]);
}