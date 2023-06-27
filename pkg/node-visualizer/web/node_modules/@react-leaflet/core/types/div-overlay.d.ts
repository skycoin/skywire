import { Popup, Tooltip } from 'leaflet';
import { LeafletContextInterface } from './context';
import { LeafletElement, ElementHook } from './element';
import { LayerProps } from './layer';
export declare type DivOverlay = Popup | Tooltip;
export declare type SetOpenFunc = (open: boolean) => void;
export declare type DivOverlayLifecycleHook<E, P> = (element: LeafletElement<E>, context: LeafletContextInterface, props: P, setOpen: SetOpenFunc) => void;
export declare type DivOverlayHook<E extends DivOverlay, P> = (useElement: ElementHook<E, P>, useLifecycle: DivOverlayLifecycleHook<E, P>) => (props: P, setOpen: SetOpenFunc) => ReturnType<ElementHook<E, P>>;
export declare function createDivOverlayHook<E extends DivOverlay, P extends LayerProps>(useElement: ElementHook<E, P>, useLifecycle: DivOverlayLifecycleHook<E, P>): (props: P, setOpen: SetOpenFunc) => ReturnType<ElementHook<E, P>>;
