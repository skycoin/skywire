# Proxy Ports Over Skywire

## `skywire-cli fwd`

To forward ports over skywire, register the port via the CLI

### CLI
CLI can be used if you do not want to make any changes to the code of the http server or if it is written in another language.

- Register
    `skywire-cli fwd -p <local-port>`
    Register a local port to be accessed by remote visors

- deregister
    `skywire-cli fwd -d <local-port>`
    Deregister a local port to be accessed by remote visors

- ls-ports
    `skywire-cli fwd -l`
    List all registered ports

### RPC

RPC can be used for integration with applications to register and deregister within the http server code so that the process is automatic. First create a RPC client conn to the local visor

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
id, err := rpcClient.Publish(port)
err = rpcClient.Depublish(id)
```
[Example](../example/http-server/README.md)


## Connect to the forwarded server

Connect to a server forwarded by a remote visor.

### CLI

- connect
    `skywire-cli rev <remote-pk> -p <local-port> -r <remote-port>`
    Connect to a server running on a remote visor machine.
    The http server is proxied to the specified local port.

- disconnect
    `skywire-cli rev -d <id>`
    Disconnect from the server running on a remote visor machine

- ls
    `skywire-cli rev -l`
    List all configured connections
