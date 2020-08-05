[![Build Status](https://travis-ci.com/skycoin/dmsg.svg?branch=master)](https://travis-ci.com/skycoin/dmsg)

# dmsg

`dmsg` is a distributed messaging system comprised of three types of services:
- `dmsg.Client` represents a user/client that wishes to use the dmsg network to establish `dmsg.Session`s and `dmsg.Stream`s.
- `dmsg.Server` represents a service that proxies `dmsg.Stream`s between `dmsg.Client`s.
- `dmsg.Discovery` acts like a *DNS* of `dmsg.Server`s and `dmsg.Client`s, identifying them via their public keys.

```
           [D]

     S(1)        S(2)
   //   \\      //   \\
  //     \\    //     \\
 C(A)    C(B) C(C)    C(D)
```

Legend:
- ` [D]` - `dmsg.Discovery`
- `S(X)` - `dmsg.Server`
- `C(X)` - `dmsg.Client`

`dmsg.Client`s and `dmsg.Server`s are identified via `secp256k1` public keys, and store records of themselves in the `dmsg.Discovery`. Records of `dmsg.Client`s also includes public keys of `dmsg.Server`s that are delegated to proxy data between it and other `dmsg.Client`s.

The connection between a `dmsg.Client` and `dmsg.Server` is called a `dmsg.Session`. A connection between two `dmsg.Client`s (via a `dmsg.Server`) is called a `dmsg.Stream`. A data unit of the dmsg network is called a `dmsg.Frame`.

## Dmsg tools and libraries

- [`dmsgget`](./docs/dmsgget.md) - Simplified `wget` over `dmsg`.

## Additional resources
- [`dmsg` examples.](./examples)
- [`dmsg.Discovery` documentation.](./cmd/dmsg-discovery/README.md)
- [Starting a local `dmsg` environment.](./integration/README.md)

