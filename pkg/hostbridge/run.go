// Package hostbridge pkg/hostbridge/run.go c3-vis-wasm
package hostbridge

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
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

// DefaultTokenFile is where the bridge keeps its token, so the URL a visor
// saved still works after the bridge restarts. It is empty when the OS has
// no config directory.
func DefaultTokenFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "skywire", "hostbridge-token")
}

// LoadToken reads the token in path, or writes a new one there.
func LoadToken(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // the operator names the token file
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	}
	t, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return t, os.WriteFile(path, []byte(t+"\n"), 0o600)
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

// ResolveToken returns token when set, else the one kept in tokenFile. An
// empty tokenFile means DefaultTokenFile, and "none" a new token each run.
func ResolveToken(token, tokenFile string) (string, error) {
	if token != "" {
		return token, nil
	}
	if tokenFile == "" {
		tokenFile = DefaultTokenFile()
	}
	if tokenFile == "" || tokenFile == "none" {
		return NewToken()
	}
	return LoadToken(tokenFile)
}
