import { createPathComponent, updateCircle } from '@react-leaflet/core';
import { CircleMarker as LeafletCircleMarker } from 'leaflet';
export const CircleMarker = createPathComponent(function createCircleMarker({
  center,
  children: _c,
  ...options
}, ctx) {
  const instance = new LeafletCircleMarker(center, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, updateCircle);