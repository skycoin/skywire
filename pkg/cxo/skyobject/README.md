skyobject
=========

[![GoDoc](https://godoc.org/github.com/skycoin/cxo/skyobject?status.svg)](https://godoc.org/github.com/skycoin/cxo/skyobject)

Skyobject represents CX objects tree.

See [wiki](https://godoc.org/github.com/skycoin/cxo/wiki/Skyobject)

<!--

---

### Database representation

Possible types are

- Signed integer
  - `int8`
  - `int16`
  - `int32`
  - `int64`
- Unsigned integer
  - `uint8`
  - `uint16`
  - `uint32`
  - `uint64`
- Float
  - `float32`
  - `float64`
- Also
  - `string`

...and all types like

```go
type Age uint32
```

- Fixed size arrays of this types
- Slices of this types
- Structures that contains only this types

> Note: a structure can contains internal types that will not be used by CXO

For examples:

```go
type Age uint32

func (a Age) IsTooYoung() bool { return a < LegalAge }

type Name string

func (n Name) IsValid() bool { return len(n) > 0}

type User struct {
	Age uint32
	Name
}
```

-->
