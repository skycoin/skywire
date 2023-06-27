/// <reference types="react" />
import { Control, ControlOptions, FeatureGroup, Layer, Path } from 'leaflet';
import { PropsWithChildren } from './component';
import { LeafletContextInterface } from './context';
import { LeafletElement } from './element';
import { LayerProps } from './layer';
import { DivOverlay, DivOverlayLifecycleHook } from './div-overlay';
import { PathProps } from './path';
interface LayerWithChildrenProps extends LayerProps, PropsWithChildren {
}
interface PathWithChildrenProps extends PathProps, PropsWithChildren {
}
export declare function createControlComponent<E extends Control, P extends ControlOptions>(createInstance: (props: P) => E): import("react").ForwardRefExoticComponent<import("react").PropsWithoutRef<P> & import("react").RefAttributes<E>>;
export declare function createLayerComponent<E extends Layer, P extends LayerWithChildrenProps>(createElement: (props: P, context: LeafletContextInterface) => LeafletElement<E>, updateElement?: (instance: E, props: P, prevProps: P) => void): import("react").ForwardRefExoticComponent<import("react").PropsWithoutRef<P> & import("react").RefAttributes<E>>;
export declare function createOverlayComponent<E extends DivOverlay, P extends LayerWithChildrenProps>(createElement: (props: P, context: LeafletContextInterface) => LeafletElement<E>, useLifecycle: DivOverlayLifecycleHook<E, P>): import("react").ForwardRefExoticComponent<import("react").PropsWithoutRef<P> & import("react").RefAttributes<E>>;
export declare function createPathComponent<E extends FeatureGroup | Path, P extends PathWithChildrenProps>(createElement: (props: P, context: LeafletContextInterface) => LeafletElement<E>, updateElement?: (instance: E, props: P, prevProps: P) => void): import("react").ForwardRefExoticComponent<import("react").PropsWithoutRef<P> & import("react").RefAttributes<E>>;
export declare function createTileLayerComponent<E extends Layer, P extends LayerProps>(createElement: (props: P, context: LeafletContextInterface) => LeafletElement<E>, updateElement?: (instance: E, props: P, prevProps: P) => void): import("react").ForwardRefExoticComponent<import("react").PropsWithoutRef<P> & import("react").RefAttributes<E>>;
export {};
