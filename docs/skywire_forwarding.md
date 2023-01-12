# Skywire forwarding

## Register http server for forwarding
In order to for other visors to connect to the http server we need to register it via either the CLI or directly via the RPC.

### CLI
CLI can be used if you do not want to make any changes to the code of the http server or if it is written in another language.
- Register
    `skywire-cli skyfwd register -l <local-port>`
    Register a local port to be accessed by remote visors
- deregister
    `skywire-cli skyfwd deregister -l <local-port>`
    Deregister a local port to be accessed by remote visors
- ls-ports
    `skywire-cli skyfwd ls-ports`
    List all registered ports

### RPC
RPC can be used to register and deregister within the http server code so that the process is automatic.
First create a RPC client conn to the local visor 
```
func client() (visor.API, error) {
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", "localhost:3435", rpcDialTimeout)
	if err != nil {
		return nil, err
	}
	logger := logging.MustGetLogger("api")
	return visor.NewRPCClient(logger, conn, visor.RPCPrefix, 0), nil
}
```
And then use the created RPC conn to register and deregister the server
```
err = rpcClient.RegisterHTTPPort(port)
err = rpcClient.DeregisterHTTPPort(port)
```
[Example](../example/http-server/README.md)


## Connect to the forwarded server
In order to connect to a server forwarded by a remote visor use the CLI.

### CLI
- connect
    `skywire-cli skyfwd connect <remote-pk> -l <local-port> -r <remote-port>`
    Connect to a server running on a remote visor machine. The http server will then be forwarded to the specified local port. 
- disconnect
    `skywire-cli skyfwd disconnect <id>`
    Disconnect from the server running on a remote visor machine
- ls
    `skywire-cli skyfwd ls`
    List all ongoing skyforwarding connections