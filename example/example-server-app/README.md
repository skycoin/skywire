# Skywire Example server app (Skywire visor app)

`example-server-app` app implements server for the `example-client-app` over skywire net.

## Configuration

Full config of the server should look like this:
```json5
{
  "app": "example-server-app",
  "auto_start": true,
  "port": 45,
}
```

## Running app

Compile app binaries, update config with `example-server-app` and start a visor:

```sh
$ make build-example
$ ./build/skywire-cli config gen -irm
$ ./build/skywire-visor skywire-config.json
```