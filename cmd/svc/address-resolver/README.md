# Address Resolver

## API endpoints

### POST `/bind/stcpr`
Binds the visor with `STCPR` and saves the connection data.

### DELETE `bind/stcpr`
Deletes the bind of a visor(only a visor can delete it's own bind).

###  GET `/resolve/{type}/{pk}`
Gets the bind info of a PK and it's binded transport type either `STCPR` or `SUDPH` if available.

###  GET `/health`
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

### GET `/transports`
Gets the list of PK's binded as `STCPR` and `SUDPH`. e.g.
```
{
    "sudph": [
        "0218905f5d9079bab0b62985a05bd162623b193e948e17e7b719133f2c60b92093",
        "0214456f6727b0dffacc3e4a9b331ff9bf7b7d97a9810c213772199f0f7ee59247"
    ],
    "stcpr": [
        "0218905f5d9079bab0b62985a05bd162623b193e948e17e7b719133f2c60b92093"
    ]
}
```

### DELETE `/deregister/{network}`
Deletes the binding of the PK's mentioned in the request. Can only be used by services that are whitelisted to use it.

### GET `/security/nonces/{pk}`
Gets the nonce for a particular PK. Used by the nonce store.
