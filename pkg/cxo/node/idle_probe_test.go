package node

import (
	"testing"
	"time"
)

// A conn silent for idleProbeAfter is probed, and the probe and its answer
// count as inbound on both ends, so neither reaper closes a healthy quiet
// conn. A conn silent past idleWatchdogThreshold is still closed.
func TestReapIdleProbesQuietConn(t *testing.T) {
	ln := getTestNode("probe-server")
	defer ln.Close() //nolint:errcheck,gosec
	cn, err := NewNode(getTestConfigNotListen("probe-client"))
	assertNil(t, err)
	defer cn.Close() //nolint:errcheck,gosec

	c, err := cn.TCP().Connect(ln.TCP().Address())
	assertNil(t, err)
	waitForCondition(t, "server sees the client", func() bool {
		return len(ln.Connections()) == 1
	})
	sc := ln.Connections()[0]

	quiet := time.Now().Add(-idleProbeAfter - time.Second).UnixNano()
	c.lastActivityNs.Store(quiet)
	sc.lastActivityNs.Store(quiet)

	cn.reapIdle(time.Now())

	waitForCondition(t, "the server receives the probe", func() bool {
		return sc.lastActivityNs.Load() > quiet
	})
	waitForCondition(t, "the client receives the answer", func() bool {
		return c.lastActivityNs.Load() > quiet
	})
	select {
	case <-c.Done():
		t.Fatal("probed conn was closed")
	default:
	}

	c.lastActivityNs.Store(time.Now().Add(-idleWatchdogThreshold - time.Second).UnixNano())
	cn.reapIdle(time.Now())
	select {
	case <-c.Done():
	case <-time.After(TM):
		t.Fatal("conn silent past the threshold was not closed")
	}
}
