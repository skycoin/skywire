import { Control, ControlOptions } from 'leaflet';
import { ElementHook } from './element';
export declare function createControlHook<E extends Control, P extends ControlOptions>(useElement: ElementHook<E, P>): (props: P) => ReturnType<ElementHook<E, P>>;
