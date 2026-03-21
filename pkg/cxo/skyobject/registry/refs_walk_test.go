package registry

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
)

func isRefsInitialized(r *Refs) bool {
	return r.refsNode != nil && r.mods&loadedMod != 0
}

func TestRefs_Walk(t *testing.T) {

	var (
		pack = getTestPack()

		users []cipher.SHA256

		r   Refs
		err error
	)

	for _, flags := range testRefsFlags() {

		pack.ClearFlags(^0)
		pack.AddFlags(flags)

		t.Logf("flags %08b", flags)

		for _, degree := range testRefsDegrees(pack) {

			t.Log("degree", degree)

			for _, length := range testRefsLengths(degree) {

				t.Log("length", length)

				clearRefs(t, &r, pack, degree)
				users = getHashList(getTestUsers(length))

				t.Run("blank", func(t *testing.T) {

					var isInitialized = isRefsInitialized(&r)
					var called int

					err = r.Walk(pack,
						nil,
						func(
							hash cipher.SHA256,
							depth int,
						) (
							deepper bool,
							err error,
						) {

							called++
							if hash != (cipher.SHA256{}) {
								t.Error("unexpected hash given")
							}

							deepper = depth != 0
							return

						})

					if err != nil {
						t.Error(err)
					} else if called != 1 {
						t.Errorf("wrong times called %d, want 1", called)
					} else if isInitialized == true {
						if isRefsInitialized(&r) == false {
							t.Error("uninitialized")
						}
					} else if isRefsInitialized(&r) == true {
						t.Error("not reset")
					}

				})

				if err = r.AppendHashes(pack, users...); err != nil {
					t.Fatal(err)
				}

				var usersMap = make(map[cipher.SHA256]struct{})

				for _, hash := range users {
					usersMap[hash] = struct{}{}
				}

				var tree []cipher.SHA256 // the tree

				err = r.Walk(pack,
					nil,
					func(
						hash cipher.SHA256,
						depth int,
					) (
						bool,
						error,
					) {
						if depth == 0 {
							if _, ok := usersMap[hash]; !ok {
								t.Error("unexpected hash in leaf")
							}
						}
						tree = append(tree, hash)
						return depth != 0, nil
					})

				if err != nil {
					t.Error(err)
				}

				t.Run("load", func(t *testing.T) {
					var i int
					r.Reset()
					err = r.Walk(pack,
						nil,
						func(
							hash cipher.SHA256,
							depth int,
						) (
							bool,
							error,
						) {
							if depth == 0 {
								if _, ok := usersMap[hash]; !ok {
									t.Error("unexpected hash in leaf")
								}
							}
							if tree[i] != hash {
								t.Error("unexpected hash")
							}
							i++
							return depth != 0, nil
						})

					if err != nil {
						t.Error(err)
					}
				})

				// TODO (kostyarin): improve the test case

			}

		}

	}

}
