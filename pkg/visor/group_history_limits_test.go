//go:build !js

package visor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestGroupHistoryMessages(t *testing.T) {
	for _, c := range []struct {
		sc   *visorconfig.Skychat
		want int
	}{
		{nil, defaultGroupHistoryMessages},
		{&visorconfig.Skychat{}, defaultGroupHistoryMessages},
		{&visorconfig.Skychat{GroupHistoryMessages: 250000}, 250000},
		{&visorconfig.Skychat{GroupHistoryMessages: -1}, 0},
	} {
		if got := groupHistoryMessages(c.sc); got != c.want {
			t.Errorf("groupHistoryMessages(%+v) = %d, want %d", c.sc, got, c.want)
		}
	}
}

// The group store keeps taking messages once its file is past the 10 MB that
// caps the 1:1 store. Each group is bounded by its own cap instead.
func TestGroupHistoryLimits_NoFileCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "group-history.db")
	limits := groupHistoryLimits(&visorconfig.Skychat{GroupHistoryMessages: 2500})
	st, err := history.NewBoltStore(path, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck
	text := strings.Repeat("x", 3500)
	ts := time.Now().UTC()
	write := func(group string, n int) {
		for i := range n {
			if err := st.AppendGroup(history.GroupMessage{GroupID: group, SenderPK: "pk", Text: text, Timestamp: ts.Add(time.Duration(i))}); err != nil {
				t.Fatalf("append %s #%d: %v", group, i, err)
			}
		}
	}
	write("a", 2600)
	write("b", 800)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < 10*1024*1024 {
		t.Fatalf("store is only %d bytes; the test must pass 10 MB", fi.Size())
	}
	write("b", 10)
	got, err := st.ListByGroup("a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2500 {
		t.Fatalf("group a holds %d messages, want its cap of 2500", len(got))
	}
}
