import { PathProps } from '@react-leaflet/core';
import { LatLngExpression, PolylineOptions, Polygon as LeafletPolygon } from 'leaflet';
import { ReactNode } from 'react';
export interface PolygonProps extends PolylineOptions, PathProps {
    children?: ReactNode;
    positions: LatLngExpression[] | LatLngExpression[][] | LatLngExpression[][][];
}
export declare const Polygon: import("react").ForwardRefExoticComponent<PolygonProps & import("react").RefAttributes<LeafletPolygon<any>>>;
