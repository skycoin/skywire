import { LayerOptions } from 'leaflet';
import { LeafletContextInterface } from './context';
export declare function withPane<P extends LayerOptions>(props: P, context: LeafletContextInterface): P;
