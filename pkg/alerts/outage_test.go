package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func site(n int, ip string) []Target {
	out := make([]Target, n)
	for i := range out {
		pk, _ := cipher.GenerateKeyPair()
		out[i] = Target{PK: pk, PublicIP: ip, LocalIP: "192.168.1." + string(rune('1'+i)), Online: true}
	}
	return out
}

func with(ts []Target, f func(i int, t *Target)) []Target {
	out := append([]Target(nil), ts...)
	for i := range out {
		f(i, &out[i])
	}
	return out
}

func TestSiteOutageSendsOneAlert(t *testing.T) {
	const ip = "198.51.100.7"
	delay := 5 * time.Minute
	t0 := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		uptime time.Duration
		want   string
	}{
		{"power", 2 * time.Minute, ReasonRebooted},
		{"internet", 240 * time.Hour, ReasonStayedUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := site(3, ip)
			tr := NewTracker(nil)
			require.Empty(t, tr.Observe(t0, delay, ts))
			down := with(ts, func(i int, t *Target) {
				t.Online = false
				t.LastSeen = t0.Add(time.Duration(i*20) * time.Second)
			})
			require.Empty(t, tr.Observe(t0.Add(time.Minute), delay, down))
			evs := tr.Observe(t0.Add(6*time.Minute), delay, down)
			require.Len(t, evs, 1, "one site alert, not three board alerts")
			ev := evs[0]
			require.Equal(t, KindOffline, ev.Kind)
			require.Equal(t, ScopeSite, ev.Scope)
			require.Equal(t, ip, ev.Site)
			require.Len(t, ev.Members, 3)
			require.Equal(t, t0, ev.Since)
			require.Equal(t, "[skywire] site 198.51.100.7: all 3 visors offline", Subject(ev))
			require.Empty(t, tr.Observe(t0.Add(7*time.Minute), delay, down))
			for _, st := range tr.States() {
				require.Equal(t, ip, st.Site)
			}

			// One board back holds the recovery until the rest follow.
			oneBack := with(down, func(i int, t *Target) { t.Online = i == 0; t.Uptime = Duration(tc.uptime) })
			require.Empty(t, tr.Observe(t0.Add(20*time.Minute), delay, oneBack))
			back := with(down, func(_ int, t *Target) { t.Online = true; t.Uptime = Duration(tc.uptime) })
			evs = tr.Observe(t0.Add(21*time.Minute), delay, back)
			require.Len(t, evs, 1, "one site recovery")
			require.Equal(t, KindOnline, evs[0].Kind)
			require.Equal(t, ScopeSite, evs[0].Scope)
			require.Equal(t, tc.want, evs[0].RecoveryReason)
			require.Equal(t, "[skywire] site 198.51.100.7 is back online", Subject(evs[0]))
			require.Empty(t, tr.Saved())
		})
	}
}

func TestPartialSiteSendsBoardAlerts(t *testing.T) {
	delay := 5 * time.Minute
	t0 := time.Now()
	ts := site(3, "198.51.100.8")
	tr := NewTracker(nil)
	tr.Observe(t0, delay, ts)
	down := with(ts, func(i int, t *Target) { t.Online = i == 2 })
	tr.Observe(t0.Add(time.Minute), delay, down)
	evs := tr.Observe(t0.Add(7*time.Minute), delay, down)
	require.Len(t, evs, 2)
	for _, ev := range evs {
		require.Equal(t, KindOffline, ev.Kind)
		require.Equal(t, ScopeBoard, ev.Scope)
	}

	// A lone visor at its IP is a board too.
	solo := site(1, "198.51.100.9")
	tr = NewTracker(nil)
	off := with(solo, func(_ int, t *Target) { t.Online = false })
	tr.Observe(t0, delay, off)
	evs = tr.Observe(t0.Add(6*time.Minute), delay, off)
	require.Len(t, evs, 1)
	require.Equal(t, ScopeBoard, evs[0].Scope)
}

func TestSiteRecoveryLeavesDeadBoardAsBoardAlert(t *testing.T) {
	delay := 5 * time.Minute
	t0 := time.Now()
	ts := site(3, "198.51.100.10")
	tr := NewTracker(nil)
	down := with(ts, func(_ int, t *Target) { t.Online = false })
	tr.Observe(t0, delay, down)
	require.Len(t, tr.Observe(t0.Add(6*time.Minute), delay, down), 1)
	partial := with(ts, func(i int, t *Target) { t.Online = i != 1; t.Uptime = Duration(time.Minute) })
	require.Empty(t, tr.Observe(t0.Add(10*time.Minute), delay, partial), "waits for the rest")
	evs := tr.Observe(t0.Add(15*time.Minute), delay, partial)
	require.Len(t, evs, 2)
	require.Equal(t, ScopeSite, evs[0].Scope)
	require.Equal(t, KindOnline, evs[0].Kind)
	require.Contains(t, evs[0].Diagnosis, "1 of 3 visors still offline")
	require.Equal(t, ReasonRebooted, evs[0].RecoveryReason)
	require.Equal(t, ScopeBoard, evs[1].Scope)
	require.Equal(t, KindOffline, evs[1].Kind)
	require.Equal(t, ts[1].PK, evs[1].Target.PK)
}

