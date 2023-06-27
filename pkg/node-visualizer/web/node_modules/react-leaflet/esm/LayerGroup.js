import { createLayerComponent } from '@react-leaflet/core';
import { LayerGroup as LeafletLayerGroup } from 'leaflet';
export const LayerGroup = createLayerComponent(function createLayerGroup({
  children: _c,
  ...options
}, ctx) {
  const instance = new LeafletLayerGroup([], options);
  return {
    instance,
    context: { ...ctx,
      layerContainer: instance
    }
  };
});