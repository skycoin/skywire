// Package disc — pkg/dmsg/disc/interface.go
//
// Holds the cross-build interface definitions (EntryReader,
// EntryWriter, APIClient) that callers depend on for type-checking
// regardless of whether they actually instantiate an HTTP client.
//
// The HTTP implementation lives in client.go under //go:build !js;
// keeping the interfaces in their own file (with no build tag and
// no net/http import) means consumers like
// pkg/visor/visorconfig — which only need *Entry plus the
// interface shapes for type aliasing — can compile under
// GOOS=js GOARCH=wasm (TinyGo included) without the WASM build
// graph pulling in net/http and tripping over TinyGo 0.41.1's
// net/http stdlib bug.
package disc

import (
	"context"

	jsoniter "github.com/json-iterator/go"

	"github.com/skycoin/skywire/pkg/cipher"
)

// json is the package-wide JSON codec. Declared here (rather than
// in client.go) because entry.go also uses it for Marshal/Unmarshal
// and client.go is now build-tag-gated; the variable needs to be
// available across all build configurations.
var json = jsoniter.ConfigFastest

// EntryReader provides read-only access to discovery entries.
type EntryReader interface {
	Entry(context.Context, cipher.PubKey) (*Entry, error)
	AvailableServers(context.Context) ([]*Entry, error)
	AllServers(context.Context) ([]*Entry, error)
}

// EntryWriter provides write access to discovery entries.
type EntryWriter interface {
	PostEntry(context.Context, *Entry) error
	PutEntry(context.Context, cipher.SecKey, *Entry) error
	DelEntry(context.Context, *Entry) error
}

// APIClient implements dmsg discovery API client.
type APIClient interface {
	EntryReader
	EntryWriter
	AllEntries(ctx context.Context) ([]string, error)
	AllClientsByServer(ctx context.Context) (map[string][]*Entry, error)
	ClientsByServer(ctx context.Context, serverPK cipher.PubKey) ([]*Entry, error)
}
