package logrusbridge

import (
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

type capture struct{ got []*logging.Entry }

func (c *capture) Levels() []logging.Level     { return logging.AllLevels }
func (c *capture) Fire(e *logging.Entry) error { c.got = append(c.got, e); return nil }

func TestForwards(t *testing.T) {
	m := logging.Discard()
	m.SetLevel(logging.InfoLevel)
	c := &capture{}
	m.AddHook(c)
	lr := New(m.WithField("_module", "skydex"))

	lr.Debug("hidden")
	lr.WithField("k", 1).Warnf("x=%d", 2)

	if len(c.got) != 1 {
		t.Fatalf("got %d entries, want 1", len(c.got))
	}
	e := c.got[0]
	if e.Level != logging.WarnLevel || e.Message != "x=2" || e.Data["k"] != 1 || e.Data["_module"] != "skydex" {
		t.Fatalf("unexpected entry %+v", e)
	}
}
