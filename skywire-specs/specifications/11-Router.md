# Router

The `Router` (located within the `skywire/pkg/node` module) uses the *Transport Manager* and the *Routing Table* internally, and is responsible for the handling incoming Packets (either from external nodes via transports, or internally via the `AppServer`), and also the process for setting up routes.

Regarding the Route setup process, a router should be able to interact with multiple trusted *Setup Nodes*.

Every transport created or accepted by the *Transport Manager* should be handled by the *Router*. All incoming packets should be cross-referenced against *Routing Table* and either be forwarded to next *Node* or to a local *App*. 

*Transport Manager* is also responsible for managing connections on local ports for node's apps. *App Node* will request new local connection from the *Router* on *App* startup. All incoming packets from the app's connections should be forwarded based on *App* rules defined in a local routing table. *Transport Manager* should also be capable of requesting new loops from a *Setup Node*.

## Port management

Router is responsible for port management. Port allocation algorithm should work similarly to how `tcp` manages ports:

- Certain range of ports should be reserved and be inaccessible for general purpose apps. Default staring port is `10`.
- All allocated local ports should be unique.
- App should be able to allocate static ports that it will be accessible on for remote connections. Static port allocation is performed during `app` init.
- App should be able to dynamically allocate local port for newly created loops.
- Allocated ports should be closed on app shutdown or disconnect from the node.
