# SkyHTTP app

Simple http-proxy apps called SkyHTTP. It's work behind skysocks-client, also could use for other socks5 address out of skywire platform.

## Configuration example

```json
{
  "apps": [
    {
      "app": "skyhttp",
      "binary": "skyhttp",
      "auto_start": true,
      "args": [
        "-addr",
        ":11080",
        "-socks",
        "127.0.0.1:1080"
      ],
      "port": 4
    }
  ]
}
```
