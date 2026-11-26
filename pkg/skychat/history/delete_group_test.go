//go:build !js

package history

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestDeleteGroup_ErasesTheGroup(t *testing.T) {
	withStore(t, func(t *testing.T, s Store) {
		base := time.Now().UTC()
		for i := range 3 {
			if err := s.AppendGroup(GroupMessage{GroupID: "gone", SenderPK: "pk", Text: "m", Timestamp: base.Add(time.Duration(i))}); err != nil {
				t.Fatalf("AppendGroup: %v", err)
			}
		}
		if err := s.AppendGroup(GroupMessage{GroupID: "kept", SenderPK: "pk", Text: "m", Timestamp: base}); err != nil {
			t.Fatalf("AppendGroup: %v", err)
		}
		n, err := s.DeleteGroup("gone")
		if err != nil || n != 3 {
			t.Fatalf("DeleteGroup = %d, %v; want 3, nil", n, err)
		}
		groups, err := s.Groups()
		if err != nil {
			t.Fatalf("Groups: %v", err)
		}
		if slices.Contains(groups, "gone") || !slices.Contains(groups, "kept") {
			t.Fatalf("Groups after delete = %v, want only kept", groups)
		}
		if n, err := s.DeleteGroup("unknown"); err != nil || n != 0 {
			t.Fatalf("DeleteGroup(unknown) = %d, %v; want 0, nil", n, err)
		}
		if _, err := s.DeleteGroup(""); !errors.Is(err, ErrEmptyPeer) {
			t.Fatalf("DeleteGroup(\"\") err = %v, want ErrEmptyPeer", err)
		}
	})
}
