package memory

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/user"
)

//UserRepo Implements the Repository Interface to provide an in-memory storage provider
type UserRepo struct {
	user user.User
}

//NewRepo Constructor
func NewUserRepo(pk cipher.PubKey) *UserRepo {
	uR := UserRepo{}

	var err error
	uR.user, err = uR.NewUser()
	uR.user.GetInfo().SetPK(pk)
	if err != nil {
		fmt.Println(err)
	}

	return &uR
}

//New fills repo with a new user, if none has been set
//also returns a user when a user has been set already
func (r *UserRepo) NewUser() (user.User, error) {
	if !r.user.IsEmpty() {
		return r.user, fmt.Errorf("user already defined")
	} else {
		r.SetUser(user.NewDefaultUser())
		fmt.Printf("New DefaultUser at %p\n", &r.user)
		return r.user, nil
	}
}

//Get Returns the user
func (r *UserRepo) GetUser() (*user.User, error) {
	fmt.Printf("user-repo adress %p\n", r)
	if r.user.IsEmpty() {
		return nil, fmt.Errorf("user not found")
	} else {
		fmt.Printf("Get User at %p", &r.user)
		return &r.user, nil
	}
}

//Update the provided user
func (r *UserRepo) SetUser(user *user.User) error {
	r.user = *user
	return nil
}
