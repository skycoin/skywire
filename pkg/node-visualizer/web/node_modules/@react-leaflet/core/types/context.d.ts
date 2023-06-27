/// <reference types="react" />
import { Control, Layer, LayerGroup, Map } from 'leaflet';
export declare const CONTEXT_VERSION = 1;
export interface ControlledLayer {
    addLayer(layer: Layer): void;
    removeLayer(layer: Layer): void;
}
export interface LeafletContextInterface {
    __version: number;
    map: Map;
    layerContainer?: ControlledLayer | LayerGroup;
    layersControl?: Control.Layers;
    overlayContainer?: Layer;
    pane?: string;
}
export declare const LeafletContext: import("react").Context<LeafletContextInterface | null>;
export declare const LeafletProvider: import("react").Provider<LeafletContextInterface | null>;
export declare function useLeafletContext(): LeafletContextInterface;
