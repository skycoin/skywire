// Package pty pkg/pty/const.go
package pty

// Constants related to pty.
const (
	PtyRPCName  = "pty"
	PtyURI      = "dmsgpty/pty"
	PtyProxyURI = "dmsgpty/proxy"
)

const (
	// DefaultCLINet for windows
	DefaultCLINet = "unix"
)

// Constants related to dmsg.
const (
	DefaultPort     = uint16(22)
	DefaultCmd      = "/bin/bash"
	DefaultFlagExec = "-c"
)

// Constants related to whitelist.
const (
	WhitelistRPCName = "whitelist"
	WhitelistURI     = "dmsgpty/whitelist"
)

// Subsystem URIs share the dmsgpty mux + noise-protected stream + whitelist
// auth. The mux dispatches on URI alone — new subsystems are added by
// registering a new URI rather than bumping a wire-protocol version, so
// older peers either serve the URIs they know (pty) and reject the rest
// (preserving backwards-compat) or, on the client side, simply never ask
// for what they don't implement.
const (
	// SftpURI is the dispatch key for the sftp subsystem. Server-side
	// hands the conn to github.com/pkg/sftp.NewServer instead of the
	// net/rpc-driven pty gateway. Whitelist gating is identical to PtyURI.
	SftpURI = "dmsgpty/sftp"
)
