import { createPathComponent, updateCircle } from '@react-leaflet/core';
import { Circle as LeafletCircle } from 'leaflet';
export const Circle = createPathComponent(function createCircle({
  center,
  children: _c,
  ...options
}, ctx) {
  const instance = new LeafletCircle(center, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, updateCircle);