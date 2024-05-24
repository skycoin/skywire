# Skywire Visor

A visor is a node that is part of the Skywire network and is represented by a key pair (using the `secp256k1` curve). It handles Transports to remote visors, sets up routes and loops (via Routing Rules and interaction with the *Setup Node*), and manages Apps.

Each App is it's own executable that communicates with an *App Node* using a pair of *POSIX* pipes. A piped connection is setup on *App* startup and inherited by a forked *App* process using file descriptor `3` and `4`. Setup process for a forked *App* is handled by the `app` package.

```
    [Skywire Visor]
    /      |     \
   /       |      \
[App 1] [App 2] [App 3]
```

## Communication reliability

Currently, loop ACK's are not implemented. They will need to be implemented at a later stage. ACK's are implemented for the dmsg implementation.

## App Programming Interface

*App* programming interface (located within the `skywire/pkg/app` module) should expose methods for *Apps* to connect to a piped connection, perform handshake and exchange data with remote nodes. 

*App* interface should expose following methods:

```golang
// Addr implements net.Addr for App connections.
type Addr struct {
	PubKey transport.PubKey
	Port   uint16
}

// LoopAddr stores addressing parameters of a loop package.
type LoopAddr struct {
	Port   uint16
	Remote Addr
}

// Packet represents message exchanged between App and Visor.
type Packet struct {
	Addr    *LoopAddr
	Payload []byte
}

// Config defines configuration parameters for an App
type Config struct {
	AppName         string
	AppVersion      string
	ProtocolVersion string
}

// Setup sets up an app using default pair of pipes and performs handshake.
func Setup(config *Config) (*App, error) {}

// Accept awaits for incoming loop confirmation request from a Visor and
// returns net.Conn for a received loop.
func (app *App) Accept() (net.Conn, error) {}

// Dial sends create loop request to a Visor and returns net.Conn for created loop.
func (app *App) Dial(raddr *Addr) (net.Conn, error) {}

// Addr returns empty Addr, implements net.Listener.
func (app *App) Addr() net.Addr {}

// Close implements io.Closer for an App.
func (app *App) Close() error {}
```

## App to Visor Communication protocol

Communication between *Visor* and an *App* happens over the piped connection using binary multiplexed protocol. 

The following is the expected format of a App Packet:

```
| Packet Len | Type   | Message ID | JSON Body |
| 2 bytes    | 1 byte | 1 byte     | ~         |
```

- ***Packet Len*** specifies the total packet length in bytes (exclusive of the *Packet Len* field).
- ***Type*** specifies the *App Packet Type*.
- ***Message ID*** specifies multiplexing ID of a message, response for this message should contain the same ID.
- ***JSON Body*** is the packet body (in JSON format) that is unique depending on the packet type.

**App Packet Types Summary:**

| Type | Name |
| ---- | ---- |
| 0x00 | `Init` |
| 0x01 | `CreateLoop` |
| 0x02 | `ConfirmLoop` |
| 0x03 | `Send` |
| 0x04 | `Close` |
| 0xfe | `ResponseFailure` |
| 0xff | `ResponseSuccess` |

### `0x00 Init`

Sent by an *App* to a *Visor*. This packet is used to handshake connection between an *App* and a *Visor*. *Visor* will typically check if app is allowed by the config file and which port should be statically allocated it.

**JSON Body:**

```json
{
    "app-name": "foo",
    "app-version": "0.0.1",
    "protocol-version": "0.0.1"
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` without body.

### `0x01 CreateLoop`

Sent by an *App* to a *Visor*. This packet is used to open new *Loop* to a remote *Visor*.

**JSON Body:**

```json
{
    "pk": "<remote-pk>",
    "port": <remote-port>
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with
    ```json
    {
        "pk": "<local-pk>",
        "port": <local-port>
    }
    ```

### `0x02 ConfirmLoop`

Sent by a *Visor* to an *App* to notify about request to open new *Loop* from a remote *Visor*

**JSON Body:**

```json
[
    {
        "pk": "<local-pk>",
        "port": <local-port>
    },
    {
        "pk": "<remote-pk>",
        "port": <remote-port>
    }
]
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty body.

### `0x03 Send`

Sent by a *Visor* and an *App*. This message is used to exchange messages through a previously established *Loop*.

**JSON Body:**

