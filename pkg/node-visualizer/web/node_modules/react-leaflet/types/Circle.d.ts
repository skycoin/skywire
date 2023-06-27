/// <reference types="react" />
import { CircleMarkerProps } from '@react-leaflet/core';
import { Circle as LeafletCircle } from 'leaflet';
export declare type CircleProps = CircleMarkerProps;
export declare const Circle: import("react").ForwardRefExoticComponent<CircleMarkerProps & import("react").RefAttributes<LeafletCircle<any>>>;
