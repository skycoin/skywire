package registry

import (
	"bytes"
	"testing"
)

//
// helpers
//

func shouldPanic(t *testing.T) {
	if recover() == nil {
		t.Error("missing panic")
	}
}

func shouldNotPanic(t *testing.T) {
	if err := recover(); err != nil {
		t.Error("unexpected panic:", err)
	}
}

//
// tests
//

func TestNewRegistry(t *testing.T) {

	var (
		reg = NewRegistry(func(reg *Reg) {})
		ref RegistryRef
	)

	if ref = reg.Reference(); ref == (RegistryRef{}) {
		t.Error("empty RegistryRef")
	}

	// keep going on

	type A struct {
		Name string
	}

	reg = NewRegistry(func(reg *Reg) {
		reg.Register("test.A", A{})
	})

	if reg.Reference() == ref {
		t.Error("same RegistryRef")
	}

	//  A L L O W
	//     ||
	//     \/

	// // double registering
	//
	// t.Run("twice", func(t *testing.T) {
	// 	defer shouldPanic(t)
	//
	// 	reg = NewRegistry(func(reg *Reg) {
	// 		reg.Register("test.A", A{})
	// 		reg.Register("test.P", &A{})
	// 	})
	//
	// })

	// unregistered field

	t.Run("unregistered field", func(t *testing.T) {

		type B struct {
			Name string
			Age  uint32
		}

		type C struct {
			A A
			B B
		}

		defer shouldPanic(t)

		reg = NewRegistry(func(reg *Reg) {
			reg.Register("test.A", A{})
			reg.Register("test.C", C{})
		})
	})

	// missing Ref tag

	t.Run("missing Ref tag", func(t *testing.T) {

		type D struct {
			User Ref
		}

		defer shouldPanic(t)

		reg = NewRegistry(func(reg *Reg) {
			reg.Register("test.D", D{})
		})
	})

	// missing Refs tag

	t.Run("missing Refs tag", func(t *testing.T) {

		type E struct {
			User Refs
		}

		defer shouldPanic(t)

		reg = NewRegistry(func(reg *Reg) {
			reg.Register("test.E", E{})
		})
	})

	// missing Ref schema

	t.Run("missing Ref schema", func(t *testing.T) {

		type F struct {
			User Ref `skyobject:"schema=User"`
		}

		defer shouldPanic(t)

		reg = NewRegistry(func(reg *Reg) {
			reg.Register("test.F", F{})
		})
	})

	// missing Refs schema

	t.Run("missing Refs schema", func(t *testing.T) {

		type G struct {
			User Refs `skyobject:"schema=User"`
		}

		defer shouldPanic(t)

		reg = NewRegistry(func(reg *Reg) {
			reg.Register("test.G", G{})
		})
	})

	// valid

	t.Run("valid", func(t *testing.T) {

		type H struct {
			Name string
		}

		type I struct {
			H Ref  `skyobject:"schema=test.H"`
			S Refs `skyobject:"schema=test.H"`
			D Dynamic
		}

		reg = NewRegistry(func(reg *Reg) {
			reg.Register("test.I", I{})
			reg.Register("test.H", H{})
		})

		ref = reg.Reference()

		// pointers

		reg = NewRegistry(func(reg *Reg) {
			reg.Register("test.I", &I{})
			reg.Register("test.H", &H{})
		})

		if reg.Reference() != ref {
			t.Error("wrong ref")
		}

	})

}

func TestDecodeRegistry(t *testing.T) {

	var reg = NewRegistry(func(r *Reg) {
		r.Register("test.User", TestUser{})
		r.Register("test.Group", TestGroup{})
	})

	var d *Registry
	var err error

	if d, err = DecodeRegistry(reg.Encode()); err != nil {
		t.Fatal("can't decode Registry:", err)
	}

	if bytes.Compare(d.Encode(), reg.Encode()) != 0 {
		t.Error("different")
	}

	for _, name := range []string{
		"test.User",
		"test.Group",
	} {
		if _, err = d.SchemaByName(name); err != nil {
			t.Error("missing schema")
		}
	}

	//
	// from intro/discovery/echange failure:
	//

	t.Run("intro/discovery", func(t *testing.T) {

		// A User's information
		type User struct {
			Name string // user's name
			Age  uint32 // user's age
		}

		// A Feed of the User
		type Feed struct {
			Head  string // head of the feed
			Info  string // brief information about the feed
			Posts Refs   `skyobject:"schema=discovery.Post"` // posts
		}

		// A Post represnts post in the Feed
		type Post struct {
			Head string // head of the Post
			Body string // content of the Post
		}

		// Registry that contains User, Feed and Post types
		var reg = NewRegistry(func(r *Reg) {
			// the name can be any, e.g. the "discovery.User" can be
			// "usr" or any other; feel free to choose
			r.Register("discovery.User", User{})
			r.Register("discovery.Feed", Feed{})
			r.Register("discovery.Post", Post{})
		})

		var rr, err = DecodeRegistry(reg.Encode())

		if err != nil {
			t.Fatal(err)
		}

		_ = rr // TODO

	})

}

