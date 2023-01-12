# HTTP server over skynet via `skyfwd`

This server runs on the local port `9080` which is registered via `skyfwd` so that it can be accessed via other visors.

To run the server start the `skywire-visor` first and then run `go run ./example/http-server/server.go`

After this you will be able to connect to this server from another visor over skynet via the subcommand `skyfwd connect`