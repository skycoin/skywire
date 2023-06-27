import { createLayerComponent, updateMediaOverlay } from '@react-leaflet/core';
import { VideoOverlay as LeafletVideoOverlay } from 'leaflet';
export const VideoOverlay = createLayerComponent(function createVideoOverlay({
  bounds,
  url,
  ...options
}, ctx) {
  const instance = new LeafletVideoOverlay(url, bounds, options);

  if (options.play === true) {
    var _instance$getElement;

    (_instance$getElement = instance.getElement()) == null ? void 0 : _instance$getElement.play();
  }

  return {
    instance,
    context: { ...ctx,
      overlayContainer: instance
    }
  };
}, function updateVideoOverlay(overlay, props, prevProps) {
  updateMediaOverlay(overlay, props, prevProps);

  if (typeof props.url === 'string' && props.url !== prevProps.url) {
    overlay.setUrl(props.url);
  }

  const video = overlay.getElement();

  if (video != null) {
    if (props.play === true && !prevProps.play) {
      video.play();
    } else if (!props.play && prevProps.play === true) {
      video.pause();
    }
  }
});