// Package visor pkg/visor/api_skychat.go
//
// Skychat support — password management + reverse proxy.
//
// The hypervisor UI surfaces skychat as a first-class tab. The
// browser doesn't talk to skychat directly; it goes through the
// hypervisor at /api/visors/<pk>/skychat/* which forwards to the
// skychat app's local HTTP listener. The proxy attaches a
// per-startup random "internal token" so authenticated hvui
// sessions bypass any password the user set on the standalone
// :8001 surface.
package visor

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	skychatPasswordFile = "skychat-password"
	skychatPasswordArg  = "--password-file"
	skychatTokenArg     = "--internal-token"
	skychatAddrArg      = "--addr"

	// passwordSaltLen mirrors the hypervisor user-store value so the
	// hashing scheme is identical for both surfaces (see
	// pkg/visor/usermanager/user.go).
	skychatPasswordSaltLen = 16
	skychatMinPasswordLen  = 6
	skychatMaxPasswordLen  = 64
)

var (
	// skychatTokenOnce holds the per-process internal token used for
	// hvui→skychat proxy auth bypass. Generated lazily on first
	// access and re-used for the visor's lifetime so we don't need
	// to coordinate token rotation with skychat restart sequencing.
	skychatTokenOnce sync.Once
	skychatToken     string
)

// SkychatPasswordIsSet reports whether a non-empty password file
// exists for the skychat app on this visor.
func (v *Visor) SkychatPasswordIsSet() (bool, error) {
	path := v.skychatPasswordPath()
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return st.Size() > 0, nil
}

