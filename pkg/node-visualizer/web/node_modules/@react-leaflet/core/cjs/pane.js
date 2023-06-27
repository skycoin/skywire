"use strict";

exports.__esModule = true;
exports.withPane = withPane;

function withPane(props, context) {
  const pane = props.pane ?? context.pane;
  return pane ? { ...props,
    pane
  } : props;
}