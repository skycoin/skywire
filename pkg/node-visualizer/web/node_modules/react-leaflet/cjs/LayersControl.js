"use strict";

exports.__esModule = true;
exports.LayersControl = void 0;
exports.createControlledLayer = createControlledLayer;
exports.useLayersControlElement = exports.useLayersControl = void 0;

var _core = require("@react-leaflet/core");

var _leaflet = require("leaflet");

var _react = _interopRequireWildcard(require("react"));

function _getRequireWildcardCache(nodeInterop) { if (typeof WeakMap !== "function") return null; var cacheBabelInterop = new WeakMap(); var cacheNodeInterop = new WeakMap(); return (_getRequireWildcardCache = function (nodeInterop) { return nodeInterop ? cacheNodeInterop : cacheBabelInterop; })(nodeInterop); }

function _interopRequireWildcard(obj, nodeInterop) { if (!nodeInterop && obj && obj.__esModule) { return obj; } if (obj === null || typeof obj !== "object" && typeof obj !== "function") { return { default: obj }; } var cache = _getRequireWildcardCache(nodeInterop); if (cache && cache.has(obj)) { return cache.get(obj); } var newObj = {}; var hasPropertyDescriptor = Object.defineProperty && Object.getOwnPropertyDescriptor; for (var key in obj) { if (key !== "default" && Object.prototype.hasOwnProperty.call(obj, key)) { var desc = hasPropertyDescriptor ? Object.getOwnPropertyDescriptor(obj, key) : null; if (desc && (desc.get || desc.set)) { Object.defineProperty(newObj, key, desc); } else { newObj[key] = obj[key]; } } } newObj.default = obj; if (cache) { cache.set(obj, newObj); } return newObj; }

const useLayersControlElement = (0, _core.createElementHook)(function createLayersControl({
  children: _c,
  ...options
}, ctx) {
  const instance = new _leaflet.Control.Layers(undefined, undefined, options);
  return {
    instance,
    context: { ...ctx,
      layersControl: instance
    }
  };
}, function updateLayersControl(control, props, prevProps) {
  if (props.collapsed !== prevProps.collapsed) {
    if (props.collapsed === true) {
      control.collapse();
    } else {
      control.expand();
    }
  }
});
exports.useLayersControlElement = useLayersControlElement;
const useLayersControl = (0, _core.createControlHook)(useLayersControlElement);
exports.useLayersControl = useLayersControl;
// @ts-ignore
const LayersControl = (0, _core.createContainerComponent)(useLayersControl);
exports.LayersControl = LayersControl;

function createControlledLayer(addLayerToControl) {
  return function ControlledLayer(props) {
    const parentContext = (0, _core.useLeafletContext)();
    const propsRef = (0, _react.useRef)(props);
    const [layer, setLayer] = (0, _react.useState)(null);
    const {
      layersControl,
      map
    } = parentContext;
    const addLayer = (0, _react.useCallback)(layerToAdd => {
      if (layersControl != null) {
        if (propsRef.current.checked) {
          map.addLayer(layerToAdd);
        }

        addLayerToControl(layersControl, layerToAdd, propsRef.current.name);
        setLayer(layerToAdd);
      }
    }, [layersControl, map]);
    const removeLayer = (0, _react.useCallback)(layerToRemove => {
      layersControl == null ? void 0 : layersControl.removeLayer(layerToRemove);
      setLayer(null);
    }, [layersControl]);
    const context = (0, _react.useMemo)(() => ({ ...parentContext,
      layerContainer: {
        addLayer,
        removeLayer
      }
    }), [parentContext, addLayer, removeLayer]);
    (0, _react.useEffect)(() => {
      if (layer !== null && propsRef.current !== props) {
        if (props.checked === true && (propsRef.current.checked == null || propsRef.current.checked === false)) {
          map.addLayer(layer);
        } else if (propsRef.current.checked === true && (props.checked == null || props.checked === false)) {
          map.removeLayer(layer);
        }

        propsRef.current = props;
      }
    });
    return props.children ? /*#__PURE__*/_react.default.createElement(_core.LeafletProvider, {
      value: context
    }, props.children) : null;
  };
}

LayersControl.BaseLayer = createControlledLayer(function addBaseLayer(layersControl, layer, name) {
  layersControl.addBaseLayer(layer, name);
});
LayersControl.Overlay = createControlledLayer(function addOverlay(layersControl, layer, name) {
  layersControl.addOverlay(layer, name);
});