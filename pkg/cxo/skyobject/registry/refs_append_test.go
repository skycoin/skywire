package registry

import (
	"fmt"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
)

// clear Refs, and set provided degree to the Refs
func clearRefs(
	t *testing.T,
	r *Refs,
	pack Pack,
	degree Degree,
) {

	var err error

	r.Clear() // clear the Refs making it Refs{}

	if degree != pack.Degree() { // if it's not default
		if err = r.SetDegree(pack, degree); err != nil { // change it
			t.Fatal(err)
		}
	}

}

func TestRefs_Append(t *testing.T) {
	// Append(pack Pack, refs *Refs) (err error)

	//

}

func TestRefs_AppendValues(t *testing.T) {
	// AppendValues(pack Pack, values ...interface{}) (err error)

	// TODO (kostyarin): the AppendValues method based on the AppendHashes
	//                   method and this test case is not important a lot,
	//                   thus I mark it as low priority

}

func testRefsAppendHashesCheck(
	t *testing.T, //           : the testing
	r *Refs, //                : the Refs
	pack Pack, //              : the Pack
	shift int, //              : length of the Refs before appending
	hashes []cipher.SHA256, // : appended values
) (
	nl int, //                 : new length
) {

	var err error

	if nl, err = r.Len(pack); err != nil {

		t.Error(err)

	} else if nl != shift+len(hashes) {

		t.Errorf("wrong new length %d, but want %d", nl, shift+len(hashes))

	} else {

		var h cipher.SHA256
		for i, hash := range hashes {
			if h, err = r.HashByIndex(pack, shift+i); err != nil {
				t.Errorf("shift %d, i %d, %s", shift, i, err.Error())
			} else if h != hash {
				t.Errorf("wrong hash of %d: %s, want %s",
					shift+i,
					h.Hex()[:7],
					hash.Hex()[:7])
			}
		}

	}

	if shift > 0 {
		hashes = append(hashes, hashes...) // stub for now
	}

	logRefsTree(t, r, pack, false)

	return

}

// peristence but not number of elements
func testRefsHashTableIndex(t *testing.T, r *Refs, hashes []cipher.SHA256) {

	if r.refsIndex == nil {
		t.Error("Refs has not hash-table index")
		return
	}

	for _, hash := range hashes {
		if _, ok := r.refsIndex[hash]; !ok {
			t.Error("misisng hash in hash table:", hash.Hex()[:7])
		}
	}

}

