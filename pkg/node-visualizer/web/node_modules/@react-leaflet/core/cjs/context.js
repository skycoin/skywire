"use strict";

exports.__esModule = true;
exports.LeafletProvider = exports.LeafletContext = exports.CONTEXT_VERSION = void 0;
exports.useLeafletContext = useLeafletContext;

var _react = require("react");

const CONTEXT_VERSION = 1;
exports.CONTEXT_VERSION = CONTEXT_VERSION;
const LeafletContext = /*#__PURE__*/(0, _react.createContext)(null);
exports.LeafletContext = LeafletContext;
const LeafletProvider = LeafletContext.Provider;
exports.LeafletProvider = LeafletProvider;

function useLeafletContext() {
  const context = (0, _react.useContext)(LeafletContext);

  if (context == null) {
    throw new Error('No context provided: useLeafletContext() can only be used in a descendant of <MapContainer>');
  }

  return context;
}