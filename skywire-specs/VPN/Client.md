# VPN Client

Client is mostly the same as a [server](./Server.md). What really differs server from the client is overall routing. Only outbound traffic should be going through the interface. Not sure if the inbound traffic matters. So, client reads all the outbound from the adapter and passes it to the remote VPN server through the open connection.

Since internal Skywire traffic actually provides the tunnel functionality, we pass all the outbound traffic through the interface except for packets targeting Skywire services. To exclude, we pass IPs of our services to the client on startup via ENVs. During its work we add or remove such direct IPs via network hooks between visor and app. 