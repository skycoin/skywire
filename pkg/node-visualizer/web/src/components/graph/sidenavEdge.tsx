import React from "react";
import { EdgeData } from "../../models/models";

export interface SidenavEdgeProps {
    edge: EdgeData;
}

const SidenavEdge: React.FC<SidenavEdgeProps> = ({ edge }) => {
    return (
        <div id="node-info">
            <h2>Transport</h2>
            <p className="tt">{edge.t_id}</p>
            <strong>Source IP:</strong>
            {edge.source}
            <br />
            <strong>Source IP:</strong>
            {edge.source}
            <br />
            <strong>Target IP:</strong>
            {edge.target}
            <br />
            <strong>Transport Type::</strong>
            {edge.t_type}
            <br />
            <strong>Transport Label:</strong>
            {edge.t_label}
            <br />
            <strong>Source Visor:</strong>
            <p>{edge.sourcePKey}</p>
            <strong>Target Visor:</strong>
            <p>{edge.targetPKey}</p>
            <br />
        </div>
    );
};

export default SidenavEdge;
