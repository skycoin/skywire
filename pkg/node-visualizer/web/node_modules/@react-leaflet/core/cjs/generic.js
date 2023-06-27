"use strict";

exports.__esModule = true;
exports.createControlComponent = createControlComponent;
exports.createLayerComponent = createLayerComponent;
exports.createOverlayComponent = createOverlayComponent;
exports.createPathComponent = createPathComponent;
exports.createTileLayerComponent = createTileLayerComponent;

var _component = require("./component");

var _control = require("./control");

var _element = require("./element");

var _layer = require("./layer");

var _divOverlay = require("./div-overlay");

var _path = require("./path");

function createControlComponent(createInstance) {
  function createElement(props, context) {
    return {
      instance: createInstance(props),
      context
    };
  }

  const useElement = (0, _element.createElementHook)(createElement);
  const useControl = (0, _control.createControlHook)(useElement);
  return (0, _component.createLeafComponent)(useControl);
}

function createLayerComponent(createElement, updateElement) {
  const useElement = (0, _element.createElementHook)(createElement, updateElement);
  const useLayer = (0, _layer.createLayerHook)(useElement);
  return (0, _component.createContainerComponent)(useLayer);
}

function createOverlayComponent(createElement, useLifecycle) {
  const useElement = (0, _element.createElementHook)(createElement);
  const useOverlay = (0, _divOverlay.createDivOverlayHook)(useElement, useLifecycle);
  return (0, _component.createDivOverlayComponent)(useOverlay);
}

function createPathComponent(createElement, updateElement) {
  const useElement = (0, _element.createElementHook)(createElement, updateElement);
  const usePath = (0, _path.createPathHook)(useElement);
  return (0, _component.createContainerComponent)(usePath);
}

function createTileLayerComponent(createElement, updateElement) {
  const useElement = (0, _element.createElementHook)(createElement, updateElement);
  const useLayer = (0, _layer.createLayerHook)(useElement);
  return (0, _component.createLeafComponent)(useLayer);
}