import dotenv from "dotenv";
import React from "react";
import "./App.css";
import Graph from "./components/graph/graph";

dotenv.config();

interface AppProps {}

const App: React.FunctionComponent<AppProps> = () => {
    return (
        <div>
            <Graph />
        </div>
    );
};

export default App;
