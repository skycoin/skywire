// Package visorapi pkg/visor/visorapi/doc.go c3-vis-core
//
// Package visorapi is the visor's RPC contract: the API interface, the types
// its methods take and return, and the net/rpc client that implements it
// against a running visor.
//
// It exists so that callers of a visor (the CLI, apps, services) can depend
// on the contract without linking the visor itself. pkg/visor implements API
// and serves it; nothing here imports pkg/visor.
package visorapi
