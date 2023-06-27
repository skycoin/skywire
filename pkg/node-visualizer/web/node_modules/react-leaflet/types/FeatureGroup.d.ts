/// <reference types="react" />
import { PathProps } from '@react-leaflet/core';
import { FeatureGroup as LeafletFeatureGroup } from 'leaflet';
import { LayerGroupProps } from './LayerGroup';
export interface FeatureGroupProps extends LayerGroupProps, PathProps {
}
export declare const FeatureGroup: import("react").ForwardRefExoticComponent<FeatureGroupProps & import("react").RefAttributes<LeafletFeatureGroup<any>>>;
