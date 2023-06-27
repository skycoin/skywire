import { FeatureGroup, Path, PathOptions } from 'leaflet';
import { LeafletElement, ElementHook } from './element';
import { InteractiveLayerProps } from './layer';
export interface PathProps extends InteractiveLayerProps {
    pathOptions?: PathOptions;
}
export declare function usePathOptions(element: LeafletElement<FeatureGroup | Path>, props: PathProps): void;
export declare function createPathHook<E extends FeatureGroup | Path, P extends PathProps>(useElement: ElementHook<E, P>): (props: P) => ReturnType<ElementHook<E, P>>;
