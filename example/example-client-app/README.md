# Skywire Example client app (External app)

`example-client-app` app implements client for the `example-server-app` over skywire net and is a external app.

It opens persistent `skywire` connection to the configured `example-server-app` and send a `hello` message and gets a response of `hi`.

## Configuration

The app takes the following flags
- `-addr` (required) - pubKey and port of the server to connect to (e.g. <pk>:<skywire-port>)
- `-procAddr` - proc server address to connect to (default: localhost:5505)
- `-procKey` (required) - proc server address to connect to

## Running app

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
