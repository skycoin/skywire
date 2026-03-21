CXDS
====

[![GoDoc](https://godoc.org/github.com/skycoin/cxo/data/cxds?status.svg)](https://godoc.org/github.com/skycoin/cxo/data/cxds)

CXDS is CX data store. The CXDS is implementation of
[data.CXDS](https://godoc.org/github.com/skycoin/cxo/data#CXDS). There are
on-drive CXDS based on [boltdb](github.com/boltdb/bolt) and in-memory CXDS
based on golang mutexes and map.


## Schema

Schema is simple

```
hash - > {rc, object}
```

Where the `hash` is SHA256 hash of the object. The object is any non-blank
`[]byte`. And the `rc` is references counter. The `rc` is number of other
objects that point to this one

---
