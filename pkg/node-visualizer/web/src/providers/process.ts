import {
    EdgeData,
    GeoIPRequest,
    GeoIPResponse,
    IPResult,
    NodeData,
    PollResult,
    PreProcessedGraphResult,
} from "../models/models";
import {IP_API_URL, SKY_NODEVIZ_URL} from "../utils/constants";
import {httpClient} from "./httpClient";

export const preprocessGraph = async ({nodes, edges}: PollResult): Promise<PreProcessedGraphResult> => {
    if (nodes === null || edges === null) {
        throw new Error("nodes and edges are null");
    }

    let finalNodes: NodeData[] = [];
    let pkeyMap = new Map<string, string>();
    let ips = Object.keys(nodes!);
    let apiresult: Record<string, IPResult> = await getips(ips);
    // Process Node and create a Map (Public Key -> IP)
    for (const key in apiresult) {
        if (nodes![key] === undefined) continue
        let node = {
            id: key,
            title: key,
            x: apiresult[key].latitude,
            y: apiresult[key].longitude,
            public_keys: nodes![key].public_keys,
        };
        nodes![key].public_keys.forEach(p_key => {
            pkeyMap.set(p_key, key);
        });
        finalNodes.push(node);
    }
    // Process Edges
    let finalEdges: EdgeData[] = edges!.map(edge => {
        let edgeData: EdgeData = {
            handleTooltipText: `Source : ${edge.edges[0]} \n Target : ${edge.edges[1]}`,
            sourcePKey: edge.edges[0],
            targetPKey: edge.edges[1],
            t_id: edge.t_id,
            t_type: edge.type,
            t_label: edge.label,
            source: pkeyMap.get(edge.edges[0])!,
            target: pkeyMap.get(edge.edges[1])!,
        };
        return edgeData;
    });
    return {
        nodes: finalNodes,
        edges: finalEdges,
        pkeyMap: pkeyMap,
    };
};

async function getips(ips: string[]): Promise<Record<string, IPResult>> {
    let requests = chunk(ips, 300);
    let rec: Record<string, IPResult> = {};

    // chunk it
    for (let i = 0; i < requests.length; i++) {
        let req: GeoIPRequest = {
            ips: requests[i],
        };
        const data = JSON.stringify(req);
        const res = await httpClient.post(IP_API_URL, data);
        res.data.result.forEach((ipres: IPResult) => {
            rec[ipres.ip_address] = ipres;
        });
    }

    return rec;
}

function chunk(arr: string[], size: number): string[][] {
    const length = arr.length;
    const output: string[][] = new Array(Math.ceil(length / size));
    let seekIndex = 0, outputIndex = 0;
    while (seekIndex < length) {
        output[outputIndex++] = arr.slice(seekIndex, seekIndex += size);
    }
    return output;
}

export async function fetchUptimePoll(): Promise<PollResult> {
    let result: PollResult = {};
    try {
        await httpClient.get(SKY_NODEVIZ_URL).then((r) => {
            result = r.data;
        });
        return result;
    } catch (e) {
        throw e;
    }
}
