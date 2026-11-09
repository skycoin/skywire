package alerts

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// IOErrorWindow is how far back the kernel log counts storage errors.
const IOErrorWindow = 24 * time.Hour

// RootReadOnly reports whether /proc/mounts, read from r, has the root
// filesystem mounted read-only. The last mount on / wins.
func RootReadOnly(r io.Reader) bool {
	ro := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[1] != "/" || f[0] == "rootfs" {
			continue
		}
		ro = false
		for _, o := range strings.Split(f[3], ",") {
			if o == "ro" {
				ro = true
			}
		}
	}
	return ro
}

// storageErrorRe matches kernel log lines that mean the storage is
// failing: block layer I/O errors, ext4 and journal errors, a forced
// read-only remount and MMC transfer errors and timeouts.
var storageErrorRe = regexp.MustCompile(`I/O error|EXT4-fs error|Remounting filesystem read-only|JBD2: Error|mmcblk\d+: error|mmc\d+: Timeout waiting for hardware`)

// StorageErrors counts the storage error records among /dev/kmsg lines
// ("prio,seq,usec,flags;message", Documentation/ABI/testing/dev-kmsg)
// logged at or after since, a time since boot, and returns the last one.
func StorageErrors(lines []string, since time.Duration) (n int, last string) {
	for _, l := range lines {
		head, msg, ok := strings.Cut(l, ";")
		if !ok {
			continue
		}
		f := strings.Split(head, ",")
		if len(f) < 3 {
			continue
		}
		usec, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || time.Duration(usec)*time.Microsecond < since {
			continue
		}
		if storageErrorRe.MatchString(msg) {
			n++
			last = strings.TrimSpace(msg)
		}
	}
	return n, last
}
