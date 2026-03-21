package registry

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

func TestDynamic_IsValid(t *testing.T) {
	// IsValid() bool

	var dr Dynamic

	if false == dr.IsValid() {
		t.Error("blank Dynamic is invlaid")
	}

	dr.Schema = SchemaRef{1, 2, 3}

	if false == dr.IsValid() {
		t.Error("non-blank Schema, but blank Hash turns Dynamic invalid")
	}

	dr.Schema = SchemaRef{}
	dr.Hash = cipher.SHA256{1, 2, 3}

	if true == dr.IsValid() {
		t.Error("IsValid() returns true for invalid Dynamic reference")
	}
}

func TestDynamic_Short(t *testing.T) {
	// Short() string

	// TODO (kostyarin): lowest priority

}

func TestDynamic_String(t *testing.T) {
	// String() string

	// TODO (kostyarin): lowest priority

}

func TestDynamic_IsBlank(t *testing.T) {
	// IsBlank() bool

	var dr Dynamic

	if true != dr.IsBlank() {
		t.Error("blank is not blank")
	}

	dr.Schema = SchemaRef{1, 2, 3}

	if true == dr.IsBlank() {
		t.Error("not blank is blank")
	}

	dr.Schema = SchemaRef{}
	dr.Hash = cipher.SHA256{1, 2, 3}

	if true == dr.IsBlank() {
		t.Error("not blank is blank")
	}

	dr.Schema = SchemaRef{1, 2, 3}

	if true == dr.IsBlank() {
		t.Error("not blank is blank")
	}

}

func TestDynamic_Value(t *testing.T) {
	// Value(pack Pack, obj interface{}) (err error)

	var pack = getTestPack() // dummy pack

	var usr = TestUser{
		Name:   "Alice",
		Age:    15,
		Hidden: []byte("secret"),
	}

	var (
		data = encoder.Serialize(&usr)
		hash = cipher.SumSHA256(data)

		s   Schema
		err error
	)

	if s, err = pack.Registry().SchemaByName("test.User"); err != nil {
		t.Error(err)
		return
	}

	var (
		sr  = s.Reference()
		dec TestUser
		dr  Dynamic
	)

	if err = dr.Value(pack, &dec); err == nil {
		t.Error("misisng error") // blank
	} else if err != ErrReferenceRepresentsNil {
		t.Error("wrong error:", err)
	}

	dr.Hash = cipher.SHA256{1, 2, 3}

	if err = dr.Value(pack, &dec); err == nil {
		t.Error("missing error")
	} else if err != ErrInvalidDynamicReference {
		t.Error("wrong error:", err)
	}

	dr.Schema = sr
	dr.Hash = hash

	if err = dr.Value(pack, &dec); err == nil {
		t.Error("missing error")
	}

	if err = pack.Set(hash, data); err != nil {
		t.Fatal(err)
	}

	if err = dr.Value(pack, &dec); err != nil {
		t.Error(err)
	}

}

func TestDynamic_SetValue(t *testing.T) {
	// SetValue(pack Pack, obj interface{}) (err error)

	var (
		pack = getTestPack()

		dr   Dynamic
		hash cipher.SHA256
		err  error

		usr = TestUser{"Alice", 15, nil}
	)

	hash = cipher.SumSHA256(encoder.Serialize(&usr))

	if err = dr.SetValue(pack, &usr); err != nil {
		t.Fatal(err)
	}

	if dr.Schema != (SchemaRef{}) {
		t.Error("schema reference has been set")
	}

	if dr.Hash != hash {
		t.Error("wrong or missing hash")
	}

	// error bubbling

	var ep = &errorPack{pack}

	if err = dr.SetValue(ep, &usr); err == nil {
		t.Error("missing error")
	} else if err != errTest {
		t.Error("wrong error")
	}

}

func TestDynamic_Clear(t *testing.T) {
	// Clear()

	var dr Dynamic

	dr.Schema = SchemaRef{1, 2, 3}
	dr.Hash = cipher.SHA256{1, 2, 3}

	dr.Clear()

	if dr.Hash != (cipher.SHA256{}) || dr.Schema != (SchemaRef{}) {
		t.Error("not clear")
	}

}