func TestSiteBackWithinDelaySendsNothing(t *testing.T) {
	delay := 5 * time.Minute
	t0 := time.Now()
	ts := site(2, "198.51.100.11")
	tr := NewTracker(nil)
	tr.Observe(t0, delay, ts)
	down := with(ts, func(_ int, t *Target) { t.Online = false })
	require.Empty(t, tr.Observe(t0.Add(time.Minute), delay, down))
	require.Empty(t, tr.Observe(t0.Add(4*time.Minute), delay, down))
	require.Empty(t, tr.Observe(t0.Add(5*time.Minute), delay, ts))
}

func TestRecoveryReason(t *testing.T) {
	require.Equal(t, ReasonRebooted, RecoveryReason(3*time.Minute, 10*time.Minute))
	require.Equal(t, ReasonStayedUp, RecoveryReason(48*time.Hour, 10*time.Minute))
	require.Empty(t, RecoveryReason(0, 10*time.Minute), "unknown uptime")

	pk, _ := cipher.GenerateKeyPair()
	t0 := time.Now()
	tr := NewTracker(nil)
	tr.Observe(t0, time.Minute, []Target{{PK: pk, LastSeen: t0}})
	tr.Observe(t0.Add(2*time.Minute), time.Minute, []Target{{PK: pk}})
	evs := tr.Observe(t0.Add(30*time.Minute), time.Minute, []Target{{PK: pk, Online: true, Uptime: Duration(5 * time.Minute)}})
	require.Equal(t, ReasonRebooted, evs[0].RecoveryReason)
}

func TestLANCheckWording(t *testing.T) {
	board := Target{PK: mustPK(), Hostname: "b1", PublicIP: "203.0.113.5", LocalIP: "192.168.1.10"}
	nb := Target{PK: mustPK(), Hostname: "b2", PublicIP: "203.0.113.5", LocalIP: "192.168.1.11", Online: true}
	other := Target{PK: mustPK(), PublicIP: "203.0.113.6", LocalIP: "192.168.1.12", Online: true}
	otherLAN := Target{PK: mustPK(), PublicIP: "203.0.113.5", LocalIP: "10.0.0.2", Online: true}

	var asked []string
	answer, fail := true, false
	s := &Service{Probe: func(_ context.Context, via cipher.PubKey, ip string) (bool, error) {
		asked = append(asked, via.String()+" "+ip)
		if fail {
			return false, errors.New("boom")
		}
		return answer, nil
	}}
	ctx := context.Background()
	all := []Target{board, nb, other, otherLAN}

	require.Equal(t, DiagLANUp+" (checked from b2)", s.lanCheck(ctx, board, all))
	require.Equal(t, []string{nb.PK.String() + " 192.168.1.10"}, asked, "only the same site and subnet")
	answer = false
	require.Equal(t, DiagLANDown+" (checked from b2)", s.lanCheck(ctx, board, all))
	require.Equal(t, DiagNoNeighbor, s.lanCheck(ctx, board, []Target{board, other, otherLAN}))
	noIP := board
	noIP.LocalIP = ""
	require.Equal(t, DiagNoLocalIP, s.lanCheck(ctx, noIP, all))
	fail = true
	require.Equal(t, "LAN check failed: b2: boom", s.lanCheck(ctx, board, all))

	fail, answer = false, true
	s.Self = func() Target {
		return Target{PK: mustPK(), Hostname: "hv", PublicIP: "203.0.113.5", LocalIP: "192.168.1.2"}
	}
	require.Equal(t, DiagLANUp+" (checked from hv)", s.lanCheck(ctx, board, []Target{board}), "the watcher itself is a neighbor")

	require.Empty(t, (&Service{}).lanCheck(ctx, board, all), "no prober wired")
}

