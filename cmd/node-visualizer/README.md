# Uptime Tracker

## Purpose

The Node visualizer is used to visualize the running visors geographically on a map as a graph where the nodes of the graph represent an IP address having one or more active visors and the edges represent the active transports/connections . It is a server side rendered react application . The server is build using go .

## Local Runtime intructions

### Run just the react application locally in env environment

```bash
yarn --cwd ./pkg/node-visualizer/web install
yarn --cwd ./pkg/node-visualizer/web start
```

### Run the server side application

```bash
make build
./bin/node-visualizer
```
