# Hypervisor

The *Hypervisor* is responsible for managing *Visors* and is identified via it's public key and TCP address.

The *Visor* is responsible for including a trusted *Hypervisor* in it's configuration file, and attempt to connect to it on startup. The connection is authenticated and encrypted via the Noise protocol where the `XK` handshake pattern is used with the *Visor* being the initiator.

After the connection is successfully established between an *Visor* and a *Hypervisor*, the *Visor* acts as the RPC server while the *Hypervisor* is the RPC client. In this way, the *Hypervisor* can execute commands on the *Visor*.

The *Hypervisor* serves a REST API which the end user can interact with. To access the API, the user is required to log in via a username and password.

The *Hypervisor* should be implemented in `/pkg/hypervisor`.

## Hypervisor REST API (and User Interface)

- Login/logout.
- Change password.
- List connected *Visor*, each Visor should contain the following summary; Public key, local address, number of transports established, number of apps running, Uptime.
- The user can click into a listed *Visor* and perform node-specific actions; specifically, the [RPC commands as specified above.](#visor-rpc-interface)