func mustPK() cipher.PubKey {
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

func TestStorageWarningsOncePerCondition(t *testing.T) {
	pk := mustPK()
	tr := NewTracker(nil)
	now := time.Now()
	obs := func(s Storage) []Event {
		now = now.Add(30 * time.Second)
		return tr.Observe(now, time.Minute, []Target{{PK: pk, Hostname: "b1", Online: true, Storage: &s}})
	}
	require.Empty(t, obs(Storage{DiskUsedPercent: 50}))
	evs := obs(Storage{IOErrors: 3, LastIOError: "I/O error, dev mmcblk0, sector 2048"})
	require.Len(t, evs, 1)
	require.Equal(t, KindStorage, evs[0].Kind)
	require.Equal(t, []string{"3 storage I/O errors in the kernel log, last: I/O error, dev mmcblk0, sector 2048"}, evs[0].Warnings)
	require.Equal(t, "[skywire] visor b1 storage warning", Subject(evs[0]))
	require.Empty(t, obs(Storage{IOErrors: 4, LastIOError: "x"}), "once per condition")
	evs = obs(Storage{IOErrors: 4, ReadOnlyRoot: true})
	require.Len(t, evs, 1)
	require.Equal(t, []string{"root filesystem is read-only"}, evs[0].Warnings, "only the new condition")
	require.Empty(t, obs(Storage{}), "clearing sends nothing")
	require.Len(t, obs(Storage{ReadOnlyRoot: true}), 1, "a cleared condition can warn again")

	require.Len(t, obs(Storage{DiskUsedPercent: 91}), 1)
	require.Empty(t, obs(Storage{DiskUsedPercent: 88}), "inside the hysteresis")
	require.Empty(t, obs(Storage{DiskUsedPercent: 84}))
	require.Len(t, obs(Storage{DiskUsedPercent: 91}), 1)
	tr.DiskWarn = 95
	require.Empty(t, obs(Storage{DiskUsedPercent: 89}))
	require.Empty(t, obs(Storage{DiskUsedPercent: 94}))

	// The last storage report goes into the offline mail.
	off := []Target{{PK: pk}}
	tr.Observe(now.Add(time.Minute), time.Minute, off)
	evs = tr.Observe(now.Add(3*time.Minute), time.Minute, off)
	require.Len(t, evs, 1)
	require.Equal(t, KindOffline, evs[0].Kind)
	require.Empty(t, evs[0].Diagnosis, "94% is under the 95% limit")
	tr.Observe(now.Add(4*time.Minute), time.Minute, []Target{{PK: pk, Online: true, Storage: &Storage{IOErrors: 2}}})
	tr.Observe(now.Add(5*time.Minute), time.Minute, off)
	evs = tr.Observe(now.Add(7*time.Minute), time.Minute, off)
	require.Equal(t, "had storage errors before going offline: 2 storage I/O errors in the kernel log", evs[0].Diagnosis)
}

func TestStorageWarningsSurviveRestart(t *testing.T) {
	pk := mustPK()
	tr := NewTracker(nil)
	on := []Target{{PK: pk, Online: true, Storage: &Storage{ReadOnlyRoot: true}}}
	require.Len(t, tr.Observe(time.Now(), time.Minute, on), 1)
	tr = NewTracker(tr.Saved())
	require.Empty(t, tr.Observe(time.Now(), time.Minute, on), "no repeat after a restart")
}

func TestWebhookOutageFields(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var m map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&m))
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer srv.Close()

	ts := site(3, "203.0.113.20")
	cur := ts
	svc, err := NewService("", &Settings{Webhook: srv.URL, OfflineAfter: Duration(time.Minute)}, &Sender{})
	require.NoError(t, err)
	svc.Targets = func(context.Context, Settings) []Target { return cur }
	svc.Probe = func(context.Context, cipher.PubKey, string) (bool, error) { return true, nil }
	ctx := context.Background()
	t0 := time.Now()
	svc.Tick(ctx, t0)

	// One board down while its neighbors are up.
	cur = with(ts, func(i int, t *Target) { t.Online = i != 0 })
	svc.Tick(ctx, t0.Add(30*time.Second))
	svc.Tick(ctx, t0.Add(2*time.Minute))
	require.Len(t, got, 1)
	require.Equal(t, "offline", got[0]["event"])
	require.Equal(t, "board", got[0]["kind"])
	require.True(t, strings.HasPrefix(got[0]["diagnosis"].(string), DiagLANUp))
	require.Equal(t, ts[0].LocalIP, got[0]["local_ip"])

	cur = with(ts, func(_ int, t *Target) { t.Online = true; t.Uptime = Duration(time.Hour) })
	svc.Tick(ctx, t0.Add(3*time.Minute))
	require.Len(t, got, 2)
	require.Equal(t, "online", got[1]["event"])
	require.Equal(t, ReasonStayedUp, got[1]["recovery_reason"])

	// The whole site down.
	cur = with(ts, func(_ int, t *Target) { t.Online = false })
	svc.Tick(ctx, t0.Add(4*time.Minute))
	svc.Tick(ctx, t0.Add(6*time.Minute))
	require.Len(t, got, 3)
	require.Equal(t, "site", got[2]["kind"])
	require.Equal(t, "203.0.113.20", got[2]["public_ip"])
	require.Len(t, got[2]["members"], 3)
	require.NotContains(t, got[2], "pk", "a site has no single key")

	st := svc.Status()
	require.Len(t, st.LastEvents, 3)
	require.Equal(t, ScopeSite, st.LastEvents[2].Scope)
}

