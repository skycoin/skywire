"use strict";

exports.__esModule = true;
exports.addClassName = addClassName;
exports.removeClassName = removeClassName;
exports.updateClassName = updateClassName;

var _leaflet = require("leaflet");

function splitClassName(className) {
  return className.split(' ').filter(Boolean);
}

function addClassName(element, className) {
  splitClassName(className).forEach(cls => {
    _leaflet.DomUtil.addClass(element, cls);
  });
}

function removeClassName(element, className) {
  splitClassName(className).forEach(cls => {
    _leaflet.DomUtil.removeClass(element, cls);
  });
}

function updateClassName(element, prevClassName, nextClassName) {
  if (element != null && nextClassName !== prevClassName) {
    if (prevClassName != null && prevClassName.length > 0) {
      removeClassName(element, prevClassName);
    }

    if (nextClassName != null && nextClassName.length > 0) {
      addClassName(element, nextClassName);
    }
  }
}