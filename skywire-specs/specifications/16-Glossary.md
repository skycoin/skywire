# Glossary

**Skywire Transport:**

Identified via a Transport ID, a Skywire Transport is represented as an interface and can be implemented as different transport types. It is a bi-directional line of communication between two Skywire nodes (transport edges) and constructs a single hop of a route.

It is the responsibility of a Skywire Factory to generate Skywire Transports.

**Skywire Transport ID:**

A Skywire Transport is an uint16 integer that refers to a Skywire Transport and identifies it in the transport discovery. Skywire Transport IDs are assigned uniquely by and for a transport edge. Therefore, the same transport can be referenced by two different Transport IDs, assigned by the 2 edges. 

**Transport Edge:** 

A transport edge is one of the two Skywire nodes that make up a transport. It is represented by a unique public key that identifies the Skywire node. 

**Transport Type:** 

A Transport Type (represented by a string) refers to the underlying implementation of a Transport Factory or a Transport. A Transport Factory of a certain type can only construct Transports of that type. 

Initially, we will have two transport types; messaging and native. The messaging transport is implemented by the messaging system. The native transport is non-dependent on the current internet and in the future will be the main transport implementation for Skywire.

**Transport Factory:**

A transport factory is used by a Skywire node which constructs transports of a certain transport type. It is an interface that can either dial or listen for remotely initiated transports. 

**Transport Perspective:**

A transport perspective is the assumed state of a transport (number of packets and bandwidth sent and received over that transport; whether transport is up or down). It only represents the perspective (on a transport) from a single transport edge and therefore the perspectives of two edges on the same transport might conflict. 

**Transport Discovery:** 

The transport discovery is a service that registers transports and the associated transport perspectives. It thereby provides the basis for the route finding service because it represents the public network topology. It is queried by the route finding service to discover routes.

**Hop:**

A hop is equivalent to a transport and is a single unit of a route.

**Skywire Route:**

A Skywire Route is a unidirectional network path that allows apps and services to communicate. It can be made up of one or more hops between Skywire Nodes. It is identified by route IDs, interpreted by individual Skywire Nodes via the routing table. 

**Skywire Route ID:** 

A route ID is represented by 32 bits and is the basis for a Skywire Node’s routing rules. Route IDs are changed along every hop of a route and there is a unique set of route IDs for every node. 

**Route Finder:**

The route finder is a service that evaluates the network topology via the information of the transport discovery to provide possible routes to inquiring Skywire nodes. Currently it evaluates possible routes only on the basis of the hop metric.


**Routing Table:**

The Routing Table is a key-value store that determines the action to be performed on an incoming Packet.

Using the Routing ID (from the Packet) as the key, a Skywire Node can obtain either the next transport that the packet is to be sent over (and the new Routing ID) or whether the packet is to be consumed by the node itself.

**Stream:**

A stream represents a bi-directional line of communication between two Skywire Apps. A stream can use multiple Routes to establish itself.

**Skywire App:** 

A Skywire App is an executable that interacts with the Skywire App Node via Unix pipes. Skywire apps provide services for the end user such as a proxy, ssh, chat, etc. 

**Messaging System:** 

The messaging system is primitive fallback implementation of Transport and Transport Factory over the Internet. It consists of a messaging discovery, client and server instances, where the server instance relays messages between clients. The communication between two clients is called a messaging channel. 

**Messaging Instance:** 

Messaging instances (alongside the messaging discovery) are the main components of the messaging system. A messaging instance can be either a messaging server or messaging client. 

**Messaging Link:** 

A messaging link is the direct line of communication between a server instance and a client instance. A messaging channel between two client instances can be constructed from two messaging links to a shared server instance. 

**Messaging Discovery:** 

The messaging discovery is a key value store that registers the messenger servers that a given messenger client is connected to. It allows clients to get the information necessary to establish a messaging transport with another node by querying the public keys associated messaging servers. 

**Messaging Channel:**    

A Messaging Channel represents the bi-directional connection-oriented line of communication between two client instances of the messaging system.

**Skywire Node:**

A Skywire Node is the general term for nodes that make up the Skywire Network. Any entity that manages Transports and Packets is considered to be a Skywire Node.

Examples of Skywire Nodes include; App Node (which manage Skywire Apps), Control Node (which administers App Nodes) and the Route Setup Node (which coordinates the construction of Routes with App Nodes).

**Skywire App Node:**

An App Node is a Skywire Node that runs, stops, monitors and sets permissions for Skywire Apps. Internally, it handles and coordinates packets incoming and outgoing between the Skywire App and the external Skywire network.

An App Node can also forward packets to external Skywire Nodes (based on the set Routing Rules).

**Skywire Control Nodes:**

A Skywire Control node is similar to an app node, with the difference that it has administrative permissions for other nodes in a cluster and is being sent logs from other nodes. It should not run user-based Skywire Apps, nor should it forward Packets.

**Skywire Route Setup Node:** 

The Route Setup Node is a Skywire Node which runs a service that allows it to set up routes for other Skywire nodes. It does this by relaying the routing rules to individual Skywire App Nodes.