func TestIPConflict(t *testing.T) {
	ts := site(3, "203.0.113.30")
	clash := with(ts, func(i int, t *Target) {
		if i < 2 {
			t.LocalIP = "192.168.0.2"
		}
	})
	tr := NewTracker(nil)
	now := time.Now()
	evs := tr.Observe(now, time.Minute, clash)
	require.Len(t, evs, 1)
	ev := evs[0]
	require.Equal(t, KindConflict, ev.Kind)
	require.Equal(t, "[skywire] IP conflict: 2 visors at site 203.0.113.30 share LAN IP 192.168.0.2", Subject(ev))
	body := (&Sender{}).Body(ev)
	require.Contains(t, body, clash[0].PK.String())
	require.Contains(t, body, clash[1].PK.String())
	require.NotContains(t, body, clash[2].PK.String())

	// Flapping: one board offline but seen a minute ago still counts.
	flap := with(clash, func(i int, t *Target) {
		if i == 1 {
			t.Online, t.LastSeen = false, now
		}
	})
	require.Empty(t, tr.Observe(now.Add(time.Minute), time.Hour, flap), "once per conflict")

	// A board long gone does not hold the conflict open.
	gone := with(flap, func(i int, t *Target) {
		if i == 1 {
			t.LastSeen = now.Add(-time.Hour)
		}
	})
	require.Empty(t, tr.Observe(now.Add(2*time.Minute), time.Hour, gone))
	require.Len(t, tr.Observe(now.Add(3*time.Minute), time.Hour, clash), 1, "a cleared conflict can alert again")

	// The same LAN IP at two different sites is no conflict.
	other := site(1, "203.0.113.31")
	other[0].LocalIP = "192.168.0.2"
	tr = NewTracker(nil)
	require.Empty(t, tr.Observe(now, time.Minute, append([]Target{clash[0]}, other...)))
}

func TestRootReadOnly(t *testing.T) {
	rw := `sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
/dev/mmcblk0p1 / ext4 rw,noatime,errors=remount-ro,commit=600 0 0
tmpfs /tmp tmpfs rw,nosuid 0 0
`
	ro := `rootfs / rootfs rw 0 0
/dev/mmcblk0p1 / ext4 ro,noatime,errors=remount-ro,commit=600 0 0
/dev/mmcblk0p2 /data ext4 rw 0 0
`
	other := `/dev/sda1 / ext4 rw,relatime 0 0
/dev/sdb1 /mnt ext4 ro,relatime 0 0
`
	require.False(t, RootReadOnly(strings.NewReader(rw)))
	require.True(t, RootReadOnly(strings.NewReader(ro)))
	require.False(t, RootReadOnly(strings.NewReader(other)), "only / counts")
	require.False(t, RootReadOnly(strings.NewReader("")))
}

func TestStorageErrors(t *testing.T) {
	lines := []string{
		"6,100,1000000,-;Booting Linux on physical CPU 0x0",
		"3,200,2000000,-;mmc1: error -110 whilst initialising SDIO card", //nolint:misspell // kernel text
		"3,300,3600000000,-;mmcblk0: error -110 transferring data, sector 123456, nr 8, cmd response 0x900, card status 0xb00",
		"3,301,3600100000,-;I/O error, dev mmcblk0, sector 123456 op 0x1:(WRITE) flags 0x800 phys_seg 1 prio class 0",
		"3,302,3600200000,-;blk_update_request: I/O error, dev mmcblk0, sector 2048 op 0x0:(READ)",
		"3,303,3600300000,-;EXT4-fs error (device mmcblk0p2): ext4_find_entry:1455: inode #2: comm systemd: reading directory lblock 0",
		" SUBSYSTEM=block",
		"2,304,3600400000,-;EXT4-fs (mmcblk0p2): Remounting filesystem read-only",
		"3,305,3600500000,-;mmc0: Timeout waiting for hardware interrupt.",
		"3,306,3600600000,-;JBD2: Error -5 detected when updating journal superblock for mmcblk0p2-8.",
		"6,307,3600700000,-;eth0: Link is Up - 1Gbps/Full",
		"garbage",
	}
	n, last := StorageErrors(lines, 0)
	require.Equal(t, 7, n, "the SDIO init line is not a storage error")
	require.Equal(t, "JBD2: Error -5 detected when updating journal superblock for mmcblk0p2-8.", last)
	n, _ = StorageErrors(lines, 3600350*time.Millisecond)
	require.Equal(t, 3, n, "older records fall outside the window")
	n, last = StorageErrors(lines[:2], 0)
	require.Zero(t, n)
	require.Empty(t, last)
}
