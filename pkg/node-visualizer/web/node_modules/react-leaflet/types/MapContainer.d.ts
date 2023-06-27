import { FitBoundsOptions, LatLngBoundsExpression, Map as LeafletMap, MapOptions } from 'leaflet';
import { CSSProperties, MutableRefObject, ReactNode } from 'react';
export interface MapContainerProps extends MapOptions {
    bounds?: LatLngBoundsExpression;
    boundsOptions?: FitBoundsOptions;
    children?: ReactNode;
    className?: string;
    id?: string;
    placeholder?: ReactNode;
    style?: CSSProperties;
    whenCreated?: (map: LeafletMap) => void;
    whenReady?: () => void;
}
export declare function useMapElement(mapRef: MutableRefObject<HTMLElement | null>, props: MapContainerProps): LeafletMap | null;
export declare function MapContainer<Props extends MapContainerProps = MapContainerProps>({ children, className, id, placeholder, style, whenCreated, ...options }: Props): JSX.Element;
