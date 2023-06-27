import { createLayerComponent, updateMediaOverlay } from '@react-leaflet/core';
import { ImageOverlay as LeafletImageOverlay } from 'leaflet';
export const ImageOverlay = createLayerComponent(function createImageOveraly({
  bounds,
  url,
  ...options
}, ctx) {
  const instance = new LeafletImageOverlay(url, bounds, options);
  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updateImageOverlay(overlay, props, prevProps) {
  updateMediaOverlay(overlay, props, prevProps);

  if (props.url !== prevProps.url) {
    overlay.setUrl(props.url);
  }
});