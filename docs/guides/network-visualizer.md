# Network visualization UI

Skywire includes a network visualization and visor control interface that can run in two modes.

**Visor-embedded mode** (recommended when visor is running):
```
skywire cli tp viz --visor
```
This starts the UI as part of the visor, with direct access to local transport and route data.

**Standalone mode** (network visualization only):
```
skywire cli tp viz
```
This runs a standalone visualization server using transport discovery data.

The web UI (default `localhost:8080`) provides:

* **Real-time network graph**: Visual representation of visors and their connections in the Skywire network
* **Transport information**: View active transports with details on type (STCPR, SUDPH, DMSG), remote public keys, and connection status
* **Geographic clustering**: Visors are grouped by country and IP subnet for easier network topology understanding
* **Click-to-copy**: Easily copy public keys by clicking on nodes in the graph
* **Visor control** (visor mode): Direct interface for managing the local visor

Note: This is a separate UI from the hypervisor interface and caches transport data locally.
