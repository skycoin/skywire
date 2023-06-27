import { Evented, LeafletEventHandlerFnMap } from 'leaflet';
import { LeafletElement } from './element';
export interface EventedProps {
    eventHandlers?: LeafletEventHandlerFnMap;
}
export declare function useEventHandlers(element: LeafletElement<Evented>, eventHandlers: LeafletEventHandlerFnMap | null | undefined): void;
