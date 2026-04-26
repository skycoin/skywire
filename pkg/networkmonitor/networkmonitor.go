// Package networkmonitor pkg/networkmonitor/networkmonitor.go
package networkmonitor

// WhitelistPKs store whitelisted keys of network monitor
type WhitelistPKs map[string]struct{}

// GetWhitelistPKs returns the stuct WhitelistPKs
func GetWhitelistPKs() WhitelistPKs {
	return make(WhitelistPKs)
}

// Set sets the whitelist with the given pk in the struct
func (wl WhitelistPKs) Set(nmPkString string) {
	wl[nmPkString] = struct{}{}
}

// Get gets the pk from the whitelist
func (wl WhitelistPKs) Get(nmPkString string) bool {
	_, ok := wl[nmPkString]
	return ok
}
