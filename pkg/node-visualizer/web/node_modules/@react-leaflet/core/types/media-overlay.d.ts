import { LatLngBoundsExpression, ImageOverlay as LeafletImageOverlay, ImageOverlayOptions, SVGOverlay as LeafletSVGOverlay, VideoOverlay as LeafletVideoOverlay } from 'leaflet';
import { InteractiveLayerProps } from './layer';
export interface MediaOverlayProps extends ImageOverlayOptions, InteractiveLayerProps {
    bounds: LatLngBoundsExpression;
}
export declare function updateMediaOverlay<E extends LeafletImageOverlay | LeafletSVGOverlay | LeafletVideoOverlay, P extends MediaOverlayProps>(overlay: E, props: P, prevProps: P): void;
