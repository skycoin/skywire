import { createContainerComponent, createDivOverlayComponent, createLeafComponent } from './component';
import { createControlHook } from './control';
import { createElementHook } from './element';
import { createLayerHook } from './layer';
import { createDivOverlayHook } from './div-overlay';
import { createPathHook } from './path';
export function createControlComponent(createInstance) {
  function createElement(props, context) {
    return {
      instance: createInstance(props),
      context
    };
  }

  const useElement = createElementHook(createElement);
  const useControl = createControlHook(useElement);
  return createLeafComponent(useControl);
}
export function createLayerComponent(createElement, updateElement) {
  const useElement = createElementHook(createElement, updateElement);
  const useLayer = createLayerHook(useElement);
  return createContainerComponent(useLayer);
}
export function createOverlayComponent(createElement, useLifecycle) {
  const useElement = createElementHook(createElement);
  const useOverlay = createDivOverlayHook(useElement, useLifecycle);
  return createDivOverlayComponent(useOverlay);
}
export function createPathComponent(createElement, updateElement) {
  const useElement = createElementHook(createElement, updateElement);
  const usePath = createPathHook(useElement);
  return createContainerComponent(usePath);
}
export function createTileLayerComponent(createElement, updateElement) {
  const useElement = createElementHook(createElement, updateElement);
  const useLayer = createLayerHook(useElement);
  return createLeafComponent(useLayer);
}