//go:build !(js && wasm)

// Package servicedisc pkg/servicedisc/stringarray_native.go c2-net-discovery
//
// On server/native builds LocalIPs is backed by pq.StringArray so the
// service-discovery store can persist it into a Postgres text[] column
// (gorm tag `type:text[]`). The browser build has no database and no
// business dragging in github.com/lib/pq — which also fails to compile
// under the TinyGo fork's 32-bit int + software crypto/tls — so it aliases
// to a plain []string instead (see stringarray_js.go). On the wire both are
// identical: pq.StringArray has no custom JSON codec, it marshals as []string.
package servicedisc

import (
	pq "github.com/lib/pq"
)

type stringArray = pq.StringArray
