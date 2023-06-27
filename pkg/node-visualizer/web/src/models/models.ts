export interface PollResult {
    nodes?: NodesResult;
    edges?: TransportEntry[];
}

export interface PreProcessedGraphResult {
    nodes: NodeData[];
    edges: EdgeData[];
    pkeyMap: Map<string, string>;
}

export interface NodesResult {
    [key: string]: VisorIPResponse;
}

export interface TransportEntry {
    t_id: string;
    edges: string[];
    type: string;
    label: string;
}

export interface VisorIPResponse {
    count: number;
    public_keys: string[];
}

export interface GeoIPRequest {
    ips: string[];
}

export interface GeoIPResponse {
    result: IPResult[];
}

export interface IPResult {
    longitude: number; // longitude
    latitude: number; // latitude
    ip_address: string;
}

export interface NodeData {
    id?: string;
    title?: string;
    type?: string;
    x?: number | null | undefined;
    y?: number | null | undefined;
    public_keys?: string[];
}

export interface EdgeData {
    handleTooltipText?: string;
    sourcePKey?: string;
    targetPKey?: string;
    t_id?: string;
    t_type?: string;
    t_label?: string;
    source: string;
    target: string;
    type?: string;
}

export interface SelectedData {
    node: NodeData | undefined;
    edges: EdgeData[] | undefined;
}
