import React, { forwardRef, useEffect, useImperativeHandle, useState } from 'react';
import { createPortal } from 'react-dom';
import { LeafletProvider } from './context';
export function createContainerComponent(useElement) {
  function ContainerComponent(props, ref) {
    const {
      instance,
      context
    } = useElement(props).current;
    useImperativeHandle(ref, () => instance);
    return props.children == null ? null : /*#__PURE__*/React.createElement(LeafletProvider, {
      value: context
    }, props.children);
  }

  return /*#__PURE__*/forwardRef(ContainerComponent);
}
export function createDivOverlayComponent(useElement) {
  function OverlayComponent(props, ref) {
    const [isOpen, setOpen] = useState(false);
    const {
      instance
    } = useElement(props, setOpen).current;
    useImperativeHandle(ref, () => instance);
    useEffect(function updateOverlay() {
      if (isOpen) {
        instance.update();
      }
    }, [instance, isOpen, props.children]); // @ts-ignore _contentNode missing in type definition

    const contentNode = instance._contentNode;
    return contentNode ? /*#__PURE__*/createPortal(props.children, contentNode) : null;
  }

  return /*#__PURE__*/forwardRef(OverlayComponent);
}
export function createLeafComponent(useElement) {
  function LeafComponent(props, ref) {
    const {
      instance
    } = useElement(props).current;
    useImperativeHandle(ref, () => instance);
    return null;
  }

  return /*#__PURE__*/forwardRef(LeafComponent);
}