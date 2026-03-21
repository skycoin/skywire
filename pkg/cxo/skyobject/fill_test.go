package skyobject

import (
	"fmt"
	"sync"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A User's information
type User struct {
	Name string // user's name
	Age  uint32 // user's age
}

// A Feed of the User
type Feed struct {
	Head  string        // head of the feed
	Info  string        // brief information about the feed
	Posts registry.Refs `skyobject:"schema=test.Post"` // posts
}

// A Post represnts post in the Feed
type Post struct {
	Head string // head of the Post
	Body string // content of the Post
}

// Registry that contains User, Feed and Post types
var testRegistry = registry.NewRegistry(func(r *registry.Reg) {
	r.Register("test.User", User{})
	r.Register("test.Feed", Feed{})
	r.Register("test.Post", Post{})
})

func getTestConfig() (c *Config) {
	c = NewConfig()
	c.InMemoryDB = true
	return
}

func getTestContainer() (c *Container) {
	var err error
	if c, err = NewContainer(getTestConfig()); err != nil {
		panic(err)
	}
	return
}

func assertNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertTrue(t *testing.T, v bool, msg string) {
	t.Helper()
	if v == false {
		t.Fatal(msg)
	}
}

func Test_fillinng(t *testing.T) {

	var (
		sc, rc = getTestContainer(), getTestContainer()
		pk, sk = cipher.GenerateKeyPair() //nolint:errcheck,gosec
	)

	assertNil(t, sc.AddFeed(pk))
	assertNil(t, rc.AddFeed(pk))

	var up, err = sc.Unpack(sk, testRegistry)
	assertNil(t, err)

	var r = new(registry.Root)

	r.Pub = pk
	r.Nonce = 9021

	assertNil(t, sc.Save(up, r))
	testFillRoot(t, sc, rc, r)
	testFillDBs(t, sc, rc)

	var usr = User{
		Name: "Alice",
		Age:  19,
	}

	r.Refs = []registry.Dynamic{
		createDynamic(up, testRegistry, "test.User", &usr),
	}

	assertNil(t, sc.Save(up, r))
	testFillRoot(t, sc, rc, r)
	testFillDBs(t, sc, rc)

	var feed = Feed{
		Head: "Alices' feed",
		Info: "an average feed",
	}

	r.Refs = append(
		r.Refs,
		createDynamic(up, testRegistry, "test.Feed", &feed),
	)

	assertNil(t, sc.Save(up, r))
	testFillRoot(t, sc, rc, r)
	testFillDBs(t, sc, rc)

	for i := 0; i < 100; i++ {

		t.Log(i)

		assertNil(t, feed.Posts.AppendValues(up, Post{
			Head: fmt.Sprintf("Head #%d", i),
			Body: fmt.Sprintf("Body #%d", i),
		}))

		assertNil(t, r.Refs[1].SetValue(up, &feed))

		assertNil(t, sc.Save(up, r))
		testFillRoot(t, sc, rc, r)
		testFillDBs(t, sc, rc)
	}

}

func testFillRoot(t *testing.T, sc, rc *Container, r *registry.Root) {
	//t.Helper()

	var hs []cipher.SHA256 // hashes in order

	assertNil(t, sc.Walk(r, func(key cipher.SHA256, _ int) (bool, error) {
		hs = append(hs, key)
		return true, nil
	}))

	var (
		rq = make(chan cipher.SHA256, 10)
		f  = rc.Fill(r, rq, 10)
	)

	var wg sync.WaitGroup

	// the rq channel
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			var key, ok = <-rq

			if ok == false {
				return
			}

			var val, _, err = sc.Get(key, 0)
			assertNil(t, err)

			_, err = rc.SetWanted(key, val)
			assertNil(t, err)
		}

	}()

	assertNil(t, f.Run())

	close(rq)
	wg.Wait()

	var i int

	assertNil(t, rc.Walk(r, func(key cipher.SHA256, _ int) (bool, error) {
		assertTrue(t, key == hs[i], "wrong hash")
		i++
		return true, nil
	}))

	assertTrue(t, i == len(hs), "wrong number of hashes")

}

func createDynamic(
	pack registry.Pack,
	reg *registry.Registry,
	name string,
	obj interface{},
) (
	dr registry.Dynamic,
) {

	var sch, err = reg.SchemaByName(name)
	if err != nil {
		panic(err)
	}

	err = dr.SetValue(pack, obj)
	if err != nil {
		panic(err)
	}

	dr.Schema = sch.Reference()
	return
}

func testFillDBs(t *testing.T, sc, rc *Container) {
	t.Helper()

	var (
		ks  []cipher.SHA256
		err error
	)

	assertNil(t, sc.db.CXDS().Iterate(
		func(key cipher.SHA256, _ uint32, val []byte) (_ error) {
			ks = append(ks, key)
			return
		}))

	if len(ks) == 0 {
		t.Fatal("no objects in DB")
	}

	for _, key := range ks {

		var src, rrc int
		_, src, err = sc.Get(key, 0)
		assertNil(t, err)

		_, rrc, err = rc.Get(key, 0)
		assertNil(t, err)

		if src != rrc {
			t.Error("wrong rc", rrc, src, key.Hex()[:7])
		}
	}

	if t.Failed() == true {
		t.FailNow()
	}
}
