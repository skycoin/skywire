# Overview

Skywire is an decentralized network that attempts to replace the current internet. The *Skywire Network* is made up of physical *Skywire Nodes* which run the *Skywire-Visor*. There are currently two types of *Skywire Nodes*; *Skywire Visor* and *Setup Node*.

Each *Skywire Visor* is represented by a unique public key. A direct line of communication between two *Skywire Visors* is called a *Transport*. Each *Transport* is represented by a unique *Transport ID* which is of a *Transport Type*, and the two *Skywire Visors* that are connected via the *Transport* are named the *Transport Edges*.

A *Route* is unidirectional and delivers data units called *Packets*. It is made up of multiple hops where each hop is a *Transport*. Two *Routes* of opposite directions make a *Loop* when associated with the given *Ports* at each *Loop Edge*. *Loops* handle the communication between two *Skywire Apps* and are represented via the *Loop's* source and destination visor's public keys and the source and destination ports (similar to how TCP/UDP handles ports).

A *Packet* is prefixed with a *Route ID* which helps *Skywire Visors* identify how the *Packet* is to be handled (either to be forward to a remote node, or to be consumed internally). Every *Skywire Visor* has a *Routing Table* that has the *Routing Rules* for that particular *Skywire Visor*.

In summary,

- *Transports* are responsible for single-hop communication between two *Skywire Visors* and are bidirectional.
- *Routes* are responsible for multi-hop communication between two *Skywire Visors* and are unidirectional.
- *Loops* are responsible for communication between two *Skywire Apps* and are bidirectional.

There are many ways in which we can implement a *Transport*. Each unique method is called a *Transport Type*.

Initially, we need to implement a MVP in which we assume that there are no malicious nodes in the network and discovery of routes, transports and nodes are to be done in a centralized manner.
