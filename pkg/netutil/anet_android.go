//go:build android

// Package netutil pkg/netutil/anet_android.go c0-com-util
package netutil

import (
	"os"
	"strconv"

	"github.com/wlynxg/anet"
)

// APILevelEnv carries the host app's Build.VERSION.SDK_INT to the visor
// process.
//
// Android 11 (API 30) stopped letting unprivileged processes run the
// RTM_GETLINK netlink dump the Go standard library uses to enumerate network
// interfaces, so net.Interfaces() fails with "route ip+net: netlinkrib:
// permission denied" and everything built on it — the visor overview, the
// address-resolver binds — fails with it. anet avoids that by enumerating
// through RTM_GETADDR instead, but only once it knows the device is on API
// 30+; its own detection calls android_get_device_api_level() through cgo,
// and the shipped payload is built CGO_ENABLED=0. The device API level is
// also not discoverable from a pure-Go app process (/system/build.prop is
// not readable and there is no property-service client without cgo), so the
// host app passes it in.
const APILevelEnv = "SKYWIRE_ANDROID_API_LEVEL"

// androidFallbackAPILevel is assumed when the env var is absent or unusable.
// anet's RTM_GETADDR path is permitted on every Android version, so treating
// an unknown device as "restricted" is the safe direction: it keeps interface
// enumeration working instead of failing on the majority of devices.
const androidFallbackAPILevel = 30

func init() {
	level := androidFallbackAPILevel
	if raw := os.Getenv(APILevelEnv); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			level = parsed
		}
	}
	anet.SetAndroidVersion(uint(level))
}
