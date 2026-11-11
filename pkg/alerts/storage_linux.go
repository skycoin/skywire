//go:build linux

package alerts

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// storageCacheFor spaces out kernel log reads, the summary poll runs
// every 30s.
const storageCacheFor = time.Minute

var storageCache struct {
	sync.Mutex
	at       time.Time
	readOnly bool
	n        int
	last     string
}

// CheckStorage reports whether the root filesystem is read-only and the
// storage errors in the kernel log over the last IOErrorWindow. Reading
// the kernel log needs root or CAP_SYSLOG; without it the count is zero.
func CheckStorage() (readOnly bool, ioErrors int, last string) {
	c := &storageCache
	c.Lock()
	defer c.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < storageCacheFor {
		return c.readOnly, c.n, c.last
	}
	c.at = time.Now()
	c.readOnly, c.n, c.last = false, 0, ""
	// Android always mounts the system image on / read-only.
	if runtime.GOOS != "android" {
		if f, err := os.Open("/proc/mounts"); err == nil {
			c.readOnly = RootReadOnly(f)
			_ = f.Close() //nolint:errcheck
		}
	}
	if up := uptime(); up > 0 {
		c.n, c.last = StorageErrors(readKmsg(), up-IOErrorWindow)
	}
	return c.readOnly, c.n, c.last
}

func uptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(s * float64(time.Second))
}

// readKmsg reads every record in the kernel log buffer. Non-blocking raw
// reads end at EAGAIN; os.File would park on the poller instead.
func readKmsg() []string {
	fd, err := unix.Open("/dev/kmsg", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(fd) //nolint:errcheck
	var out []string
	buf := make([]byte, 8192)
	for i := 0; i < 100000; i++ {
		n, err := unix.Read(fd, buf)
		if errors.Is(err, unix.EPIPE) {
			continue // records overwritten while reading
		}
		if err != nil || n <= 0 {
			break
		}
		out = append(out, string(buf[:n]))
	}
	return out
}
