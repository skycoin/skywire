package dmsg

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// countingWriter records publish attempts so a test can prove none were made.
type countingWriter struct {
	disc.APIClient
	puts  int
	posts int
}

func (w *countingWriter) PutEntry(_ context.Context, _ cipher.SecKey, _ *disc.Entry) error {
	w.puts++
	return nil
}

func (w *countingWriter) PostEntry(_ context.Context, _ *disc.Entry) error {
	w.posts++
	return nil
}

// TestNoRegisterSuppressesPublish is the point of the flag: a client told not
// to register must make no publish attempt at all.
//
// The behavior it replaces was expressible only by passing a discovery whose
// writes silently succeed without publishing (direct.NewClient's PutEntry
// returns nil), which left the client believing it had registered — the reason
// this needed to become explicit rather than a property of the injected
// discovery.
func TestNoRegisterSuppressesPublish(t *testing.T) {
	w := &countingWriter{}
	c := &EntityCommon{}
	pk, sk := cipher.GenerateKeyPair()
	c.init(pk, sk, w, logging.MustGetLogger("noregister_test"), 0)
	c.noRegister = true

	done := make(chan struct{})
	if err := c.updateClientEntry(context.Background(), done, ""); err != nil {
		t.Fatalf("updateClientEntry returned %v, want nil", err)
	}
	if w.puts != 0 || w.posts != 0 {
		t.Errorf("a non-registering client published: %d PutEntry, %d PostEntry", w.puts, w.posts)
	}
}

// TestRegisterByDefault pins that the flag is opt-in — the zero value must keep
// publishing, or every existing caller would silently stop being resolvable.
func TestRegisterByDefault(t *testing.T) {
	c := &EntityCommon{}
	if c.noRegister {
		t.Fatal("registration must be enabled by default")
	}

	conf := DefaultConfig()
	if conf.NoRegister {
		t.Error("DefaultConfig must register")
	}
}
