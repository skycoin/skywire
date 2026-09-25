// Package visor pkg/visor/profile.go c3-vis-core
//
// The visor half of skychat profiles: read and write this visor's own
// published identity, and fetch a peer's.
//
// Thin, like pkg/visor/group.go is thin — every rule about what a profile
// may contain lives in pkg/skychat/profile, and the transport lives on the
// group manager's describe port. What is here is the RPC-facing shape and
// the decision of which of the two answers (local store, remote round trip)
// a given public key deserves.
package visor

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	skychataddr "github.com/skycoin/skywire/pkg/skychat/address"
	skychatprofile "github.com/skycoin/skywire/pkg/skychat/profile"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// ErrProfileDisabled is returned when the profile store failed to open, so
// this visor can neither publish nor persist an identity this run.
//
// Distinct from "no profile set", which is not an error: an empty profile
// is the normal state of a visor whose operator never opened the dialog.
var ErrProfileDisabled = errors.New("profile: store not initialized")

// profileFetchBudget caps one remote profile fetch. Tight: it sits under a
// user watching a name appear in a dialog that is already usable without
// it, so failing fast and showing the key is better than a long wait.
const profileFetchBudget = 8 * time.Second

// profileStore returns the visor's own profile store, or nil.
func (v *Visor) profileStore() *skychatprofile.Store {
	v.initLock.RLock()
	defer v.initLock.RUnlock()
	return v.grouping.profile
}

// ProfileGet returns what this visor publishes about itself.
//
// An unset profile is reported as an empty one with the key and address
// filled in, not as an error: those two fields are what the "my address"
// dialog needs, and they are true whether or not a name was ever set.
func (v *Visor) ProfileGet() (visorapi.Profile, error) {
	store := v.profileStore()
	if store == nil {
		return visorapi.Profile{}, ErrProfileDisabled
	}
	p, err := store.Load()
	if err != nil {
		return visorapi.Profile{}, err
	}
	return toProfile(v.conf.PK, p), nil
}

// ProfileSet writes this visor's published profile and returns what was
// actually stored — normalization may have trimmed the name, and the caller
// should show the user what their peers will see rather than what they
// typed.
func (v *Visor) ProfileSet(args visorapi.ProfileSetArgs) (visorapi.Profile, error) {
	store := v.profileStore()
	if store == nil {
		return visorapi.Profile{}, ErrProfileDisabled
	}
	if args.Clear {
		if err := store.Clear(); err != nil {
			return visorapi.Profile{}, err
		}
		return toProfile(v.conf.PK, skychatprofile.Profile{}), nil
	}
	avatar, err := skychatprofile.DecodeAvatar(args.Avatar)
	if err != nil {
		return visorapi.Profile{}, err
	}
	saved, err := store.Save(skychatprofile.Profile{Name: args.Name, Avatar: avatar})
	if err != nil {
		return visorapi.Profile{}, err
	}
	return toProfile(v.conf.PK, saved), nil
}

// ProfileFetch asks the visor at pk who it is.
//
// A zero key, or this visor's own, is answered from the local store — the
// same courtesy GroupCatalog gives an operator inspecting their own
// listing, and the reason a UI can use one call for both.
//
// A peer that does not answer is NOT an error to the caller's user: every
// call site has the public key itself as a perfectly good fallback. It is
// still returned as an error here so the caller can tell "reachable, says
// nothing" from "could not ask", which are different things to show.
func (v *Visor) ProfileFetch(pk cipher.PubKey) (visorapi.Profile, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.Profile{}, ErrGroupingDisabled
	}
	if pk == (cipher.PubKey{}) || pk == v.conf.PK {
		return v.ProfileGet()
	}
	ctx, cancel := context.WithTimeout(context.Background(), profileFetchBudget)
	defer cancel()
	p, err := mgr.FetchProfile(ctx, pk)
	if err != nil {
		return visorapi.Profile{}, err
	}
	return toProfile(pk, p), nil
}

// toProfile converts a stored/fetched profile into the RPC shape, deriving
// the data URI and the address from the key the caller asked about.
func toProfile(pk cipher.PubKey, p skychatprofile.Profile) visorapi.Profile {
	return visorapi.Profile{
		PK:      pk,
		Name:    p.Name,
		Avatar:  p.AvatarDataURI(),
		Updated: p.Updated,
		Address: skychataddr.DM(pk).String(),
	}
}