```json
{
    "addr": {
        "port": <local-port>,
        "remote": {
            "pk": "<remote-pk>",
            "port": <remote-port>
        }
    },
    "payload": "<binary-data>"
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty body.

### `0x04 Close`

Sent by a *Visor* and an *App*. *App* uses this message to notify about closed *Loop*. *Visor* sends this message after remote node is requested to close established *Loop*.

**JSON Body:**

```json
{
    "port": <local-port>,
    "remote": {
        "pk": "<remote-pk>",
        "port": <remote-port>
    }
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty body.

## App Node Configuration

The following is the JSON representation of a Skywire configuration.

```json
{
  "version": "1.0",
  "node": {
    "static_public_key": "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7",
    "static_secret_key": "42bca4df2f3189b28872d40e6c61aacd5e85b8e91f8fea65780af27c142419e5"
  },
  "messaging": {
    "discovery_addresses": ["http://localhost:9090"],
    "server_count": 1
  },
  "apps": [
    {
      "app": "helloworld",
      "version": "1.0",
      "auto_start": true,
      "port": 10,
      "args": []
    }
  ],
  "transport_discovery": "http://localhost:9091",
  "setup_nodes": ["02603d53d49b6575a0b8cee05b70dd23c86e42cd6cba99af769d61a6196ea2bcb1"],
  "trusted_nodes": ["0348c941c5015a05c455ff238af2e57fb8f914c399aab604e9abb5b32b91a4c1fe"],
  "messaging_path": "./messaging",
  "apps_path": "./apps",
  "local_path": "./local",
  "log_level": "info",
  "interfaces": {
    "rpc": ":3436"
  }
}
```

- `"version"` represents the version of the Skywire Visor (and also the configuration format version).

- `"node"` includes the public/private keys that identify the visor.

- `"messaging"` configures the dmsg client instance included within the Skywire Visor.
    - When `"public"` is set, the dmsg Client Instance will advertise itself to discovery.
    - `"discovery_addresses"` specifies the dmsg Discovery Services that the Skywire Visor is to try.
    - `"server_count"` specifies the number of servers to ensure connection with on first startup.

- `"apps"` lists all available Skywire Apps. These configurations include; the App's name, whether the specified App should auto-start, and the ports that is reserved for the App. If these are not defined for an App, the App will not auto-start, nor have ports reserved for the App.
    - If `"version"` is not specified, the highest stable version will be selected.

- `"node_path"` stores logs, routing tables, and any data that the visor may use.

- `"messaging_path"` holds the path which the dmsg Client Instance can use to store cache or additional configurations.

- `"apps_path"` holds all the app executables. App executable files should be named with no spaces or weird characters (TODO: define properly). They should also be appended with the semantic version of the App after a dot; `{app_name}.v{semantic_version}`.

- `"local_path"` contains the working directories of the apps. An app named `chat` of version `v1.0` should have a working directory within `{root_dir}/{local_path}/chat/v1.0/`. The contents of the App's working directory is specified by the App.

## App Node RPC Interface

The Visor should attempt to connect to the assigned *Hypervisor* on startup. The connection is to be encrypted via Noise (KK handshake pattern) so that the nodes can identify one another. 

For the *Visor* to connect to the *Hypervisor*, it needs the public key and tcp address of the *Hypervisor* in it's configuration.

After connection has been established, the *Visor* becomes the RPC Server and the *Hypervisor* becomes the RPC client that can execute commands on the *Visor* (the RPC Server).

Additionally, the Visor should listen on a port so that a local command-line executable (`skywire-cli`) can interact with it. This local port should hence, only accept connections from localhost.

### Commands

The following sub-commands should be supported. Note that command-line actions are listed below, but they should be served via RESTFUL interfaces.

**General:**

- **`summary`** obtains a summary of the current state of the Visor.

**App Management:**

- **`list-apps`** lists applications and applications stats (running/not running) (auto-start/non-auto-start) (local/remote ports). There should be flags for filtering (to be defined later).

- **`start-app <app>`** starts a Skywire app.

- **`stop-app <app>`** stops a Skywire app if running.
  
- **`add-autostart-app <app> [--start-now]`** adds a Skywire app to auto-start. After calling this command, the actual app does not actually start unless `--start-now` is set.

- **`rm-autostart-app <app> [--stop-now]`** removes an app from auto-starting. After calling this command, the actual app does not stop running unless `--stop-now` is set.

**Messaging System Management:**

- **`messaging list-discoveries`** lists saved discoveries and their statuses

- **`messaging add-discovery <discovery-address>`** connects to and saves a discovery.

- **`messaging rm-discovery <discovery-address>`** disconnects from and removes a discovery.

- **`messaging list-servers`** lists connected messaging servers and their statuses (connected/disconnected) (auto-connect/non-auto-connect).

- **`messaging connect-server (<public-key>|--auto)`** connects to a messaging server for this session (does not save server for auto-connect). If `--auto` is set, the transport discovery is queried for a random available messaging server.

- **`messaging disconnect-server <public-key>`** disconnects from a messaging server for this session (does not affect auto-connect settings).

- **`messaging add-autoconnect-server <public-key> [--connect-now]`** adds a messaging server to auto-connect. This command does not connect to the specified messaging server unless `--connect-now` is set.

- **`messaging rm-autoconnect-server <public-key> [--disconnect-now]`** removes a messaging server from auto-connecting. This command does not disconnect from the specified messaging server unless `--disconnect-now` is set.

**Transport Management:**

- **`transport-types`** lists all transport types used by the visor (represented as strings).
- **`list-transports [--filter-types=<type1>,<type2>,...] [--filter-pks=<pk1>,<pk2>,...] [--no-logs]`** lists all transports associated with the visor. Filters can be used to only show visors associated with specified transport types or public keys. By default, transports are displayed with their logging information.
- **`add-transport <transport-type> <remote-pk> [--timeout=<seconds>]`** adds a transport of a given type and public key. If `--timeout` is not set, the default timeout is used.
- **`rm-transport [--tid=<transport-id>|--remote-pk=<remote-pk>]`** removes a transport; either for a given transport ID, or all transports connected to a remote visor (identified via the remote visor's public key).

**Routes Management:**

- **`list-rules [--rid-range=<start-rid>:<end-rid>]`** lists all routing rules. A route ID range filter can be specified.
- **`rm-rules [--list=<rid-1>,<rid-2>,...|--range=<start-rid>:<end-rid>]`** removes routing rules; either via a list of route ID keys, or via a range of route ID keys (note that routing rules are identified via their `<rid>` key). This action may consequently destroy loops, and may cause the *Setup Node* to request destruction of more routing rules.

**Loops Management:**

- **`list-loops [--local-port=<port>] [--remote-addr=<remote-pk>[:<remote-port>]]`** lists all loops. A local port filter can be specified, where the returned loops will only be of the specified local port (there is an equivalent remote address filter).
- **`add-loop --local-port=<port> --remote-addr=<remote-pk>:<remote-port> [--setup-node=<pk>]`** attempts to create a loop with the assigned setup node. The setup node is automatically chosen if not specified.

## Ports Management

Within the `AppsConfig` file, ports are reserved for certain Apps. The following rules are to be opposed:
- Ports are either "reserved" or "unreserved".
    - No two Apps are allowed to "reserve" the same port.
    - Ports are reserved via the `AppsConfig` file.
- Reserved ports are either "active" or "inactive".
    - A port is "active" when the port is "reserved" for an App, and that App is running.
    - A port is "inactive" either when the port is "unclaimed", or when the port is "claimed" but the App is not running.

## App Example

Simple `ping-pong` client and server apps can be implemented in such way:

Server:

```golang
package server

import (
	"log"

	"github.com/watercompany/skywire/pkg/app"
)

func main() {
    // Open connection with visor
	helloworldApp, err := app.Setup(&app.Config{AppName: "helloworld-server", AppVersion: "1.0", ProtocolVersion: "0.0.1"})
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer helloworldApp.Close()

	log.Println("listening for incoming connections")
    // Start listening loop
	for {
        // Wait for new Loop
		conn, err := helloworldApp.Accept()
		if err != nil {
			log.Fatal("Failed to accept conn: ", err)
		}

		log.Println("got new connection from:", conn.RemoteAddr())
        // Handle incoming connection
		go func() {
			buf := make([]byte, 4)
			if _, err := conn.Read(buf); err != nil {
				log.Println("Failed to read remote data: ", err)
			}

			log.Printf("Message from %s: %s", conn.RemoteAddr().String(), string(buf))
			if _, err := conn.Write([]byte("pong")); err != nil {
				log.Println("Failed to write to a remote visor: ", err)
			}
		}()
	}
}
```

Client:

```golang
package server

import (
	"log"
	"os"

	"github.com/watercompany/skywire/pkg/app"
	"github.com/watercompany/skywire/pkg/cipher"
)

func main() {
    // Open connection with visor
	helloworldApp, err := app.Setup(&app.Config{AppName: "helloworld-client", AppVersion: "1.0", ProtocolVersion: "0.0.1"})
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer helloworldApp.Close()

    // Read remote PK from stdin
	remotePK := cipher.PubKey{}
	if err := remotePK.UnmarshalText([]byte(os.Args[1])); err != nil {
		log.Fatal("Failed to construct PubKey: ", err, os.Args[1])
	}

    // Dial to remote visor
	conn, err := helloworldApp.Dial(&app.Addr{PubKey: remotePK, Port: 10})
	if err != nil {
		log.Fatal("Failed to open remote conn: ", err)
	}

    // Send payload
	if _, err := conn.Write([]byte("ping")); err != nil {
		log.Fatal("Failed to write to a remote visor: ", err)
	}

    // Receive payload
	buf := make([]byte, 4)
	if _, err = conn.Read(buf); err != nil {
		log.Fatal("Failed to read remote data: ", err)
	}

	log.Printf("Message from %s: %s", conn.RemoteAddr().String(), string(buf))
}
```
