# VPN Server

Server is responsible for listening to incoming VPN client connections over Skywire network, creating TUN/TAP adapter, setting routing up, reading packets from adapter and passing them to the remote VPN server.

## Implementation Note

Due to the firewall used on MacOS server cannot be implemented for the system. Windows needs to be investigated.

## Routing

We allocate TUN interface for each VPN client. This way we may easily distribute traffic between clients. For the system to work both client and server TUN interfaces must be in the same subnetwork. And they must have different IPs. Gateway probably may be the same, but just to be sure, we're giving different ones. For the generation process details please consult [handshake](./Handshake.md) section.

Let's say we have server-side TUN `tun0` with IP `192.168.255.2` and gateway `192.168.255.1`.

- Linux
```
/sbin/ifconfig tun0 192.168.255.2 192.168.255.1 mtu 1500 netmask 255.255.255.248 up
```

Then we set up routing. First, we need to allow kernel pass packets from one interface to another. This is done like this:
- Linux
```
sudo sysctl -w net.ipv4.ip_forward=1 // for IPv4
sudo sysctl -w net.ipv6.conf.all.forwarding=1 // for IPv6 
```

Then we need to let the system work as NAT, so that packets flow as expected with their source IPs changed to the IP of the default interface.
```
sudo iptables -t nat -A POSTROUTING -o wlan0 -j MASQUERADE
```
Here `wlan0` is a default network interface in the system. May be fetched from the output of `netstat -rn` on Unix-like systems.

For cleanup we may fetch the old value of forwarding like:
```
sudo sysctl net.ipv4.ip_forward
sudo sysctl net.ipv6.conf.all.forwarding
```
and then we may assign old values on cleanup. Routing rule may be removed like this:
```
sudo iptables -t nat -D POSTROUTING -o wlan0 -j MASQUERADE
```
