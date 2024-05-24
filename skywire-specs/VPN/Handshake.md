# Client/Server Handshake

Before client and server start exchange the actual traffic the handshake process must take place. Client and server exchange their specific hello messages to agree on exchange details. Both messages are just JSON objects being sent over the app connection. Client sends its message first.

As stated in the [general server description](./Server.md), we need to choose 4 different IP addresses in the same subnetwork to give these to client- and server-side TUN interfaces. These IPs must be in the same network. Interfaces of different clients must not share the same subnet. To make this process deterministic, server will be responsible for choosing these addresses for each connecting client. Obviously for the system to work these generated IPs must not clash neither with any of the IP of the local client network interfaces nor with its default network gateway. 

### Client Hello Message

```
type ClientHello struct {
	UnavailablePrivateIPs []net.IP `json:"unavailable_private_ips"`
    Passcode              string   `json:"passcode"`
}
```

Here we have only one field - IPs that server must exclude from the generation. Usually client includes in this field IPs of all its local network interfaces plus its default network gateway. Client may also want to include some of the IPs that it's going to connect to directly without VPN.

### Server Hello Message

```
type ServerHello struct {
	Status     HandshakeStatus `json:"status"`
	TUNIP      net.IP          `json:"tun_ip"`
	TUNGateway net.IP          `json:"tun_gateway"`
}
```

Status represents the handshake process result. May be one of the following:

- 0 - OK
- 1 - Client message was malformed
- 2 - No free IPs left to give
- 3 - Internal server error
- 4 - Forbidden (invalid passcode)

`TUNIP` and `TUNGateway` fields are used by the client to set up its local TUN interface

### Server-Side IP Generation
 
We need to generate 4 different IPs lying in the same network. For this we'll use the `/29` (`255.255.255.248`) mask. Server iterates over all private IP ranges:
- `192.168.0.0` - `192.168.255.255`
- `172.16.0.0` - `172.31.255.255`
- `10.0.0.0` - `10.255.255.255`

Generation step is 8, so the IPs will be generate like:
`192.168.0.0, 192.168.0.8, 192.168.0.16, ...`

This way the generated IP address will be the address of the subnet. Let's say we have the generated IP - `192.168.0.0`. Then server will assign the following addresses:
- `192.168.0.1` - Server-side TUN gateway
- `192.168.0.2` - Server-side TUN IP
- `192.168.0.3` - Client-side TUN gateway
- `192.168.0.4` - Client-side TUN IP