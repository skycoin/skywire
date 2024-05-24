# VPN

Basically VPN consists of 2 applications - [client](./Client.md) and [server](./Server.md). Both applications are made in form of a Skywire app (like skychat currently). They are run under control of a Skywire visor.

## TUN/TAP adapter

We use TUN/TAP to create a virtual interface for both client and server. Client and server are connected through a tunnel (SkyTunnel?). This is not a tunnel in common understanding like direct connection, apps are connected over Skywire network through visors like all other apps.

All the system traffic is routed through created virtual network interface to the application which had created it. For the initial implementation I suggest that we go with TUN adapter which allows us to inspect and handle IP packets, so we may concentrate on the overall VPN functionality.

### TUN/TAP creation

To create the adapter we may use the following library. Linux is fully supported, MacOS is only for TUN adapter (initial implementation).

https://github.com/songgao/water

Also, these links may help:
- http://tuntaposx.sourceforge.net/

For Windows this can only be achieved with the installation of `WinTUN` driver and if running client with the `SYSTEM` account, apparently having administrator privileges is not enough. Library wrapper to create the actual interface we use:

- https://golang.zx2c4.com/wireguard/tun

The easiest way to get `WinTUN` installed is to install the `Wireguard` itself. Also acquiring `SYSTEM` rights is a plain pain, so, unfortunately I can not guide through these processes. This is not user-ready in any case. But the only thing I'm sure of is that it works, so this approach can be used as a proof of concept.

The route setup trick is done by OpenVPN on all platforms, it's the same, and we do the same.

- Linux/MacOS

This one sets up the interface itself with `192.168.255.6` as interface address and `192.168.255.5` as a destination address for P2P connection:
```
/sbin/ifconfig utun2 192.168.255.6 192.168.255.5 mtu 1500 netmask 255.255.255.255 up
```

Example of setting routing up:
```
/sbin/route add -net 134.209.17.43 192.168.1.1
/sbin/route add -net 0.0.0.0 192.168.255.5 128.0.0.1
/sbin/route add -net 128.0.0.0 192.168.255.5 128.0.0.1
```

And the corresponding cleanup:
```
/sbin/route delete -net 134.209.17.43 192.168.1.1
/sbin/route delete -net 0.0.0.0 192.168.255.5
/sbin/route delete -net 128.0.0.0 192.168.255.5
```

Setup:
- `134.209.17.43` is IP of one of Skywire services (we add all the IPs we need to connect directly. Like setup node, Dmsg servers, discoveries, etc).
- `192.168.1.1` is our router IP which serves as a default gateway when VPN is down. This IP can be fetched on Unix machines by: `netstat -rn | grep default | grep {DEFAULT_INTERFACE_NAME} | awk '{print $2}` 
- `192.168.255.6` is a TUN interface's IP (question is how this one is being chosen. on this current run my laptop IP in the local network is 192.168.1.5. Probably OpenVPN just gets this addr from `ifconfig` and changes its 3rd octet to 255)
- `192.168.255.5` is a gateway for TUN interface (destination IP for P2P connection) (can't say for now how this one is determined, probably TUN interface's IP and plus 1 to the 4th octet)

Basically in this example we do the following. Route all the traffic to the Skywire services through our router, like a usual connection does by default. Then we route all traffic from subnets `0.0.0.0` and `128.0.0.0` to the VPN gateway `192.168.255.5`. So, we cover all the IPv4 range of addresses with this. Netmask `128.0.0.0` should be applied to both half ranges. So first half range covers `0.0.0.0` through `127.255.255.255` and the second one covers `128.0.0.0` through `255.255.255.255`. We could use a single route `0.0.0.0/0` but this would override the default route in the system and will make cleanup more complicated. This way we will be routing all the IPv4 traffic from the system to gateway `192.168.255.5`, it will go to `192.168.255.6` by the P2P connection and we'll be reading this traffic out of TUN interface in the app.

This command set should work for all Unix systems, the only difference is the binaries location.

Localhost traffic shouldn't be affected by all this routing. So app/visor communication will be going on as usual. The part that bothers us is visor-to-other-services communication. All of the used services are put into visor's config. So, when visor starts apps, it's fully initialized itself. So, it may take all of external services and pass their domains/IPs to the VPN app. This way VPN app can resolve IPs and add needed routes. The problem for now is Dmsg servers and other visors that are being add to the local STCP table. These entities are being added at runtime, so we need to pass these to the VPN app and to update the routing table. Based on this link https://unix.stackexchange.com/questions/188584/which-order-is-the-route-table-analyzed-in , routing table is being consulted from the most specific rules to the least specific. We're adding highly specific routes, so it should work like a charm. App should have a mechanism to get new values from the visor on the fly.

- Windows

We provide just the examples of commands we use, without specific IPs, cause it is already demonstrated above.

Setting up interface and its MTU requires 2 separate commands:

```
netsh interface ip set address name="${INTERFACE_NAME}" source=static addr=${IP} mask=${MASK} gateway=${GATEWAY}
netsh interface ipv4 set subinterface "${INTERFACE_NAME}" mtu=${MTU}
```

After we use these commands there's a lag before we can set up routes, cause interface doesn't get ready instantly (Windows, what can I say). Just be aware, that it may take several seconds (we wait for 10 in our code just to be sure).

Setting and removing routes:
```
route add ${IP} mask ${MASK} ${GATEWAY}
route delete ${IP} mask ${MASK} ${GATEWAY} 
```

#### Cleanup

Regardless of other cleanup routines that need to be run on app shutdown, I guess all the possible interruption signals should be caught so we could at least remove the routes and let the system network stack work as usual not to ruin UX.

### MTU

MTU setup is not yet clear. I see that my OpenVPN instance uses 1500 which is an Ethernet MTU. Is it fixed for all hardware configurations possible? We'll have it fixed for now. These links may be useful:
- https://community.spiceworks.com/topic/217130-mtu-issues-in-vpn-connections
- https://www.sonassi.com/help/troubleshooting/setting-correct-mtu-for-openvpn

## Configuration

Both client and server can be configured like any other VPN app. 

Server flags:
- `--pk` - server pub key;
- `--sk` - server secret key;
- `--passcode` - password for the client to authenticate;
- `--secure` - by default client can access machines in the server's local network (SSH in, for example). Some people may use this as a feature, while others consider this a security breach. So, setting this flag forbids access to the local network.

Client flags:
- `--srv` - server's pub key;
- `--pk` - client's pub key;
- `--sk` - client's secret key;
- `--passcode` - password to authenticate;
- `--killswitch` - If VPN tunnel goes down and client tries to reconnect. By default during this process direct Internet access gets restored. If we set this flag, there won't be any direct Internet access, user will wait till VPN tunnel is up again.  

## Encryption

We rely on underlying Skywire transports for encryption.

## Authentication

Authentication is implied by the Skywire protocol itself. No further actions needed.