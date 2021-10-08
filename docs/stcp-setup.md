## `skywire-tcp` setup

`skywire-tcp` allows establish *skywire transports* to other skywire visors over `tcp`. This transport is used mostly for testing. It requires substantial manual configuration but is more flexible than `stcpr` transport because it can be used between visors in the same network and does not require a connection to an external server. 

As visors are identified with PubKeys rather than IP addresses, 
we need to directly map their IP address and PubKeys. 
This is done in the configuration file for `skywire-visor`.

```json
{
  "skywire-tcp": {
    "pk_table": {
      "024a2dd77de324d543561a6d9e62791723be26ddf6b9587060a10b9ba498e096f1": "127.0.0.1:7031",
      "0327396b1241a650163d5bc72a7970f6dfbcca3f3d67ab3b15be9fa5c8da532c08": "127.0.0.1:7032"
    },
    "listening_address": "127.0.0.1:7033"
  }
}
```

In the above example, we have two other visors running on localhost (that we wish to connect to via `skywire-tcp`).
- The field `skywire-tcp.pk_table` holds the associations of `<public_key>` to `<ip_address>:<port>`.
- The field `skywire-tcp.listening_address` should only be specified if you want the visor in question to listen for incoming 
`skywire-tcp` connection.