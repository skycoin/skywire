"use strict";

exports.__esModule = true;
exports.createContainerComponent = createContainerComponent;
exports.createDivOverlayComponent = createDivOverlayComponent;
exports.createLeafComponent = createLeafComponent;

var _react = _interopRequireWildcard(require("react"));

var _reactDom = require("react-dom");

var _context = require("./context");

function _getRequireWildcardCache(nodeInterop) { if (typeof WeakMap !== "function") return null; var cacheBabelInterop = new WeakMap(); var cacheNodeInterop = new WeakMap(); return (_getRequireWildcardCache = function (nodeInterop) { return nodeInterop ? cacheNodeInterop : cacheBabelInterop; })(nodeInterop); }

function _interopRequireWildcard(obj, nodeInterop) { if (!nodeInterop && obj && obj.__esModule) { return obj; } if (obj === null || typeof obj !== "object" && typeof obj !== "function") { return { default: obj }; } var cache = _getRequireWildcardCache(nodeInterop); if (cache && cache.has(obj)) { return cache.get(obj); } var newObj = {}; var hasPropertyDescriptor = Object.defineProperty && Object.getOwnPropertyDescriptor; for (var key in obj) { if (key !== "default" && Object.prototype.hasOwnProperty.call(obj, key)) { var desc = hasPropertyDescriptor ? Object.getOwnPropertyDescriptor(obj, key) : null; if (desc && (desc.get || desc.set)) { Object.defineProperty(newObj, key, desc); } else { newObj[key] = obj[key]; } } } newObj.default = obj; if (cache) { cache.set(obj, newObj); } return newObj; }

function createContainerComponent(useElement) {
  function ContainerComponent(props, ref) {
    const {
      instance,
      context
    } = useElement(props).current;
    (0, _react.useImperativeHandle)(ref, () => instance);
    return props.children == null ? null : /*#__PURE__*/_react.default.createElement(_context.LeafletProvider, {
      value: context
    }, props.children);
  }

  return /*#__PURE__*/(0, _react.forwardRef)(ContainerComponent);
}

function createDivOverlayComponent(useElement) {
  function OverlayComponent(props, ref) {
    const [isOpen, setOpen] = (0, _react.useState)(false);
    const {
      instance
    } = useElement(props, setOpen).current;
    (0, _react.useImperativeHandle)(ref, () => instance);
    (0, _react.useEffect)(function updateOverlay() {
      if (isOpen) {
        instance.update();
      }
    }, [instance, isOpen, props.children]); // @ts-ignore _contentNode missing in type definition

    const contentNode = instance._contentNode;
    return contentNode ? /*#__PURE__*/(0, _reactDom.createPortal)(props.children, contentNode) : null;
  }

  return /*#__PURE__*/(0, _react.forwardRef)(OverlayComponent);
}

function createLeafComponent(useElement) {
  function LeafComponent(props, ref) {
    const {
      instance
    } = useElement(props).current;
    (0, _react.useImperativeHandle)(ref, () => instance);
    return null;
  }

  return /*#__PURE__*/(0, _react.forwardRef)(LeafComponent);
}