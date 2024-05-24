# Hole-punch transport

Hole-punch transport uses a point-to-point connection between two parties that are behind NATs or firewalls.

### UDP hole-punch transport description

To be able to establish a connection between two visors (`A` and `B`) using UDP hole-punching,
an intermediate server (`S`) between two nodes is necessary.

The connection to `S` and then connections between `A` and `B` should be created from the same address.

On start, `A` and `B` listen on ports `AP` and `BP` and send UDP requests to `S` to port `SP` 
to register their addresses in `S`.
 
This connection should be kept alive within the lifecycle of hole-punching transport to keep NAT mapping. 
When `A` tries to hole-punch `B`, 
it needs to send packets from the same port `AP` (to reuse existing NAT mapping) to port `BP`. 
What about `B`, it listens on the same port `BP` for a connection from `AP`. 
So, we need to have 2 connections on `A` (`AP` -> `SP`, `AP` -> `BP`) 
and 2 connections on `B` (`BP` -> `SP`, `BP` -> `AP`).
 
The local port is same for both connections, therefore UDP listener (`*net.UDPConn`) is shared between both connections. 
So in fact, connections to server and other visor reuse the same UDP connection, but remote ports and addresses differ. 
When a new [KCP](https://github.com/xtaci/kcp-go) connection is created from a `*net.UDPConn`, 
it requires setting remote address and drops packets from different addresses. 
Therefore, if `*net.UDPConn` is wrapped by KCP, there's a question how to receive packets from different remote addresses, 
which is required for UDP hole punching. 

To solve that, a middleware between `*net.UDPConn` and KCP is used. 
It extracts packets sent from different remote addresses. 
Packets sent from an expected by KCP address would be passed further to KCP.

#### Connection from visor to address resolving server

As written above, visors use the connection for sending a public key they want to connect to 
and for receiving an IP they are asked to connect to. Therefore, the connection is bi-directional.
Visor sends an HTTP request which is upgraded to websocket connection.
When visor sends its public key, it should be authenticated. For that, `httpauth` is used for the HTTP request.

#### Address resolving server

Address resolving server needs to store visor address and its corresponding address. 
The server has two types of storage:

- non-persistent in-memory storage
- persistent `Redis` storage

Address-resolver has two types of API that work with UDP hole-punching 

- HTTP
- UDP

The `--addr` argument specifies an address address-resolver listens both HTTP and UDP on.

The UDP server is used to bind PKs that visors send on start with their addresses. 
PKs are authorized by a handshake procedure same with other handshakes (using noise).

- `GET` `/resolve/sudph/{pk}`

It is used by dialing visor to resolve public key to address of dialed visor. It requires PK authorization.

- `/security/nonces/`

It is used by `httpauth` middleware for public key authorization for resolving addresses.

#### Communication format

Visor starts UDP communication with address-resolver and performs a noise handshake with it. 
Then it sends a JSON with a port it listens UDP connections on 
and with local IPs to be able to connect with local IPs locally.

If address resolver sees that external IPs of visors trying to connect to each other are same, 
it allows them to try to connect to a local IP first.   

## Useful links

- https://bford.info/pub/net/p2pnat/index.html