func TestRefs_AppendHashes(t *testing.T) {
	// AppendHashes(pack Pack, hashes ...cipher.SHA256) (err error)

	var (
		pack = getTestPack()

		refs Refs
		err  error

		users []cipher.SHA256 // hashes of the users
	)

	for _, degree := range []Degree{
		pack.Degree(),     // default
		pack.Degree() + 1, // changed
	} {

		pack.ClearFlags(^0) // all

		t.Run(
			fmt.Sprintf("append nothing to blank Refs (degree is %d)", degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree)
				var ln int
				if err = refs.AppendHashes(pack); err != nil {
					t.Error(err)
				} else if ln, err = refs.Len(pack); err != nil {
					t.Error(err)
				} else if ln != 0 {
					t.Error("wrong length")
				}
			})

		var length = int(degree*degree) + 1

		t.Logf("Refs with %d elements (degree %d)", length, degree)

		clearRefs(t, &refs, pack, degree)

		// generate users
		users = getHashList(getTestUsers(length))

		t.Run(
			fmt.Sprintf("reset-append increasing number of elements %d:%d",
				length,
				degree),
			func(t *testing.T) {

				for k := 0; k <= len(users) && t.Failed() == false; k++ {

					clearRefs(t, &refs, pack, degree) // can call t.Fatal

					if err = refs.AppendHashes(pack, users[:k]...); err != nil {
						t.Fatal(err)
					}

					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k])

				}

			})

		t.Run(fmt.Sprintf("append one by one %d:%d", length, degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree) // can call t.Fatal

				for k := 0; k < len(users) && t.Failed() == false; k++ {

					t.Log("append hash", users[k].Hex()[:7])

					if err = refs.AppendHashes(pack, users[k]); err != nil {
						t.Fatal(err)
					}

					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k+1])

				}

			})

		t.Run(fmt.Sprintf("append many to full Refs %d:%d", length, degree),
			func(t *testing.T) {

				logRefsTree(t, &refs, pack, false)

				// now the Refs contains all the hashes, let's append them twice
				if err = refs.AppendHashes(pack, users...); err != nil {
					t.Fatal(err)
				}

				testRefsAppendHashesCheck(t, &refs, pack, len(users), users)

			})

		t.Run(fmt.Sprintf("append-reset-append %d:%d", length, degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree)

				for k := 0; k < len(users) && t.Failed() == false; k++ {

					if err = refs.AppendHashes(pack, users[k]); err != nil {
						t.Fatal(err)
					}

					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k+1])

					refs.Reset() // keep degree

				}

			})

		t.Run(fmt.Sprintf("append-reset-append (entire) %d:%d", length, degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree)
				pack.AddFlags(EntireRefs)

				for k := 0; k < len(users) && t.Failed() == false; k++ {

					if err = refs.AppendHashes(pack, users[k]); err != nil {
						t.Fatal(err)
					}

					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k+1])

					refs.Reset() // keep degree

				}

			})

		t.Run(
			fmt.Sprintf("append-reset-append (hash-table index) %d:%d",
				length,
				degree),
			func(t *testing.T) {

				pack.ClearFlags(^0)
				pack.AddFlags(HashTableIndex)

				clearRefs(t, &refs, pack, degree)

				for k := 0; k < len(users) && t.Failed() == false; k++ {

					if err = refs.AppendHashes(pack, users[k]); err != nil {
						t.Fatal(err)
					}

					testRefsHashTableIndex(t, &refs, users[:k+1])
					testRefsAppendHashesCheck(t, &refs, pack, 0, users[:k+1])

					refs.Reset() // keep degree

				}

			})

	}

	t.Run("blank hash", func(t *testing.T) {

		clearRefs(t, &refs, pack, pack.Degree())

		var hashes = []cipher.SHA256{
			{}, // the blank one
			{}, // the blank two
		}

		if err = refs.AppendHashes(pack, hashes...); err != nil {
			t.Fatal(err)
		}

		testRefsAppendHashesCheck(t, &refs, pack, 0, hashes)

	})

}

func TestRefs_AppendHashes_nodes(t *testing.T) {
	// AppendHashes(pack Pack, hashes ...cipher.SHA256) (err error)

	var (
		pack = getTestPack()

		refs Refs
		err  error

		users []cipher.SHA256 // hashes of the users
	)

	for _, degree := range []Degree{
		pack.Degree(),     // default
		pack.Degree() + 1, // changed
	} {

		pack.ClearFlags(^0) // all

		t.Run(
			fmt.Sprintf("append nothing to blank Refs (degree is %d)", degree),
			func(t *testing.T) {

				clearRefs(t, &refs, pack, degree)
				var ln int
				if err = refs.AppendHashes(pack); err != nil {
					t.Error(err)
				} else if ln, err = refs.Len(pack); err != nil {
					t.Error(err)
				} else if ln != 0 {
					t.Error("wrong length")
				}
			})

		var length = int(degree*degree) + 1

		t.Logf("Refs with %d elements (degree %d)", length, degree)

		clearRefs(t, &refs, pack, degree)

		// generate users
		users = getHashList(getTestUsers(length))

		if err = refs.AppendHashes(pack, users...); err != nil {
			t.Fatal(err)
		}

		var hashes []cipher.SHA256

		err = refs.Walk(pack, nil,
			func(key cipher.SHA256, depth int) (bool, error) {
				hashes = append(hashes, key)
				return depth > 0, nil
			})
		if err != nil {
			t.Fatal(err)
		}

		clearRefs(t, &refs, pack, degree) // can call t.Fatal

		for k := 0; k < len(users) && t.Failed() == false; k++ {

			t.Log("append hash", users[k].Hex()[:7])

			if err = refs.AppendHashes(pack, users[k]); err != nil {
				t.Fatal(err)
			}

		}

		var i int

		err = refs.Walk(pack, nil,
			func(key cipher.SHA256, depth int) (bool, error) {
				if key != hashes[i] {
					t.Fatal("wrong hash", i)
				}
				i++
				return depth > 0, nil
			})
		if err != nil {
			t.Fatal(err)
		}

	}

}