// SetSkychatPassword writes a salted SHA256 hash of newPassword to
// the visor's skychat-password file and ensures the skychat app
// is launched with --password-file pointing at it. If a password
// is currently set, oldPassword must verify against it. Mirrors
// the hypervisor's ChangePassword shape.
func (v *Visor) SetSkychatPassword(oldPassword, newPassword string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if err := checkSkychatPassword(newPassword); err != nil {
		return err
	}

	path := v.skychatPasswordPath()
	if hadPrev, err := readSkychatPassword(path); err != nil {
		return err
	} else if hadPrev != nil {
		// A password is already set — require the old one.
		if !verifySkychatPassword(hadPrev, oldPassword) {
			return errors.New("incorrect current skychat password")
		}
	}

	salt := cipher.RandByte(skychatPasswordSaltLen)
	hash := cipher.SumSHA256(append([]byte(newPassword), salt...))
	line := hex.EncodeToString(salt) + ":" + hex.EncodeToString(hash[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec
		return err
	}
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil { //nolint:gosec
		return err
	}

	return v.applySkychatLaunchArgs(path)
}

// ClearSkychatPassword removes the password file and re-launches
// skychat without --password-file so the gate goes back to off.
func (v *Visor) ClearSkychatPassword(oldPassword string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	path := v.skychatPasswordPath()
	if cur, err := readSkychatPassword(path); err != nil {
		return err
	} else if cur != nil {
		if !verifySkychatPassword(cur, oldPassword) {
			return errors.New("incorrect current skychat password")
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Re-launch skychat with --password-file = "" so it knows the
	// password is gone (UpdateAppArg with empty value clears the arg
	// and restarts the app via launcher).
	return v.conf.UpdateAppArg(v.appL, skyenv.SkychatName, skychatPasswordArg, "")
}

// SkychatProxy proxies a request to the local skychat HTTP server.
// Returns the response status, headers and body. Used by the
// hypervisor's /api/visors/<pk>/skychat/* mount.
//
// path must be the suffix after "/skychat/" (e.g. "sse",
// "history/peers"). The proxy attaches the internal token so the
// password gate is bypassed for in-process hvui calls.
func (v *Visor) SkychatProxy(method, path string, query string, headers http.Header, body io.Reader) (int, http.Header, []byte, error) {
	addr := skychatLocalAddr(v)
	if addr == "" {
		return 0, nil, nil, errors.New("skychat addr not configured")
	}
	url := "http://" + addr + "/" + strings.TrimPrefix(path, "/")
	if query != "" {
		url += "?" + query
	}
	// SSRF check is suppressed: addr is the visor's own configured
	// skychat bind address from the launcher config — not user
	// input — and only flows to localhost in the default config.
	req, err := http.NewRequest(method, url, body) //nolint:gosec // visor-controlled URL
	if err != nil {
		return 0, nil, nil, err
	}
	for k, vv := range headers {
		// Skip Host / Connection — net/http will manage them.
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, h := range vv {
			req.Header.Add(k, h)
		}
	}
	req.Header.Set("X-Skychat-Internal-Token", v.skychatInternalToken())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // visor-controlled URL
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	return resp.StatusCode, resp.Header, respBody, nil
}

// SkychatLocalAddr returns the host:port the visor's skychat is
// configured to bind. Used by the UI to show / link the standalone
// surface. Empty when skychat isn't configured on this visor.
func (v *Visor) SkychatLocalAddr() (string, error) {
	addr := skychatLocalAddr(v)
	if addr == "" {
		return "", errors.New("skychat not configured")
	}
	return addr, nil
}

// applySkychatLaunchArgs ensures skychat will start with the
// password-file + internal-token args set. UpdateAppArg writes the
// arg to the running config and restarts the app — so the user's
// next page-load picks up the new gating without any manual step.
func (v *Visor) applySkychatLaunchArgs(passwordFile string) error {
	if err := v.conf.UpdateAppArg(v.appL, skyenv.SkychatName, skychatTokenArg, v.skychatInternalToken()); err != nil {
		return err
	}
	if err := v.conf.UpdateAppArg(v.appL, skyenv.SkychatName, skychatPasswordArg, passwordFile); err != nil {
		return err
	}
	return nil
}

func (v *Visor) skychatPasswordPath() string {
	root := v.conf.LocalPath
	if root == "" {
		root = "."
	}
	return filepath.Join(root, skychatPasswordFile)
}

func (v *Visor) skychatInternalToken() string {
	skychatTokenOnce.Do(func() {
		var b [24]byte
		if _, err := rand.Read(b[:]); err != nil {
			// Fall back to a time-derived value — still unguessable
			// for the lifetime of the process, just less ideal.
			skychatToken = fmt.Sprintf("t-%d", time.Now().UnixNano())
			return
		}
		skychatToken = hex.EncodeToString(b[:])
	})
	return skychatToken
}

// skychatLocalAddr returns the host:port skychat will bind by
// reading its --addr arg out of the visor's launcher config. Falls
// back to the env-configured default. The "*:PORT" form (used by
// the docker integration configs) is rewritten to "0.0.0.0:PORT"
// so a Go HTTP client can reach it from the visor process.
func skychatLocalAddr(v *Visor) string {
	addr := readAppArg(v, skyenv.SkychatName, skychatAddrArg)
	if addr == "" {
		addr = skyenv.SkychatAddr
	}
	switch {
	case strings.HasPrefix(addr, "*:"):
		return "127.0.0.1:" + strings.TrimPrefix(addr, "*:")
	case strings.HasPrefix(addr, ":"):
		return "127.0.0.1" + addr
	default:
		return addr
	}
}

// readAppArg fetches the value of a "--name VALUE" / "-name VALUE"
// flag from an app's launcher config. Returns "" when the app or
// flag isn't present.
func readAppArg(v *Visor, appName, flag string) string {
	if v.conf == nil || v.conf.Launcher == nil {
		return ""
	}
	for _, app := range v.conf.Launcher.Apps {
		if app.Name != appName {
			continue
		}
		args := app.Args
		for i := 0; i < len(args)-1; i++ {
			a := args[i]
			if a == flag || a == "-"+strings.TrimPrefix(flag, "--") {
				return args[i+1]
			}
		}
	}
	return ""
}

// readSkychatPassword reads the on-disk password record. Returns
// nil with no error when the file is absent or empty.
type skychatPasswordRecord struct {
	salt []byte
	hash cipher.SHA256
}

func readSkychatPassword(path string) (*skychatPasswordRecord, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return nil, nil
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return nil, errors.New("malformed skychat password file")
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	hashBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	rec := &skychatPasswordRecord{salt: salt}
	if len(hashBytes) != len(rec.hash) {
		return nil, errors.New("malformed skychat password hash")
	}
	copy(rec.hash[:], hashBytes)
	return rec, nil
}

func verifySkychatPassword(rec *skychatPasswordRecord, password string) bool {
	if rec == nil {
		return password == ""
	}
	got := cipher.SumSHA256(append([]byte(password), rec.salt...))
	return got == rec.hash
}

func checkSkychatPassword(p string) error {
	if len(p) < skychatMinPasswordLen || len(p) > skychatMaxPasswordLen {
		return fmt.Errorf("skychat password length must be %d-%d chars", skychatMinPasswordLen, skychatMaxPasswordLen)
	}
	return nil
}
