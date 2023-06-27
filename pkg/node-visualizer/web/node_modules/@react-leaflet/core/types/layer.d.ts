import { InteractiveLayerOptions, Layer, LayerOptions } from 'leaflet';
import { LeafletContextInterface } from './context';
import { LeafletElement, ElementHook } from './element';
import { EventedProps } from './events';
export interface LayerProps extends EventedProps, LayerOptions {
}
export interface InteractiveLayerProps extends LayerProps, InteractiveLayerOptions {
}
export declare function useLayerLifecycle(element: LeafletElement<Layer>, context: LeafletContextInterface): void;
export declare function createLayerHook<E extends Layer, P extends LayerProps>(useElement: ElementHook<E, P>): (props: P) => ReturnType<ElementHook<E, P>>;
