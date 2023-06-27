import { Icon, LatLng, LatLngBounds } from "leaflet";
import React, { Fragment, useEffect, useState } from "react";
import { MapContainer, Marker, Popup, TileLayer } from "react-leaflet";
import Modal from "react-modal";
import { toast } from "react-toastify";
import marker from "../../images/marker.svg";
import { EdgeData, NodeData, PreProcessedGraphResult, SelectedData } from "../../models/models";
import { fetchUptimePoll, preprocessGraph } from "../../providers/process";
import SidenavEdge from "./sidenavEdge";

export interface GraphProps {
}

interface GraphState {
    loading: boolean;
    selected: SelectedData;
    preprocessedResult: PreProcessedGraphResult;
    errMsg: string;
}

const Graph: React.FC<GraphProps> = () => {
    const markerIcon = new Icon({
        iconUrl: marker,
        iconSize: [25, 25],
    });
    const [isOpen, setIsOpen] = useState(false);

    function toggleModal() {
        setIsOpen(!isOpen);
    }

    const [isInitial, setIsInitial] = useState<boolean>(false);
    const [selectedEdge, setSelectedEdge] = useState<EdgeData | undefined>(undefined);
    const [graphState, setGraphState] = useState<GraphState>({
        loading: false,
        errMsg: "",
        selected: {
            node: undefined,
            edges: undefined,
        },
        preprocessedResult: {
            nodes: [],
            edges: [],
            pkeyMap: new Map<string, string>(),
        },
    });

    const TIMEOUT_MS = 60000 * 5;

    useEffect(() => {
        fetchUpdate().catch((e) => setGraphState({ ...graphState, errMsg: e, loading: false }));
        setIsInitial(true);
    }, []);

    useEffect(() => {
        const interval = setInterval(() => {
            fetchUpdate().catch((e) => setGraphState({ ...graphState, errMsg: e, loading: false }));
        }, TIMEOUT_MS);

        return () => clearInterval(interval);
    }, [isInitial]);

    async function fetchUpdate() {
        setGraphState({ ...graphState, loading: true });
        try {
            fetchUptimePoll().then((r) => {
                preprocessGraph(r).then((res) => {
                    setGraphState({ ...graphState, preprocessedResult: res });
                });
            });
        } catch (e: any) {
            setGraphState({ ...graphState, errMsg: e, loading: false });
        }
    }

    function filterEdges(edge: EdgeData, index: number, array: EdgeData[]): boolean {
        let keys = graphState.selected.node?.public_keys;
        if (keys === undefined) return false;
        for (let j = 0; j < keys.length; j++) {
            if (edge.sourcePKey === keys![j] || edge.targetPKey === keys[j]) {
                return true;
            }
        }
        return false;
    }

    function getEdges(selected: NodeData, edges: EdgeData[]): EdgeData[] | undefined {
        return edges.filter(filterEdges);
    }

    const southWest = new LatLng(-90, -180);
    const northEast = new LatLng(90, 180);

    const handleEdges = (key: string) => {
        if (graphState.selected.edges === undefined) return;
        let edge = graphState.selected.edges!.filter((el, idx, arr) => el.sourcePKey === key || el.targetPKey === key);
        if (edge.length > 0) {
            setSelectedEdge(edge[0]);
            return (e: any) => toggleModal();
        } else return (e: any) => {};
    };

    const pubKeyRender = (pubkeys: string[]) => {
        return pubkeys.map((k: string, i: number) => <li onClick={handleEdges(k)}>{k}</li>);
    };

    return (
        <Fragment>
            <MapContainer
                zoom={3}
                center={[45.00, 90.00]}
                scrollWheelZoom={false}
                bounds={new LatLngBounds(southWest, northEast)}
                maxBounds={new LatLngBounds(southWest, northEast)}
            >
                <TileLayer url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png" />
                {graphState.preprocessedResult.nodes.map((n) => (
                    <Marker
                        key={n.id}
                        position={[n.x!, n.y!]}
                        eventHandlers={{
                            click: (_: any) => {
                                let selectedEdges = getEdges(n, graphState.preprocessedResult.edges);
                                console.log("EDGES: ", selectedEdges);
                                setGraphState({
                                    ...graphState,
                                    selected: {
                                        node: n,
                                        edges: selectedEdges,
                                    },
                                });
                            },
                        }}
                        icon={markerIcon}
                    >
                        <Popup>
                            <div className="map-popup">
                                <h3>Visors</h3>
                                <ul>
                                    {n.public_keys !== undefined && n.public_keys.length > 0
                                        ? pubKeyRender(n.public_keys)
                                        : <p>Empty</p>}
                                </ul>
                                {selectedEdge !== undefined
                                    ? (
                                        <Modal
                                            isOpen={isOpen}
                                            onRequestClose={toggleModal}
                                            className="edge-modal"
                                            overlayClassName="edge-overlay"
                                            preventScroll={false}
                                        >
                                            <SidenavEdge edge={selectedEdge} />
                                        </Modal>
                                    )
                                    : <div />}
                            </div>
                            {/*<SidenavNode node={graphState.selected.node!} />*/}
                        </Popup>
                    </Marker>
                ))}
            </MapContainer>
            {graphState.errMsg !== ""
                ? toast({
                    error: graphState.errMsg,
                    position: "top-right",
                    autoClose: 5000,
                    closeOnClick: true,
                    pauseOnHover: true,
                    draggable: true,
                })
                : <div />}
            {graphState.loading
                ? toast({
                    info: "loading data",
                    position: "top-right",
                    dismiss: graphState.loading,
                })
                : <div />}
        </Fragment>
    );
};

export default Graph;
