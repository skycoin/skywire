import { createPathComponent } from '@react-leaflet/core';
import { FeatureGroup as LeafletFeatureGroup } from 'leaflet';
export const FeatureGroup = createPathComponent(function createFeatureGroup({
  children: _c,
  ...options
}, ctx) {
  const instance = new LeafletFeatureGroup([], options);
  const context = { ...ctx,
    layerContainer: instance,
    overlayContainer: instance
  };
  return {
    instance,
    context
  };
});