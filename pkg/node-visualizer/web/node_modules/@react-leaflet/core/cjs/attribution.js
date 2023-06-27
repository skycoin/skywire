"use strict";

exports.__esModule = true;
exports.useAttribution = useAttribution;

var _react = require("react");

function useAttribution(map, attribution) {
  const attributionRef = (0, _react.useRef)(attribution);
  (0, _react.useEffect)(function updateAttribution() {
    if (attribution !== attributionRef.current && map.attributionControl != null) {
      if (attributionRef.current != null) {
        map.attributionControl.removeAttribution(attributionRef.current);
      }

      if (attribution != null) {
        map.attributionControl.addAttribution(attribution);
      }
    }

    attributionRef.current = attribution;
  }, [map, attribution]);
}