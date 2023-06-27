import { LatLngExpression, CircleMarker as LeafletCircleMarker, CircleMarkerOptions } from 'leaflet';
import { ReactNode } from 'react';
import { PathProps } from './path';
export interface CircleMarkerProps extends CircleMarkerOptions, PathProps {
    center: LatLngExpression;
    children?: ReactNode;
}
export declare function updateCircle<P extends CircleMarkerProps = CircleMarkerProps>(layer: LeafletCircleMarker, props: P, prevProps: P): void;
