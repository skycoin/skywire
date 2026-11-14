//go:build linux

// Package call pkg/skychat/call/pulse_server_linux.go c3-app-chat
package call

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jfreymuth/pulse"
)

// A visor run as root by systemd has neither PULSE_SERVER nor XDG_RUNTIME_DIR,
// so the client finds no server. Root may use a logged-in user's socket.
const (
	runUserDir = "/run/user"
	seat0File  = "/run/systemd/seats/seat0"
)

// newPulseClient connects to the audio server, using a logged-in user's
// server when the visor runs as root without a server of its own.
func newPulseClient() (*pulse.Client, error) {
	if p := rootPulseSocket(); p != "" {
		return pulse.NewClient(pulse.ClientServerString("unix:" + p))
	}
	return pulse.NewClient()
}

// rootPulseSocket is the user socket a root visor should use, or "" when the
// environment already names a server or the visor is not root.
func rootPulseSocket() string {
	if os.Geteuid() != 0 {
		return ""
	}
	if _, ok := os.LookupEnv("PULSE_SERVER"); ok {
		return ""
	}
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		return ""
	}
	return findUserPulseSocket(runUserDir, seat0File)
}

// findUserPulseSocket returns runUser/<uid>/pulse/native for the user of the
// active seat, else for the lowest uid of 1000 or more that has one.
// PipeWire's PulseAudio server listens on the same path.
func findUserPulseSocket(runUser, seatFile string) string {
	socket := func(uid int) string {
		p := filepath.Join(runUser, strconv.Itoa(uid), "pulse", "native")
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return p
		}
		return ""
	}
	if uid, ok := seatActiveUID(seatFile); ok && uid >= 1000 {
		if p := socket(uid); p != "" {
			return p
		}
	}
	entries, err := os.ReadDir(runUser)
	if err != nil {
		return ""
	}
	var uids []int
	for _, e := range entries {
		if uid, err := strconv.Atoi(e.Name()); err == nil && uid >= 1000 {
			uids = append(uids, uid)
		}
	}
	sort.Ints(uids)
	for _, uid := range uids {
		if p := socket(uid); p != "" {
			return p
		}
	}
	return ""
}

// seatActiveUID reads ACTIVE_UID from logind's seat state file. The format is
// not a stable interface, so any failure just falls back to the uid scan.
func seatActiveUID(path string) (int, bool) {
	f, err := os.Open(path) //nolint:gosec // fixed system path
	if err != nil {
		return 0, false
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ACTIVE_UID="); ok {
			uid, err := strconv.Atoi(strings.TrimSpace(v))
			return uid, err == nil
		}
	}
	return 0, false
}

// ProbeAudio checks that an audio server answers, without opening a device.
func ProbeAudio() error {
	c, err := newPulseClient()
	if err != nil {
		return err
	}
	c.Close()
	return nil
}
