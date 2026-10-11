// Package visorapi pkg/visor/visorapi/hostbridge.go c3-vis-wasm
package visorapi

// HostBridgeStatus is the visor's session to a host bridge, which lends a
// browser visor the sockets of its host. See pkg/hostbridge.
type HostBridgeStatus struct {
	URL       string              `json:"url,omitempty"`
	Connected bool                `json:"connected"`
	Error     string              `json:"error,omitempty"`
	Forwards  []HostBridgeForward `json:"forwards"`
}

// HostBridgeForward is a TCP listener on the bridge's host whose
// connections are passed to a local address of the visor.
type HostBridgeForward struct {
	// Host is the address asked for on the host, Bound the one it got.
	Host  string `json:"host"`
	Bound string `json:"bound,omitempty"`
	Local string `json:"local"`
	Error string `json:"error,omitempty"`
}

// HostBridgeForwardArgs asks for a forward from Host to Local.
type HostBridgeForwardArgs struct {
	Host  string
	Local string
}
