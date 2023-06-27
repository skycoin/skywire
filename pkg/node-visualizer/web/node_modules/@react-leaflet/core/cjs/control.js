"use strict";

exports.__esModule = true;
exports.createControlHook = createControlHook;

var _react = require("react");

var _context = require("./context");

function createControlHook(useElement) {
  return function useLeafletControl(props) {
    const context = (0, _context.useLeafletContext)();
    const elementRef = useElement(props, context);
    const {
      instance
    } = elementRef.current;
    const positionRef = (0, _react.useRef)(props.position);
    const {
      position
    } = props;
    (0, _react.useEffect)(function addControl() {
      instance.addTo(context.map);
      return function removeControl() {
        instance.remove();
      };
    }, [context.map, instance]);
    (0, _react.useEffect)(function updateControl() {
      if (position != null && position !== positionRef.current) {
        instance.setPosition(position);
        positionRef.current = position;
      }
    }, [instance, position]);
    return elementRef;
  };
}