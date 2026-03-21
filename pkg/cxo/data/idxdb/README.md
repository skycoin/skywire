IdxDB
=====

[![GoDoc](https://godoc.org/github.com/skycoin/cxo/data/idxdb?status.svg)](https://godoc.org/github.com/skycoin/cxo/data/idxdb)


The IdxDB is machine local index for CXDS and used by CXO (skyobject).
The idxDB contains meta information about CX objects and also contains
some necessary information that required by skyobject. Such as feeds we
are subscribed to, roots ordered by seq, etc, etc

### Scehma

Schema of the IdxDB is simple.

```
feed -> [ head -> [ root, ...], ... ]
```

Key for a feed is public key. Key for a head is nonce (`uint64`). And all
root objects sorted by seq number (the seq is key).
