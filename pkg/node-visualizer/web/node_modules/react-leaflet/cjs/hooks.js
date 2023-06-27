"use strict";

exports.__esModule = true;
exports.useMap = useMap;
exports.useMapEvent = useMapEvent;
exports.useMapEvents = useMapEvents;

var _core = require("@react-leaflet/core");

var _react = require("react");

function useMap() {
  return (0, _core.useLeafletContext)().map;
}

function useMapEvent(type, handler) {
  const map = useMap();
  (0, _react.useEffect)(function addMapEventHandler() {
    // @ts-ignore event type
    map.on(type, handler);
    return function removeMapEventHandler() {
      // @ts-ignore event type
      map.off(type, handler);
    };
  }, [map, type, handler]);
  return map;
}

function useMapEvents(handlers) {
  const map = useMap();
  (0, _react.useEffect)(function addMapEventHandlers() {
    map.on(handlers);
    return function removeMapEventHandlers() {
      map.off(handlers);
    };
  }, [map, handlers]);
  return map;
}