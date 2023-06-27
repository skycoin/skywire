"use strict";

exports.__esModule = true;
exports.Pane = Pane;

var _core = require("@react-leaflet/core");

var _react = _interopRequireWildcard(require("react"));

var _reactDom = require("react-dom");

function _getRequireWildcardCache(nodeInterop) { if (typeof WeakMap !== "function") return null; var cacheBabelInterop = new WeakMap(); var cacheNodeInterop = new WeakMap(); return (_getRequireWildcardCache = function (nodeInterop) { return nodeInterop ? cacheNodeInterop : cacheBabelInterop; })(nodeInterop); }

function _interopRequireWildcard(obj, nodeInterop) { if (!nodeInterop && obj && obj.__esModule) { return obj; } if (obj === null || typeof obj !== "object" && typeof obj !== "function") { return { default: obj }; } var cache = _getRequireWildcardCache(nodeInterop); if (cache && cache.has(obj)) { return cache.get(obj); } var newObj = {}; var hasPropertyDescriptor = Object.defineProperty && Object.getOwnPropertyDescriptor; for (var key in obj) { if (key !== "default" && Object.prototype.hasOwnProperty.call(obj, key)) { var desc = hasPropertyDescriptor ? Object.getOwnPropertyDescriptor(obj, key) : null; if (desc && (desc.get || desc.set)) { Object.defineProperty(newObj, key, desc); } else { newObj[key] = obj[key]; } } } newObj.default = obj; if (cache) { cache.set(obj, newObj); } return newObj; }

const DEFAULT_PANES = ['mapPane', 'markerPane', 'overlayPane', 'popupPane', 'shadowPane', 'tilePane', 'tooltipPane'];

function omitPane(obj, pane) {
  const {
    [pane]: _p,
    ...others
  } = obj;
  return others;
}

function createPane(props, context) {
  const name = props.name;

  if (DEFAULT_PANES.indexOf(name) !== -1) {
    throw new Error(`You must use a unique name for a pane that is not a default Leaflet pane: ${name}`);
  }

  if (context.map.getPane(name) != null) {
    throw new Error(`A pane with this name already exists: ${name}`);
  }

  const parentPaneName = props.pane ?? context.pane;
  const parentPane = parentPaneName ? context.map.getPane(parentPaneName) : undefined;
  const element = context.map.createPane(name, parentPane);

  if (props.className != null) {
    (0, _core.addClassName)(element, props.className);
  }

  if (props.style != null) {
    Object.keys(props.style).forEach(key => {
      // @ts-ignore
      element.style[key] = props.style[key];
    });
  }

  return element;
}

function Pane(props) {
  const [paneElement, setPaneElement] = (0, _react.useState)();
  const context = (0, _core.useLeafletContext)();
  const newContext = (0, _react.useMemo)(() => ({ ...context,
    pane: props.name
  }), [context]);
  (0, _react.useEffect)(() => {
    setPaneElement(createPane(props, context));
    return function removeCreatedPane() {
      const pane = context.map.getPane(props.name);
      pane == null ? void 0 : pane.remove == null ? void 0 : pane.remove(); // @ts-ignore map internals

      if (context.map._panes != null) {
        // @ts-ignore map internals
        context.map._panes = omitPane(context.map._panes, props.name); // @ts-ignore map internals

        context.map._paneRenderers = omitPane( // @ts-ignore map internals
        context.map._paneRenderers, props.name);
      }
    }; // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return props.children != null && paneElement != null ? /*#__PURE__*/(0, _reactDom.createPortal)( /*#__PURE__*/_react.default.createElement(_core.LeafletProvider, {
    value: newContext
  }, props.children), paneElement) : null;
}