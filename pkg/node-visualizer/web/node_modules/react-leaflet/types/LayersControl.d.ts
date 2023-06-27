import { Control, Layer } from 'leaflet';
import React, { ForwardRefExoticComponent, FunctionComponent, ReactNode, RefAttributes } from 'react';
export interface LayersControlProps extends Control.LayersOptions {
    children?: ReactNode;
}
export declare const useLayersControlElement: (props: LayersControlProps, context: import("@react-leaflet/core").LeafletContextInterface) => React.MutableRefObject<import("@react-leaflet/core").LeafletElement<Control.Layers, any>>;
export declare const useLayersControl: (props: LayersControlProps) => React.MutableRefObject<import("@react-leaflet/core").LeafletElement<Control.Layers, any>>;
export interface ControlledLayerProps {
    checked?: boolean;
    children: ReactNode;
    name: string;
}
export declare const LayersControl: ForwardRefExoticComponent<LayersControlProps & RefAttributes<Control.Layers>> & {
    BaseLayer: FunctionComponent<ControlledLayerProps>;
    Overlay: FunctionComponent<ControlledLayerProps>;
};
declare type AddLayerFunc = (layersControl: Control.Layers, layer: Layer, name: string) => void;
export declare function createControlledLayer(addLayerToControl: AddLayerFunc): (props: ControlledLayerProps) => JSX.Element | null;
export {};