func TestRegistry_identity(t *testing.T) {

	var r1 = NewRegistry(func(r *Reg) {
		r.Register("test.User", TestUser{})
		r.Register("test.Group", TestGroup{})
	})

	var r2 = NewRegistry(func(r *Reg) {
		r.Register("test.Group", TestGroup{})
		r.Register("test.User", TestUser{})
	})

	if r1.Reference() != r2.Reference() {
		t.Error("not equal")
	}

}

func TestRegistry_SchemaByName(t *testing.T) {

	var reg = NewRegistry(func(r *Reg) {
		r.Register("test.User", TestUser{})
		r.Register("test.Group", TestGroup{})
	})

	var u, g Schema
	var err error

	if u, err = reg.SchemaByName("test.User"); err != nil {
		t.Error(err)
	} else if u.Name() != "test.User" {
		t.Error("name is ", u.Name())
	}

	if _, err = reg.SchemaByName("nothing"); err == nil {
		t.Error("missing error")
	}

	if g, err = reg.SchemaByName("test.Group"); err != nil {
		t.Error(err)
	} else if g.Name() != "test.Group" {
		t.Error("name is ", g.Name())
	} else if len(g.Fields()) != 4 {
		t.Error("wrong fields count", len(g.Fields()))
	} else if uf := g.Fields()[2].Schema().Elem(); uf.Name() != "test.User" {
		t.Error("wrong field schema:", g.Fields()[2].Schema())
	} else if uf != u {
		t.Error("not the same") // must be the same instance of Schema
	}
}

func TestRegistry_SchemaByReference(t *testing.T) {

	var reg = testRegistry()

	var sn, sr Schema
	var err error

	for _, tt := range testTypes() {
		t.Log(tt.Name)

		if sn, err = reg.SchemaByName(tt.Name); err != nil {
			t.Error(err)
			continue
		}

		if sr, err = reg.SchemaByReference(sn.Reference()); err != nil {
			t.Error(err)
			continue
		}

		if sr != sn {
			t.Error("unnecessary memory overhead")
		}

	}

}

func TestRegistry_Encode(t *testing.T) {
	//
}

func TestRegistry_Reference(t *testing.T) {
	//
}

func TestRegistry_slice(t *testing.T) {

	type Post struct {
		Name    string
		Content string
	}

	type Vote struct {
		Up   uint32
		Down uint32
	}

	type PostVotePage struct {
		Post  Ref  `skyobject:"schema=Post"`
		Votes Refs `skyobject:"schema=Vote"`
	}

	type PostVoteContainer struct {
		Posts []PostVotePage
	}

	r := NewRegistry(func(r *Reg) {
		r.Register("Post", Post{})
		r.Register("Vote", Vote{})
		r.Register("PostVotePage", PostVotePage{})
		r.Register("PostVoteContainer", PostVoteContainer{})
	})

	defer shouldNotPanic(t)

	if dr, err := DecodeRegistry(r.Encode()); err != nil {
		t.Fatal(err)
	} else if dr.Reference() != r.Reference() {
		t.Error("different decoder reference")
	}

}

func TestRegistry_userProvidedName(t *testing.T) {

	type Info struct {
		About string
	}

	type Brief struct {
		Note string
	}

	type Any struct {
		Info
		Brief Brief
	}

	var reg = NewRegistry(func(r *Reg) {
		r.Register("test.Info", Info{})
		r.Register("test.Brief", Brief{})
		r.Register("test.Any", Any{})
	})

	for _, name := range []string{
		"test.Info",
		"test.Brief",
		"test.Any",
	} {
		if s, err := reg.SchemaByName(name); err != nil {
			t.Error(err)
		} else if s.Name() != name {
			t.Error("differen names:", name, s.Name())
		}
	}

}
