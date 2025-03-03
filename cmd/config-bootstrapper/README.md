# Config Bootstrapper

## API endpoints

### GET `/health`
Gets the health info of the service. e.g.
```
{
    "build_info": {
        "version": "v1.0.1-267-ge1617c5b",
        "commit": "e1617c5b0121182cfd2b610dc518e4753e56440e",
        "date": "2022-10-25T11:01:52Z"
    },
    "started_at": "2022-10-25T11:10:45.152629597Z"
}
```

### GET `/`
Gets the service urls
```
{
    "dmsg_discovery": "http://dmsgd.skywire.dev",
    "transport_discovery": "http://tpd.skywire.dev",
    "address_resolver": "http://ar.skywire.dev",
    "route_finder": "http://rf.skywire.dev",
    "setup_nodes": [
        "026c2a3e92d6253c5abd71a42628db6fca9dd9aa037ab6f4e3a31108558dfd87cf"
    ],
    "uptime_tracker": "http://ut.skywire.dev",
    "service_discovery": "http://sd.skywire.dev",
    "stun_servers": [
        "192.46.224.108:3478",
        "139.177.185.210:3478",
        "139.162.17.54:3478",
        "139.162.17.107:3478",
        "139.162.17.156:3478",
        "45.118.134.168:3478",
        "139.177.185.180:3478",
        "139.162.17.48:3478"
    ]
}
```