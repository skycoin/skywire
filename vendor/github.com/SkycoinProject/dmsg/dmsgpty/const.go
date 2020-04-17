package dmsgpty

// Constants related to pty.
const (
	PtyRPCName  = "pty"
	PtyURI      = "dmsgpty/pty"
	PtyProxyURI = "dmsgpty/proxy"
)

// Constants related to whitelist.
const (
	WhitelistRPCName = "whitelist"
	WhitelistURI     = "dmsgpty/whitelist"
)

// Constants related to CLI.
const (
	DefaultCLINet  = "unix"
	DefaultCLIAddr = "/tmp/dmsgpty.sock"
)

// Constants related to dmsg.
const (
	DefaultPort = uint16(22)
	DefaultCmd  = "/bin/bash"
)
