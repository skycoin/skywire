// Package cipher pkg/cipher/ustils_pubkey.go
package cipher

// SamePubKeys returns true when the provided public key slices have the same keys.
// The slices do not need to be in the same order.
// It is assumed that there are no duplicate elements within the slices.
func SamePubKeys(pks1, pks2 []PubKey) bool {
	if len(pks1) != len(pks2) {
		return false
	}

	m := make(map[PubKey]struct{}, len(pks1))
	for _, pk := range pks1 {
		m[pk] = struct{}{}
	}

	for _, pk := range pks2 {
		if _, ok := m[pk]; !ok {
			return false
		}
	}

	return true
}
