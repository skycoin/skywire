# Route finder

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

### POST `/routes`<br>  
Gets the available routes for a specific source and destination public key and the available reverse routes from destination to source.
Optionally with custom min and max hop parameters.
Body:
```
    {
    "edges": ["<src-pk>", "<dst-pk>"],
        "opts": {
            "min_hops": 0,
            "max_hops": 0
        }
    }
```