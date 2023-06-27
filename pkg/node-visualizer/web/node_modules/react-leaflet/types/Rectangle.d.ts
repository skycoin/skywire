import { PathProps } from '@react-leaflet/core';
import { LatLngBoundsExpression, Rectangle as LeafletRectangle, PathOptions } from 'leaflet';
import { ReactNode } from 'react';
export interface RectangleProps extends PathOptions, PathProps {
    bounds: LatLngBoundsExpression;
    children?: ReactNode;
}
export declare const Rectangle: import("react").ForwardRefExoticComponent<RectangleProps & import("react").RefAttributes<LeafletRectangle<any>>>;
