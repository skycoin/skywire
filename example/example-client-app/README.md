# Skywire Example client app

`example-client-app` app implements client for the `example-server-app` over skywire net.

It opens persistent `skywire` connection to the configured `example-server-app` and send a `hello` message and gets a response of `hi`.

## Configuration

This app can be ran from skywire as well as an external app

### Skywire App

Additional arguments may be passed to the application via `args` array. These are:
- `-addr` (required) - pubKey and port of the server to connect to (e.g. <pk>:<skywire-port>)
- `-procAddr` - proc server address to connect to (default: localhost:5505) (not required if run from skywire)
- `-procKey` - proc server address to connect to (not required if run from skywire)

Full config of the client should look like this:
```json5
{
  "app": "example-client-app",
  "auto_start": false,
  "port": 46,
  "args": [
    "-addr",
    "03e9019b3caa021dbee1c23e6295c6034ab4623aec50802fcfdd19764568e2958d:45",
  ]
}
```

### External App
The app takes the following flags
- `-addr` (required) - pubKey and port of the server to connect to (e.g. <pk>:<skywire-port>)
- `-procAddr` - proc server address to connect to (default: localhost:5505)
- `-procKey` (required) - proc server address to connect to

## Running app

### Skywire App
Compile app binaries, update config with `example-client-app` and start a visor:

```sh
$ make build-example
$ ./skywire-cli config gen -irm
$ ./skywire-visor skywire-config.json
```

Start the app from either cli or hypervisor UI

### External App
Compile app binary and start a visor:

```sh
$ make build-example
$ ./skywire-visor skywire-config.json
```

Register app and generate proc key
```sh
$ ./skywire-cli visor app register -a example-client-app
01cd10e65d88494481c50a1bb0659af2
```

Run the app with the `example-server-app` addr and the generated proc key 
```sh
$ ./apps/example-client-app -addr <example-server-app-pk>:<example-server-app-skywire-port> -procAddr 01cd10e65d88494481c50a1bb0659af2
```

Deregister app after stopping the `example-client-app`
```sh
$ ./skywire-cli visor app deregister -k 01cd10e65d88494481c50a1bb0659af2
```
