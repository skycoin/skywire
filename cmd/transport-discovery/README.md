# skywire-services
Certain services that are required for Skywire to function.

## Transport Discovery
The Transport Discovery is a service that exposes a RESTFUL interface and interacts with a database on the back-end.

### Running:

Setup the database:
```
docker run -p6379:6379 -d redis
```

Run the tests:
```
go test ./pkg/transport-discovery/... -race
```

Start the server:
```
go run cmd/transport-discovery/transport-discovery.go serve --redis 'redis://localhost:6379'
```

Send a request:
```bash
curl http://localhost:9091/security/nonces/02aaeeedea55c1f216f863c0e750346fe2d0ac40b937a72d81b8460a7b136d8662 | jq
```

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

### GET `/transports/id:{id}`
    Gets info of a transport as per the given ID.

### GET `/transports/edge:{edge}`
    Gets info of a transport as per the given edge.

### POST `/transports/`
    Adds transport to TPD.

### DELETE `/transports/id:{id}`
    Deletes the transport from the TPD.

### GET `/all-transports`
    Gets info of all transports.

### POST `/statuses`
    Depreciated.
    
### GET `/security/nonces/{pk}`
    Gets the nonce for a particular PK. Used by the nonce store.
