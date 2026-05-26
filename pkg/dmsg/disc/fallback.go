//go:build !js

// Package disc — fallback discovery client.
//
// Build-tag-gated off the WASM path because it imports pkg/logging
// (which wraps logrus, which transitively pulls encoding/json).
// FallbackClient is constructed only from pkg/dmsgc at visor
// runtime; the WASM install-page graph doesn't reach it.
//
// FallbackClient wraps two APIClients (primary + fallback) and tries
// the primary first; on error it transparently retries the same call
// against the fallback. Used by the visor's dmsg layer when an
// operator points the visor at a hypervisor-hosted dmsg-discovery
// proxy as primary, with the canonical public discovery as the
// safety-net fallback. Means visors stay reachable for lookups even
// when the hypervisor itself is offline, while still benefiting from
// the proxy's local-relay shortcut when it is online.
package disc

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// FallbackClient implements APIClient by delegating to a primary
// client and falling back to a secondary on error.
type FallbackClient struct {
	primary  APIClient
	fallback APIClient
	log      *logging.Logger
}

// NewFallbackClient returns an APIClient that tries `primary` first
// and falls back to `fallback` whenever primary returns a non-nil
// error. Both clients must be non-nil.
func NewFallbackClient(primary, fallback APIClient, log *logging.Logger) APIClient {
	return &FallbackClient{primary: primary, fallback: fallback, log: log}
}

func (f *FallbackClient) logFallback(method string, err error) {
	if f.log == nil {
		return
	}
	f.log.WithError(err).WithField("method", method).
		Debug("Primary dmsg-discovery failed, falling back")
}

// Entry tries primary, falls back to fallback on error.
func (f *FallbackClient) Entry(ctx context.Context, pk cipher.PubKey) (*Entry, error) {
	entry, err := f.primary.Entry(ctx, pk)
	if err == nil {
		return entry, nil
	}
	f.logFallback("Entry", err)
	return f.fallback.Entry(ctx, pk)
}

// PostEntry tries primary, falls back on error. Both clients receive
// the entry on success — the primary's success short-circuits the
// fallback so we don't double-write in the common case.
func (f *FallbackClient) PostEntry(ctx context.Context, entry *Entry) error {
	if err := f.primary.PostEntry(ctx, entry); err == nil {
		return nil
	} else {
		f.logFallback("PostEntry", err)
	}
	return f.fallback.PostEntry(ctx, entry)
}

// PutEntry tries primary, falls back on error.
func (f *FallbackClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *Entry) error {
	if err := f.primary.PutEntry(ctx, sk, entry); err == nil {
		return nil
	} else {
		f.logFallback("PutEntry", err)
	}
	return f.fallback.PutEntry(ctx, sk, entry)
}

// DelEntry tries primary, falls back on error.
func (f *FallbackClient) DelEntry(ctx context.Context, entry *Entry) error {
	if err := f.primary.DelEntry(ctx, entry); err == nil {
		return nil
	} else {
		f.logFallback("DelEntry", err)
	}
	return f.fallback.DelEntry(ctx, entry)
}

// AvailableServers tries primary, falls back on error.
func (f *FallbackClient) AvailableServers(ctx context.Context) ([]*Entry, error) {
	servers, err := f.primary.AvailableServers(ctx)
	if err == nil {
		return servers, nil
	}
	f.logFallback("AvailableServers", err)
	return f.fallback.AvailableServers(ctx)
}

// AllServers tries primary, falls back on error.
func (f *FallbackClient) AllServers(ctx context.Context) ([]*Entry, error) {
	servers, err := f.primary.AllServers(ctx)
	if err == nil {
		return servers, nil
	}
	f.logFallback("AllServers", err)
	return f.fallback.AllServers(ctx)
}

// AllEntries tries primary, falls back on error.
func (f *FallbackClient) AllEntries(ctx context.Context) ([]string, error) {
	entries, err := f.primary.AllEntries(ctx)
	if err == nil {
		return entries, nil
	}
	f.logFallback("AllEntries", err)
	return f.fallback.AllEntries(ctx)
}

// AllClientsByServer tries primary, falls back on error.
func (f *FallbackClient) AllClientsByServer(ctx context.Context) (map[string][]*Entry, error) {
	res, err := f.primary.AllClientsByServer(ctx)
	if err == nil {
		return res, nil
	}
	f.logFallback("AllClientsByServer", err)
	return f.fallback.AllClientsByServer(ctx)
}

// ClientsByServer tries primary, falls back on error.
func (f *FallbackClient) ClientsByServer(ctx context.Context, serverPK cipher.PubKey) ([]*Entry, error) {
	res, err := f.primary.ClientsByServer(ctx, serverPK)
	if err == nil {
		return res, nil
	}
	f.logFallback("ClientsByServer", err)
	return f.fallback.ClientsByServer(ctx, serverPK)
}
