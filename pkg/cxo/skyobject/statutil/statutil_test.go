package statutil

import (
	"testing"
)

func TestVolume_String(t *testing.T) {
	// String() (s string)

	type vs struct {
		vol Volume
		s   string
	}

	for i, vs := range []vs{
		{0, "0B"},
		{1023, "1023B"},
		{1024, "1kB"},
		{1030, "1.01kB"},
		{1224, "1.2kB"},
		{1424, "1.39kB"},
		{10241024, "9.77MB"},
	} {
		if vs.vol.String() != vs.s {
			t.Errorf("wrong %d: %d - %s", i, vs.vol, vs.vol.String())
		} else {
			t.Logf("      %d: %d - %s", i, vs.vol, vs.vol.String())
		}
	}
}
