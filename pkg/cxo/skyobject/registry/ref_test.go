package registry

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

func TestRef_IsBlank(t *testing.T) {
	// IsBlank() bool

	var ref Ref

	if true != ref.IsBlank() {
		t.Error("blank is not blank")
	}

	ref.Hash = cipher.SHA256{1, 2, 3}

	if true == ref.IsBlank() {
		t.Error("not blank is blank")
	}

}

func TestRef_Short(t *testing.T) {
	// Short() string

	// TODO (kostyarin): lowest priority

}

func TestRef_String(t *testing.T) {
	// String() string

	// TODO (kostyarin): lowest priority

}

func TestRef_Value(t *testing.T) {
	// Value(pack Pack, obj interface{}) (err error)

	var (
		pack = getTestPack() // dummy pack

		usr = TestUser{
			Name:   "Alice",
			Age:    15,
			Hidden: []byte("secret"),
		}

		dec TestUser

		ref Ref

		data = encoder.Serialize(&usr)
		hash = cipher.SumSHA256(data)

		err error
	)

	if err = ref.Value(pack, &dec); err == nil {
		t.Error("misisng error") // blank
	} else if err != ErrReferenceRepresentsNil {
		t.Error("wrong error:", err)
	}

	ref.Hash = cipher.SHA256{1, 2, 3}

	if err = ref.Value(pack, &dec); err == nil {
		t.Error("missing error")
	}

	ref.Hash = hash

	if err = ref.Value(pack, &dec); err == nil {
		t.Error("missing error")
	}

	if err = pack.Set(hash, data); err != nil {
		t.Fatal(err)
	}

	if err = ref.Value(pack, &dec); err != nil {
		t.Error(err)
	}

}

func TestRef_SetValue(t *testing.T) {
	// SetValue(pack Pack, obj interface{}) (err error)

	var (
		pack = getTestPack()

		ref  Ref
		hash cipher.SHA256
		err  error

		usr = TestUser{"Alice", 15, nil}
	)

	hash = cipher.SumSHA256(encoder.Serialize(&usr))

	if err = ref.SetValue(pack, &usr); err != nil {
		t.Fatal(err)
	}

	if ref.Hash != hash {
		t.Error("wrong or missing hash")
	}

	// error bubbling

	var ep = &errorPack{pack}

	if err = ref.SetValue(ep, &usr); err == nil {
		t.Error("missing error")
	} else if err != errTest {
		t.Error("wrong error")
	}

}

func TestRef_Clear(t *testing.T) {
	// Clear()

	var ref Ref

	ref.Hash = cipher.SHA256{1, 2, 3}
	ref.Clear()

	if ref.Hash != (cipher.SHA256{}) {
		t.Error("not clear")
	}

}
