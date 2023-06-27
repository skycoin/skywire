"use strict";

exports.__esModule = true;
exports.createElementHook = createElementHook;

var _react = require("react");

function createElementHook(createElement, updateElement) {
  if (updateElement == null) {
    return function useImmutableLeafletElement(props, context) {
      return (0, _react.useRef)(createElement(props, context));
    };
  }

  return function useMutableLeafletElement(props, context) {
    const elementRef = (0, _react.useRef)(createElement(props, context));
    const propsRef = (0, _react.useRef)(props);
    const {
      instance
    } = elementRef.current;
    (0, _react.useEffect)(function updateElementProps() {
      if (propsRef.current !== props) {
        updateElement(instance, props, propsRef.current);
        propsRef.current = props;
      }
    }, [instance, props, context]);
    return elementRef;
  };
}