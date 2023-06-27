import { MutableRefObject } from 'react';
import { LeafletContextInterface } from './context';
export interface LeafletElement<T, C = any> {
    instance: T;
    context: LeafletContextInterface;
    container?: C | null;
}
export declare type ElementHook<E, P> = (props: P, context: LeafletContextInterface) => MutableRefObject<LeafletElement<E>>;
export declare function createElementHook<E, P, C = any>(createElement: (props: P, context: LeafletContextInterface) => LeafletElement<E>, updateElement?: (instance: E, props: P, prevProps: P) => void): (props: P, context: LeafletContextInterface) => ReturnType<ElementHook<E, P>>;
