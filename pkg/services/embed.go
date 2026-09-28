// Package services pkg/services/embed.go c2-vis-appsvc
//
// Embedding: a service that can run inside a host process which already
// owns a dmsg client and an identity. The host (the visor) mounts the
// service's HTTP handler under a path prefix on its own dmsg HTTP port, so
// the service is addressed as dmsg://<host pk>:80/<prefix>/... and needs no
// key, no listener and no dmsg sessions of its own. Its CXO work runs on
// the host's client under the host's key.
package services

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// Host is what an embedding process provides to a service.
type Host struct {
	// DmsgClient is the host's dmsg client. Nil means the service runs
	// its HTTP surface only and starts none of its CXO publishers or
	// aggregators (tests, or a host without dmsg).
	DmsgClient *dmsg.Client
	// PK and SK are the host's identity. Services that bind a CXO node
	// identity use SK; PK names the host on /health.
	PK cipher.PubKey
	SK cipher.SecKey
	// DmsgAddr is "<pk>:<port>", the dmsg address the service reports on
	// /health as its own.
	DmsgAddr string
	// Log is the host-supplied logger for the service.
	Log *logging.Logger
}

// Embeddable is a Service that can also run inside a host process.
// Embed builds the service, starts its background work, and returns
// the HTTP handler for the host to mount. It returns as soon as the
// service is up; the service stops when ctx is canceled.
type Embeddable interface {
	Service
	Embed(ctx context.Context, host Host) (http.Handler, error)
}

// defaultPrefixes maps a service type to the path prefix it is mounted
// under when the block names none.
var defaultPrefixes = map[string]string{
	"transport-discovery": "/tpd",
	"address-resolver":    "/ar",
	"route-finder":        "/rf",
	"service-discovery":   "/sd",
	"dmsg-discovery":      "/dmsgd",
}

// Prefix returns the path prefix the block is mounted under: its
// "prefix" field, or the default for its type, or "/<type>". Always
// starts with "/" and never ends with one.
func (b *Block) Prefix() string {
	var probe struct {
		Prefix string `json:"prefix,omitempty"`
	}
	_ = json.Unmarshal(b.Raw, &probe) //nolint:errcheck // Raw already parsed once; a bad prefix falls through to the default
	p := probe.Prefix
	if p == "" {
		p = defaultPrefixes[b.Type]
	}
	if p == "" {
		p = "/" + b.Type
	}
	p = "/" + strings.Trim(p, "/")
	return p
}

// MarshalJSON writes the block back exactly as it was read, so a config
// that carries service blocks survives a load-and-save round trip.
func (b Block) MarshalJSON() ([]byte, error) {
	if len(b.Raw) == 0 {
		if b.Type == "" {
			return []byte("null"), nil
		}
		return json.Marshal(struct {
			Type string `json:"type"`
			Name string `json:"name,omitempty"`
		}{Type: b.Type, Name: b.Name})
	}
	return b.Raw, nil
}

// Mount attaches handler under prefix on mux: a request for
// prefix/health reaches handler as /health. A request for the bare
// prefix is redirected to prefix/ by the mux.
func Mount(mux *http.ServeMux, prefix string, handler http.Handler) {
	prefix = "/" + strings.Trim(prefix, "/")
	mux.Handle(prefix+"/", http.StripPrefix(prefix, handler))
}
