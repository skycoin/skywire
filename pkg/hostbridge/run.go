// Package hostbridge pkg/hostbridge/run.go c3-vis-wasm
package hostbridge

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"strings"
)

// NewToken returns a random 128-bit token in hex.
func NewToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Run listens on addr and serves tabs until the listener fails. ready gets
// the URL the tab connects to, token included.
func Run(addr string, cfg Config, ready func(url string)) error {
	if cfg.Token == "" {
		t, err := NewToken()
		if err != nil {
			return err
		}
		cfg.Token = t
	}
	s, err := NewServer(cfg)
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer l.Close() //nolint:errcheck
	if ready != nil {
		ready("ws://" + l.Addr().String() + "/" + cfg.Token)
	}
	return s.Serve(l)
}

// SplitOrigins parses a comma separated origin list.
func SplitOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			out = append(out, o)
		}
	}
	return out
}
