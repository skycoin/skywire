# Uptime Tracker

## Purpose

Uptime tracker is used to track uptime of all the visors in the network. 
We define uptime as a total amount of seconds which visor had been online within 
a time interval.

## Algorithm
Visors perform a request to the uptime tracker each second. So, on each visor request 
we simply increment number of seconds it's been online. Uptimes are distributed by month 
and year. This means that for each visor we have a key in Redis hash set which consists 
of year and month. And we increment uptime value associated with that key.

## API

### GET `/v4/update`
Increments visors uptime

Required headers:
- `SW-Public` - visor's public key;
- `SW-Sig` - request body signature;
- `SW-Nonce` - security nonce.

### GET `/visors`
Gets lat and longs of all the visors.

### GET `/uptimes[?visors=pk1,pk2&month=1&year=2020]`
Gets uptimes for given visors for the specified month and year which is rate limited.

Query parameters:
- `visors` - list of visors' pub keys to get uptimes for. Pub keys are comma separated list. May be omitted, in this case uptimes for all the known visors will be returned;
- `month` - month to request uptimes for. If either month or year are omitted, uptimes will be fetched for the current year and month;
- `year` - year to request uptimes for. If either month or year are omitted, uptimes will be fetched for the current year and month.

### GET `/uptime/{pk}[?month=1&year=2020]`
Gets uptime for given visor for the specified month and year which is not rate limited.

Query parameters:
- `month` - month to request uptimes for. If either month or year are omitted, uptimes will be fetched for the current year and month;
- `year` - year to request uptimes for. If either month or year are omitted, uptimes will be fetched for the current year and month.

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

### GET `/dashboard`
Gets a uptime tracker bar graph.

### GET `/security/nonces/{pk}`
Gets the nonce for a particular PK. Used by the nonce store.

## Private API
There is only one endpoint on port :9086. :9086 is the default port for the httpserver and can be changed with the flag -p.

### GET `/visor-ips`
Gets all the IP's of the registered visors.