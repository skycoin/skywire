# Skywire Chat app

Chat implements basic text messaging between skywire visors.

Messaging UI is exposed via web interface.

Chat only supports one WEB client user at a time.

## Local setup

Create 2 visor config files:

`skywire1.json`

```json
{
  "apps": [
    {
      "app": "skychat",
      "version": "1.0",
      "auto_start": true,
      "port": 1
    }
  ]
}
```

`skywire2.json`

```json
{
  "apps": [
    {
      "app": "skychat",
      "version": "1.0",
      "auto_start": true,
      "port": 1,
      "args": ["-addr", ":8002"]
    }
  ]
}
```

Compile binaries and start 2 visors:

```bash
$ go build -o ./build/apps/skychat.v1.0 ./cmd/apps/skychat
$ cd ./build
$ ./skywire-visor skywire1.json
$ ./skywire-visor skywire2.json
```

Chat interface will be available on ports `8001` and `8002`.
