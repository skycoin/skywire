import React, { CSSProperties, ReactNode } from 'react';
export interface PaneProps {
    children?: ReactNode;
    className?: string;
    name: string;
    pane?: string;
    style?: CSSProperties;
}
export declare function Pane(props: PaneProps): React.ReactPortal | null;
