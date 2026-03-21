package tests

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

func testKeyValue(s string) (key cipher.SHA256, val []byte) {
	val = []byte(s)
	key = cipher.SumSHA256(val)
	return
}

func testIncs() []int {
	return []int{-1, 0, 1}
}

func shouldNotExistInCXDS(t *testing.T, ds data.CXDS, key cipher.SHA256) {
	t.Helper()
	if _, rc, err := ds.Get(key, 0); err == nil {
		t.Error("missing error")
	} else if err != data.ErrNotFound {
		t.Error("unexpected error:", err)
	} else if rc != 0 {
		t.Error("wrong rc:", rc)
	}
}

func shouldExistInCXDS(
	t *testing.T,
	ds data.CXDS,
	key cipher.SHA256,
	rc uint32,
	val []byte,
) {

	t.Helper()

	if gval, grc, err := ds.Get(key, 0); err != nil {
		t.Error(err)
	} else if grc != rc {
		t.Errorf("wrong rc %d, want %d", grc, rc)
	} else if want, got := string(val), string(gval); want != got {
		t.Errorf("wrong value: want %q, got %q", want, got)
	}
}

func shouldPanic(t *testing.T) {
	t.Helper()

	if recover() == nil {
		t.Error("missing panic")
	}
}

// CXDSGet tests Get method of CXDS
func CXDSGet(t *testing.T, ds data.CXDS) {

	key, value := testKeyValue("something")

	t.Run("not exist", func(t *testing.T) {

		for _, inc := range testIncs() {
			if val, rc, err := ds.Get(key, inc); err == nil {
				t.Error("missing error")
			} else if err != data.ErrNotFound {
				t.Error("unexpected error:", err)
			} else if rc != 0 {
				t.Error("wrong rc", rc)
			} else if val != nil {
				t.Error("not nil")
			}
		}
	})

	if _, err := ds.Set(key, value, 1); err != nil {
		t.Error(err)
		return
	}

	t.Run("existing", func(t *testing.T) {

		t.Run("inc 0", func(t *testing.T) {
			if val, rc, err := ds.Get(key, 0); err != nil {
				t.Error(err)
			} else if rc != 1 {
				t.Error("wrong rc", rc)
			} else if want, got := string(value), string(val); want != got {
				t.Errorf("wrong value: want %q, got %q", want, got)
			}
		})

		t.Run("inc 1", func(t *testing.T) {
			if val, rc, err := ds.Get(key, 1); err != nil {
				t.Error(err)
			} else if rc != 2 {
				t.Error("wrong rc", rc)
			} else if want, got := string(value), string(val); want != got {
				t.Errorf("wrong value: want %q, got %q", want, got)
			}
		})

		t.Run("dec 1", func(t *testing.T) {
			if val, rc, err := ds.Get(key, -1); err != nil {
				t.Error(err)
			} else if rc != 1 {
				t.Error("wrong rc", rc)
			} else if want, got := string(value), string(val); want != got {
				t.Errorf("wrong value: want %q, got %q", want, got)
			}
		})

		t.Run("remove", func(t *testing.T) {
			for i := 0; i < 2; i++ {
				if val, rc, err := ds.Get(key, -1); err != nil {
					t.Error(err)
				} else if rc != 0 {
					t.Error("wrong rc", rc)
				} else if want, got := string(value), string(val); want != got {
					t.Errorf("wrong value: want %q, got %q", want, got)
				}
				shouldExistInCXDS(t, ds, key, 0, value)
			}
		})

	})

}

// CXDSSet tests Set method of CXDS
func CXDSSet(t *testing.T, ds data.CXDS) {

	key, value := testKeyValue("something")

	t.Run("zero", func(t *testing.T) {
		defer shouldPanic(t)
		ds.Set(key, value, 0)
	})

	t.Run("negaive", func(t *testing.T) {
		defer shouldPanic(t)
		ds.Set(key, value, -1)
	})

	t.Run("new", func(t *testing.T) {
		if rc, err := ds.Set(key, value, 1); err != nil {
			t.Error(err)
		} else if rc != 1 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 1, value)
	})

	t.Run("twice", func(t *testing.T) {
		if rc, err := ds.Set(key, value, 1); err != nil {
			t.Error(err)
		} else if rc != 2 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 2, value)
	})

	t.Run("three times", func(t *testing.T) {
		if rc, err := ds.Set(key, value, 2); err != nil {
			t.Error(err)
		} else if rc != 4 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 4, value)
	})

}

// CXDSInc tests Inc method of CXDS
func CXDSInc(t *testing.T, ds data.CXDS) {

	var key, value = testKeyValue("something")

	t.Run("not exist", func(t *testing.T) {
		for _, inc := range testIncs() {
			if rc, err := ds.Inc(key, inc); err == nil {
				t.Error("missing error")
			} else if err != data.ErrNotFound {
				t.Error("unexpected error:", err)
			} else if rc != 0 {
				t.Error("wrong rc", rc)
			}
			shouldNotExistInCXDS(t, ds, key)
		}
	})

	if _, err := ds.Set(key, value, 1); err != nil {
		t.Error(err)
		return
	}

	t.Run("zero", func(t *testing.T) {
		if rc, err := ds.Inc(key, 0); err != nil {
			t.Error(err)
		} else if rc != 1 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 1, value)
	})

	t.Run("inc", func(t *testing.T) {
		if rc, err := ds.Inc(key, 1); err != nil {
			t.Error(err)
		} else if rc != 2 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 2, value)
	})

	t.Run("dec", func(t *testing.T) {
		if rc, err := ds.Inc(key, -1); err != nil {
			t.Error(err)
		} else if rc != 1 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 1, value)
	})

	t.Run("inc 2", func(t *testing.T) {
		if rc, err := ds.Inc(key, 2); err != nil {
			t.Error(err)
		} else if rc != 3 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 3, value)
	})

	t.Run("dec 2", func(t *testing.T) {
		if rc, err := ds.Inc(key, -2); err != nil {
			t.Error(err)
		} else if rc != 1 {
			t.Error("wrong rc", rc)
		}
		shouldExistInCXDS(t, ds, key, 1, value)
	})

	t.Run("remove", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			if rc, err := ds.Inc(key, -1); err != nil {
				t.Error(err)
			} else if rc != 0 {
				t.Error("wrong rc", rc)
			}
			shouldExistInCXDS(t, ds, key, 0, value)
		}
	})

}

// CXDSClose tests Close method of CXDS
func CXDSClose(t *testing.T, ds data.CXDS) {
	if err := ds.Close(); err != nil {
		t.Error(err)
	}
	if err := ds.Close(); err != nil {
		t.Error(err)
	}
}
