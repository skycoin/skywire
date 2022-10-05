package dmsgpty

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
