import { EventedProps } from '@react-leaflet/core';
import { LayerGroup as LeafletLayerGroup, LayerOptions } from 'leaflet';
import { ReactNode } from 'react';
export interface LayerGroupProps extends LayerOptions, EventedProps {
    children?: ReactNode;
}
export declare const LayerGroup: import("react").ForwardRefExoticComponent<LayerGroupProps & import("react").RefAttributes<LeafletLayerGroup<any>>>;
