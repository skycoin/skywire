import { useEffect, useRef } from 'react';
export function createElementHook(createElement, updateElement) {
  if (updateElement == null) {
    return function useImmutableLeafletElement(props, context) {
      return useRef(createElement(props, context));
    };
  }

  return function useMutableLeafletElement(props, context) {
    const elementRef = useRef(createElement(props, context));
    const propsRef = useRef(props);
    const {
      instance
    } = elementRef.current;
    useEffect(function updateElementProps() {
      if (propsRef.current !== props) {
        updateElement(instance, props, propsRef.current);
        propsRef.current = props;
      }
    }, [instance, props, context]);
    return elementRef;
  };
}