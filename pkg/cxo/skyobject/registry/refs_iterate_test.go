package registry

import (
	"fmt"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
)

func testRefsAscend(t *testing.T, r *Refs, pack Pack, users []cipher.SHA256) {

	var err error
	var called int

	err = r.Ascend(pack, func(i int, hash cipher.SHA256) (err error) {

		if i < 0 {
			t.Error("got negative index", i)
		} else if i >= len(users) {
			t.Error("got index out of rage", i)
		} else if users[i] != hash {
			t.Errorf("wrong hash, want %s, got %s, or wrong index %d",
				users[i].Hex()[:7],
				hash.Hex()[:7],
				i)
		}

		called++

		return // continue
	})

	if err != nil {
		t.Error(err)
	} else if called != len(users) {
		t.Errorf("wrong times called %d, but want %d", called, len(users))
	}

}

func TestRefs_Ascend(t *testing.T) {
	// Ascend(pack Pack, ascendFunc IterateFunc) (err error)

	var (
		pack = getTestPack()

		refs Refs
		err  error

		users []cipher.SHA256

		clear = func(t *testing.T, r *Refs, degree Degree) {
			pack.ClearFlags(^0)          // clear flags of pack
			refs.Clear()                 // clear the Refs making it Refs{}
			if degree != pack.Degree() { // if it's not default
				if err = refs.SetDegree(pack, degree); err != nil { // change it
					t.Fatal(err)
				}
			}
		}
	)

	for _, degree := range []Degree{
		pack.Degree(),     // default
		pack.Degree() + 1, // changed
	} {

		t.Run(fmt.Sprintf("blank (degree %d)", degree), func(t *testing.T) {

			clear(t, &refs, degree)

			var called int

			err = refs.Ascend(pack, func(int, cipher.SHA256) (_ error) {
				called++
				return
			})

			if err != nil {
				t.Error(err)
			} else if called > 0 {
				t.Errorf("called %d times, but should not be called at all",
					called)
			}

		})

		// fill
		//   (1) only leafs
		//   (2) leafs and branches
		//   (3) branches with branches with leafs

		for _, length := range []int{
			int(degree),            // only leafs
			int(degree) + 1,        // leafs and branches
			int(degree*degree) + 1, // branches with branches with leafs
		} {

			t.Logf("Refs with %d elements (degree %d)", length, degree)

			clear(t, &refs, degree)

			// generate users
			users = getHashList(
				testFillRefsWithUsers(t, &refs, pack, length),
			)

			if t.Failed() {
				t.FailNow()
			}

			logRefsTree(t, &refs, pack, false)

			t.Run(fmt.Sprintf("fresh %d:%d", length, degree),
				func(t *testing.T) {

					testRefsAscend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("break %d:%d", length, degree),
				func(t *testing.T) {

					var called int

					err = refs.Ascend(pack, func(int, cipher.SHA256) (_ error) {
						called++
						return ErrStopIteration
					})

					if err != nil {
						t.Error(err)
					} else if called > 1 {
						t.Error("called too many times", called)
					}

					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset() // reset the refs

					testRefsAscend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load entire %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset()              // reset the refs
					pack.AddFlags(EntireRefs) // load entire Refs

					testRefsAscend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("hash table index %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset()

					pack.ClearFlags(EntireRefs)
					pack.AddFlags(HashTableIndex)

					testRefsAscend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

		}

	}

	t.Run("blank hash", func(t *testing.T) {

		refs.Clear()
		pack.ClearFlags(^0) // all

		if err = refs.AppendHashes(pack, cipher.SHA256{}); err != nil {
			t.Fatal(err)
		}

		err = refs.Ascend(pack, func(i int, hash cipher.SHA256) (err error) {
			if i != 0 {
				t.Error("wrong index given")
			}
			if hash != (cipher.SHA256{}) {
				t.Error("wrong hash given")
			}
			return
		})

	})

	// -------------------------------------------------------------------------

	// TODO (kostyaring): extended testing of the Ascend
	// TODO (kostyaring): error bubbling test (and for desc, asc_from, etc)

}

func testRefsAscendFrom(
	t *testing.T, //          :
	r *Refs, //               :
	pack Pack, //             :
	users []cipher.SHA256, // :
) {

	var err error

	for k := 0; k < len(users); k++ {

		var called int

		err = r.AscendFrom(pack, k,
			func(i int, hash cipher.SHA256) (err error) {

				if i < k {
					t.Errorf("(%d) got small index %d", k, i)
				} else if i >= len(users) {
					t.Errorf("(%d) got index out of range %d", k, i)
				} else if users[i] != hash {
					t.Errorf(
						"(%d) wrong hash, want %s, got %s, or wrong index %d",
						k,
						users[i].Hex()[:7],
						hash.Hex()[:7],
						i)
				}

				called++

				return // continue
			})

		if err != nil {
			t.Error(err)
		} else if called != len(users)-k {
			t.Errorf("(%d) wrong times called %d, but want %d",
				k,
				called,
				len(users)-k)
		}

	}

}

func TestRefs_AscendFrom(t *testing.T) {
	// AscendFrom(pack Pack, from int, ascendFunc IterateFunc) (err error)

	var (
		pack = getTestPack()

		refs Refs
		err  error

		users []cipher.SHA256

		clear = func(t *testing.T, r *Refs, degree Degree) {
			pack.ClearFlags(^0)          // clear flags of pack
			refs.Clear()                 // clear the Refs making it Refs{}
			if degree != pack.Degree() { // if it's not default
				if err = refs.SetDegree(pack, degree); err != nil { // change it
					t.Fatal(err)
				}
			}
		}
	)

	for _, degree := range []Degree{
		pack.Degree(),     // default
		pack.Degree() + 1, // changed
	} {

		t.Run(fmt.Sprintf("blank (degree %d)", degree), func(t *testing.T) {

			clear(t, &refs, degree)

			var called int

			err = refs.AscendFrom(pack, 0, func(int, cipher.SHA256) (_ error) {
				called++
				return
			})

			if err != nil {
				t.Error(err)
			} else if called > 0 {
				t.Errorf("called %d times, but should not be called at all",
					called)
			}

		})

		// fill
		//   (1) only leafs
		//   (2) leafs and branches
		//   (3) branches with branches with leafs

		for _, length := range []int{
			int(degree),            // only leafs
			int(degree) + 1,        // leafs and branches
			int(degree*degree) + 1, // branches with branches with leafs
		} {

			t.Logf("Refs with %d elements (degree %d)", length, degree)

			clear(t, &refs, degree)

			// generate users
			users = getHashList(
				testFillRefsWithUsers(t, &refs, pack, length),
			)

			if t.Failed() {
				t.FailNow()
			}

			logRefsTree(t, &refs, pack, false)

			t.Run(fmt.Sprintf("fresh %d:%d", length, degree),
				func(t *testing.T) {

					testRefsAscendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("break %d:%d", length, degree),
				func(t *testing.T) {

					var called int

					err = refs.AscendFrom(pack, 0,
						func(int, cipher.SHA256) (_ error) {
							called++
							return ErrStopIteration
						})

					if err != nil {
						t.Error(err)
					} else if called > 1 {
						t.Error("called too many times", called)
					}

					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset() // reset the refs

					testRefsAscendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load entire %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset()              // reset the refs
					pack.AddFlags(EntireRefs) // load entire Refs

					testRefsAscendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("hash table index %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset()

					pack.ClearFlags(EntireRefs)
					pack.AddFlags(HashTableIndex)

					testRefsAscendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

		}

	}

	t.Run("blank hash", func(t *testing.T) {

		refs.Clear()
		pack.ClearFlags(^0) // all

		err = refs.AppendHashes(pack, cipher.SHA256{}, cipher.SHA256{})

		if err != nil {
			t.Fatal(err)
		}

		err = refs.AscendFrom(pack, 1,
			func(i int, hash cipher.SHA256) (err error) {
				if i != 1 {
					t.Error("wrong index given")
				}
				if hash != (cipher.SHA256{}) {
					t.Error("wrong hash given")
				}
				return
			})

		if err != nil {
			t.Error(err)
		}

	})

	// -------------------------------------------------------------------------

	// TODO (kostyaring): extended testing of the AscendFrom

}

func testRefsDescend(t *testing.T, r *Refs, pack Pack, users []cipher.SHA256) {

	var err error
	var called int

	err = r.Descend(pack, func(i int, hash cipher.SHA256) (err error) {

		if i < 0 {
			t.Error("got negative index", i)
		} else if i >= len(users) {
			t.Error("got index out of rage", i)
		} else if users[i] != hash {
			t.Errorf("wrong hash, want %s, got %s, or wrong index %d",
				users[i].Hex()[:7],
				hash.Hex()[:7],
				i)
		}

		called++

		return // continue
	})

	if err != nil {
		t.Error(err)
	} else if called != len(users) {
		t.Errorf("wrong times called %d, but want %d", called, len(users))
	}

}

func TestRefs_Descend(t *testing.T) {
	// Descend(pack Pack, descendFunc IterateFunc) (err error)

	var (
		pack = getTestPack()

		refs Refs
		err  error

		users []cipher.SHA256

		clear = func(t *testing.T, r *Refs, degree Degree) {
			pack.ClearFlags(^0)          // clear flags of pack
			refs.Clear()                 // clear the Refs making it Refs{}
			if degree != pack.Degree() { // if it's not default
				if err = refs.SetDegree(pack, degree); err != nil { // change it
					t.Fatal(err)
				}
			}
		}
	)

	for _, degree := range []Degree{
		pack.Degree(),     // default
		pack.Degree() + 1, // changed
	} {

		t.Run(fmt.Sprintf("blank (degree %d)", degree), func(t *testing.T) {

			clear(t, &refs, degree)

			var called int

			err = refs.Descend(pack, func(int, cipher.SHA256) (_ error) {
				called++
				return
			})

			if err != nil {
				t.Error(err)
			} else if called > 0 {
				t.Errorf("called %d times, but should not be called at all",
					called)
			}

		})

		// fill
		//   (1) only leafs
		//   (2) leafs and branches
		//   (3) branches with branches with leafs

		for _, length := range []int{
			int(degree),            // only leafs
			int(degree) + 1,        // leafs and branches
			int(degree*degree) + 1, // branches with branches with leafs
		} {

			t.Logf("Refs with %d elements (degree %d)", length, degree)

			clear(t, &refs, degree)

			// generate users
			users = getHashList(
				testFillRefsWithUsers(t, &refs, pack, length),
			)

			if t.Failed() {
				t.FailNow()
			}

			logRefsTree(t, &refs, pack, false)

			t.Run(fmt.Sprintf("fresh %d:%d", length, degree),
				func(t *testing.T) {

					testRefsDescend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("break %d:%d", length, degree),
				func(t *testing.T) {

					var called int

					err = refs.Descend(pack, func(int, cipher.SHA256) (_ error) {
						called++
						return ErrStopIteration
					})

					if err != nil {
						t.Error(err)
					} else if called > 1 {
						t.Error("called too many times", called)
					}

					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset() // reset the refs

					testRefsDescend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load entire %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset()              // reset the refs
					pack.AddFlags(EntireRefs) // load entire Refs

					testRefsDescend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("hash table index %d:%d", length, degree),
				func(t *testing.T) {

					refs.Reset()

					pack.ClearFlags(EntireRefs)
					pack.AddFlags(HashTableIndex)

					testRefsDescend(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

		}

	}

	t.Run("blank hash", func(t *testing.T) {

		refs.Clear()
		pack.ClearFlags(^0) // all

		if err = refs.AppendHashes(pack, cipher.SHA256{}); err != nil {
			t.Fatal(err)
		}

		err = refs.Descend(pack, func(i int, hash cipher.SHA256) (err error) {
			if i != 0 {
				t.Error("wrong index given")
			}
			if hash != (cipher.SHA256{}) {
				t.Error("wrong hash given")
			}
			return
		})

	})

	// -------------------------------------------------------------------------

	// TODO (kostyaring): extended testing of the Descend

}

func findIndex(users []cipher.SHA256, hash cipher.SHA256) (i int) {
	var user cipher.SHA256
	for i, user = range users {
		if user == hash {
			return
		}
	}
	return -1
}

func testRefsDescendFrom(
	t *testing.T,
	r *Refs,
	pack Pack,
	users []cipher.SHA256,
) {

	var err error

	for k := 0; k < len(users); k++ {

		var called int

		err = r.DescendFrom(pack, k,
			func(i int, hash cipher.SHA256) (err error) {

				if i > k {
					t.Errorf("(%d) got big index %d", k, i)
				} else if i < 0 {
					t.Errorf("(%d) got negative index %d", k, i)
				} else if users[i] != hash {
					t.Errorf(
						"(%d) wrong hash, want %s, got %s (%d), "+
							"or wrong index %d",
						k,
						users[i].Hex()[:7],
						hash.Hex()[:7],
						findIndex(users, hash),
						i)
				}

				called++

				return // continue
			})

		if err != nil {
			t.Error(err, "here")
		} else if called != k+1 {
			t.Errorf("(%d) wrong times called %d, but want %d",
				k,
				called,
				k+1)
		}

	}

}

func TestRefs_DescendFrom(t *testing.T) {
	// DescendFrom(pack Pack, from int, descendFunc IterateFunc) (err error)

	var (
		pack = getTestPack()

		refs Refs
		err  error

		users []cipher.SHA256

		clear = func(t *testing.T, r *Refs, degree Degree) {
			pack.ClearFlags(^0)          // clear flags of pack
			refs.Clear()                 // clear the Refs making it Refs{}
			if degree != pack.Degree() { // if it's not default
				if err = refs.SetDegree(pack, degree); err != nil { // change it
					t.Fatal(err)
				}
			}
		}
	)

	for _, degree := range testRefsDegrees(pack) {

		t.Run(fmt.Sprintf("blank (degree %d)", degree), func(t *testing.T) {

			clear(t, &refs, degree)

			var called int

			err = refs.DescendFrom(pack, 0, func(int, cipher.SHA256) (_ error) {
				called++
				return
			})

			if err == nil {
				t.Error("missing error")
			} else if err != ErrIndexOutOfRange {
				t.Errorf("wrong error given %q, expected ErrIndexOutOfRange",
					err.Error())
			}

		})

		// fill
		//   (1) only leafs
		//   (2) leafs and branches
		//   (3) branches with branches with leafs

		for _, length := range testRefsLengths(degree) {

			t.Logf("Refs with %d elements (degree %d)", length, degree)

			clear(t, &refs, degree)

			// generate users
			users = getHashList(
				testFillRefsWithUsers(t, &refs, pack, length),
			)

			if t.Failed() {
				t.FailNow()
			}

			logRefsTree(t, &refs, pack, false)

			t.Run(fmt.Sprintf("fresh %d:%d", length, degree),
				func(t *testing.T) {

					testRefsDescendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("break %d:%d", length, degree),
				func(t *testing.T) {

					t.Skip("skip")

					var called int

					err = refs.DescendFrom(pack, 0,
						func(int, cipher.SHA256) (_ error) {
							called++
							return ErrStopIteration
						})

					if err != nil {
						t.Error(err)
					} else if called != 1 {
						t.Error("wrong times called", called)
					}

					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load %d:%d", length, degree),
				func(t *testing.T) {

					t.Skip("skip")

					refs.Reset() // reset the refs

					testRefsDescendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("load entire %d:%d", length, degree),
				func(t *testing.T) {

					t.Skip("skip")

					refs.Reset()              // reset the refs
					pack.AddFlags(EntireRefs) // load entire Refs

					testRefsDescendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

			t.Run(fmt.Sprintf("hash table index %d:%d", length, degree),
				func(t *testing.T) {

					t.Skip("skip")

					refs.Reset()

					pack.ClearFlags(EntireRefs)
					pack.AddFlags(HashTableIndex)

					testRefsDescendFrom(t, &refs, pack, users)
					logRefsTree(t, &refs, pack, false)

				})

		}

	}

	t.Run("blank hash", func(t *testing.T) {

		t.Skip("skip")

		refs.Clear()
		pack.ClearFlags(^0) // all

		err = refs.AppendHashes(pack, cipher.SHA256{}, cipher.SHA256{})

		if err != nil {
			t.Fatal(err)
		}

		err = refs.DescendFrom(pack, 1,
			func(i int, hash cipher.SHA256) (err error) {
				if !(i == 0 || i == 1) {
					t.Error("wrong index given", i)
				}
				if hash != (cipher.SHA256{}) {
					t.Error("wrong hash given")
				}
				return
			})

		if err != nil {
			t.Error(err)
		}

	})

	// -------------------------------------------------------------------------

	// TODO (kostyaring): extended testing of the DescendFrom

}
