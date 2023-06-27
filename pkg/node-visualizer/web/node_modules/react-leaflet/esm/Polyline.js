import { createPathComponent } from '@react-leaflet/core';
import { Polyline as LeafletPolyline } from 'leaflet';
export const Polyline = createPathComponent(function createPolyline({
  positions,
  ...options
}, ctx) {
  const instance = new LeafletPolyline(positions, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updatePolyline(layer, props, prevProps) {
  if (props.positions !== prevProps.positions) {
    layer.setLatLngs(props.positions);
  }
});