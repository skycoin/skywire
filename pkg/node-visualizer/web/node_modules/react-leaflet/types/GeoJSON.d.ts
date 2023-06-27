/// <reference types="react" />
import { PathProps } from '@react-leaflet/core';
import { GeoJsonObject } from 'geojson';
import { GeoJSON as LeafletGeoJSON, GeoJSONOptions } from 'leaflet';
import { LayerGroupProps } from './LayerGroup';
export interface GeoJSONProps extends GeoJSONOptions, LayerGroupProps, PathProps {
    data: GeoJsonObject;
}
export declare const GeoJSON: import("react").ForwardRefExoticComponent<GeoJSONProps & import("react").RefAttributes<LeafletGeoJSON<any>>>;
